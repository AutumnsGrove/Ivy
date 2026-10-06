# 4e — identities and reply logic (design)

Stage 4e is "DeepSeek, solo" (`next_steps.md`, round 60): pure logic, low risk, no escalation gate.
The four open choices were put to the operator before any code and are recorded in
`docs/qa-log.md` round 63. **Nothing here is implemented yet.**

`compose/` already builds and validates a message, and `send_queue`/`drafts` own the two durable
paths. Identities are the missing piece: who an account may send **as**, the signature each address
carries, and the pure logic that turns an incoming message into a reply, a reply-all or a forward.

## 1. What 4e must do

1. Store per-address identities (display name and signature) in `state.db`, backed up (CLAUDE.md
   rule 5); the account's own address is always an identity.
2. Let `POST /send` and the draft save accept any configured identity as `From`, not only the
   account's own address (the 4c/4d placeholder is removed).
3. Compute a reply, a reply-all and a forward from an incoming message: recipients, subject,
   `In-Reply-To`/`References`, the quoted/empty body, and the identity to send as.
4. Offer a settings editor so the operator can add aliases and signatures; the live send-as check
   per address is the operator's exit step.

## 2. Settled decisions (operator, round 63)

1. **Surface: API + settings editor.** 4e ships the store, the CRUD API, the reply logic and a small
   identities/signatures section on the account settings screen, so the operator can add aliases and
   run the live check without waiting for 4f. The From picker in the compose screen is still 4f.
2. **Signature: per identity, plain text, `-- ` separator.** A non-empty signature is appended after
   the body as `\n\n-- \n<signature>`. When markdown is on the whole text (body plus signature) is
   rendered to HTML, so the signature is styled with the message.
3. **Forward: attribution block.** The forward prefill is a blank line, then
   `---------- Forwarded message ----------` and `From`/`Date`/`Subject`/`To` lines, then the
   original plain text. Attachments are not attached (only when asked, CHUNK4-BRIEF section 3).
4. **Aliases: configured only, offer to add.** `From` must be the account's address or a stored
   identity. When a reply's delivered-to address has no identity, the prefill says so and the
   compose screen can offer to add it in one tap. Nothing sends from an unconfigured address.

## 3. Rows (`state.db`, append-only migration 14)

`identities` — one address an account may send as, with the display name and signature the builder
uses. Keyed by `(account_id, address)`; `id` is the injected row id the API addresses a non-primary
identity by.

| column | meaning |
|---|---|
| `id` | row id (injected) |
| `account_id` | whose mailbox |
| `address` | the addr-spec; must be ASCII (Purelymail has no `SMTPUTF8`) |
| `display_name` | optional; RFC 2047 encoded by the builder |
| `signature` | optional plain text, `-- ` separated when applied |
| `created_at` / `updated_at` | times |

**The account's own address is always an identity.** It is not seeded by a migration (accounts live
in the rebuildable mirror), so reading an account's identities *merges* the stored rows with a
synthetic primary row for `accounts.address` when none matches it. The primary row is never
deletable; editing it (a display name or signature) creates the stored row through the ordinary
upsert. This keeps one source of truth for the address and never lets a rebuild resurrect a
duplicate.

## 4. Store API (`store/identities.go`)

```
type Identity struct { ID, AccountID, Address, DisplayName, Signature string; CreatedAt, UpdatedAt time.Time }
type IdentityInput struct { ID, AccountID, Address, DisplayName, Signature string; Now time.Time }

ListIdentities(ctx, accountID) ([]Identity, error)          // ordered by address
GetIdentity(ctx, accountID, address) (Identity, error)      // case-insensitive; ErrNotFound
GetIdentityByID(ctx, id) (Identity, error)
UpsertIdentity(ctx, IdentityInput) (Identity, error)        // by (account_id, address); preserves created_at
DeleteIdentity(ctx, id) error
```

- An address is validated before it is stored: non-empty, ASCII, no CR/LF/NUL or whitespace, a bare
  addr-spec. `compose.Build` validates it again on the wire, so a bad row could never forge a header.
- Bounds: `MaxIdentitiesPerAccount` (50), `MaxIdentityAddressBytes` (320), `MaxIdentityNameBytes`
  (256 runes enforced at the API), `MaxSignatureBytes` (8 KiB). Over any is a clear error.
- `DeleteIdentity` of the row matching the account's own address is refused by the gateway
  (`primary_identity`), not the store, because only the gateway knows the account's address.

## 5. Reply / reply-all / forward (`compose/reply.go`, pure)

Input is the parsed incoming message; the package imports no store type, so tests are pure tables
over hostile and odd header sets:

```
type Incoming struct {
    From, To, Cc, ReplyTo, DeliveredTo []Address
    Subject string; Date time.Time; MessageID string
    References []string; Text string
}
type IdentityRef struct { Address, DisplayName, Signature string }

type Prefill struct {
    From Address; To, Cc []Address
    Subject string; Text string
    InReplyTo string; References []string
    ReplyTarget string     // "Replying to …"; empty for forward
    MissingIdentity string // delivered-to address with no identity; empty when fine
}

Reply(orig Incoming, ids []IdentityRef, accountDefault IdentityRef, all bool) Prefill
Forward(orig Incoming, ids []IdentityRef, accountDefault IdentityRef) Prefill
AppendSignature(text, signature string) string
```

Recipient rules (settled in round 60, `List-Post`/lists out of v1):

- **Direct target** = `Reply-To` when present and non-empty, else `From` (first mailbox of the
  header).
- **Reply:** `To` = the direct target; `Cc` empty.
- **Reply-all:** `To` = the direct target; `Cc` = the original `To` + `Cc`, de-duplicated
  case-insensitively, with every one of the account's own addresses (all identities + the account
  address) and anyone already in `To` removed.
- **Subject:** `Re: ` added unless the subject already begins with a `Re:`/`RE[n]:` variant.
- **In-Reply-To/References:** `In-Reply-To` is the original `Message-ID`;
  `References` = the original `References` plus the original `Message-ID`, oldest kept within
  `MaxReferences`.
- **Body:** a reply is empty (the reader already shows the thread); a forward is the attribution
  block plus the original plain text. The chosen identity's signature is applied by
  `AppendSignature` after the body.
- **Identity:** the first address among `DeliveredTo`, `To`, `Cc` that matches a configured
  identity, case-insensitively; otherwise the account's default identity. When a `DeliveredTo`
  address has no identity, `MissingIdentity` names it so the screen can offer to add it.
- A header carrying multiple `From` mailboxes, an empty `From`, or a self-referential reply is
  handled without panic and with a defined outcome (no recipient, or the account's own address
  filtered out).

## 6. API

- `GET /api/v1/accounts/{id}/identities` — stored identities merged with the synthetic primary.
- `PUT /api/v1/accounts/{id}/identities` — upsert by `address` (create or edit, primary or alias).
- `DELETE /api/v1/accounts/{id}/identities/{identityId}` — delete a non-primary row.
- `GET /api/v1/messages/{id}/reply?all=true` — the reply/reply-all prefill.
- `GET /api/v1/messages/{id}/forward` — the forward prefill.

`POST /send` and the draft save look the `From` up: the account's own address, or a stored identity
of the same account. A `From` outside that set stays `bad_from`.

## 7. Limits (added to `STANDARDS.md` 4a when this lands)

- identities per account: 50; address 320 bytes; display name 120 runes; signature 8 KiB;
- identities list is bounded (`MaxIdentitiesPerAccount`); a reply prefill reads one message's bounded
  stored headers and body text, never the raw blob whole.

## 8. Scope of 4e

`store` (migration 14, identities CRUD), `compose` (reply/reply-all/forward, signature), the
`gateway` handlers and the identity-aware `From`, the account-settings identities editor, and the
docs. No compose screen (4f); no attachments (4g); no list (`List-Post`) handling.

## 9. Plan of small commits

1. this design, the qa-log round 63, and `next_steps.md`;
2. `store`: migration 14, the identities CRUD, and its tests;
3. `compose`: the reply/reply-all/forward logic, signature application, and pure table tests;
4. `gateway`: the identities API, the reply/forward prefill, the identity-aware `From`, and tests;
5. `web`: the identities and signatures editor on the account settings screen, API client and tests;
6. docs (`ARCHITECTURE.md`, `STANDARDS.md`, `openapi.yaml`, `BUILD-LOG.md`) and `next_steps.md`.

The live send-as check per address is the operator's exit step and is its own pending line in
`next_steps.md`, never folded into a stage marked done.
