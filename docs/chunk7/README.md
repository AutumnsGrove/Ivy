# Chunk 7: accounts — Gmail, and any IMAP server

Planned 2026-10-22. The operator wants a second mailbox in Ivy: Gmail at the least, Proton if it is
honest to. This chunk is mostly **one architectural change** (a message can live in several mailboxes
at once) plus the auth and provider plumbing Gmail needs.

## Purpose

1. A message may sit in more than one mailbox, without being stored twice (7a). This is what makes
   Gmail's labels truthful and is not optional: Gmail's IMAP presents each label as a folder, and one
   message is in several.
2. An account can be **found** from its address, or typed by hand, including host, port and TLS (7b).
3. Gmail is a first-class account: OAuth2 sign-in over SASL XOAUTH2, Gmail's own send and archive
   semantics, its limits (7c, 7e).
4. An app password stays possible for any server, including Gmail (7d), because the OAuth path has a
   Google-side trap that we cannot design away (see 7c).
5. Proton is documented, not coded (7f), with the cost of that stated plainly.

## Depends on

3a (runner, `sync_state`, QRESYNC), 3b (disabled mail), 3d (the outbox, which already speaks content
keys), 3e (tags), 4a-4e (send, identities, the Sent copy and the APPEND that Gmail must not get).
Nothing in chunk 5 or 6.

## Settled by the operator, 2026-10-22

- **OAuth2 first** for Gmail (SASL XOAUTH2), rather than an app password as the primary path.
- **Normalise to multi-mailbox**, not Gmail-shaped special cases: one row per message, N mailbox
  memberships. Accepted cost: a migration and a join in every list query.
- **Scope is Gmail plus generic IMAP** (a provider profile found from the address, and manual
  host/port/TLS fields). Not other APIs.
- **Proton is documented only.** No Proton-specific code in this chunk.
- **Still true from earlier:** IMAP/SMTP only, Purelymail first, no calendar, no CalDAV, no CardDAV,
  no JMAP, writes always go to the server first, and nothing is ever erased.

## Where we start

Checked against the code on 2026-10-22:

| Piece | Today |
|---|---|
| `messages` | `folder_id TEXT NOT NULL` and `UNIQUE(folder_id, uid)`: **one mailbox per message**. `idx_messages_inbox ON messages(folder_id, date DESC, id DESC)` is the list index. `content_key` has a non-unique index per account. |
| `folders` | per account, `name`, `role`, `uidvalidity`, `highestmodseq`, `gone_at`. |
| Flags and unread | denormalised on the message (`seen`, `flags_json`). |
| `outbox` | already keyed by **content key + folder id**, so the write path does not depend on a message row's folder. This is the single most encouraging fact for 7a. |
| `accountsvc.Connector` | `Connect(ctx, address, password, smart)` probes one **hardcoded Purelymail provider** and stores the password as a private file. `Provider.Insecure` exists for the loopback fake and is never stored. No OAuth, no token refresh, no host fields in the UI. |
| `accounts` table | `imap_host`, `imap_port`, `smtp_host`, `smtp_port`, `username` — already generic; only the UI and the connector are Purelymail-shaped. |
| Tags | local, written to IMAP keywords with a server read-back, local-only fallback when a server refuses arbitrary keywords. |
| Search | FTS5 over the mirror, by content key. |
| go-imap | `github.com/emersion/go-imap/v2`, replaced by our fork (QRESYNC). `go-sasl` is present and has **`oauthbearer` but no XOAUTH2**. |
| Proton | nothing. |

## Scope

### 7a. The mailbox model — L, the risky stage

**What.** `messages` loses `folder_id` and `uid`; a new `message_mailboxes` table holds
`(message_id, folder_id, uid, uidvalidity_at_sync)`, `UNIQUE(folder_id, uid)` and
`UNIQUE(message_id, folder_id)`. A message with no memberships is **disabled, never erased**
(non-negotiable 5): that is how a server-side expunge, a label removal and a Gmail "delete" all read.

**Why the join stays fast.** The membership row carries the message's `date` (immutable), so the list
query is `WHERE folder_id = ? ORDER BY date DESC` over an index `(folder_id, date DESC, message_id)`
with no sort step. Decided now, measured in the benchmark: if the join costs more than a stated budget
on the potato, the fallback is a `primary_folder_id` denormalisation and nothing else changes.

**Flag semantics, stated as a simplification.** Flags stay on the message, not on the membership.
Gmail agrees (its `\Seen` is per message); a plain IMAP server that keeps per-mailbox flags will show
one shared state, which is the price of the simpler model. Recorded so nobody calls it a bug later.

**Queries that change:** inbox and folder lists, unread and need counts, search's folder filter, the
tag view, the reader's "which mailbox is this in" line, drafts (a draft is one membership), the Sent
copy, disabled and purge, mirror health, and the backfill cursor per folder.

**Tests.** Extend the 3a convergence property test to memberships: seeded sequences where a message
moves, gains a second mailbox, loses both and comes back; a message in three mailboxes appears once in
All Mail, once in each label view, and once in Inbox; deleting a membership never erases a row; the
unread count never doubles because a message is in two mailboxes; the archive and move outbox ops keep
working unchanged (they already use content keys).

**Migration.** Append-only, one version: create the table, copy each message's `folder_id`/`uid` into
it, drop the columns in a rebuilt `messages` table (the pattern migration 5 already uses), reindex.
The mirror is rebuildable, but the migration must still be correct in place: the operator's real
mirror has 41 messages and should not need a resync.

### 7b. Provider profiles, discovery and the manual fields — M

**What.** The connect screen stops assuming Purelymail. An address gets a **preset** (Gmail, iCloud,
Fastmail, Yahoo, Purelymail, generic IMAP), the screen shows exactly what it found (host, port, TLS,
auth method), and every field is editable. No MX or SRV lookups in this stage: a preset table and an
override is honest and has no DNS-failure mode.

**Tests.** A preset resolves to the expected host and auth method; a mistyped address falls back to
the manual form with a reason; a manual account (host, port, implicit TLS) connects against the fake
mail world; the probe still stores **nothing** when it fails (the existing rule); the stored account
keeps `Insecure` unset, as today.

### 7c. Gmail by OAuth2 (SASL XOAUTH2) — M

**What.** The operator registers **her own** Google Cloud project (documented step by step in
`docs/GMAIL.md`): OAuth consent screen, scope `https://mail.google.com/`, a Web client, and Ivy's
callback URL on the Tailscale host. Ivy stores the client id and secret as settings, keeps the refresh
token in the secrets directory (never in `state.db`, never in a log), caches the access token with its
expiry against the injected clock, and speaks XOAUTH2 on both IMAP and SMTP. A ~20-line `sasl.Client`
in `internal/accounts` — `go-sasl` has `oauthbearer` and not XOAUTH2, and that is cheaper than a
dependency.

**The Google trap, recorded before anyone is surprised by it.** An external-user project whose
publishing status is **Testing** gets refresh tokens that **expire in 7 days**, for any scope beyond
profile. `https://mail.google.com/` is a **restricted** scope, so publishing it publicly needs
verification. For one operator the workable path is: **publish to production while unverified**, click
through the unverified-app warning once, and stay under the 100-user lifetime cap. A Google Workspace
account can instead use an **Internal** user type, which is clean. Ivy must therefore:

- show what to do when the token is dead (a banner with "Sign in to Gmail again", not a stack trace),
- never assume a token survives a restart,
- and say, in `docs/GMAIL.md`, that a Testing-status project will need re-consent every week.

**Tests.** A token refresh at the moment of expiry and one second before it; a revoked refresh token
gives the re-consent banner and no retry loop; the access token never appears in a log or an API
response; XOAUTH2's exact wire format is asserted against the fake (`\x01`-separated); a Gmail account
with smart features off still syncs.

### 7d. App password, for any server and for Gmail — S

**What.** The existing password path, kept as a first-class option in the connect screen, with a note
that Gmail wants 2-Step Verification on for it. It exists because 7c's alternative for a personal
account is a Google Cloud project with a weekly re-consent, and because every other IMAP server on
earth wants it.

**Tests.** Gmail preset with "password" chosen uses implicit TLS on 993/465; the failure text names 2SV
when Google rejects the login (asserted against a fake AUTHENTICATIONFAILED).

### 7e. Gmail's verbs and limits — M

The places where Gmail is not a generic IMAP server, each with its own test:

- **Do not APPEND to Sent.** Gmail files its own copy when it accepts an SMTP message. Ivy's send
  queue skips the Sent APPEND for an account flagged `server_files_sent` (a column on `accounts`), or
  the operator gets two copies of every reply.
- **Archive is not a move.** In Gmail, archiving is removing the `\Inbox` membership, leaving the
  message in All Mail. With 7a that is one membership delete, and the message stays reachable — which
  is also what Ivy's own Archive view must show.
- **Junk** is a move to `[Gmail]/Spam`; **delete** is a move to `[Gmail]/Trash`; "delete forever" is
  the existing purge path and stays a card.
- **Keywords:** Gmail refuses arbitrary keywords, so tags are local-only for now (the existing
  fallback). `X-GM-LABELS` as a way to write Gmail labels is a later option, not this chunk.
- **From must be an alias Gmail knows.** An identity that Gmail has not verified will be rewritten or
  rejected on SMTP; the identities screen says so for a Gmail account.
- **Limits:** 2500 MB/day IMAP download, 500 MB/day upload, about 10 000 messages per label visible
  over IMAP. So the backfill is **paced by bandwidth and windowed by date**, resumable, and Mirror
  health states plainly when a mailbox is larger than what IMAP will show.
- **Throttling** ("User is authenticated but not connected" and friends) is a transient failure:
  back off and retry, never mark the account broken.

**Tests, all against a fake that misbehaves on purpose:** a withheld Sent APPEND is absent for a
`server_files_sent` account and present for Purelymail; a throttling response backs off and does not
flip the account to `auth-failed`; a date-windowed backfill resumes where it stopped; a label at the
ceiling stops with a stated reason in Mirror health.

### 7f. Proton, documented only — S

Proton Mail has **no IMAP in the cloud**. Every path is a local bridge, and each one costs something
the operator should see before deciding:

| Path | What it needs | Works on the potato? |
|---|---|---|
| **Proton Mail Bridge** | A paid Proton plan. The Linux build is **x86_64 only** (Proton: "doesn't support ARM devices other than Apple silicon"). Needs a keyring/`pass` store, and is designed for a desktop or a headless build via its own CLI. TLS with a **self-signed certificate**. | **No.** It must run on another machine on the LAN, and Ivy must trust its certificate. |
| **hydroxide** | A third-party Go server ("for power users, designed to run on a server"), unofficial and casually maintained, migrated to Codeberg. Its IMAP is **unencrypted local connections only** and marked work in progress. | Probably buildable on arm64 (Go), but Ivy refuses a plaintext connection. |

`docs/PROTON.md` documents both, with the honest conclusion: **today neither connects to Ivy without a
code change**, because Ivy only speaks implicit TLS to a verified certificate. The Bridge path is
reachable without code by installing the bridge's certificate in the container's trust store; the
hydroxide path would need an explicit plaintext-loopback allowance, which is a security decision for
another day (`7g`, below, not in this chunk).

## Tests and exit

Per stage, test first and watched failing; `make check`; Playwright where a screen changes (the connect
screen and the account settings). The mirror changes add a benchmark on the potato for the list query
with the join, and an idempotent-migration test: running 7a's migration on a copy of a real mirror
leaves every message, tag and membership intact.

**Exit:** a real Gmail account syncs its inbox, its labels and its Sent, archives without creating a
stray label, and sends one message that appears once in Gmail's Sent — all on the dev stack with a fake
Gmail first, then once against the operator's own account as her live check. Proton is documented and
declined, in writing.

## Failure paths (`STANDARDS.md` 4a)

| Input | Bound | Above it |
|---|---|---|
| Mailbox memberships per message | a stated maximum (a message in 200 labels is not a message) | further memberships dropped with a recorded warning; the message is still listed |
| Backfill per account per day | Gmail's published ceilings as the floor, a byte budget as the cap | the backfill pauses and says so; it resumes next day |
| Token refresh | a deadline per call, one retry, then the re-consent banner | no retry loop, account marked `auth-failed` with the reason |
| OAuth callback | state parameter, PKCE, one use, a short expiry, the address it was issued for | refused |
| A label at Gmail's ceiling | detected on the first empty page beyond it | Mirror health says "showing the newest N; Gmail will not show older", not a silent stop |
| A bridge certificate | verified as today | refused with a message naming the trust store, never a toggle to ignore TLS |

## Open questions (ask before 7a and 7c start)

1. **7a's flag simplification:** flags per message (Gmail-like, simpler) rather than per mailbox. Right
   for one operator, wrong if she ever wants a message read in one label and unread in another.
2. **7a's migration on the real mirror:** migrate in place, or accept one resync of the 41-message
   account? In place is the plan; a resync is the escape hatch.
3. **Gmail project ownership:** her own Google Cloud project (the plan) or none, with an app password
   only? The 7c trap makes this a real choice, and it is hers to make.
4. **Proton, for real:** document-and-decline (this chunk) or add 7g, the trusted-local-host plumbing
   (certificate fingerprint pinning, and an explicit plaintext-loopback allowance for hydroxide)? The
   operator asked for "document only", and this plan records that Proton therefore does not connect.
5. **`X-GM-LABELS`:** write Ivy tags into Gmail labels in a later stage, or keep tags local forever for
   Gmail accounts?
