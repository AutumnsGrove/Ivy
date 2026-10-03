# CLAUDE.md

Rules for any agent working in this repo. These override generic or global instructions. Full
detail is in `docs/`; this file is the short version.

## The project

Ivy is a self-hosted web mail client: one Go binary with an embedded SvelteKit frontend. It mirrors
an existing IMAP/SMTP mailbox (first: Purelymail, on the operator's own domain) into SQLite and adds fast
search, tags and careful LLM features. Single operator, used mostly from Safari on an iPhone and
iPad over Tailscale (sometimes Firefox), deployed on a Le Potato SBC (aarch64, ~800 MB RAM free).
Sibling of Polaris in philosophy, fully independent code. No auth for now; it must feel frictionless.

## Phase

**Frontend first (operator decision, 2026-10-02, qa-log round 28).** The plan is approved and the
SvelteKit app in `web/` is being built against mock data behind `web/src/lib/api/client.ts`. The Go
module, mailworld and the spikes in `docs/SPIKES.md` come next; nothing in the Go backend starts
before the spikes that gate it (S1-S3). Record every Q&A answer in `docs/qa-log.md` and fold settled
decisions into the docs. **Work goes directly on main** (operator's explicit instruction for this
phase, overriding the old "never on main" rule), committed in small stages.

Frontend facts worth knowing: SvelteKit **3** (config lives in `vite.config.ts`; aliases are the
`#lib/...` imports map, not `$lib`); `pnpm test` (Vitest, includes a token guard that fails on raw
px/colour literals outside `tokens.css`), `pnpm check`, `pnpm exec playwright test` (WebKit phone +
Chromium desktop; `e2e/screens.spec.ts` visits every route in `e2e/routes.ts`). `?scenario=` forces
designed edge states while the backend is mocked; `/gallery` links every screen.

## Doc map

`docs/PLAN.md` product and milestones · `docs/ARCHITECTURE.md` design · `docs/STANDARDS.md`
engineering standards (read before writing code) · `docs/STACK.md` libraries · `docs/TESTING.md`
test strategy and definition of done · `docs/PERFORMANCE.md` compression and budgets ·
`docs/CI.md` GitHub Actions plan (the repo will be public: no secrets in PR workflows, no
self-hosted runners) · `docs/DEV.md` the offline seeded local dev stack (`make dev`) · `docs/JEV.md` the cheap decision
engine · `docs/qa-log.md` every decision, in order · `docs/BUILD-LOG.md` what each finished chunk
delivered · `next_steps.md` live status and the one backlog · `papercuts.md` review findings.

All development and UI iteration happens against the local dev stack and mailworld, never a real
mailbox. Real credentials live only in a git-ignored `.env` and are used only by `live` tests.

## Non-negotiables

1. **Test first, and watch it fail.** Write the test, run it, see it fail for the right reason,
   write the minimum code, see it pass, then refactor. Bugfix tests must be seen failing without
   the fix. Never write implementation before its test.
2. **Integration over unit.** Most tests run the real core against the fake outside world
   (`internal/mailworld`: IMAP, SMTP, OpenRouter, Ollama, clock). Do not mock our own packages.
   Unit tests only for pure branchy logic. A thin end-to-end slice exists from day one.
3. **Pure Go, no cgo.** `CGO_ENABLED=0` always. Reject any dependency that needs a C toolchain.
   Standard library first; every other dependency needs an entry in `docs/STACK.md`.
4. **Compression and bounded data from day one.** Precompressed embedded assets (brotli/zstd/gzip),
   negotiated per `Accept-Encoding`; compressed dynamic responses; paged lists and threads; no
   unbounded reads. Performance budgets are tests (`docs/PERFORMANCE.md`).
5. **Writes go to IMAP first**, never DB-only. The DB follows the server, but nothing is ever
   erased: server-deleted mail is flagged disabled and hidden. Locally owned state (tags and their
   membership, rules, snoozes, settings, the API cost ledger, the outbox) lives in its own
   `state.db` and is the only thing backed up; the mirror is a separate `mirror.db`, rebuilt from
   IMAP. Both refer to mail by a stable content key, so moves never re-derive anything.
6. **Email is hostile input.** Sanitize server-side, sandbox in the browser, block remote content,
   guard server-side fetches (SSRF), and keep every remote API call (LLM, embeddings, anything
   that costs money) behind the single gate (per-account opt-in, caps, a ledger row per call with
   its exact cost). LLM output on the reading surface is plain text and never labelled as AI.
   Nothing sends, deletes or moves without an explicit confirmation.
7. **Measure, don't guess.** Hot paths get benchmarks (`benchstat`); real numbers come from the
   potato, not mocks.
8. **Failure paths are first-class.** The happy path passing is not done. Every input has a
   documented maximum (size, count, depth, time) with a defined outcome above it; nothing blocks
   without a deadline or context; no failure is silent; anything sized by the sender (bodies,
   attachments) is streamed to and from disk, never held whole in memory; and the second attempt,
   a stalled peer, cancellation and hostile or huge input are tested alongside the success case.
   Full rules and the limits table: `docs/STANDARDS.md` section 4a.

## Settled decisions (don't re-litigate; ask first)

Name Ivy, standalone repo, AGPL-3.0 · Go + pure-Go SQLite (`modernc.org/sqlite`; D1-friendly SQL,
Postgres rejected) · SvelteKit (`adapter-static`, Svelte 5), pure CSS custom properties (no
Tailwind), pnpm, one typed API client module · JSON REST + SSE · mirror model with IMAP-first
writes and an outbox · enmime, go-imap v2, bluemonday · embeddings behind an `Embedder`
interface: OpenRouter by default (`perplexity/pplx-embed-v1-0.6b`), local Ollama optional, each message embedded
once · tags kept both locally and as IMAP keywords · LLM layer `decide()` (Jev via OpenRouter `/systemone`, model `jev-latest`),
`complete()`, `see()`, all through one gate (`docs/JEV.md`) · container deploy: GitHub Actions
builds a multi-arch image (frontend and pure-Go binary) on merge to main and publishes it to GHCR,
the target only pulls it via a host-side update watcher, `ivy update` (nothing compiles on the
target; nothing compiled is committed) · server-deleted mail is disabled (hidden),
never erased · daily backups of `state.db`, 15 days kept (the mirror is not backed up) · design: night
botanical garden, Grove vine tile, moonlight-lilac accent, Lexend + Newsreader, Lucide, bottom tab
bar on phone (mockups in `docs/design/canvas/`) · undo-send delay is a setting · a Polaris-style
stats panel of all remote API spend.

## Code conventions (summary of `docs/STANDARDS.md`)

- Go: `gofumpt`, `go vet`, `staticcheck`, `golangci-lint`, `govulncheck`; zero warnings. Errors
  wrapped with `%w` and context; `context.Context` first on I/O; no goroutine without an owner;
  injected clock and IDs; `log/slog` with no mail content or secrets in logs; parameterised SQL
  only; append-only migrations. Small packages, interfaces defined at the point of use, functional
  core with a thin I/O shell.
- Frontend: TypeScript strict, pnpm (never npm), no raw px or colour literals outside the token
  file, no `fetch` outside the API client, accessibility and byte budgets are part of done.
- Comments explain *why*, never *what*. Don't add abstractions, options or error handling for
  cases that can't happen.
- Tools: `go build`, `go test -race ./...`, `go vet ./...`, pnpm. Use Edit/Write for file changes,
  never python/sed scripts. `uv`/Python guidance in global files doesn't apply here.

## Definition of done

The checklist in `docs/TESTING.md` section 9 plus `docs/STANDARDS.md` section 10: tests first and
seen failing, integration + (UI) E2E on phone and desktop viewports, live check on the dev mailbox
or the potato when touching sync/send/update/resources, benchmarks for hot paths, docs updated.

## Commits

Commit at each stage rather than batching. Present tense, first line under 50 characters, the body
explains why. Never commit secrets, real mail, or `.env`. Don't open a PR unless asked.
