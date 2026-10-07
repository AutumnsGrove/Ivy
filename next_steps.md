# Next steps

Working note, not a project doc: where we are, what is next, and the one backlog. History is in
`docs/BUILD-LOG.md`, rules are in `docs/STANDARDS.md` and `CLAUDE.md`, decisions are in
`docs/qa-log.md`, review findings are in `papercuts.md`. It is tracked in git; **update it and commit
it in the same stage as the work**, so the next session can resume after a context clear.

last updated: 2026-10-06. **Chunk 4 (send) is complete: 4a-4h are done.** **4h (rich text)** landed with the decisions settled in qa-log round 66 and `docs/handoffs/2026-10-06-4h-richtext-design.md`: the editor is `squire-rte` (MIT, zero deps, 16.1 KiB brotli, chosen over a 98.3 KiB minimal TipTap), both modes stay with rich as the default and the mode fixed once typed, a `bodyFormat` field is sanitised server-side with a derived `text/plain`, and the paste walker is our own allow-list. `docs/BUILD-LOG.md` has the entry. What is left is the operator's live checks. **4g is done** (outgoing attachments and images; round 65): gate G4 cleared the dependency question — browser-first image preparation, no server decoder and no new dependency, 25 MiB limits, inline `cid:` included. **4f is done** (the compose screen; round 64): the client calls and the new
`/drafts` screen, the From picker over the real identities, People autocomplete, reply/forward
prefills, debounced autosave to the server's Drafts folder (and once on leave), the Sending/Undo
toast, the Not-sent sheet and the resend-free `unconfirmed` notice. The operator chose autosave plus
the new screen, saves debounced and on leave, and the attach sheet kept as a preview that blocks send
until 4g. **4a-4e are done** (`compose/` + `smtp/`, the `send_queue` +
Sent copy, then the send API and undo). `POST /send` builds both copies and queues with a server-side
undo deadline; `POST /send/{id}/undo` cancels before it and hands the draft back. **4d (drafts) is
done** and **4e (identities and reply logic) is done**: the addresses an account may send as and
the reply/reply-all/forward logic live in `state.db` and `compose/`, with the identities API and the
account-settings editor (design at `docs/handoffs/2026-10-06-4e-identities-design.md`, round 63).
**In-app account
setup is built (round 59)**, the last thing before the first install: an account is typed into
`/welcome/account`, tested against Purelymail, stored as a private password file plus a `state.db`
row, and started without a restart; what is left is `sudo ./install.sh` on the board and the live
connect. 3g and 3h are done. 3g: rules as data with a local engine and an ingest pass, local
hide-until snooze, derived People with merges, and Reading as the reserved tag, wired through the
API and the screens. 3h: the container image, the GHCR publish workflow, the host-side update watcher
and `ivy update` (CLI and in-app) with SSE progress. Next is the C0 canvas board; 3b's disabled-mail
screens still wait on it, and chunk 4 (send) is next after that.
Chunks 0, 1 and 2a-2h are done except the visual baselines of 2g and 2h (they need the CI harness
regenerated). 3a and 3b's backend are done, 3c (backups, including the disabled-blob store), 3d (the
outbox and write path), 3e (tags), **3f (search)**, **3g (rules, snooze, People, Reading)** and
**3h (the deploy track)** are done; **the C0 canvas board is the one thing blocking the 3b
screens**.

## How to run a chunk

1. Read `CLAUDE.md`, then `docs/STANDARDS.md` (TDD, test shape, Go standards, failure paths 4a),
   `docs/ARCHITECTURE.md`, and the chunk's own docs. `docs/qa-log.md` rounds 21 onward explain the
   current design.
2. Work **directly on main**, small commits, present-tense subjects under 50 chars, the body says
   why. Commit this file with the work.
3. **TDD, always:** write the test, watch it fail for the right reason, then the minimum code.
   Integration through real boundaries is the bulk; fakes are `internal/mailworld`.
4. **Feed new parsers and walks malformed real-world input, not just well-formed mail** (a bad
   base64 attachment cost a whole message in #48 while every test was green).
5. Edit/Write for file changes, never python/sed. `CGO_ENABLED=0` for builds; `make test` runs
   `-race` with cgo on.
6. At the end of a sub-chunk run `make check` and `pnpm exec playwright test` (in `web/`), fold new
   decisions into the docs, and update this file.

## Where we stand

| Chunk | State |
|---|---|
| 0 Contract + Go skeleton | done |
| 1 Harness (mailworld, ivy-dev, compression, CI, smoke) | done |
| 2a-2f Read: store, fetch, parse, render, thread, gateway | done |
| 2g Frontend reader swap + account customization + settings + spend | done except visual baselines (need CI harness) |
| 2h `state.db` fast seeder + named-state Playwright | done except visual baselines (need CI harness) |
| 3 Sync (backfill, QRESYNC/IDLE, outbox, tags, search, rules, backup, `ivy update`) | 3a done; 3b backend done (screens wait on C0); 3c done; 3d done; 3e done; 3f done; 3g done; **3h done** |
| 4 Send (compose, identities, undo send, SMTP + APPEND to Sent) | **done** (4a-4h; rounds 60-66; see "Chunk 4 stages" and `docs/CHUNK4-BRIEF.md`) |
| 5 Triage (Jev, the gate and ledger, newsletters, receipts, vision, ask, stats) | **planned, not started** (2026-10-07): standing rules in `docs/CHUNK5-BRIEF.md`, one plan per feature in `docs/chunk5/` (5a-5j, foundation first); every plan's open questions are still to be settled with the operator before its stage starts |

Frontend: SvelteKit 3 app in `web/`. The reader endpoints (`/accounts`, `/inbox`,
`/messages/{id}`, `/summary`, `/mirror/health`, account profile and photo), tags (`/tags`, the
`tag`/`untag` outbox actions), **search (`/search`)**, **send and its undo (`/send`)**, **drafts
(`/drafts`)** and **identities** are real behind `web/src/lib/api/http.ts`, and **outgoing
attachments** (stage, serve, free, "From your mail") go through the same client; ask and checks stay
mock-backed until chunk 5, and the mock E2E suite fakes the real requests at the network boundary
(`e2e/api.ts`). Archive, delete, flag, junk and tag are real writes through the outbox; send goes
through the send queue with a server-side undo window; a rule's save and apply are real (3g).

Backend: `store/` (two DBs plus the disabled-blob store), `config/`, `sync/` (runner, IDLE,
QRESYNC deltas, derivation, attachment extraction), `mime/`, `render/`, `thread/`,
`extract/` (tier 0-1 document text), `llm/` (the embeddings gate, providers, vectors; the cost
ledger), `search/` (chunking, embed-once queue, hybrid ranking), `gateway/` (read API, search,
body/inline/attachment documents, account profile, hidden-mail restore/purge, the upload staging and
"From your mail" copy), `backup/` (snapshot,
prune, mirror, restore), `internal/*` (mailworld, devstack, compress, asset, blobstore, lockfile),
`cmd/` (`ivy`, `ivy-dev`, `ivy-assets`). No `Dockerfile` or image-publish workflow yet.

## ▶ Now

**Chunk 4 is done.** 4h (rich text) is the last stage and landed with `squire-rte` (MIT, zero deps,
16.1 KiB brotli, route-split to `/compose`), an explicit `bodyFormat` on the compose/API contract
sanitised server-side with a derived `text/plain`, a mode pill fixed once the body has content, and
our own paste allow-list walker (no DOMPurify). `docs/BUILD-LOG.md` has the entry; the five decisions
are in qa-log round 66. The definition-of-done live checks for send, drafts, attachments and send-as
are the operator's (below). **Next: chunk 5 (triage)** once the operator is ready, and the open
`papercuts`/backlog items.

**4e is done** (2026-10-06; design at the reply gate in
`docs/handoffs/2026-10-06-4e-identities-design.md`, qa-log round 63). State migration 14 adds
`identities`, one address per (account, address) with a display name and a plain-text signature; the
account's own address is merged on read as a synthetic, non-deletable primary, so no seed and no
mirror dependence. `compose/reply.go` is the pure reply, reply-all and forward logic: `Reply-To`
then `From` for the direct target, the original To/Cc minus the operator's addresses for reply-all,
an attribution block for a forward, and the identity chosen from the configured Delivered-To/To/Cc
addresses with an unconfigured one reported for a one-tap add. `GET/PUT /accounts/{id}/identities`,
`DELETE /accounts/{id}/identities/{identityId}`, `GET /messages/{id}/reply?all=` and
`GET /messages/{id}/forward` are real; `POST /send` and the draft save accept any configured
identity as the `From` and default its display name. The account settings screen has a "Send as"
editor. `make check`, `pnpm test` and the account Playwright suite are green. **Next: 4f**, the
compose screen. **The live send-as check per address is the operator's** (below).

**4f is done** (2026-10-06; qa-log round 64; `docs/BUILD-LOG.md` has the entry). The compose screen is
wired to the real send and draft APIs: identities behind the From picker, People autocomplete on
To/Cc, reply/reply-all and forward prefills, debounced autosave to the server's Drafts folder plus a
save on leave, and the Sending, Undo, Not-sent and `unconfirmed` states. A new `/drafts` screen lists
and resumes a draft. The format buttons stay inert until 4h. Wiring 4f surfaced and fixed a 4d gap: `POST /send` now records the draft
version it came from (`draftMessageId`) so a sent copy leaves Drafts, and the reply/forward prefill
now names its account (`accountId`) so the screen loads the right identities. `make check`, `pnpm
test` and the mock Playwright suite (330 passed, 10 skipped, phone and desktop) are green.

**4g is done** (2026-10-06; gate G4 cleared, design at
`docs/handoffs/2026-10-06-4g-attachments-G4.md`, round 65; `docs/BUILD-LOG.md` has the entry). The
operator chose browser-first image preparation, so no server decoder and **no new dependency**: the
browser applies EXIF rotation, downscales and re-encodes via canvas, stripping EXIF/GPS by
construction. Limits are 25 MiB per file / 25 MiB total / 20 attachments, and inline `cid:` images
are in. `internal/blobstore` backs content-addressed staging under `data/uploads/` with a `state.db`
`uploads` table (migration 15); the upload API streams to disk, sniffs and refuses markup and a
denylist of executables and scripts; `compose` builds file and inline parts; send and draft requests
name staged ids and a resume or undo re-materialises fresh staging from the stored MIME. The attach
sheet opens the real pickers, lists "From your mail" and prepares photos in the browser. `make
check`, `pnpm test` (343) and the mock Playwright suite (336 passed, phone and desktop) are green.
**The live send-with-attachment check is the
operator's** (below). **4h followed and is done** (see the top of this section).

**4c is done** (2026-10-06). `compose.undo_delay_seconds` is a global/per-account setting (default 10,
0 = off, clamped to 120). `POST /send` builds the wire and Sent copies, stores the original request
as the draft, and commits the queue row with an `undo_deadline`; a client `id` makes a retried tap
idempotent. `POST /send/{id}/undo` cancels a queued row before the server-side deadline and returns
the draft; at or after it the answer is 409 `too_late`. `GET /send` and `GET /send/{id}` expose the
state, and every change publishes a `send.state` hint. The From must be the account's own address
until 4e brings identities. State migration 11 adds the draft column and the `cancelled` state. See
`docs/BUILD-LOG.md`. **Next: 4e identities.**

**4d is done** (2026-10-06; design at the 4d gate in `docs/handoffs/2026-10-06-4d-drafts-design.md`,
qa-log round 62). State migration 12 adds `drafts`, one immutable row per saved version, committed
with its outbox op; migration 13 adds `send_queue.draft_message_id`/`draft_remove_id`. A dedicated
`draft` outbox op appends the new version and expunges the one it supersedes by `Message-ID` (the
existing `expunge` stays Trash-only), so a replace needs no mirror row and two quick autosaves are
still correct. The list merges the local heads with the account's mirrored Drafts folder, saves are
optimistic-versioned (a stale save is `409 draft_conflict` carrying the newer content), resume
returns the stored compose request or parses a server-only draft, and a send records the version it
came from so its copy leaves Drafts after the `250` (an undo or a permanent failure keeps it).
`GET/POST /drafts` and `GET/DELETE /drafts/{id}` are real. `make check` green. **Next: 4e identities.**

**4b is done** (2026-10-06; gates G2 and G3 in `docs/handoffs/2026-10-06-G2-send-queue-design.md`
and `2026-10-06-G3-send-crash.md`). State migration 10 adds `send_queue`: the whole message (wire and
Sent bodies), an undo-deadline column for 4c, and the states from the design. `submitting` is the
durable may-have-been-sent point before `DATA`; a crash from there is `unconfirmed` and never
resent. The `append` outbox kind files the Sent copy with `\Seen`, searching by `Message-ID` so it
can never file twice, and a failed copy leaves the send `done` with `sent_copy_failed`. `send/` runs
as a third worker per account and publishes `send.state`. Tests cover 12 repeated accept-then-drop
crashes, a crash during `DATA`, a crash after the 250, and the Sent-copy edge cases; see
`docs/BUILD-LOG.md`. **Next: 4c** (the send API and undo).

**4a is done** (2026-10-06; gate G1 in `docs/handoffs/2026-10-06-G1-compose-smtp-tests.md`).
`compose/` is a pure builder (see `docs/BUILD-LOG.md`): every header-bound value is validated, an
outgoing addr-spec must be ASCII (no `SMTPUTF8`), a display name or subject that could be read as an
RFC 2047 word is force-encoded, the operator's markdown is the `text/plain` part and goldmark renders
the HTML alternative, and `Bcc` is an envelope recipient on the wire and a header only on the Sent
copy. `smtp/` is the only transport: one implicit-TLS connection per call, `AUTH PLAIN`, the EHLO
`SIZE` read, per-RCPT classification, and a refused recipient aborts the whole transaction (round
61). Both suites are `-race` green and `make check` is green; the fake gained `SIZE`, `SMTPStall` and
`SMTPRejectRcpt`. **Next: 4b**, the `send_queue` and the Sent `APPEND`, starting from the G2 design.

**3c is done** (round 43). `internal/blobstore` is a content-addressed, de-duplicated, append-only
store under `data/blobs`; `sync` copies a hidden message's bytes there at disable time (mirror
migration 10 records `messages.disabled_blob`, and a backup-time reconcile heals older rows).
`backup/` takes a verified `VACUUM INTO` + zstd snapshot of `state.db`, mirrors the blob store to
every target, and prunes each target only after its verified new snapshot (15 days, floor 10).
`ivy backup` runs one now, `ivy run` schedules one daily at `backup.at` (default 03:00), `ivy
restore <snapshot>` replaces `state.db` in place behind the `ivy.lock` flock that `ivy run` holds,
and `ivy doctor` warns when every target shares the data disk. Tests: `internal/blobstore`,
`internal/lockfile`, `store/blob_test.go`, `sync/blob_test.go`, `backup/backup_test.go`,
`config/backup_test.go`, `cmd/backup_test.go`.

**3d is done** (gate C4 passed; see `docs/handoffs/2026-10-04-C4-outbox-crash.md` and the 3d entry
in `docs/BUILD-LOG.md`). State migration 5 adds the `outbox` table and `store/outbox.go` its state
machine; `sync/outbox.go` drains it FIFO on its own connection, resolves each message's UID at
dispatch, records the resolved identity before any command, and recovers a crashed op by asking the
server what happened rather than guessing; sync defers to a row with a live op (invariant 4,
refreshed per folder). The failure-injection test kills the worker between the ack and the DB write
over 16 messages (plus 8 ack-then-drop seeds) and proves exactly-once; `mailworld` gained an
`AckThenDrop` fault. The HTTP surface (`POST/GET /outbox`, retry, dismiss), the confirm modal for
moves/deletes, the optimistic overlay with rollback and the undo toast are all committed, and one
outbox worker per account is wired into `ivy run`.

**Reader entry points are now in too** (`960cea5`): the read API carries a denormalised `flagged`
column, so the list shows a star and the reader toggles it with no confirmation; the More menu moves
mail to and from Junk through the same outbox; and `/settings/outbox` lists what is waiting with
retry and dismiss. **Empty Trash is done too**: `GET /inbox?folder=trash|archive|junk` serves the
folder views, and the Trash view's confirmed **Empty Trash** button enqueues one `expunge` per
listed message through the same outbox (no second path to erasure). It empties the page on screen,
so a Trash larger than one page needs the button pressed again. 3d has no leftovers.

**3e is done** (rounds 51 and 52; `docs/BUILD-LOG.md` has the entry). A tag is a `flags` outbox op
for `$ivy-<slug>`; the membership follows the server's acknowledgement; a server without `\*`
keeps the tag local-only; sync reads keyword transitions back (source `server`, unknown and
hostile slugs ignored, 32 per message) and re-applies keywords to a rebuilt mailbox; deleting a tag
clears its keyword everywhere first or refuses with `outbox_full`. `/tags` is real, the reader has a
tag picker, and `make check`, the mock Playwright suite (268 passed), `make smoke` (10/10, with a
real-binary tag round trip), `golangci-lint` and the race suite are green. Left for the operator: a
live check against the real Purelymail mailbox (that a keyword written by Ivy shows in another
client and one set there arrives in Ivy), and `BenchmarkTagsForMessagesPage` on the potato.

**3g is done** (round 56; `docs/BUILD-LOG.md` has the entry). Rules are data (`conditions` and
`actions` validated against a closed vocabulary; `rules.Match` is a pure substring test), the
`Settle` pass evaluates the enabled rules scoped to an account over messages it has not seen
(`rule_eval`) and records each match once (`rule_hits`); a new rule reaches old mail only through an
explicit apply after a free dry run. Tag and Reading actions are `$ivy-<slug>` keywords through the
outbox; snooze is local hide-until. Reading is the reserved `reading` tag (created at startup,
undeletable), and the inbox loads active snooze keys and Reading membership from state and excludes
them in the mirror query, so the two databases still never join. People is derived per (address,
account) in `Settle`, with locally owned merges applied when reading; the operator's own addresses
are filtered at read time. The Rules list, manual editor and builder, People list and page and
`/reading` are real. `make check`, the mock Playwright suite (268 passed / 10 skipped) and
`make smoke` (10/10) are green. **Left for the operator:** a live check against the real mailbox of
a rule tagging mail and a snooze waking, and the potato numbers for the rule pass over a large
mailbox.

**3h is done** (round 57; `docs/BUILD-LOG.md` has the entry). The multi-stage `Dockerfile` builds
the frontend and cross-compiles the pure-Go binary, both stages pinned to `$BUILDPLATFORM`, and a
non-root `alpine` runtime serves the embedded, precompressed assets; it was built and booted
locally (arm64) with a healthy healthcheck. `.github/workflows/docker-publish.yml` publishes
`ghcr.io/autumnsgrove/ivy:latest` and the short SHA for amd64+arm64 on merge to main (never on a
PR). A new `update/` package resolves the `:latest` digest from GHCR, waits out an in-progress
publish run before resolving (best-effort, optional `GITHUB_TOKEN`), and writes the host watcher's
signal; `POST`/`GET /api/v1/update` expose it with a single in-process slot and a new `update.state`
SSE hint, `ivy update` runs the same core, and the settings screen shows the real version and
drives the Update button. `compose/watcher/update.sh` (systemd oneshot, unprivileged) pulls the
exact digest, recreates the service, waits for the image healthcheck and rolls back on failure;
`install.sh` installs the units, the hash-pinned root unit-re-sync wrapper and its sudoers rule.
`make check`, the Go `-race` suite and the mock Playwright suite are green; the watcher's happy,
rollback and bad-digest paths were exercised against stubs. **Left for the operator:** the image is
published and public (run 37352785674), so what remains is `sudo ./install.sh` on the board and one
real `ivy update` end to end.

**Second-opinion review of 3e-3h (2026-10-05)** is on branch `review/3e-3h-audit` (about 30 commits on top of
`3d26e01`; `papercuts.md` #95-#117 and N33-N41 have the detail). It fixed what the first install would hit:
the update watcher (invalid failure JSON, a request left behind by any `set -e` death re-running the update
in a loop, a slow first start rolled back, any image accepted), a stale "ok" result after a request the
watcher never ran, semantic search that never ran from the app, attachment text and vectors that reached
only one message, an embed queue that a single blank or refused document could stall, extraction denial of
service, per-account Snooze/Reading bleed, and an empty search index on any mirror that predated it. The
open decisions were settled in round 58 and built on the same branch: paging for the inbox (which had no
"load more" at all), Reading, Snoozed, tag views and People; estimated cost when a provider reports none;
giving up on a document refused five times; one shared search wiring for `ivy run` and `ivy-dev`; a
newer-than-binary database refused; and `docs/DEPLOY.md`. **Merge it to `main` before `sudo ./install.sh`**:
the published `:latest` image predates it. Still the operator's: the live checks in `docs/DEPLOY.md`
section 7, and the settle benchmark on the board (`docs/PERFORMANCE.md` "Settle at rest"), which decides
N36/N38/N40. Still open and unscheduled: search has no `nextCursor` (N42), and the update rollback is only
proved against stubs.

**Next, in order:**

1. **C0 canvas board** for the three 3b screens, whenever the operator is ready; the backend and API
   are already committed.
2. **Chunk 5 (triage)**, after chunk 4. **Chunk 4 (send) is done** (4a-4h; `docs/BUILD-LOG.md`),
   so send can now replace the operator's mail client. 3g leaves the free-form rule compiler for chunk
   5. The live-use issues #7-#15 are now unblocked (operator, 2026-10-06: wait until chunk 4 is
   done): file new feedback, do not fix it first. The one to raise anyway if chunk 4 touches the
   outbox is #10 (a move failing with `message_gone`).

**Deliberately deferred from 3c** (do not re-litigate):

- **S3-compatible backup targets.** The target is a local folder only for now. The later track is a
  **hand-written S3-style client for Ivy's exact use case, not an imported SDK** (operator, round 43).
- **Backup status on the reading surface.** The last successful backup time (`ARCHITECTURE.md` 9) and
  a Mirror-health line for backup failures are not exposed over the API yet; there is no backup
  endpoint in `api/openapi.yaml`. Add them when the settings/health UI next changes.
- **On-disk directory name.** The blob store lives at `data/blobs`; `ARCHITECTURE.md` 9 describes
  the concept without naming it, so the doc is unchanged.

Known partial spikes that still do not block: S1 send-as scope and Resend DMARC (taken on the
operator's report), S5 iPad over HTTPS only (no iPhone, plain HTTP or `image/heic`), S6 OOXML not
run, S4 and S10 on synthetic data.

## Backlog

Grouped by where it lands. Each item names its source so it can be found again.

### Open review findings (`papercuts.md`)

- Re-measure the `References` scan (N13, 51 hostile messages: 4.2 s to 0.15 s on a laptop) on the
  potato; the ratio is the claim, not the milliseconds.
- **N8** identical `Message-ID`s share a content key, and so tags and verdicts; needs a threat-model
  line before chunk 3.
- **N3** the fake `/systemone` has no `score` questions (chunk 5).
- The thread-subject fallback groups unrelated automated mail with generic subjects ("Your
  receipt"); revisit when newsletters and receipts have their own views.

### Features promised in `PLAN.md` that no earlier tracking mentioned

Found by the round 32 audit; each needs a home before its milestone starts.

- **Junk rescue chip and spam score** (`JEV.md` 3B): show the `X-Spam-Status` score, a quiet "looks
  real" chip with a one-tap Not junk (an IMAP move that trains the provider). Chunks 3 (move) and 5
  (the chip).
- **In-app help glossary** with plain-English names (`PLAN.md` 3). Frontend, any time; lore names
  get an entry in the change that adds them.
- **Remote-image allow-list UI**: the render policy takes `AllowRemoteImages` but nothing sets it;
  needs a per-sender setting in `state.db`, a "show remote content" control in the reader (the
  renderer already counts `RemoteBlocked`) and a re-render, which ties to `derived_version`.
- **Disabled-message restore UI and the mass-disable alert** (`ARCHITECTURE.md` 4): the backend and
  API landed in 3b (round 42); the screens are not designed and wait on the **C0** canvas board.
- **Security/abuse and contact-form mail types** (first-class, `PLAN.md` 3): chunk 5.

### Frontend

- Rich-text follow-ups (4h): changing the From identity does not rewrite the signature in rich mode,
  because the body is HTML and the signature swap is a plain-text one; the signature inserted when the
  message started stays. The link button uses `window.prompt`, so a proper inline link editor is later
  polish. A reply-in-place box in the reader is still not built.
- Stub actions are now all real (Update in 3h, send in 4f); keep the list here only for new stubs.
- 3g leftovers: the reader has no snooze or show-in-Reading control (rules and the API only); the
  `folder=snoozed` view and the inbox `tag=` filter are API-only with no screen; `rules/new/review`
  is now a dead mock route (the free-form compiler is chunk 5); and the rule editor, People and
  Reading have no Playwright test beyond the screens pass that visits them. A rule action on a
  message kept in two folders queues the keyword op for the row the pass picked, so read-back can
  drop the membership if only the other copy remains (the N8 nuance tags already accept).
- Tags: a tag on more messages than the outbox headroom (500 per account) cannot be deleted in one
  go (round 52); the follow-up is a soft delete that a sweeper finishes as the queue drains. Tag
  counts include hidden mail. Keywords that predate a tag's creation are not adopted. The tag filter
  on the inbox can now build on `search_index` (3f).
- Desktop keyboard shortcuts and a help overlay; extend the axe pass beyond the eleven screens;
  day-theme review; font subsetting and preload (fonts are 303 KiB against the 60 KiB target) and
  the CDP-throttled timing budgets; real-iPhone safe areas and `100dvh`; collapse-below-minimum list
  drag.
- `+layout.ts` uses `window.fetch`, so SvelteKit logs `window_fetch_in_load`; thread the load-time
  `fetch` through the client.
- The SPA's own CSP needs SvelteKit build-time script hashes (the body document's CSP is done).

### Performance and data

- Whole-view inbox counts scan the inbox each page (~15 ms/op at 10k); a materialised count is the
  follow-up. `threadAccount` rethreads the whole account after every fetch and
  `MessagesForThreading` is unbounded; both are measure-first on the potato.
- At-rest compression of `raw_blob` and attachments, compressed backups and `COMPRESS=DEFLATE`
  toward IMAP arrive with sync (`PERFORMANCE.md` 1).
- Re-measure N4/N5 (static serving, zstd encoder) on the potato.

### Chunk 3 constraints already known (apply to the stages in "The chunk plan")

- `sync/` is not wired to config: the runner must copy `config.Account.TrustedAuthservIDs` into
  `sync.Account`. Purelymail adds no SPF/DKIM/DMARC verdicts (spike S1), so the signal is empty until
  we verify DKIM ourselves.
- Disabling a server-deleted message must keep its row **and its spool file** (nothing is erased).
- A UIDVALIDITY change must disable-and-rebuild the old validity's rows; expunged mail must be
  disabled, not left mirrored.
- Flags changed by other clients arrive through `CHANGEDSINCE`/QRESYNC, not the one-shot fetch.

### Harness, CI and docs

- `--llm live|fake` and the OpenRouter-only-external-host rail (chunk 5); Playwright sharding.
- CI: `bench` (advisory `benchstat`), `nightly.yml` (Firefox E2E, time-boxed fuzzing, fresh
  `govulncheck`/`pnpm audit`, link rot), the manual `live` and `evals` workflows (need protected
  Environments), `docker-publish.yml` plus the `Dockerfile`, the AGPL header check (gated by
  `IVY_LICENCE_HEADERS=1`), path filtering.
- `api/openapi.yaml` covers the read surface and account profile; add cursor pagination and SSE
  event schemas per milestone. A migration-upgrade test from every prior schema version; `sqlc` once
  there are enough queries.
- Doc folds still open: QRESYNC sync, outbox APPEND-to-Sent and PDF extraction tiers. Unprobed:
  Jev real-mail accuracy (needs a labelled corpus), send-as scope, DMARC, iPhone/HEIC behaviour.
- A stray `.kilo/worktrees/tiny-knot` directory (a copy of the repo from the other model's harness)
  sits in the tree untracked; remove it when the operator confirms nothing in it is wanted.
- 3f follow-ups: extraction reads at most 32 MiB into memory (one attachment at a time) rather than
  streaming to a temp file; a changed body under the same content key is not re-embedded (only a
  model change is); the query embedding uses one selected account's provider/model, so a mixed-model
  combined search is keyword-only for the rest; the FTS index has no cursor pagination; and
  `mirror/health` still reports `searchIndex: "Not built yet"` on the mock screen.

## The chunk plan

Order 0, 1, 2 is a hard dependency chain, and 3 (sync) must precede 4 (send) and 5 (triage).
**Reordered 2026-10-03: send is chunk 4 and triage is chunk 5** (send needs no LLM gate, so Ivy can
replace the operator's mail client sooner); 4 and 5 can still be narrowed.

| Chunk | Scope | Docs |
|---|---|---|
| **3** Sync | Split into stages 3a-3h (table below). Highest-risk code: the convergence property test (3a) | `ARCHITECTURE.md` 4/9, `TESTING.md` 2/6 |
| **4** Send | Compose (markdown then rich text), identities/signatures, undo send (delay setting), drafts in the server Drafts folder, Reply-To handling, SMTP + APPEND to Sent, EXIF strip / photo downscale, size checks against the provider | `PLAN.md` 5, `ARCHITECTURE.md` 5 |
| **5** Triage | Jev layer + question registry, the single LLM gate + cost ledger (the embeddings-only piece shipped in 3f), needs-me cascade, categories, newsletters (feed/digest/unsubscribe), receipts/ledger/renewals, vision, Ask Ivy agent loop, full stats panel. Safety assertions are part of done | `JEV.md`, `ARCHITECTURE.md` 6/7, `TESTING.md` 4 |

### Chunk 3 stages (split in round 36)

Approved by the operator on 2026-10-04. This chunk as originally scoped bundled six independent
subsystems and was larger than chunks 1 and 2 together, so it is cut the way 1 and 2 were. Execution
order is the table order (3c, backups, runs before 3d; 3h is a parallel track). Each stage ends with
test-first work seen failing, `make check`, Playwright where a screen changes, the docs folded in,
and this file committed with the work.

| Stage | Scope | Depends | Size |
|---|---|---|---|
| **3a Sync core** | Per-account runner + `sync_state`; resumable envelope-first backfill; IDLE on INBOX; `QRESYNC`/`CHANGEDSINCE` with a UID/flags fallback; UIDVALIDITY change; SSE hub; model-based convergence property test (seeded sequences, shrinking) | 2 | L (may split at the backfill/steady-state line if it proves too big) |
| **3b Disabled-not-deleted** | Expunge/`VANISHED` disable with the row **and** spool kept; re-enable on reappearance; mass-disable alert; Restore/Purge endpoints + Mirror health | 3a | M |
| **3c Backups** | Daily `state.db` `VACUUM INTO` + zstd + verify; prune after 15 days with the floor of 10; targets; one-line restore; `ivy doctor` warnings. **Runs before the outbox exists**, because `outbox`/`send_queue` are the only unrecoverable state | any | S-M |
| **3d Outbox + write path** | `outbox` table; IMAP-first STORE/MOVE/EXPUNGE; retry across restarts; optimistic UI rollback; reader actions (archive, delete, flag, junk move) | 3a, 3b | L |
| **3e Tags both ways** | `$ivy-<slug>` keyword writer + server read-back (source `server`); `/tags` + membership; local-only fallback for servers without arbitrary keywords | 3d | M |
| **3f Search** | FTS5 (+ tokenizer decision); tier 0-1 extraction into `extracted_text`; the `Embedder` interface with OpenRouter default and Ollama optional; **a minimal embeddings-only gate and ledger**; embed-once queue; hybrid RRF; `/search` | 3c/2f | L |
| **3g Rules + snooze + People + reading** | Rules stored as data, header conditions evaluated locally, actions through the outbox; snooze; People; `/reading`. Jev fuzzy conditions stay in chunk 5 | 3e, 3f | M |
| **3h Deploy track** *(parallel, pulled forward)* | `Dockerfile` (frontend + pure-Go cross-build), multi-arch GHCR publish on main, host-side update watcher, in-app Update + SSE progress. Touches no mail code | none | M |

Three decisions recorded with the split:

- **Embeddings gate (3f):** the `Embedder` interface, the per-account opt-in check, the monthly cap
  and one ledger row per call are pulled forward from chunk 5, so "nothing paid is reachable except
  through the gate" holds from the first paid path. Jev, chat and vision stay behind the full gate.
- **Deploy (3h):** its own track, done early or in parallel; it blocks neither sync nor chunk 4.
- **Backups (3c):** before 3d, because the outbox is unrecoverable and must be backed up as soon as
  it exists.

**Chunk 3 is handed over with `docs/CHUNK3-BRIEF.md`** (read it after this file and before any code):
the invariants, the settled decisions (qa-log round 37: move vs delete, sync defers to the outbox,
N8, hints-only SSE), the traps earlier work found, per-stage bounds, and **escalation gates**:
fixed checkpoints C0-C5 and stop-triggers T1-T10, each leaving a committed file in
`docs/handoffs/`. Claude reviews in a separate fresh session, starting from those files. Still open
inside 3a: whether 3a needs the backfill/steady-state split (decide at checkpoint C2; the model may
propose it).

**3a is done** (2026-10-04): the C2 checkpoint is in `docs/handoffs/2026-10-04-C2-runner.md`, and
the steady-state work that followed (sync_state, QRESYNC deltas, the IDLE worker, the `ivy run`
wiring and the frontend events client) is in `docs/handoffs/2026-10-04-3a-steady-state.md`.
`Fetcher.Fetch` reconciles the mirror to the server; `TestSyncConvergesToTheServer` passes on both
variants (24 seeds, 640 operations each, deltas on every sync after the first), QRESYNC falls back
to a full scan without CONDSTORE, and a deliberate break reproduces the C1 red (38 ops shrunk to 3
in 14 replays). The runner writes `sync_state`; the worker idles on INBOX (two connections at most)
with backoff and jitter; `ivy run` starts one worker per configured account; the frontend events
client refetches on a hint. The schema that came with it: mirror migration 9 (`folders.gone_at`,
`messages.uidvalidity` in the identity and spool path, append-only), the `server_removed` reason,
and the uidvalidity-aware fast seeder. `make check`, the mock Playwright suite (242), the smoke
slice (8/8), `govulncheck` and the Go suite were run green; benchmarks are in `sync/bench_test.go`.
**C2's fresh-session review is done** (2026-10-04, `papercuts.md` #58-#60: a revived folder skipped
by the QRESYNC delta, an idle connection that ignored cancellation, no refetch when the events stream
reopens; 300 extra convergence seeds on both variants were clean). Its three open items are fixed
too (#61-#63: a folder-churn convergence mix, a 2-minute stall guard on IMAP commands, and a snapshot
that holds 190 B per message instead of 775). N22 is fixed too (#64): a disabled row is
`pending_classification` until a completed pass settles it to `moved` or `server_removed`.

**3b's backend is done** (round 42: `DisabledStats`, `RestoreMessage`, `RestoreAccountDisabled`,
`PurgeMessage`; Mirror health's `hidden` breakdown; restore/purge endpoints; the mass-disable alert as
`health.alert` `mass_disable`; N21 closed). Tests: `store/disabled_test.go`,
`sync/massdisable_test.go`, `sync/disabled_test.go`, `sync/hidden_test.go`, `gateway/mirror_test.go`.
The Restore/Purge and mass-disable **screens are not designed** and wait on gate **C0** (the operator
asked for a canvas board); the API is written and committed first, per the brief. The API contract is
in `api/openapi.yaml`.

**3c is done** (round 43: `internal/blobstore`, `internal/lockfile`, `backup/`, mirror migration 10,
the sync disable-path copy, `ivy backup`/`restore`, the daily scheduler, the `doctor` warning, all in
`docs/BUILD-LOG.md`). The decisions are in qa-log round 43; **N24 is fixed** (round 44): purge now
erases the blob locally and from every backup target when no row shares it, with a durable
retry for an offline target (marker files in `data/pending-blob-deletions/`, so a restore of an older
snapshot cannot forget one; round 45). The review of 3b and 3c (`papercuts.md` #68-#72) fixed five
defects and closed N25-N29: moves no longer count toward the mass-disable alert, a backup runs on start
when a daily slot was missed, and the go-imap fork is at `v2.0.0-beta.8-ivy.3` (clone at
`~/Documents/Projects/go-imap`).

**Remaining for the operator:** the real-mailbox live check of the daily backup and `ivy restore`,
and the potato numbers for the snapshot time.
**3f is done** (rounds 54-55; `docs/BUILD-LOG.md` has the entry). `extract/` reads tier 0-1 text
(PDF via `ledongthuc/pdf`, OOXML via stdlib) bounded by size, output, pages and a 20 s timeout and
records every outcome; mirror migrations 13-14 add `extracted_text`, the FTS5 `search_index`
(`unicode61 remove_diacritics 2`, chosen by measurement) and `embeddings`; state migration 6 adds the
`api_calls` ledger and `api_caps` counters. `llm.Gate` is the single chokepoint (unexported
providers, one ledger row per input with exact cost, an architecture test against a second path),
`search.EmbedWorker` embeds each content key once behind the configured provider, and
`gateway.handleSearch` fuses BM25 with cosine by RRF and falls back to keyword-only when the
provider is off, capped or down. `/search` is real in the web client, embeddings are configurable in
`ivy.yaml` (`llm.openrouter_base`, `embed_model`, `ollama_url`, `monthly_cap_usd` default $5;
per-account `embed_provider: openrouter|ollama`), and `make check`, the Go suite and the mock
Playwright suite (268 passed, 10 skipped) are green. **Left for the operator:** a live hybrid-search
check against the real mailbox and the embeddings bill, and potato numbers for extraction and the
vector scan (the real provider needs `OPENROUTER_API_KEY` in `.env`).

### Chunk 4 stages (split in round 60)

Approved by the operator on 2026-10-06. Cut the way chunk 3 was: execution order is 4a, 4b, 4c, then 4d
and 4e in either order, then 4f, 4g, 4h. **The smallest set that can replace Apple Mail is 4a-4f.** The
standing instructions, invariants, traps, per-stage bounds and escalation gates (G1-G5, triggers
T11-T13 on top of chunk 3's T1-T10) are in `docs/CHUNK4-BRIEF.md`; read it after `CLAUDE.md` and
`docs/STANDARDS.md`. The owner column is a suggestion by risk, so the gates show what DeepSeek can do.

| Stage | Scope | Depends | Size | Risk | Suggested owner |
|---|---|---|---|---|---|
| **4a Builder and submit** | `compose/`: build the RFC 5322 message with enmime (injected `Message-ID`/`Date`, reply headers, `Bcc` only on the envelope, plain text plus markdown HTML); implicit-TLS SMTP submit with `SIZE`, 4xx/5xx, deadlines; header-injection corpus first | none | M | Medium | DeepSeek, tests reviewed at G1 |
| **4b Send queue and Sent copy** | `send_queue` migration; queued, submitting, submitted, appended, done, failed, `unconfirmed`; the `APPEND` to Sent (`\Seen`), retried without resending; crash-window tests; extend `mailworld` SMTP with accept-then-drop | 4a | L | **High** | Claude designs (G2), DeepSeek implements, Claude reviews (G3) |
| **4c Undo send and send API** | `POST /send`, undo, status, `send.state` SSE, `compose.undo_delay_seconds`; the deadline server-side | 4b | M | Medium | DeepSeek |
| **4d Drafts** | Autosave to the server's Drafts folder through the outbox (append, then expunge the old), list, resume | 3d | M | Medium | DeepSeek, with a gate on the outbox invariants |
| **4e Identities and reply logic** | Per-address identities and signatures in `state.db`; reply and reply-all recipient logic, `Reply-To`; forward; live send-as check per address | none | M | Low | DeepSeek, solo |
| **4f Compose screen** | Wire the existing mock: markdown editor, People autocomplete, From picker, reply and forward, the Sending, Undo, Not-sent and `unconfirmed` states | 4c, 4d, 4e | L | Low-Medium | DeepSeek |
| **4g Outgoing attachments and images** | Streamed uploads, size and type limits, EXIF strip, downscale, HEIC, inline `cid:`, "From your mail" copy; any new dependency is gate G4 | 4f | L | **High** | Claude leads decoders and dependencies, DeepSeek the UI |
| **4h Rich-text editor** | The second editor after markdown, `squire-rte` (MIT, zero deps, 16.1 KiB brotli, route-split); an explicit `bodyFormat` sanitised by the same builder; must work on iOS Safari | 4f | M | Medium | DeepSeek, later |

Checkpoints: **G1** (4a, injection corpus failing before the builder), **G2** (4b, design before code),
**G3** (4b, crash tests pass), **G4** (4g, before any image dependency), **G5** (end of chunk, Claude's
Review). **4a-4h are done**: `compose/`/`smtp/`, the `send_queue` with the Sent copy, the send API
with undo, the server Drafts folder, the identities and reply logic, the compose screen and
`/drafts`, outgoing attachments and images, and the rich-text editor with `bodyFormat`. Chunk 4 is
complete; **G5 (the end-of-chunk review by a fresh session) is the one process step still open**, and
what remains besides it is the operator's live checks.

## Operator actions still open

- First install (round 59): the image is published and **the mailbox is now connected from the app**,
  so `data/` needs only `ivy.yaml` with `allowed_hosts` (and `OPENROUTER_API_KEY` in `.env` for Smart
  features). Follow `docs/DEPLOY.md`; record the live connect, a restart that keeps the account, and one
  real `ivy update` here. Follow-ups, deliberately not built: other providers as plain host fields behind
  the SSRF guard; Smart features taking effect without a restart (the embed pipeline is built at
  startup); removing an account from the app; `ivy doctor` checking `data/secrets` modes; the connect
  endpoint is open to the tailnet by the operator's choice, so revisit it when auth lands.
- Deploy (3h): **the image is published.** Run 37352785674 (commit `3d26e01`) succeeded and
  `ghcr.io/autumnsgrove/ivy:latest` resolves anonymously to `sha256:e6fdc57c…`, so the package is
  public and the potato needs no `docker login`. **Left:** on the board, `sudo ./install.sh` and one
  real `ivy update` end to end, recording the result.
- Send, drafts, attachments and rich text (4a-4h): the API and the compose screen are real, so the
  live check can run. Then, against a mailbox the operator owns: send to self and see the Sent copy in
  Apple Mail; save a draft (or let it autosave) and see it in Apple Mail; edit it there and resume it
  in Ivy; confirm a sent draft leaves Drafts; and send-as per address. For 4e, add each alias on `/settings/account`
  and do the **live send-as check**: send from the alias to an address the operator owns and confirm
  the provider accepts the `From` (Purelymail send-as works but its scope is unprobed, spike S1c) and
  the message and Sent copy are right. An alias the provider refuses is a provider setting to fix,
  not an Ivy bug; record what each address does. **For 4g:** send a photo and a PDF (and an inline
  image) to an address the operator owns, confirm the recipient and the Sent copy carry them, confirm
  the phone picker still hands back JPEG, and check "From your mail" against the real mailbox. If the
  deny list blocks a type the operator actually sends, that is a one-line change. **For 4h:** send a
  formatted message (bold, a list, a link) from the rich editor and confirm the recipient and the Sent
  copy render it and the plain-text alternative reads cleanly; switch a fresh message to Markdown and
  back; check the editor on the real iPhone (the deferrable stage's whole point).
- Repo visibility: **public** now (was private). The remaining `docs/CI.md` 6 items (gitleaks over
  full history, branch ruleset + required checks, secret scanning) are the operator's checklist.
- Bump the local Go toolchain off 1.26.1, which `govulncheck` flags (fixed in 1.26.2+); CI resolves
  the latest patch.
- Lore feature names (deferred); Purelymail follow-ups (auth headers, Resend DMARC, alias/send-as
  scope, seeding the dev mailbox); a Jev labelled corpus for real-mail accuracy.
- Housekeeping: the icon recipe is in `docs/design/brand/README.md`; the stray parent `node_modules`
  noted in round 28.
