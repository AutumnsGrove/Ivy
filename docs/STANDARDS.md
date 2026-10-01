# Engineering standards

Status: DRAFT (2026-10-01), proposed for operator approval. This is the baseline every change is
held to. `../CLAUDE.md` is the short version agents read first; this is the full version. Where the
two disagree, fix whichever is wrong in the same commit.

Priorities, in order: **correct, then simple, then fast.** Fast matters a lot here (a 1.9 GB RAM
SBC serving a phone), but only after we can prove it is correct, and only where a benchmark says so.

## 1. Workflow: test-driven, always

Every behavior change follows red, green, refactor, and the red step is *observed*, not assumed.

1. **Write the test first**, at the highest layer that can express the behavior (section 2).
2. **Run it and watch it fail**, for the right reason (an assertion about the behavior, not a
   compile error or a missing fixture). Paste the failing line into the commit body.
3. **Write the least code that passes.** Run it and watch it pass.
4. **Refactor with the tests green.** No behavior change in this step.
5. **Bugfixes:** reproduce with a failing test first. After the fix, revert the fix once to confirm
   the test goes red again, then restore it.

Commit rhythm: one commit per red-to-green step is fine; a test commit followed by an
implementation commit is the preferred shape for non-trivial work. Commit messages: present tense,
first line under 50 characters, body says *why*.

Exceptions (each needs a sentence in the commit body): pure CSS tweaks covered by a visual
baseline, doc changes, and spikes. A spike is throwaway code on a `spike/` branch that produces a
written finding; it is never merged, the real implementation is redone test-first.

## 2. Test shape: integration first

Most tests drive the system through a real boundary against realistic fakes, not isolated
functions with mocks. (This is the "implementation/integration over unit" rule.)

| Layer | Share | What it is |
|---|---|---|
| Integration | the bulk | A real Ivy core (real SQLite on a temp dir, real sync engine, real HTTP handlers) talking to the **mail world** fake (section 3). Asserts on observable outcomes: rows, HTTP responses, SSE events, what the fake server received. |
| End-to-end | a thin but real slice from day one | The compiled binary + mail world + a real browser (Playwright, phone and desktop viewports). |
| Unit | only where logic is pure and branchy | Sanitizer, JWZ threading, rule evaluation, MIME header parsing, cost caps, thresholding. Table-driven. |
| Property / fuzz | the risky parsers and sync | See `TESTING.md` sections 1-2. |
| Benchmarks | hot paths | Section 7. |

Rules:
- **Do not mock what you own.** Never mock our own packages. Fake the *outside world* (IMAP, SMTP,
  OpenRouter, Ollama, clock, filesystem limits) at the network boundary so the real client code runs.
- **No assertions on call counts or internal method order**, except where the order is the
  requirement (IMAP-first writes, the LLM gate).
- A test that needs more than about 15 lines of setup wants a scenario helper in the mail world.
- Tests are deterministic: injected clock and ID source, no `time.Sleep` (wait on a condition with a
  deadline), seeded randomness with the seed printed on failure, `-race` always on in CI.
- Tests run in parallel by default (`t.Parallel()`), each with its own temp dir and listener on
  port 0. Shared global state is a bug.
- Leak check: `goleak` in `TestMain` of every package that starts goroutines.
- Golden files for large expected outputs, regenerated with `-update` and reviewed in the diff.

## 3. The mail world (one fake that drives everything)

A single in-repo package, `internal/mailworld`, is the fake of everything outside Ivy. It is used by
Go tests, by Playwright E2E, by a local dev stack, and by a CLI, so all four exercise the same
thing.

- **IMAP:** `go-imap/v2/imapserver` with its memory backend, wrapped to advertise the capability set
  we care about (and to hide capabilities, to test fallbacks) and to inject faults.
- **SMTP:** `go-smtp` server that records every message and can answer 4xx/5xx/552 on demand.
- **OpenRouter:** fake `/systemone`, chat and vision endpoints with a call log and canned or
  rule-based answers. **Ollama:** fake embeddings endpoint (deterministic hash-based vectors).
- **Scenario API (Go):** `w.Account("autumn@grove.place").Deliver(msg)`, `.Flag`, `.Move`,
  `.Expunge`, `.BumpUIDValidity`, `w.Fault(DropConnection{After: 3})`, `w.Clock.Advance(...)`.
  Messages come from a builder (`mailworld.Msg().From(...).Subject(...).HTML(...)`) plus a corpus of
  nasty real-shaped messages in `testdata/`.
- **Seeding:** `Seed(w, mailworld.Large(100_000))` for performance and UI tests.
- **CLI:** a separate binary `cmd/ivy-dev` (not shipped in the production binary):
  - `ivy-dev stack` starts mail world + Ivy against a temp data dir with seeded mail, prints the URL
    and a control socket.
  - `ivy-dev deliver|flag|move|expunge|fault|advance-clock ...` drives a running stack, so a human
    can play "the other mail client" while watching the phone UI.
  - `ivy-dev scenario run <file.yaml>` replays a scripted scenario (also used by E2E).
  - `ivy-dev seed --messages N`.
  The CLI is a thin client of the same scenario API; it contains no logic of its own.
  The everyday human entry point is `make dev` (seeded, offline, hot-reloading); see `DEV.md`.

**Day-one E2E smoke slice (written before any feature code):** the stack boots, the first-run `init`
completes against the mail world, a delivered message appears in the inbox in the browser, opening
it renders sanitized HTML, flagging it in the UI is visible to the fake IMAP server, and killing
and restarting Ivy loses nothing. Every later milestone extends this slice rather than adding a
parallel one.

## 4. Go code standards

- **Version:** current stable Go, `go.mod` pins it; upgrade deliberately, run the whole suite.
- **Formatting and lint (CI-enforced, zero warnings):** `gofumpt`, `go vet`, `staticcheck`,
  `golangci-lint` (errcheck, errorlint, gosec, bodyclose, noctx, contextcheck, exhaustive,
  revive on exported names), `govulncheck`. Linters are run locally before every commit.
- **Pure Go only.** `CGO_ENABLED=0` in every build and test job; a CI step fails if cgo sneaks in or
  a dependency needs a C toolchain. A dependency that requires cgo is rejected, not special-cased.
- **Standard library first.** `net/http` with Go's pattern mux, `log/slog`, `database/sql`,
  `encoding/json`, `embed`, `context`, `errors`, `slices`/`maps`, `testing`. A dependency must earn
  its place with a sentence in `STACK.md`.
- **Small packages with narrow, explicit interfaces.** Interfaces are defined where they are
  *used*, kept to one to three methods, and exist only where there is a second implementation (real
  and fake) or a test seam we actually need. No premature abstractions, no `utils`/`common`.
- **Functional core, imperative shell.** Pure functions transform data (parse, thread, decide, rank,
  render); a thin shell does I/O. Prefer returning values over mutating arguments; make zero values
  useful; immutable by default for shared data. Packages that do I/O take their dependencies
  (clock, IDs, HTTP client, DB) as arguments, never reach for globals or `init()`.
- **Errors:** wrapped with `%w` and context ("sync account autumn: select INBOX: ..."), sentinel or
  typed errors only where callers branch on them, `errors.Is/As` to test. Never ignore an error and
  never log-and-return the same error. User-facing errors carry a stable code (see
  `ARCHITECTURE.md` 9b); internal detail goes to logs only.
- **Context everywhere** I/O happens, first parameter, honoured for cancellation and deadlines. No
  goroutine without an owner and a shutdown path (`errgroup`); every goroutine is covered by the
  leak check.
- **Concurrency:** share by communicating, but a mutex is fine for small state. One SQLite writer
  (a single serialized write connection) and a pool of readers; never hold a transaction across
  network I/O. Run the whole suite under `-race`.
- **SQL:** parameterised only, never string-built from input. **`sqlc` generates typed Go from plain
  `.sql` files (settled, round 21)**: build-time tool, generated code committed with a CI drift
  check, SQL kept D1-compatible. Migrations are append-only and positional (`user_version`);
  queries live next to their package; every query that can run on a
  large table has an `EXPLAIN QUERY PLAN` test asserting it uses an index.
- **Logging:** `slog`, structured, levels used honestly, no secrets, no message bodies or subjects
  at info level (mail is private; the log is a support artifact).
- **Time and randomness** come from an injected `Clock` / ID source.
- **Naming and layout:** short lowercase package names; exported names documented; files grouped by
  concept; no stuttering (`sync.Sync...`). Max function about 50 lines and one level of abstraction
  as a guide, not a law; split when a name would help the reader.
- **Comments explain why, not what.** Delete comments that restate code. Link a spec section (RFC
  number) when matching a protocol quirk.
- **Security defaults:** treat all email, headers and attachments as hostile; validate at the edge;
  secrets only from env/files and never in the DB, logs or error text; `Host`/`Origin` checks on the
  API (see section 8); server-side fetches go through the SSRF guard.

## 5. Frontend standards

- SvelteKit + Svelte 5 (runes), `adapter-static`, **TypeScript strict**, **pnpm** with a committed
  lockfile and `--frozen-lockfile` in CI. Pure CSS with custom properties; vendored Grove tokens;
  the `--space-*`, `--radius-*`, `--z-*` scales, no raw pixel or colour literals outside the token
  file. No Tailwind, no CSS-in-JS.
- **One typed API client module.** Components never call `fetch` directly. Types come from the
  shared contract (open question in `qa-log.md`, round 21), never hand-copied.
- Dependencies are rare and small: every package added to the frontend needs a justification in
  `STACK.md` and its gzip/brotli cost noted. Prefer platform features (CSS, `IntersectionObserver`,
  `<dialog>`, View Transitions) over libraries.
- **Performance budget is a test** (section 7): initial JS, CSS and critical-path bytes are asserted
  in CI after compression.
- Accessibility is part of done: labels, focus order, contrast, reduced motion, 44 px touch
  targets; axe runs in every E2E flow.
- Browser targets: current Safari on iOS and iPadOS (primary), current Firefox, current Safari on
  macOS, Chromium for CI. Evergreen only; no legacy polyfills.
- Lint/format: ESLint, Prettier, `svelte-check`; zero warnings.

## 6. API standards

- JSON REST plus SSE, one version prefix (`/api/v1`) kept internal. **The contract is
  `api/openapi.yaml`, written first (settled, round 21):** Go server types and the TypeScript
  client types are generated from it (generators are build-time tools, outputs committed, a CI
  drift check fails if they are stale), and tests are written against the spec. SSE event shapes
  are documented in the spec's extension section since OpenAPI does not model them natively.
- Cursor pagination for every list (never offset on mail), stable ordering, explicit `limit` caps.
- Idempotency keys on mutating requests that can be retried by a phone on a flaky connection
  (send, move, delete); the outbox is the enforcement point.
- Errors: `{code, message, detail?}` with an HTTP status that matches; the same codes the UI maps
  to its copy.
- Every response is compressed (section 7) and carries correct `ETag`/`Cache-Control`; list and
  thread endpoints are shaped so a large thread streams or pages rather than arriving as one blob.
- SSE has event ids and `Last-Event-ID` resume so a phone waking up catches up cleanly.

## 7. Performance and compression (from day one)

Details and budgets live in `PERFORMANCE.md`. The standards:

- **Compression is part of the server skeleton, not an optimisation pass.** Static assets are
  precompressed at build time (brotli at max level, plus zstd and gzip) and embedded; dynamic JSON
  is compressed per request with the best encoding the client accepts. No uncompressed path exists.
- **Measure before optimising, and keep the measurement.** Every hot path has a benchmark
  (`go test -bench`, `-benchmem`) and compared with `benchstat` across commits. A budget is a test
  that fails when exceeded, not a number in a doc.
- **Don't load what you can stream or page.** Messages, threads, vectors and attachments are read in
  bounded batches; large threads render progressively; bodies are fetched on demand where the list
  doesn't need them.
- Memory is a budget too (target well under 100 MB resident at idle on the potato); enforce with a
  test that runs the seeded 100k-message mailbox and asserts a heap ceiling.
- Real numbers come from the potato, not the dev machine (`TESTING.md` section 7).

## 8. Access and exposure (no auth for now)

- **Transport (revised round 23): plain Safari over the tailnet; no PWA for now.** `tailscale
  serve` HTTPS stays the recommended setup (a secure context is needed later for PWA/push and
  WebAuthn, and costs nothing) but is optional; Ivy works over plain HTTP on the tailnet. Ivy
  listens on localhost or the tailnet interface; the Host allow-list includes the tailnet name.
  Spike S5 records which one the operator's devices actually behave best with.
- **Never exposed publicly:** no Tailscale Funnel, no `0.0.0.0` on a public interface. `ivy doctor`
  and startup warn if the listener is reachable from outside the tailnet. Because there is no auth,
  *any* device on the tailnet (including shared nodes) can read the mailbox; ACLs should restrict
  who can reach the potato. Tailscale's identity headers (via `serve`) are the planned frictionless
  auth later.
- Secrets at rest: `.env` and the DB must be mode 0600 (checked by `ivy doctor`); the provider
  app-password is the crown jewel on a device with an SD card.
- Ivy listens on a configured address (default: localhost behind `tailscale serve`, never
  `0.0.0.0` unless asked). Frictionless means no login screen, not no safeguards: reject requests whose `Host` is not
  on the allow-list and mutating requests whose `Origin` does not match (blocks DNS rebinding and
  drive-by CSRF from other sites open in the same browser). Both are tested in dev and prod modes.
- Auth is a seam, not a rewrite: a single middleware slot where passkeys or a token can be added
  later without touching handlers.

## 9. Dependencies and supply chain

- Prefer pure-Go, actively maintained, permissively licensed (AGPL-compatible) libraries; record
  version, licence and reason in `STACK.md`. Pin exact versions; `go.sum` and `pnpm-lock.yaml`
  committed.
- `govulncheck` and `pnpm audit` in CI; a dependency update is a normal PR with the full suite.
- Prefer a thin wrapper around a library at the boundary (e.g. `imap/`), so swapping it touches one
  package and the rest of Ivy sees our types.

## 10. Review checklist (and the definition of done)

The per-layer checklist in `TESTING.md` section 9 applies to every change, plus:

- [ ] Test was written first and seen failing (noted in the commit).
- [ ] Linters, `-race` tests, frontend checks all pass locally with `CGO_ENABLED=0`.
- [ ] No new dependency without a `STACK.md` entry.
- [ ] Hot-path changes carry a benchmark before/after.
- [ ] No secrets, mail content or personal data in logs, fixtures or commits.
- [ ] Docs updated in the same commit.
