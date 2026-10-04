# Next steps

Working note, not a project doc: where we are, what is next, and the one backlog. History is in
`docs/BUILD-LOG.md`, rules are in `docs/STANDARDS.md` and `CLAUDE.md`, decisions are in
`docs/qa-log.md`, review findings are in `papercuts.md`. It is tracked in git; **update it and commit
it in the same stage as the work**, so the next session can resume after a context clear.

last updated: 2026-10-04 (round 41: 3a is done). The full Go suite is `-race` green, `make check`
(drift, fmt, vet, staticcheck, Go tests, `pnpm check`, 209 Vitest) is green, `govulncheck` is clean,
the mock Playwright suite is 242 passed / 10 skipped and the real-binary smoke slice is 8/8.
Chunks 0, 1 and 2a-2h are done except the visual baselines of 2g and 2h (they need the CI harness
regenerated). The next gate is C3 before 3b/3d; 3b's backend can start once C2 has Claude's
fresh-session review and you clear it.

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
| 3 Sync (backfill, QRESYNC/IDLE, outbox, tags, search, backup, `ivy update`) | 3a done; 3b backend next after C2 review (screens wait on C0) |
| 4 Send (compose, identities, undo send, SMTP + APPEND to Sent) | not started |
| 5 Triage (Jev, the gate and ledger, newsletters, receipts, vision, ask, stats) | not started |

Frontend: SvelteKit 3 app in `web/`, 32 screens. The reader endpoints (`/accounts`, `/inbox`,
`/messages/{id}`, `/summary`, `/mirror/health`, account profile and photo) are real behind
`web/src/lib/api/http.ts`; search, ask, tags, rules, people, checks and reading stay mock-backed
until chunks 3 and 5, and the mock E2E suite fakes the real requests at the network boundary
(`e2e/api.ts`). Mutating actions (archive, delete, tag, save rule, send) only toast until chunks 3
and 4.

Backend: `store/` (two DBs), `config/`, `sync/` (one-shot read fetch), `mime/`, `render/`,
`thread/`, `gateway/` (read API, body/inline/attachment documents, account profile), `internal/*`
(mailworld, devstack, compress, asset), `cmd/` (`ivy`, `ivy-dev`, `ivy-assets`). No `Dockerfile`
or image-publish workflow yet.

## ▶ Now

**1. Finish the review fallout (decided in rounds 32/32b; each test first).** In this order:

1. ~~Move account name/icon/photo to `state.db`~~ **done**: `account_profiles` (state migration 2),
   a once-only copy from the old mirror columns on `Open`, the mirror columns now unused.
2. ~~N11: `allowed_hosts`~~ **done** (#49): checked on every API request; loopback plus the listen
   host are always allowed. `ivy init` writes no config, so it prints guidance instead of prompting.
3. ~~N10/N14: `derived_version`~~ **done** (#50, #51): `SetMessageDerived` writes the derived data and
   the version in one transaction; `Fetcher.Rederive` heals rows behind `sync.DerivedVersion` (200
   per run, newest first). **Bump `DerivedVersion` whenever `mime`, `render` or the part walk
   changes output**; a fingerprint test fails if you forget. The remote-image allow-list work
   (backlog) reuses this to re-render.
4. ~~N12: sticky oldest-wins thread ids~~ **done** (#52, #53): the stored id is sticky and
   account-scoped, so features may key on `thread_id`. This also fixed a sync failure when one email
   reached two accounts.
5. ~~N15: RFC 3339 timestamps~~ **done** (#54): `date` replaces `time` on `MailSummary`,
   `MailMessage` and `SearchHit`; `web/src/lib/time.ts` formats it for the viewer. The account sync
   note followed (#57, N17): `syncedAt` is an instant and `syncLine` words it in the browser.

**Round 32b is fully implemented, and its follow-ups N13, N16 and N17 are closed** (#55 to #57).
**2. 2g is done** (round 33: settings controls, browser photo downscale, spend and calls; the
detail is in `docs/BUILD-LOG.md`). Only the visual baselines remain, once the harness is
regenerated in CI. Seams the backend must honour later: the settings mock (`api/settings.ts`) and
the ledger mock (`mock.makeLedger`, `lib/spend.ts` `summarise`/`pageCalls`) are the contracts for
the chunk 3-5 Go endpoints; the mock ledger marks half the accounts smart-off, so the call log is
heavy with "held back" rows (mock realism only).

**3. 2h is done** (qa-log rounds 34 and 35; the detail is in `docs/BUILD-LOG.md`). Only the visual
baselines remain, with the 2g ones. What it delivered:
- ~~`full` runs a one-shot sync at startup~~ **done**: `devstack.Populate` runs `sync.Fetcher` per
  account before either `up` path serves (the watched child only opens the databases).
- ~~`fast` mirror seeder~~ **done**: `Populate` replays the seeder into a throwaway mailworld
  through `mailworld.WithObserver` and writes each delivery with the sync's own `StoreRaw`,
  `RecordFolder` and `Settle`. `TestFastAndFullAgreeForDemo` compares every mirror and state table
  (masking write times and ephemeral ports) and the spool files. It found two real differences:
  the corpus is bare LF while a fetch returns CRLF (fixed in the seeder), and a server may list
  flags in any order (the store now keeps them as a sorted set).
- ~~`state.db` seed~~ **done**: account names and icons, five tags and their members (placed by
  subject), written after the mirror in both modes, **once per `state.db`** (a `dev.state_seeded`
  setting), so a restart never overwrites what the operator changed. New `store` calls:
  `UpsertTag`, `TagMessage`, `SetSetting`, `GetSetting`. No settings are seeded: nothing reads them.
- ~~`fast` is not fast~~ **settled (qa-log round 35): a built-database cache.** Measured 2026-10-04
  (laptop): seeding costs the same as syncing (`demo` 246 ms full vs 272 ms fast; `large` 100k about
  3m20s), because both pay parse plus two SQLite transactions per message. So an empty data dir is
  now restored by file copy from `.dev/cache/<key>` (profile, seed, accounts, both schema versions,
  `DerivedVersion`); only a clean build is cached, and a bad cache falls back to a visible rebuild.
  **Restore: `demo` 9 ms, `large` 3.5 s** (2.8 GB; the cache doubles the disk use, delete
  `.dev/cache` to reclaim it). `reset` keeps the cache; the first build per profile is still slow.
- **Default port is now 8418** (`config.DefaultListen`; `ivy-dev` uses the same constant). 8787 is
  wrangler's and always taken on the operator's machine. `IVY_SMOKE_PORT` moves the smoke port. The
  smoke test now asserts the seeded mailbox (it still expected the empty one from before `up`
  synced). Vite's 5173 and the preview 4173 are unchanged.
- ~~Named-state Playwright~~ **done**: the states are one file, `internal/devstack/states.json`,
  read by Go and by `web/e2e/named-states.spec.ts` (a Go test checks every named scenario exists in
  `scenario.ts`; the spec checks its screens visit exactly the scenarios the file declares; both
  guards were seen failing on a deliberate typo). One explicit gap: **`send-transient-4xx` has no
  designed screen** (needs a canvas board). Because nothing syncs after startup, a mailworld fault
  changes no screen of the real app until chunk 3's continuous sync and chunk 5's gate exist, so the
  specs use the mock `?scenario=`; add a real-stack twin of each when its consumer lands.
- Left over from 2h: the `window_fetch_in_load` warning now prints on every mock-suite page (it is the
  Frontend backlog item below); `DEV.md` 8 still has unticked items I did not verify this round
  (`make dev` from a clean checkout with the default `--llm live`, the rails, hot reload).

**4. Chunk 3 is split into eight stages (round 36; the plan is in "The chunk plan" below).** 3a
(sync core) is next: 2h is done and every spike has run (S1 to S10, S9 moot; `docs/SPIKES.md`,
write-ups in `docs/spikes/`, qa-log round 30), so nothing gates it. Known partial spikes, none of
which block 3a: S1 send-as scope and Resend DMARC (taken on the operator's report), S5 iPad over
HTTPS only (no iPhone, plain HTTP or `image/heic`), S6 OOXML not run, S4 and S10 on synthetic data.
3h (the deploy track: `Dockerfile`, GHCR publish, `ivy update`) is pulled forward and runs in
parallel because it touches no mail code.

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
- **Disabled-message restore UI and the mass-disable alert** (`ARCHITECTURE.md` 4): backend in
  chunk 3, screens to be designed.
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
reopens; 300 extra convergence seeds on both variants were clean; open items N18-N20).
**Remaining for the operator:** the real-mailbox/`ivy doctor` live check and potato numbers.
**Next gate: C3**, writing down the outbox op states before coding 3d;
3b's backend (disable/restore/purge) can start after C2 is cleared, its screens wait on C0.

## Operator actions still open

- Repo visibility (still private; checklist in `docs/CI.md` 6).
- Bump the local Go toolchain off 1.26.1, which `govulncheck` flags (fixed in 1.26.2+); CI resolves
  the latest patch.
- Lore feature names (deferred); Purelymail follow-ups (auth headers, Resend DMARC, alias/send-as
  scope, seeding the dev mailbox); a Jev labelled corpus for real-mail accuracy.
- Housekeeping: the icon recipe is in `docs/design/brand/README.md`; the stray parent `node_modules`
  noted in round 28.
