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
- **Scenario API (Go):** `w.Account("me@example.com").Deliver(msg)`, `.Flag`, `.Move`,
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
- **Pure Go only.** `CGO_ENABLED=0` in every build job; a CI step fails if cgo sneaks in or
  a dependency needs a C toolchain. The one exception is `go test -race`, which Go refuses to run
  without cgo (the race runtime is C): tests run with `CGO_ENABLED=1` and still prove nothing of ours
  needs cgo, because the `CGO_ENABLED=0` build and `nocgo` jobs gate it. A dependency that requires cgo is rejected, not special-cased.
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
  network I/O. Run the whole suite under `-race`. In code: `store.DBs.Mirror` and `.State` are
  `*store.DB{Read, Write}`; reads use `.Read` (a `query_only` pool), **every write uses `.Write`**
  (one connection that begins transactions `IMMEDIATE`, so writers queue in Go and never see
  `SQLITE_BUSY`), and a `Rows` is never left open on `.Write`.
- **SQL:** parameterised only, never string-built from input. **`sqlc` generates typed Go from plain
  `.sql` files (settled, round 21)**: build-time tool, generated code committed with a CI drift
  check, SQL kept D1-compatible. Migrations are append-only and positional (`user_version`);
  queries live next to their package; every query that can run on a
  large table has an `EXPLAIN QUERY PLAN` test asserting it uses an index.
  **A derived column has exactly one writer and its own setter** (`SetMessageBodyHTML`,
  `ReplaceThreads`, `ReplaceMessageAttachments`); `UpsertMessage` never overwrites it after the
  first insert, so a re-sync cannot lose derived data. Config and scenario YAML are strict (unknown
  keys fail), so a new key goes in the struct.
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

## 4a. Failure paths are first-class

A feature is specified, built and tested for how it behaves when things are slow, wrong, repeated,
huge or never finish, **at the same time as** when they go well. A change is not done because the
happy path passes; most real defects live on the other paths, and they are the ones a green suite
hides. Write the failure tests in the same red-green step as the success test (section 1), not
afterwards.

1. **Bound everything: size, count, depth and time.** Every input has a documented maximum (the
   table below), enforced at the edge, with a defined outcome above it (refuse, keep the headers
   only, truncate and flag). "Unbounded" and "silently truncated" are both bugs. A new limit gets a
   row in the table in the commit that introduces it.
2. **Nothing blocks without a deadline.** Every network call, child process, lock wait and channel
   receive honours a `context` or has a timeout. A library call that takes no context is made
   interruptible by closing what it waits on (`context.AfterFunc` closing the connection), and a
   test proves a stalled peer cannot hang the caller.
3. **No silent failure.** An error is returned, or recorded where the operator will see it (a status
   column, `ivy doctor`, a UI state with a stable code), never dropped. `_ =` on anything that can
   matter needs a reason on the line. A check, budget or gate must be able to fail: one that passes
   on empty input (a scan that matched nothing, a test that asserts nothing) is a defect. "I found
   nothing" and "I could not look" are different results and must be distinguishable.
4. **Large data never sits whole in memory.** Anything whose size the sender controls (a message
   body, an attachment, a response) is streamed to or from disk with a bounded buffer. Attachments
   are never loaded into memory: they are read from disk each time they are served.
5. **Repeat, resume and recover.** Operations are idempotent. The second attempt, the attempt after
   partial progress and the attempt after a crash are tested, not just the first. A fault that models
   an outage (unreachable, auth failed, provider down) lasts until it is cleared; a one-shot fault
   only tests that retrying works.
6. **Hostile input costs bounded time and memory.** Parsers have depth, count and size caps, and a
   fuzz target for every parser asserts both that it returns and that it returns in time.
7. **The zero value is the safe one.** Defaults fail closed (TLS on, loopback only, nothing cached,
   unknown config keys rejected). A flag is named for the unsafe thing (`Insecure`), never the safe one.
8. **Slow is a state, not an error.** A slow or stalled dependency produces a defined, visible state
   (progress, retry with backoff, "unreachable") and is tested with the mail world's latency and
   unreachable faults.

For every new boundary (IMAP, SMTP, HTTP, disk, parser, LLM call) the tests cover, where they
apply: **error or timeout, second call, limit exceeded, cancellation, hostile or huge input.**

### Limits

| Limit | Value | Above it |
|---|---|---|
| Message fetched into memory (`InlineMessageBytes`) | 2 MiB | streamed to a spool file on disk, parsed as a skeleton (below) |
| Message downloaded at all (`MaxMessageBytes`) | 64 MiB | not downloaded; headers kept, `body_status = too_large`, UI offers "open in webmail" |
| Inline text body kept in memory while parsing a spooled message (`mime.MaxTextPartBytes`) | 2 MiB | the part is recorded (name, type, size) and left on disk |
| Attachment or other part kept in memory (`mime.MaxLeafBytes`) | 64 KiB | recorded and left on disk; served by streaming from the file (`mime.CopyPart`) |
| Everything kept across all parts of one message (`mime.MaxSkeletonBytes`) | 8 MiB | the remaining parts are recorded and left on disk |
| Header block (`mime.MaxHeaderBytes`) | 1 MiB | body not parsed, error recorded |
| Multipart nesting (`mime.MaxMultipartDepth`) | 16 | body not parsed, headers kept, error recorded |
| Parts per message (`mime.MaxParts`) | 1000 | body not parsed, headers kept, error recorded |
| Non-fatal parse errors kept per message | 20 | dropped after the 20th |
| Inbox page (`maxInboxLimit`) | 200 | clamped |
| Account display name / icon | 120 / 16 runes | rejected with 400 |
| Account photo upload (`maxAccountPhotoBytes`) | 5 MiB | rejected with 413; only a sniffed JPEG/PNG/GIF/WebP is kept, SVG is refused |
| Sanitised HTML body (`render.MaxHTMLBytes`) | 8 MiB | body is not rendered; the plain-text view carries the message |
| IMAP dial and TLS handshake | 15 s | error, retried by the caller |
| Dev control-socket call | 30 s | error |

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
  to its copy. Unknown `/api` paths and wrong methods answer the same JSON envelope.
- Headers: every API reply carries `Cache-Control: no-store` (a handler that serves a document may
  override it), and every response `X-Content-Type-Options: nosniff`, `Referrer-Policy:
  no-referrer` and `X-Frame-Options: SAMEORIGIN`. API replies carry a deny-all CSP; the reader's
  body document overrides it with `render.ContentSecurityPolicy` as a **response header** (a
  `<meta>` CSP inside a frame is ignored by Chromium).
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
- [ ] Failure paths are covered (section 4a): limits enforced and in the table, deadlines and
      cancellation, no silent error, and the second attempt, a stalled peer and hostile or huge
      input tested where the change touches a boundary.
- [ ] Linters, `-race` tests, frontend checks all pass locally (`CGO_ENABLED=0` for builds; `make test` sets cgo on for `-race` only).
- [ ] No new dependency without a `STACK.md` entry.
- [ ] Hot-path changes carry a benchmark before/after.
- [ ] No secrets, mail content or personal data in logs, fixtures or commits.
- [ ] Docs updated in the same commit.
