# Chunk 4 brief (send)

Read this **after** `CLAUDE.md`, `docs/STANDARDS.md` and `next_steps.md`, and before writing any
code. It is the standing instruction for chunk 4, written the way `docs/CHUNK3-BRIEF.md` was for
chunk 3: the decisions already made, the invariants that must hold, the traps earlier work found, and
the points where you must stop and hand the work to Claude. `docs/TESTING.md` is the test spec; this
file adds to it. **Read `docs/CHUNK3-BRIEF.md` too**: its invariants (IMAP first, nothing erased,
hostile input, append-only migrations), its escalation machinery and its definition of done carry
over unchanged unless this file says otherwise. If this file and another doc disagree, stop (gate T4)
and ask.

Chunk 4 is moderate overall (about 6 of 10) with two stages that are not: **4b (9)**, because SMTP
cannot be asked what happened after it answers, and **4g (8)**, because it decodes hostile images.
The plan is the stage table in `next_steps.md`. Work directly on main, one stage at a time, in the
order 4a, 4b, 4c, then 4d and 4e in either order, then 4f, 4g, 4h. Do not start a stage before the
one it depends on is finished and its checkpoint (below) is cleared.

## 1. Invariants (a violation is a bug even if every test is green)

1. **Nothing is submitted without a `send_queue` row**, and a row is not `submitted` until the SMTP
   server has answered `250` for the whole transaction. There is no code path that opens an SMTP
   connection except the queue's sender.
2. **Nothing is submitted twice.** A message is handed to the SMTP server at most once per queue row.
   A retry is allowed only from a state that proves nothing was accepted (a `4xx`, a connection that
   failed before `DATA` ended). A crash or a dropped connection at or after the end of `DATA` is
   **unknown**, not failed: the row goes to `unconfirmed`, is never resent automatically, and is shown
   to the operator as "may have been sent, check Sent" (decided, round 60).
3. **Nothing is submitted inside the undo window, and the window is server-side.** The deadline is a
   column on the row. A closed tab, a lost connection or a restart neither sends early nor loses the
   message. Undo before the deadline removes the row's pending state and returns the draft; after the
   deadline there is no undo (the toast says so).
4. **The Send button is the explicit confirmation** (CLAUDE.md rule 6). Nothing else ever sends:
   not a rule, not Jev, not Ask Ivy, not a retry of a different message.
5. **Unsent work is locally owned state.** `send_queue` and the draft bodies in progress live in
   `state.db`, are backed up, and are never rebuilt from IMAP (CLAUDE.md rule 5).
6. **The Sent copy is a second, separate write.** Purelymail does not file one (spike S1), so after a
   successful submit Ivy `APPEND`s its own copy to the Sent folder, flagged `\Seen`. A failed `APPEND`
   is retried **without resending**; before each retry search Sent by `Message-ID` so a lost
   acknowledgement does not create a second copy. This goes through the outbox like any IMAP write.
7. **`Bcc` never reaches another recipient.** It is an envelope recipient only; the transmitted
   message has no `Bcc` header. The copy appended to Sent keeps it (so the operator can see it).
8. **Outgoing headers are built, never concatenated.** Every operator- or sender-supplied value that
   reaches a header (subject, display name, address, `In-Reply-To`, `References`) is validated and
   encoded by the builder; a CR, LF or NUL in any of them is rejected, not stripped.
9. **No `SMTPUTF8`** on Purelymail (spike S1). An address with a non-ASCII local part or domain is
   refused with a clear message before anything is queued. A display name may be non-ASCII
   (RFC 2047 encoded).
10. **Everything bounded** (STANDARDS 4a): recipient count, subject length, body size, total size
    against the provider's `SIZE` (about 48.8 MiB on Purelymail, read live from the `EHLO`
    response), attachment count, upload time, SMTP command and `DATA` deadlines. Each has a defined
    outcome above it, and the failure is shown, never silent.
11. **Append-only migrations.** `send_queue` and the identities table are new migrations; never edit
    an existing one.
12. **Secrets never leave the process in logs, errors or API bodies** (the mailbox password reaches
    SMTP `AUTH` only; round 59 keeps it in `data/secrets`).

## 2. Decisions that are settled (do not re-litigate; `docs/qa-log.md` round 60 has the reasoning)

- **Crash after SMTP accepts is never auto-resent** (invariant 2). The row becomes `unconfirmed`; the
  operator decides. A duplicate email to a real person costs more than a retry tap.
- **Markdown renders to HTML at send time** and goes out as `multipart/alternative` with a plain-text
  part. **The library is goldmark, pre-approved (round 60) and already in `docs/STACK.md`**, so
  importing it is not a T7 stop. Use its default safe configuration: never `html.WithUnsafe()`, and
  link targets limited to `http`, `https` and `mailto`. Its output is for recipients' mail clients,
  never the reader. `go get` it in 4a; record the version in the BUILD-LOG entry.
- **Undo-send is a setting**, `compose.undo_delay_seconds`, default 10, 0 = off, with an upper bound,
  global and per account (ARCHITECTURE 8).
- **Drafts live in the server's Drafts folder** (visible in Apple Mail): `APPEND` with `\Draft`, and a
  replace is an APPEND of the new version then an expunge of the old through the outbox. The UID
  changes on every save, so nothing may key on it (use the draft's own id and its `Message-ID`).
- **Replies default to the address the mail was sent to** (identities and signatures per address),
  `Reply-To` is honoured (the contact-form case works underneath with no extra UI), and a reply says
  "Replying to name@domain.com".
- **Compose is markdown first, rich text later** (4h, deferrable). The attachment limits, EXIF strip
  by default, optional downscale and the "From your mail" server-side copy are as in ARCHITECTURE 5.
- **SMTP is implicit TLS on 465** with `AUTH PLAIN`, exactly like IMAP's implicit TLS on 993. A
  plaintext path exists only for the loopback fake (`Insecure`), named for the unsafe thing, as in
  `sync.Account`.
- **Send-as from a routed alias works (S1c); its exact scope is unprobed.** Do not assume an
  arbitrary From is accepted; the operator verifies live per address (4e's exit).

## 3. Traps found by earlier work

- **SMTP is not IMAP.** An IMAP op can ask the server "is it already in Archive?" and recover. SMTP
  cannot: after the last `DATA` byte there is no way to look the message up. Do not design a recovery
  that pretends otherwise. The only evidence is the Sent folder, and Purelymail does not write to it.
- **`mailworld`'s SMTP** records accepted messages (`World.Sent`), delivers local recipients into the
  IMAP inboxes, and has `SMTPReject` and `SMTPAuthFail` faults. It has **no** "accept, then drop the
  connection" fault and may not advertise `SIZE`: extending it is part of 4a and 4b, and the crash
  tests are worthless without it.
- **Reuse the outbox's patterns, not its table.** `store/outbox.go` and `sync/outbox.go` are the model
  for durable points, idempotency and crash recovery (round 37, `docs/handoffs/2026-10-04-C4-outbox-crash.md`),
  but sending is a different state machine with a different failure meaning. Do not add a `send` kind
  to the IMAP outbox.
- **The Sent `APPEND` and the drafts replace use the existing outbox** because they are IMAP writes.
  Keep the two queues' responsibilities apart: `send_queue` owns SMTP, the outbox owns IMAP.
- **A MOVE changes the UID** (chunk 3). A draft replace and a Sent copy create new UIDs; key on the
  `Message-ID` and the content key.
- **Dates and ids come from the injected clock and id source.** A test must be able to fix them; a
  `Message-ID` is generated once per queue row and reused by every retry and the Sent copy.
- **The sandboxed reader never renders what you are composing.** Compose previews are the operator's
  own markdown; do not route a draft through the inbound sanitiser path or the other way round.
- **enmime's builder** emits `multipart/mixed` and `multipart/related`; check its output for header
  folding and RFC 2047 on hostile names with a test, do not trust it blindly.
- **HEIC.** iOS Safari normally hands back JPEG for `accept="image/*"`. Verify on the real phone
  before adding any decoder (STACK.md); the pure-Go fallback is a WASM decoder under wazero.
- **Never run the 100k `large` profile**, and no python or sed scripts for edits (CLAUDE.md).
- **Real mail, real recipients.** Live tests send only to addresses the operator owns. Never send
  from the dev mailbox to a third party.

## 4. Stage bounds

For each stage: the scope is in `next_steps.md`; below is what must also be true.

- **4a Builder and submit.** The injection corpus (CR, LF, NUL, very long, RFC 2047 edge cases, a
  display name containing an address, duplicate headers) is written and failing **before** the
  builder (gate G1). `Message-ID`, `Date` and boundary ids are injected. `Bcc` handling per
  invariant 7. SMTP submit with an `EHLO`-read `SIZE` check, 4xx (transient, retry) versus 5xx
  (permanent) classification, a deadline on every command and on `DATA`, and cancellation. Tests
  against `mailworld`: success, `SMTPReject` 4xx and 5xx, auth failure, a stalled peer, a recipient
  refused among several, an oversized message. No queue yet: this stage is a package and its tests.
- **4b Send queue and Sent copy.** A half-page design **before coding** (gate G2): the states, which
  step is durable, the idempotency key, and exactly what happens on a crash before `MAIL FROM`,
  during `DATA`, after `250` and before the DB write, and after the submit and before the `APPEND`.
  The failure-injection test kills the worker at each of those points repeatedly and asserts: no
  message sent twice, none lost silently, every message is `done`, `failed` or `unconfirmed`, and the
  Sent copy exists exactly once (gate G3). The state machine includes the undo deadline's column even
  though 4c builds its API.
- **4c Undo send and the send API.** `POST /send`, `POST /send/{id}/undo`, status, the
  `send.state` SSE hint, the setting and its bounds. Undo at the deadline boundary (the second
  before, the second after) is tested with the fake clock. A restart during the window resumes it.
- **4d Drafts.** Autosave to the server's Drafts folder through the outbox; replace is append then
  expunge; resume from the list; a draft that was sent leaves Drafts. Two tabs editing one draft do
  not corrupt it (last write wins, the loser is told). Apple Mail sees the draft (live check).
- **4e Identities and reply logic.** Identities and signatures in `state.db`, keyed by account and
  address; reply and reply-all recipient computation (the address mail was sent to, `Reply-To`, the
  operator's own addresses removed, `List-Post` left out of v1); forward. Branchy and pure: table
  tests with hostile and odd header sets. The live send-as check per address closes this stage.
- **4f Compose screen.** Wire the existing mock (`web/src/routes/compose`, the attach and not-sent
  sheets). The Sending, Undo and Not-sent states, the `unconfirmed` state with its plain wording,
  People autocomplete from the real People data, a From picker that offers only identities that
  exist. Playwright on phone and desktop, including a send that fails and keeps the draft.
- **4g Outgoing attachments and images.** Uploads stream to temp storage on disk and are never held
  whole in memory; size, count and type limits with defined outcomes; EXIF and location stripped by
  default; downscale; inline `cid:`; "From your mail" copies a stored attachment server-side. A new
  dependency or decoder is gate **G4** (T7): the choice and its entry in `STACK.md` come first, and
  the operator checks HEIC on the real phone. Decoders get a fuzz corpus of truncated and hostile
  images.
- **4h Rich-text editor.** Optional and deferrable. Output must still go through the same builder and
  sanitiser for outgoing HTML; the editor must work on iOS Safari (the operator's main device).

## 5. Escalation gates

The same machinery as `docs/CHUNK3-BRIEF.md` section 5: a reviewer starts from `CLAUDE.md`,
`next_steps.md`, this file and `docs/handoffs/`, so every checkpoint and stop is a **committed file**
(`docs/handoffs/<date>-<gate>-<slug>.md`), with a "waiting at G2" line in `next_steps.md` and one
sentence to the operator. The tests are the spec, so a review is of the tests and the design.

| # | When | What you bring |
|---|---|---|
| G1 | 4a: the injection corpus and the SMTP failure tests are written and seen failing for the right reason, **before the builder** | The corpus, the oracle (what "a valid, safe header" is computed from) and the failing output |
| G2 | 4b: **before coding the queue** | The half-page design above, including the `unconfirmed` state and what the operator sees |
| G3 | 4b: the crash-window tests pass | The failure-injection test, how many kill points and repetitions, and its output |
| G4 | 4g: **before choosing any image dependency** | What is needed, what stdlib covers, the `STACK.md` entry, and the phone's actual HEIC behaviour |
| G5 | End of chunk | The whole chunk, for Claude's review with the review skill |

**Triggers.** Chunk 3's T1-T10 apply unchanged. Chunk 4 adds:

- **T11** Any path where a message could be submitted twice, submitted without a `send_queue` row,
  submitted inside the undo window, or submitted without the operator's Send.
- **T12** Any path where a `Bcc` recipient could appear in a transmitted header, or a header value
  could carry an unvalidated CR, LF or NUL.
- **T13** The need to resend a message in `unconfirmed`, or any design that treats "unknown" as
  "failed" or as "sent".

**How to stop** is as in chunk 3: a handoff file that makes sense to someone who has seen none of the
session, committed, a line in `next_steps.md`, one message beginning `STOP: this needs Claude:`, and
no other work until Claude has answered.

## 6. Definition of done (every stage)

As chunk 3's section 6. In addition, any stage that touches SMTP, the queue, drafts or Sent needs a
**live check on the board with a mailbox the operator owns** before it is called done (send to self,
the Sent copy visible in Apple Mail, a draft visible in Apple Mail, send-as per address), recorded in
`next_steps.md`. Live checks that cannot be run by the agent are their own pending line, never folded
into a stage marked done.
