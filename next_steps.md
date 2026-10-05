# Next steps

Working note, not a project doc: where we are, what is next, and the one backlog. History is in
`docs/BUILD-LOG.md`, rules are in `docs/STANDARDS.md` and `CLAUDE.md`, decisions are in
`docs/qa-log.md`, review findings are in `papercuts.md`. It is tracked in git; **update it and commit
it in the same stage as the work**, so the next session can resume after a context clear.

last updated: 2026-10-04 (3d outbox and write path are done; C4 passed and the rest of the stage —
HTTP surface, confirm modal, optimistic overlay, undo — is committed. Next is 3e).
Chunks 0, 1 and 2a-2h are done except the visual baselines of 2g and 2h (they need the CI harness
regenerated). 3a and 3b's backend are done, 3c (backups, including the disabled-blob store) is done,
and 3d is done apart from a few UI entry points listed below; **the C0 canvas board is the one thing
blocking the 3b screens**.

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
| 3 Sync (backfill, QRESYNC/IDLE, outbox, tags, search, backup, `ivy update`) | 3a done; 3b backend done (screens wait on C0); 3c done; **3d done** (a few reader entry points remain); 3e next |
| 4 Send (compose, identities, undo send, SMTP + APPEND to Sent) | not started |
| 5 Triage (Jev, the gate and ledger, newsletters, receipts, vision, ask, stats) | not started |

Frontend: SvelteKit 3 app in `web/`, 32 screens. The reader endpoints (`/accounts`, `/inbox`,
`/messages/{id}`, `/summary`, `/mirror/health`, account profile and photo) are real behind
`web/src/lib/api/http.ts`; search, ask, tags, rules, people, checks and reading stay mock-backed
until chunks 3 and 5, and the mock E2E suite fakes the real requests at the network boundary
(`e2e/api.ts`). Mutating actions (archive, delete, tag, save rule, send) only toast until chunks 3
and 4.

Backend: `store/` (two DBs plus the disabled-blob store), `config/`, `sync/` (runner, IDLE,
QRESYNC deltas), `mime/`, `render/`, `thread/`, `gateway/` (read API, body/inline/attachment
documents, account profile, hidden-mail restore/purge), `backup/` (snapshot, prune, mirror, restore),
`internal/*` (mailworld, devstack, compress, asset, blobstore, lockfile), `cmd/` (`ivy`, `ivy-dev`,
`ivy-assets`). No `Dockerfile` or image-publish workflow yet.

## ▶ Now

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

**Next, in order:**

1. **3e Tags both ways**: `$ivy-<slug>` keyword writer through the outbox and server read-back.
2. **C0 canvas board** for the three 3b screens, whenever the operator is ready; the backend and API
   are already committed.
3. **3f-3h** per the chunk plan below. 3h (the deploy track) is independent and can run at any point.

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

- Wire the stub actions (archive, delete, tag, save rule, delete tag, Update, "Try on recent mail",
  send) to real calls with IMAP-first writes and undo (chunks 3 and 4).
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
**Next stage: 3e (tags both ways)**, which writes `$ivy-<slug>` keywords through the outbox built in
3d.

## Operator actions still open

- Repo visibility (still private; checklist in `docs/CI.md` 6).
- Bump the local Go toolchain off 1.26.1, which `govulncheck` flags (fixed in 1.26.2+); CI resolves
  the latest patch.
- Lore feature names (deferred); Purelymail follow-ups (auth headers, Resend DMARC, alias/send-as
  scope, seeding the dev mailbox); a Jev labelled corpus for real-mail accuracy.
- Housekeeping: the icon recipe is in `docs/design/brand/README.md`; the stray parent `node_modules`
  noted in round 28.
