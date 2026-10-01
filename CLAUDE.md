# CLAUDE.md

Rules for any agent working in this repo. These override generic or global instructions. Full
detail is in `docs/`; this file is the short version.

## The project

Ivy is a self-hosted web mail client: one Go binary with an embedded SvelteKit frontend. It mirrors
an existing IMAP/SMTP mailbox (first: Purelymail, `autumn@grove.place`) into SQLite and adds fast
search, tags and careful LLM features. Single operator, used mostly from Safari on an iPhone and
iPad over Tailscale (sometimes Firefox), deployed on a Le Potato SBC (aarch64, ~800 MB RAM free).
Sibling of Polaris in philosophy, fully independent code. No auth for now; it must feel frictionless.

## Phase

**Planning.** No application code, Go module or frontend scaffold until the operator approves
`docs/PLAN.md`. Record every Q&A answer in `docs/qa-log.md` and fold settled decisions into the
docs. When implementation starts, the first work is the test harness and the day-one E2E smoke
slice (`docs/STANDARDS.md` section 3), not features.

## Doc map

`docs/PLAN.md` product and milestones · `docs/ARCHITECTURE.md` design · `docs/STANDARDS.md`
engineering standards (read before writing code) · `docs/STACK.md` libraries · `docs/TESTING.md`
test strategy and definition of done · `docs/PERFORMANCE.md` compression and budgets ·
`docs/DEV.md` the offline seeded local dev stack (`make dev`) · `docs/JEV.md` the cheap decision
engine · `docs/qa-log.md` every decision, in order.

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
5. **Writes go to IMAP first**, never DB-only. The DB follows the server. Tags, rules and snoozes
   are the only locally owned state and are backed up.
6. **Email is hostile input.** Sanitize server-side, sandbox in the browser, block remote content,
   guard server-side fetches (SSRF), and keep every LLM call behind the single gate (per-account
   opt-in, caps, ledger). LLM output on the reading surface is plain text and never labelled as AI.
   Nothing sends, deletes or moves without an explicit confirmation.
7. **Measure, don't guess.** Hot paths get benchmarks (`benchstat`); real numbers come from the
   potato, not mocks.

## Settled decisions (don't re-litigate; ask first)

Name Ivy, standalone repo, AGPL-3.0 · Go + pure-Go SQLite (`modernc.org/sqlite`; D1-friendly SQL,
Postgres rejected) · SvelteKit (`adapter-static`, Svelte 5), pure CSS custom properties (no
Tailwind), pnpm, one typed API client module · JSON REST + SSE · mirror model with IMAP-first
writes and an outbox · enmime, go-imap v2, bluemonday · embeddings via Ollama `nomic-embed-text`
behind an interface · LLM layer `decide()` (Jev via OpenRouter `/systemone`, model `jev-latest`),
`complete()`, `see()`, all through one gate (`docs/JEV.md`) · bare-metal deploy: the target builds
the binary, frontend build output is committed and embedded, `ivy update` · design: night
botanical garden, Grove vine tile, moonlight-lilac accent, Lexend + Newsreader, Lucide, bottom tab
bar on phone (mockups in `docs/design/canvas/`) · undo-send delay is a setting · a Polaris-style
stats panel of all LLM spend.

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
