# Next steps

Working note, not a project doc. The authoritative sources stay the docs in `docs/` and `CLAUDE.md`.
It **is tracked in git** so every step is recoverable. **Update this file and commit it in the same
stage at the end of every sub-chunk (not just every chunk)** so the next session can pick up cleanly
after a context clear.

Last updated: 2026-10-02, after Chunk 2 sub-chunk 2c (the `mime/` parser and its wiring into
`sync/`) is complete. Chunk 1 (1a-1h) and 2a-2c are done; 2d is next.

## How to run a chunk (read this first)

1. Read `CLAUDE.md` (whole thing), then `docs/STANDARDS.md` (TDD, test shape, Go standards),
   `docs/ARCHITECTURE.md` (the design the code must match), and the chunk's own docs. Read
   `docs/qa-log.md` rounds 21–30 for the decisions behind the current design.
2. Work **directly on main** (operator instruction for this phase, overrides the old branch rule),
   committed in small stages. Present-tense commit subjects under 50 chars, body explains why.
   **Commit `next_steps.md` in the same stage as the work** at the end of every sub-chunk, so the
   status table and "Now" pointer always describe the committed tree.
3. **TDD, always**: write the test, run it, watch it fail for the right reason, write the minimum
   code, watch it pass, refactor. Integration tests through real boundaries are the bulk; fakes are
   `internal/mailworld`, never mocks of our own packages.
4. Use Edit/Write for file changes, never python/sed. `CGO_ENABLED=0` everywhere. `gofumpt`,
   `go vet`, `staticcheck`, `golangci-lint` should be clean.
5. At the end of each sub-chunk: run the full check set, update this file's status table and the
   "Now" section with exactly where the next session resumes, fold any new decisions into the docs,
   and commit `next_steps.md` with the work.

## Rules introduced by the 2026-10-02 audit (read before writing Go)

An independent audit of the commits through 2c (`papercuts.md` has every finding, its test and its
fix; the method is `.claude/skills/review-deepseek/SKILL.md`) changed some ground rules. They are
enforced by tests and CI, not just stated here.

- **SQLite is single-writer.** `store.DBs.Mirror` and `.State` are `*store.DB{Read, Write}`. Reads
  use `.Read` (a `query_only` pool). **All writes use `.Write`**, one connection that begins
  transactions `IMMEDIATE`, so writers queue in Go and never see `SQLITE_BUSY`. Never keep a
  `Rows` open on `.Write`. `store.Open(ctx, dir)` takes a context.
- **Linters are the documented set** (`.golangci.yml`: standard + errorlint, gosec, bodyclose,
  noctx, contextcheck, exhaustive, revive) and the tree is at zero. Use `*Context` DB/HTTP/exec
  calls. A `//nolint` needs a reason on the same line. Test files are excluded from gosec, noctx
  and bodyclose only.
- **`make test` runs `-race` with `CGO_ENABLED=1`** (the race runtime is C; Go refuses `-race`
  under `CGO_ENABLED=0`). Builds stay `CGO_ENABLED=0`.
- **Parsing hostile input is bounded.** `mime.Parse` caps multipart nesting at
  `MaxMultipartDepth` (enmime is exponential on unterminated nests); `FuzzParse` fails any input
  over 2 s. A new parser needs the same time-bound fuzz assertion.
- **Secure by default.** `sync.Account.Insecure` (plaintext) must be set explicitly; the zero
  value is implicit TLS. `Fetch` honours its context by closing the connection when it ends.
- **Config and scenario YAML are strict** (unknown keys fail). Keep new keys in the structs.
- **API responses carry** `Cache-Control: no-store`, and every response `nosniff`,
  `Referrer-Policy: no-referrer` and `X-Frame-Options: SAMEORIGIN`. 2d adds the CSP.
- **Open items that need a decision** are `N`-numbered in `papercuts.md`: N9 (trusting
  `Authentication-Results` needs an `authserv-id` allowlist), N7 (`UpsertMessage` overwrites
  `thread_id` and the sanitised HTML on conflict, so 2d/2e need targeted updates), N11 (no
  per-message size cap in the fetch), N4/N5 (measure on the potato).

## ▶ Now: Chunk 2 (Milestone 1: Read) — 2a-2c done, start 2d

Chunk 1's sub-chunks (1a-1h) are **complete** and the day-one smoke slice is the gate that passed,
so Chunk 2 is unblocked. **Chunk 2 is split into 2a-2h at chunk 1's granularity**: it is a full
feature milestone spanning eight layers — store, IMAP read fetch, MIME parse, sanitize/render, JWZ
threading, REST handlers, frontend swap, state seeder — each with its own TDD loop and its own
definition-of-done layers, and the dependency chain is strictly sequential. **2a-2c are done;
start at 2d.** Chunk 1's deliberately deferred items land in named sub-chunks below. The 1a-1h sub-chunk
notes are kept below for reference.

### Chunk 2 sub-chunks

| Sub | Scope | Exit test |
|---|---|---|
| **2a** — DONE | Mirror schema migration (`threads`, `attachments`, `accounts.icon`/`photo_blob`, indexed `seen`) + store query layer: content key, account/folder/message upsert+get, paged inbox (per-account and combined), unread + needs counts | Integration on temp SQLite; append-only + v1→v2 upgrade tests |
| **2b** — DONE | `sync/` read fetch: go-imap client connect → `LIST` + role heuristics → envelope/flags → raw body → upsert by `(folder_id, uid)` + content key, newest-first, checkpointed | Through the real `imapclient` against mailworld; resumes cleanly |
| **2c** — DONE | `mime/` parse (enmime): text/html, attachments, inline `cid:`, `Authentication-Results`, snippet, non-fatal errors; wired into `sync/` + store columns | Corpus + fuzz |
| **2d** | `render/` sanitize + sandboxed iframe/CSP: bluemonday, remote-image policy + allow-list, tracking-pixel strip, `cid:` rewrite, plain-text fallback | XSS corpus + fuzz + Playwright network assertions |
| **2e** | `thread/` JWZ threading + store `thread_id`, normalized-subject fallback | Invariant/property tests + fixtures |
| **2f** | Gateway read handlers on the real store: `/accounts`, `/inbox`, `/messages/{id}`, `/messages/{id}/summary`, `/mirror/health` | `httptest` against the generated contract |
| **2g** | Frontend swap for the reader (real `client.ts` bodies) + settings skeleton + stats-panel skeleton + account customization (rename, icon, photo); E2E both viewports, visual baselines, axe; non-reader routes stay mocked | Playwright phone + desktop |
| **2h** | `state.db` fast seeder + `ivy-dev --mode fast` + the `full == fast` agreement test for `demo` + named-state Playwright | `DEV.md` 8; seeder determinism |

Order: 2a → 2b → 2c, then 2d and 2e (threading depends only on parse, not on render), then
2f → 2g → 2h (2h needs 2a-2e to produce the same visible mailbox as `full`). 2d may overlap 2e.

**2a — mirror schema + store query layer (Go): DONE (2026-10-02, commits `42f86f3`..`229fefa`).**
`store/` migration 2 adds `threads`, `attachments`, `needs_me`, `accounts.icon`/`photo_blob` and
the denormalised `seen` column (indexed so unread counts never scan flags JSON). The query layer
gains `ContentKey` (stable across moves/UID changes), account/folder/message upsert+get with a
`store.ErrNotFound`, and the paged combined/per-account `ListInbox` with whole-view unread + needs
counts and keyset cursors. Timestamps are fixed-width so stored text sorts chronologically. Tests
are integration on a temp SQLite; `make check` is green. Deferred within 2a (with reasons):
thread *queries* — the table exists but nothing populates it until 2e; the inbox `ORDER BY` still
uses a temp b-tree (SQLite picks the `(folder_id, uid)` autoindex) — a benchmark item for 2f, with
an EXPLAIN-QUERY-PLAN guard in the meantime; the `reply_to`/`delivered_to`/`auth_results` columns
landed in 2c, state-side queries still wait for 2g.

**2b — `sync/` one-shot read fetch (Go): DONE (2026-10-02, commits `ed1c9e6`..`f7eb3f5`).**
`sync/` logs in with a real `imapclient`, `LIST`s every mailbox, classifies them with role
heuristics (SPECIAL-USE attribute when present, otherwise a name map in a few languages; the last
path segment is matched), `SELECT`s each, and fetches envelope, flags, internal date, size and the
raw body in bounded UID batches, newest first. Messages upsert by `(folder_id, uid)` with the
content key from the `Message-ID` (header-block fallback when absent); a missing mirror account row
is created, an existing one is never clobbered. The store is the checkpoint: a run reads the
folder's live UIDs and fetches only the rest, so a dropped connection resumes with no duplicates
and no gaps (tested with a fault-injected mid-session drop at batch size 1). The `folders`
`highestmodseq` is now `uint64` because sync writes the real value. Added `store.MessageUIDs` and
`store.GetMessageByUID`. `make check` is green.

**2c — `mime/` parse + store fields + sync wiring (Go): DONE (2026-10-02, commits
`12b1017`..`0a7dc96`).** `mime/` wraps enmime: `Parse(raw) Parsed` returns decoded text and HTML,
attachments and inline `cid:` parts, `References`/`In-Reply-To`, `Reply-To`/`Delivered-To`, the
SPF/DKIM/DMARC verdicts from `Authentication-Results`, a 200-rune snippet, and a bounded list of
non-fatal enmime errors. Parsing never returns an error and recovers from panics (hostile input
must not stop a sync); the routine HTML-to-text conversion is filtered out of the error list. The
package has a unit table, a hand-built nasty corpus (`mime/testdata/corpus/`, plus the mailworld
corpus), and two fuzzers — the auth parser fuzzer found a real bug (a folded verdict leaked a
newline instead of stopping at it; the input is kept as a regression seed). Store migration 3 adds
`reply_to_json`, `delivered_to_json`, `auth_results` and `parse_errors`, and `sync/` fills the
body text, snippet, attachment flag, threading headers, addresses, auth signal and parse errors as
it mirrors. `body_html` stays empty for `render/`. `make check` is green.

Deferred within 2b (with reasons): UIDVALIDITY change still re-reads the folder but leaves the old
validity's rows in place — chunk 3 owns the disable-and-rebuild sweep; a message expunged on the
server stays mirrored (disabled-not-deleted is chunk 3); the checkpoint is a full `SELECT uid`
index read per folder rather than a persisted low-UID watermark (a chunk-3/2f optimisation); a
repeat run skips a message entirely, so a flag changed by another client is picked up by chunk
3's `CHANGEDSINCE` pass, not here; no `References` (ENVELOPE does not carry it; 2c parses it from
the raw body), no `body_status`, no at-rest compression of `raw_blob`, no IDLE/QRESYNC/write path.

Two seams are fixed up front so the chunks stay independently committable:

- **Sync seam.** 2b is a deliberately small one-shot read fetch behind a `sync/` package boundary:
  LIST + role heuristics, SELECT, envelope/flags, a raw-body fetch, upsert, newest-first,
  checkpointed. No IDLE, no QRESYNC, no outbox, no writes or moves. Chunk 3 generalizes this
  boundary rather than replacing it, so 2b must not grow into the sync engine.
- **Mock seam.** Only `/accounts`, `/inbox`, `/messages/{id}`, `/messages/{id}/summary` and
  `/mirror/health` become real in 2f. `/search` (chunk 3), `/ask` + `/checks` (chunk 4),
  `/tags`/`/rules`/`/people` (chunk 3) and `/reading` (chunk 3 triage) stay mock-backed behind the
  unchanged `client.ts` signatures. Swapping them is per-milestone work, not chunk 2.

Assumed defaults for 2g/2h, veto if wrong: `/reading` stays a mock skeleton; the stats panel is UI
over any seeded `api_calls` rows while the real ledger endpoint waits for the chunk-4 gate; account
rename/icon/photo are real (schema + settings store + `/accounts` fields, `photo_blob` rendered in
the account badge). Adding `icon`/`photo` to the frozen `Account` schema is an allowed per-milestone
contract addition.

The three deferred chunk-1 items that land here, by design: `fast` mode + the `state.db` seeder
(2h), the named-state Playwright + visual baselines + `@axe-core/playwright` pass (2g/2h), and the
compressed-rendered-body cache (2d).

### Chunk 1 sub-chunk notes (done, kept for reference)

- **1a — mailworld core (Go): DONE.** `internal/mailworld`: in-memory IMAP server (wrapped
  `imapserver`/`imapmemserver`), scenario API (`Account.Deliver/Flag/Move/Expunge/BumpUIDValidity/`
  `CreateMailbox/Status`), `Msg()` builder (deterministic), injected `Clock`, and fault injection
  (`DropConnection{After}`, `AuthFail`). Tests drive it through a real `imapclient`; `goleak` on.
- **1b — CONDSTORE/QRESYNC in mailworld: DONE.** `go-imap/v2` is pinned to our fork (see
  "Standing decisions") carrying upstream PR #756 (framework + client) plus a modseq backend for
  `imapmemserver`. mailworld advertises CONDSTORE/QRESYNC by default and has `WithoutCondStore()`
  for the fallback path; `Account.HighestModSeq` reads a floor. Tests through a real `imapclient`
  cover `HIGHESTMODSEQ` on SELECT/STATUS, FETCH `MODSEQ`, `CHANGEDSINCE`, QRESYNC `VANISHED`, and
  `STORE` `UNCHANGEDSINCE`. Known fork wart: the PR's client live-`VANISHED` path has dead code;
  only the SELECT/FETCH paths are exercised so far.
- **1c — SMTP: DONE.** `internal/mailworld/smtp.go`: a real `go-smtp` server on a loopback
  port with PLAIN auth, local delivery between mailworld mailboxes (recipient account's INBOX via
  the memory store, so IDLE/EXISTS fire like a provider), external mail recorded only, a per-account
  `SentCopy` mode (`client` default, matching Purelymail's no-Sent-copy in S1; `auto` has the server
  APPEND the copy), and faults `SMTPReject{Code,Message}` (4xx transient, 5xx, 552 too large,
  consumed once so a retry succeeds) and `SMTPAuthFail`. `World.Sent()` returns accepted messages;
  `World.SMTPAddr()` is the endpoint. Tests drive it through a real `go-smtp` client.
- **1d — fake OpenRouter + Ollama: DONE.** `internal/mailworld/llm.go`: `httptest` servers on
  loopback with `World.OpenRouterURL()`/`OllamaURL()` and a call log (`World.Calls()` with provider,
  endpoint, model, raw body/response, fake-clock time). OpenRouter speaks `/api/v1/systemone`
  (rule-based, deterministic Jev answers with probabilities+confidence and a positive token cost),
  `/api/v1/chat/completions` and `/api/v1/vision` (OpenAI shape; image parts are logged as
  `vision`), and `/api/v1/embeddings`. Ollama speaks `/api/embeddings` and `/api/embed`. The
  embedder is a deterministic hashing bag-of-words vectoriser, so shared words score higher and
  fake retrieval works; dims default 1024 with `SetEmbeddingDims` for models that differ (nomic is
  768).
  `QueueChat`/`QueueJev` pin exact replies for tests. A guard test fails if any `_test.go` hardcodes
  a live provider host (proven failing on a probe, then green); the gate-level "no live in tests"
  assertion lands with the gate in chunk 4.
- **1e — seeder: DONE.** `internal/mailworld/seed.go`: `Seed(w, Empty()|Minimal()|Demo()|Large(n), WithSeed(n))`
  returns `SeedResult{Accounts, Delivered, Hash}` (hash = stable digest of every delivered message,
  for the snapshot keys in `DEV.md` 3). Profiles deliver through the real in-process APPEND path, so
  a real client sees normal UIDs and flags; `ParseProfile(name)` maps the CLI strings. `empty`
  configures accounts/mailboxes with no mail; `minimal` ~15 messages; `demo` ~450 across three
  `*.test` addresses covering every promised shape (threads, `List-Unsubscribe`, JSON-LD receipts,
  invoices, Reply-To contact mail, invites, Junk + `X-Spam-Score`, every attachment type, inline
  `cid:` and remote/tracking images, hostile HTML, non-UTF-8, RTL/emoji); `large` streams varying
  bodies. Corpus shapes live as reviewable raw fixtures in `internal/mailworld/testdata/corpus/`
  and are `go:embed`-ed. `MessageBuilder` grew `Attach`/`Inline` (deterministic MIME) so those shapes
  come from code. Tests drive a real `imapclient` and assert determinism, per-seed difference,
  `Delivered` == harvested count, shape coverage, flags, and `.test`-only addresses. Commit
  `1618cc8`. **Not yet seeded:** the demo tags/rules/snoozes/stats-ledger/Jev rows from `DEV.md` 3 —
  those live in `state.db` and arrive with the store fast-mode seeder in Chunk 2/3; 1e is mailworld
  only.
- **1f — `cmd/ivy-dev`** CLI + `make dev` with the `DEV.md` 6 safety rails as tests. **DONE
  (2026-10-02, commits `846bd1e`..`46880e3`):**
  - `internal/devstack`: `Options` + rails (loopback-only IMAP/SMTP, data under `.dev/`, `.env`
    filtered to `OPENROUTER_API_KEY`, `--expose` tailnet-only), loopback config builder and 0600
    `WriteConfig`, a JSON-over-unix-socket control protocol, `Prepare`/`Reset`, the `DEV.md` 4 state
    registry (with new mailworld faults: FETCH failure, LLM 503/429, response latency), snapshot
    recipes, a `Supervisor` that rebuilds/restarts on Go changes, a terminal QR and a scenario
    runner. Tests drive a real `imapclient`/`go-smtp`; goleak on.
  - `cmd/ivy-dev`: `up` (supervises the real binary by default, Vite proxying `/api`+SSE;
    `--watch=false` keeps the fast in-process path for tests), `seed`, `reset`,
    `snapshot save|restore|list`, `deliver`, `flag`, `move`, `expunge`, `fault`, `state`,
    `scenario run`, `advance-clock`; `make dev` = `ivy-dev up`, `make dev-fake` = `--llm fake`.
    `--accounts 0` means "all the profile seeds".
  - **Deferred because the layers do not exist yet (not 1f blockers):** `fast` mode needs the
    `state.db` seeder (chunk 2/3); `--llm live|fake` does nothing until the gate (chunk 4), and the
    OpenRouter-only-external-host rail waits with it; Playwright named-state/visual baselines and a
    browser inbox need chunk 2+.
- **1g — compression skeleton** + budget tests (`PERFORMANCE.md` 1). **DONE (2026-10-02, commits
  `9c1e7c9`..`0a3a56e`):**
  - `internal/compress`: `Accept-Encoding` negotiation with q-values and the S7 preference
    (zstd > brotli > gzip); per-request middleware that buffers to ~1 KB (`DefaultMinSize`), then
    streams through pooled zstd (default), brotli (5) and gzip (6) writers, with a corrected
    `Content-Length`, a per-coding ETag suffix, a merged `Vary: Accept-Encoding`, `Content-Type`
    sniffing, `Unwrap` and `Flush` (SSE events arrive one at a time, tested).
  - `internal/asset`: `Precompress` writes brotli 11 / zstd best / gzip 9 siblings at build time
    (skips already-compressed files and variants that are not smaller; idempotent) and
    `FileServer` serves precompressed variants with long immutable caching for
    `_app/immutable/`, `no-cache` + ETag elsewhere, and an SPA fallback to `index.html`.
  - `internal/webui` embeds `build/` (a committed `.gitkeep` placeholder; generated files are
    git-ignored). `cmd/ivy-assets` precompresses in place; `make web-assets` builds the frontend,
    copies it into the embed dir and precompresses it. `gateway.Handler` mounts the UI at `/` and
    the API (behind the middleware) at `/api/`.
  - Budget test and benchmarks for each coding live beside the middleware; `make check` is green
    and the path was verified against the real binary (`ivy run`): zstd/br chosen, correct caching
    headers, SPA fallback. **Deferred to the image build (round 29):** nothing under
    `internal/webui/build/` but the placeholder is committed; the Dockerfile will run
    `make web-assets` before the Go build.
  - **Not applicable yet (chunk 2/3):** the at-rest side of `PERFORMANCE.md` 1 (`raw_blob` and
    attachment zstd, compressed backups, `COMPRESS=DEFLATE` toward IMAP) needs the mirror/store and
    arrives with sync; the cached-compressed-body path needs the message renderer. The frontend byte
    budgets (`PERFORMANCE.md` 2) belong in the `web` CI job (1h).
- **1h — CI** (`.github/`) with the `CI.md` 2 jobs, each check proven failing once, plus the day-one
  E2E smoke against the real binary on both viewports. **DONE (2026-10-02, commits
  `cc1d6da`..`fbeb8ab`):**
  - `web/playwright.smoke.config.ts` + `web/e2e/smoke.spec.ts` run the **compiled** `ivy` binary
    (via `ivy-dev up --watch`, mailworld behind it) serving the embedded, precompressed build. It
    asserts version/health, zstd negotiation + immutable caching on a hashed bundle, the Inbox
    render with no console errors, and a cold SPA route load — on WebKit phone and Chromium
    desktop. `make smoke` builds the frontend first (`web-assets`), like the image does. The
    default `playwright.config.ts` ignores the smoke spec; `make e2e` (Vite + mocks) stays green.
  - `.github/workflows/ci.yml`: `go` (fmt, vet, staticcheck, golangci-lint, `-race` tests, arm64
    compile, govulncheck), `nocgo`, `drift`, `web`, `e2e`, `smoke`, `guard`, `deps`, `codeql`; all
    actions pinned to full SHAs and `contents: read`. `.github/workflows/docs.yml` checks local
    markdown links offline. `.github/scripts/guard.sh` (= `make guard`) rejects tracked assets
    under `internal/webui/build/`, forbids a live provider host in any test (the mailworld guard
    test), and has the AGPL header check gated behind `IVY_LICENCE_HEADERS=1`. Both guard checks
    were proved failing on bad input. `.golangci.yml` + small fixes make the tree lint-clean;
    `make golangci` runs it locally.
  - Deferred with reasons: `bench` (advisory, until numbers are trusted), `nightly.yml` and the
    manual `live`/`evals` workflows (need protected Environments/credentials),
    `docker-publish.yml` (no Dockerfile until chunk 1/3), path filtering/sharding/frontend byte
    budgets/visual baselines. Frontend byte budgets (JS/CSS) are now in `web/scripts/size-budget.mjs`
    and the `web` CI job; fonts (303 KiB vs the 60 KiB target, unsubsetted) and the CDP timing
    budgets remain open. GitHub Actions cannot run locally, so the first real workflow run is
    the first push; the `make` targets behind every job were run green here.

The smoke slice now runs against the real binary on both viewports (1h), so **Chunk 2 is
unblocked**.

Known gaps to fold into 1c onward (not bugs, just not built yet): `imapmemserver` uses `/` as the
mailbox delimiter and emits no SPECIAL-USE `LIST` attributes, and it has no `COMPRESS=DEFLATE` and
no fault injection beyond a connection drop. Its `PERMANENTFLAGS \*` (keywords) is good for tags.
CONDSTORE/QRESYNC is now provided through the fork; SMTP covers local delivery, Sent copies and
4xx/5xx/552 faults; the fake providers cover systemone/chat/vision/embeddings with a call log.

## Open items (deferred, with where they land)

Chunk 1 (a-1h) is done, but these were deliberately left for later. Each names where it lands.

**Harness (from 1f/1g/1h):**
- `fast` mode: the `state.db` fast seeder does not exist yet, so `--mode fast` is effectively a
  no-op and the `full` == `fast` agreement test for `demo` cannot run. Lands as sub-chunk **2h**.
- `--llm live|fake` does nothing until the LLM gate (chunk 4); the "only OpenRouter may be an
  external host" rail waits with it. Tests and CI already pass `--llm fake`.
- Named-state Playwright tests and visual baselines (`DEV.md` 4 states have no browser coverage
  yet); they need the real read API, so they land in sub-chunks **2g/2h**, together with the
  `@axe-core/playwright` accessibility pass and Playwright sharding.
- At-rest compression (`PERFORMANCE.md` 1): `raw_blob`/attachment zstd, compressed backups and
  `COMPRESS=DEFLATE` toward IMAP need the mirror/store and arrive with sync (chunk 2/3); the
  compressed-rendered-body cache needs the renderer (sub-chunk **2d**).
- Frontend budgets (`PERFORMANCE.md` 2): JS/CSS critical-path budgets are done (1h), but fonts are
  303 KiB unsubsetted against the 60 KiB target (and not preloaded), and the CDP-throttled timing
  budgets are not written.
- CI (`CI.md`): `bench` (advisory `benchstat` vs the base commit) waits until numbers are trusted;
  `nightly.yml` (Firefox E2E, time-boxed fuzzing, fresh `govulncheck`/`pnpm audit`, longer benches,
  link rot) is unwritten; the manual `live` and `evals` workflows need protected Environments and
  secrets; `docker-publish.yml` plus the `Dockerfile` land in chunk 1/3; the AGPL SPDX header check
  exists in `guard.sh` but is gated behind `IVY_LICENCE_HEADERS=1` until the sources carry the
  header; path filtering is not wired.
- `next_steps` housekeeping: `api/openapi.yaml` covers the read surface only; cursor pagination and
  SSE event schemas are added per milestone; a migration-upgrade test from every prior schema
  version waits until there is more than one migration; `sqlc` waits for real queries (chunk 2/3).

**Doc folds and probes still open** (see the "Still-open prerequisites" note under "Where we
stand"): QRESYNC sync, outbox APPEND-to-Sent, iframe sandbox + CSP and PDF extraction tiers are not
folded; Jev real-mail accuracy (needs a labelled corpus), send-as scope, DMARC and the iPhone/HEIC
checks remain unprobed.

**Operator:** repo visibility and the `CI.md` 6 settings verified from a fork (section below); bump
the local Go toolchain off 1.26.1, which `govulncheck` flags.

## Where we stand

- **Frontend: done against mocks, green.** SvelteKit 3 app in `web/`, all 32 screens, 123 Vitest
  tests passing, Playwright specs (WebKit phone + Chromium desktop) over `e2e/routes.ts`. The only
  seam is `web/src/lib/api/client.ts` over `web/src/lib/api/mock.ts`; `web/src/lib/types.ts` now
  re-exports the OpenAPI-generated schema (only `Settings` is hand-written). Mutating actions still
  only toast.
- **Backend: Chunk 0 done; Chunk 1 (a-1h) done; Chunk 2 (2a-2c) in progress.** Root Go module
  `github.com/AutumnsGrove/Ivy`;
  `store/` (two DBs, pragmas, positional migrations), `config/`, `gateway/` (`/api/v1/version`,
  `/api/v1/health`), `cmd/` + `main.go` (`ivy run|init|doctor`), `api/openapi.yaml` with Go + TS
  codegen committed and a `make drift` check, and a root `Makefile`. `internal/mailworld` has the
  IMAP fake (scenario API, message builder with attachments/inline, clock, faults) with
  CONDSTORE/QRESYNC through our pinned `go-imap/v2` fork, the SMTP fake (local delivery, Sent
  copies, 4xx/5xx/552 faults), the fake OpenRouter/Ollama providers with a call log, and the
  deterministic `empty|minimal|demo|large` seeder with a reviewable `testdata/corpus/`. `internal/devstack`
  now holds the dev-stack rails, control protocol and `Prepare`, and `cmd/ivy-dev` + `make dev` run the
  stack. `make check` is green. Compression is live: `internal/compress` negotiates and compresses
  API responses, `internal/asset` serves the precompressed embedded frontend, and `make web-assets`
  builds and precompresses it. CI is live: `.github/workflows/ci.yml` (`go`, `nocgo`, `drift`, `web`,
  `e2e`, `smoke`, `guard`, `deps`, `codeql`) plus `docs.yml`, with the real-binary smoke as the
  day-one gate. Chunk 2 started: `store/` has the read-path schema and query layer (2a) and `sync/`
  has the one-shot IMAP read fetch that upserts by `(folder, uid)` + content key, newest first and
  resumable (2b), and `mime/` parses the raw bodies into text, snippet, attachments, threading
  headers and auth results, which `sync/` now stores (2c). Still no `Dockerfile` and no
  image-publish workflow (chunk 1/3).
- **Spikes: all run**; findings in `docs/spikes/`. Gates that matter to chunk 1: `mailworld` must
  implement CONDSTORE/QRESYNC (S2, because Purelymail offers them per S1); Purelymail SMTP does not
  file a Sent copy (Ivy must APPEND); custom keywords persist; SMTP `SIZE` ~48.8 MiB.
- **Still-open prerequisites**: `api/openapi.yaml` is now written for the read surface; the doc
  folds for QRESYNC sync, outbox APPEND-to-Sent, iframe sandbox + CSP (chunk 2d), and PDF extraction tiers are
  not done; Jev real-mail accuracy is deferred until a labelled corpus; send-as scope, DMARC and the
  iPhone/HEIC checks remain unprobed. (Consolidated with the rest under **Open items** above.)

## The chunk plan

The backend is five planned milestones (`PLAN.md` section 5). We execute it as chunks, one per
session, each independently committable and verifiable. Order 0 -> 1 -> 2 is a hard dependency
chain; 3/4/5 can be reordered or narrowed, but send (5) is deliberately last.

| Chunk | Scope | Docs | Status |
|---|---|---|---|
| **0** | Contract + Go skeleton | `STANDARDS.md` 4/6, `ARCHITECTURE.md` 2/3 | **done** (2026-10-02, commits `aa0e438`..`68e5fce`) |
| **1** | Harness = `mailworld` + `ivy-dev` + `make dev` + day-one E2E + CI | `DEV.md`, `STANDARDS.md` 3, `TESTING.md` 1/2/8, `CI.md`, `PERFORMANCE.md` 1 | **done** (2026-10-02; open items listed above) |
| **2** | Milestone 1: Read (real mirror behind the screens) | `PLAN.md` 5, `ARCHITECTURE.md` 3/5, `TESTING.md` 3 | **in progress** (2a-2c done; 2d next) |
| **3** | Milestone 2: Sync (backfill, QRESYNC/IDLE, outbox, tags, attachments, search, backup, update) | `ARCHITECTURE.md` 4/9, `TESTING.md` 2/6 | not started |
| **4** | Milestone 3: Triage (Jev, the gate + ledger, cascade, newsletters, receipts, vision, ask, stats) | `JEV.md`, `ARCHITECTURE.md` 6/7, `TESTING.md` 4 | not started |
| **5** | Milestone 4: Send (compose, identities, undo send, drafts, SMTP + APPEND to Sent) | `PLAN.md` 5, `ARCHITECTURE.md` 5 | not started |

### Chunk 0 — Contract + Go skeleton (DONE 2026-10-02)

Delivered: `go.mod` (`github.com/AutumnsGrove/Ivy`); `store/` opening `mirror.db` + `state.db` with
per-connection pragmas and append-only positional migrations, minimum schema (accounts/folders/
messages in mirror; settings/tags/message_tags in state); `config/` (`ivy.yaml` + `.env`, secrets
from env only `IVY_<ID>_PASSWORD`); `gateway/` with `/api/v1/version` and `/api/v1/health` (503 when
a DB is down); `cmd/` + `main.go` with cobra `run|init|doctor` and graceful shutdown; `api/openapi.yaml`
(read surface only) plus committed `api/api.gen.go` and `web/src/lib/api/schema.d.ts`; `web/src/lib/types.ts`
re-exports the generated schema (only `Settings` stays hand-written); root `Makefile` (`check`, `generate`,
`drift`, `fmt`, `vet`, `lint`, `test`, `web-check`); build tools pinned via `go get -tool`
(oapi-codegen, gofumpt, staticcheck) and `openapi-typescript` in `web/`.

Deliberately deferred, and why:
- **Compression skeleton** (Accept-Encoding negotiation, budgets) -> Chunk 1, where the E2E and
  performance harness can measure it (`PERFORMANCE.md` 1).
- **sqlc** -> when there are real queries (Chunk 2/3); no point generating an empty layer now.
- **Cursor pagination / SSE event schemas** -> added per milestone as endpoints are wired; the
  contract currently matches the frontend shapes so `client.ts` stays compiling.
- **A real migration-upgrade test from every prior version** -> meaningful once there is more than
  one migration; the mechanism (positional `user_version`, append-only) is tested for fresh + reopen.

Exit criteria were met: `ivy run` serves version/health, both DBs open and migrate, `CGO_ENABLED=0`
build and `go test -race ./...` green, `make drift` passes, `svelte-check` and the 123 Vitest tests
pass against the generated types.

### Chunk 1 — Harness (Milestone 0)

`internal/mailworld` (IMAP on `imapserver`/`imapmemserver` **plus** CONDSTORE/QRESYNC, SMTP with
local delivery and 4xx/5xx/552 faults, fake OpenRouter (`/systemone`, chat, vision) and fake Ollama
embeddings, injected clock, scenario API, seeder for `empty|minimal|demo|large`), `cmd/ivy-dev`
(up/reset/snapshot/state/deliver/flag/move/expunge/fault/advance-clock/seed), `make dev`, named
states from `DEV.md` 4, the day-one E2E smoke against the **real binary** (boot, init, deliver, read,
flag, restart), compression skeleton with budget tests, and CI (`.github/`) with the jobs in
`CI.md` 2 each proven failing once. Safety rails in `DEV.md` 6 are tests.

### Chunk 2 — Milestone 1: Read

Split into 2a-2h (plan and seams in "Now" above; start at 2a). Accounts/folders/messages mirror +
query layer, a minimal one-shot IMAP read fetch (the seam chunk 3 generalizes), enmime parse,
bluemonday sanitize + sandboxed iframe render, JWZ threading, the real read REST handlers, then swap
only the reader bodies of `client.ts` for real calls (search/ask/tags/rules/people/checks/reading
stay mocked until 3/4). Also settings skeleton, stats-panel skeleton, `state.db` fast seeder and
account customization.

### Chunk 3 — Milestone 2: Sync

Backfill, QRESYNC/IDLE steady state, write path + outbox, disabled-not-deleted with mass-disable
alert, tags both ways (`$ivy-<slug>` keywords), rules/snooze, attachments + tier 0–1 extraction,
FTS5 + OpenRouter embeddings + hybrid search, People, `state.db` daily backups, `ivy update`.
Highest-risk code: the convergence property test.

### Chunk 4 — Milestone 3: Triage

Jev layer + question registry, the single LLM gate + cost ledger, needs-me cascade, categories,
newsletters (feed/digest/unsubscribe), receipts/ledger/renewals, vision, Ask Ivy agent loop, full
stats panel. Safety assertions are part of done, not polish.

### Chunk 5 — Milestone 4: Send

Compose (markdown then rich text), identities/signatures, undo send (delay setting), drafts in the
server Drafts folder, Reply-To handling, SMTP + APPEND to Sent, EXIF strip / photo downscale,
size checks against the provider.

## Standing decisions for the backend sessions

- Module path: `github.com/AutumnsGrove/Ivy` (settled, in use).
- API contract: `api/openapi.yaml` is the source of truth; Go + TS output is committed and
  `make drift` checks it. Chunk 0 covers the read endpoints the frontend already calls; add
  mutations and SSE event bodies per milestone. `Settings` is the one hand-written frontend type.
- Build tools are pinned through `go get -tool` (oapi-codegen, gofumpt, staticcheck) so `make check`
  needs no global installs. `sqlc` is deferred until there are queries to generate.
- **`go-imap/v2` is pinned to our fork.** Upstream `imapserver` lacks server-side
  CONDSTORE/QRESYNC and its parser is unexported, so it cannot be extended from outside our
  dependency. Upstream PR #756 (framework + client support) is mergeable against beta.8 but has
  had no review since May 2026 (a competing PR #690 has sat since Jun 2025), so we do not block on
  it. Fork: `github.com/AutumnsGrove/go-imap`, branch `ivy-beta8-condstore`, tags
  `v2.0.0-beta.8-ivy.N`; the tag is `v2.0.0-beta.8` + cherry-pick of #756 + a modseq backend for
  `imapmemserver` (needed because `mailworld.Account.Deliver` mutates the memory store directly).
  Ivy wires it with a `replace` in `go.mod`; nothing is vendored into this repo. Retire the fork
  when #756 lands upstream. Recorded in `docs/STACK.md`.
- Two SQLite files: `mirror.db` (rebuildable, never backed up) and `state.db` (local state, backed
  up), both referring to mail by the **content key** (SHA-256 of the lower-cased `Message-ID`, or of
  the header block when absent) — never by mirror row id.
- Pure Go only, `CGO_ENABLED=0`; stdlib-first; new deps need a `docs/STACK.md` entry.
- Secrets only in `.env`/`ivy.yaml`, mode 0600, never in the DB or logs.
- Compression and bounded/paged data from day one; budgets are tests.

## Frontend work still open (does not block chunks 0/1; pick up as it wires)

- Wire the stub actions (archive, delete, tag, save rule, delete tag, Update, "Try on recent mail",
  send) to real API calls with IMAP-first writes and undo — lands with chunks 3/5.
- Desktop keyboard shortcuts + help overlay; a11y pass with `@axe-core/playwright`; day-theme
  review; font subsetting/preload and the CDP timing budgets (JS/CSS byte budgets are done, 1h);
  real-iPhone safe areas/`100dvh`; collapse-below-minimum list drag;
  settings gaps (undo-send delay, photo size, remote images, digest time).
- `Dockerfile` + GHCR publish workflow (chunk 1 puts CI in place; the image job is a chunk-1/3
  concern per `CI.md` 3).

## Operator decisions / actions still open

- Repo visibility (still private; checklist in `docs/CI.md` 6).
- Local Go is 1.26.1, which `govulncheck` flags for 16 standard-library advisories (fixed in
  1.26.2+). CI resolves the latest 1.26 patch; bump the local toolchain (or add a `toolchain`
  directive to `go.mod`) before the next security pass.
- Lore feature names deferred; Purelymail follow-ups (auth headers, Resend DMARC, alias/send-as
  scope, seeding the dev mailbox); Jev labelled corpus for real-mail accuracy.
- Housekeeping: possible stray Vite dev server on :5173; icon recipe in
  `docs/design/brand/README.md`; the stray parent `node_modules` noted in round 28.

## Historical notes worth keeping (frontend phase)

- Frontend facts: SvelteKit **3** config lives in `vite.config.ts`; aliases are the `#lib/...`
  imports map (no `$lib`); `error(status, message, props)`; `goto` uses `reset: false`. `cookie`
  must stay a direct devDependency or Kit's copy is shadowed. sonner needs specificity tricks.
- Process slips from round 28: `sed` was used on a few files against the Edit/Write rule (results
  checked); work went on main per the operator's instruction.
