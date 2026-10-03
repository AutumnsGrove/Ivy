# Next steps

Working note, not a project doc: where we are, what is next, and the one backlog. History is in
`docs/BUILD-LOG.md`, rules are in `docs/STANDARDS.md` and `CLAUDE.md`, decisions are in
`docs/qa-log.md`, review findings are in `papercuts.md`. It is tracked in git; **update it and commit
it in the same stage as the work**, so the next session can resume after a context clear.

last updated: 2026-10-03 (round 32, cleanup phase). Chunks 0, 1 and 2a-2f are done; 2g is nearly
done. Baseline at this date: `make check` green (Go `-race` with cgo, golangci-lint, drift,
svelte-check, 132 Vitest), Playwright 150 passed / 8 viewport-conditional skips.

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
| 2g Frontend reader swap + account customization | nearly done: settings/stats skeletons and visual baselines left |
| 2h `state.db` fast seeder + named-state Playwright | not started |
| 3 Sync (backfill, QRESYNC/IDLE, outbox, tags, search, backup, `ivy update`) | not started |
| 4 Send (compose, identities, undo send, SMTP + APPEND to Sent) | not started |
| 5 Triage (Jev, the gate and ledger, newsletters, receipts, vision, ask, stats) | not started |

Frontend: SvelteKit 3 app in `web/`, 32 screens. The reader endpoints (`/accounts`, `/inbox`,
`/messages/{id}`, `/summary`, `/mirror/health`, account profile and photo) are real behind
`web/src/lib/api/http.ts`; search, ask, tags, rules, people, checks and reading stay mock-backed
until chunks 3/4, and the mock E2E suite fakes the real requests at the network boundary
(`e2e/api.ts`). Mutating actions (archive, delete, tag, save rule, send) only toast until 3/5.

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
5. N15: RFC 3339 timestamps in the API, formatted in the browser (a contract change; update
   `openapi.yaml`, regenerate, the fixture and the screens).

**2. Finish 2g.** The settings and stats-panel skeletons need **intentional design** (no design
exists): Q&A first. Then the visual baselines, once the harness is regenerated in CI.

**3. 2h.** The `state.db` fast seeder, `ivy-dev --mode fast`, the `full == fast` agreement test and
named-state Playwright (`DEV.md` 4, 8).

**4. Chunk 3**, once the gating spikes (`docs/SPIKES.md`) are confirmed.

## Backlog

Grouped by where it lands. Each item names its source so it can be found again.

### Open review findings (`papercuts.md`)

- **N13** `References` is honoured without a cap (linear, 20 µs per id per message). Cap it
  (recommendation: first id plus the last 128) and add the `STANDARDS.md` 4a row; re-measure on the
  potato first.
- **N8** identical `Message-ID`s share a content key, and so tags and verdicts; needs a threat-model
  line before chunk 3.
- **N3** the fake `/systemone` has no `score` questions (chunk 5).
- The thread-subject fallback groups unrelated automated mail with generic subjects ("Your
  receipt"); revisit when newsletters and receipts have their own views.

### Features promised in `PLAN.md` that no earlier tracking mentioned

Found by the round 32 audit; each needs a home before its milestone starts.

- **Junk rescue chip and spam score** (`JEV.md` 3B): show the `X-Spam-Status` score, a quiet "looks
  real" chip with a one-tap Not junk (an IMAP move that trains the provider). Chunks 3 (move) and 4
  (the chip).
- **In-app help glossary** with plain-English names (`PLAN.md` 3). Frontend, any time; lore names
  get an entry in the change that adds them.
- **Remote-image allow-list UI**: the render policy takes `AllowRemoteImages` but nothing sets it;
  needs a per-sender setting in `state.db`, a "show remote content" control in the reader (the
  renderer already counts `RemoteBlocked`) and a re-render, which ties to `derived_version`.
- **Disabled-message restore UI and the mass-disable alert** (`ARCHITECTURE.md` 4): backend in
  chunk 3, screens to be designed.
- **Account photo size and format**: the 5 MiB limit will reject some iPhone photos and HEIC is
  refused. Decide on client-side downscale to a small square before upload (design call, with the
  settings work).
- **Security/abuse and contact-form mail types** (first-class, `PLAN.md` 3): chunk 5.

### Frontend

- Wire the stub actions (archive, delete, tag, save rule, delete tag, Update, "Try on recent mail",
  send) to real calls with IMAP-first writes and undo (chunks 3/5).
- Desktop keyboard shortcuts and a help overlay; extend the axe pass beyond the eleven screens;
  day-theme review; font subsetting and preload (fonts are 303 KiB against the 60 KiB target) and
  the CDP-throttled timing budgets; real-iPhone safe areas and `100dvh`; collapse-below-minimum list
  drag; settings gaps (undo-send delay, photo size, remote images, digest time).
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

### Chunk 3 constraints already known

- `sync/` is not wired to config: the runner must copy `config.Account.TrustedAuthservIDs` into
  `sync.Account`. Purelymail adds no SPF/DKIM/DMARC verdicts (spike S1), so the signal is empty until
  we verify DKIM ourselves.
- Disabling a server-deleted message must keep its row **and its spool file** (nothing is erased).
- A UIDVALIDITY change must disable-and-rebuild the old validity's rows; expunged mail must be
  disabled, not left mirrored.
- Flags changed by other clients arrive through `CHANGEDSINCE`/QRESYNC, not the one-shot fetch.

### Harness, CI and docs

- `fast` mode and the `full == fast` agreement test (2h); `--llm live|fake` and the
  OpenRouter-only-external-host rail (chunk 5); named-state Playwright and sharding.
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
| **3** Sync | Backfill, QRESYNC/IDLE steady state, write path + outbox, disabled-not-deleted with mass-disable alert, tags both ways (`$ivy-<slug>` keywords), rules/snooze, attachments + tier 0-1 extraction, FTS5 + OpenRouter embeddings + hybrid search, People, daily `state.db` backups, `ivy update`. Highest-risk code: the convergence property test | `ARCHITECTURE.md` 4/9, `TESTING.md` 2/6 |
| **4** Send | Compose (markdown then rich text), identities/signatures, undo send (delay setting), drafts in the server Drafts folder, Reply-To handling, SMTP + APPEND to Sent, EXIF strip / photo downscale, size checks against the provider | `PLAN.md` 5, `ARCHITECTURE.md` 5 |
| **5** Triage | Jev layer + question registry, the single LLM gate + cost ledger, needs-me cascade, categories, newsletters (feed/digest/unsubscribe), receipts/ledger/renewals, vision, Ask Ivy agent loop, full stats panel. Safety assertions are part of done | `JEV.md`, `ARCHITECTURE.md` 6/7, `TESTING.md` 4 |

## Operator actions still open

- Repo visibility (still private; checklist in `docs/CI.md` 6).
- Bump the local Go toolchain off 1.26.1, which `govulncheck` flags (fixed in 1.26.2+); CI resolves
  the latest patch.
- Lore feature names (deferred); Purelymail follow-ups (auth headers, Resend DMARC, alias/send-as
  scope, seeding the dev mailbox); a Jev labelled corpus for real-mail accuracy.
- Housekeeping: the icon recipe is in `docs/design/brand/README.md`; the stray parent `node_modules`
  noted in round 28.
