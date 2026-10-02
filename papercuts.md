# Papercuts

Audit log of the commit-by-commit review of `aa0e438` (Add Go module and two-database store)
through `a049bfb` (Record chunk 2c complete and resume at 2d). One entry per issue found; each is
fixed in its own commit on the review branch unless marked **open**.

Entry format: `#N` · commit under review · file · severity · what was wrong · how it was fixed.

Severity: **bug** (wrong behaviour), **risk** (works today, fails under a plausible condition),
**standards** (violates CLAUDE.md or `docs/STANDARDS.md`), **nit** (clarity or consistency).

## Baseline (before the per-commit pass)

Starting state at `a049bfb`: `go build`, `go vet`, `staticcheck` and `gofumpt -l` are clean, and
the suite passes with `CGO_ENABLED=1 go test -race`.

- **#1** · baseline · `Makefile`, `.github/workflows/ci.yml`, `docs/STANDARDS.md`, `docs/CI.md` ·
  **bug** · `make test` and the CI `go` job ran `CGO_ENABLED=0 go test -race ./...`. Go refuses
  that combination on Linux (`go: -race requires cgo`), so the test step could never pass, locally
  or in CI. Fix: run the race pass with `CGO_ENABLED=1` and say why in the Makefile, CI and docs.
  The "pure Go" guarantee is unchanged, because the `CGO_ENABLED=0` build, the arm64 cross-compile
  and the `nocgo` dependency check still gate it.


## `aa0e438` Add Go module and two-database store

- **#2** · `aa0e438` · `store/store.go` · **bug** · `openDB` built the DSN as `"file:" + path`.
  SQLite parses it as a URI, so a data dir containing `?`, `#` or `%` was truncated or decoded
  into a different path and the databases were created somewhere else. Found with a failing test
  (`TestOpenHandlesURIMetacharactersInPath`); fixed by escaping the path.
- **N1 (open, design)** · `aa0e438` · `store/store.go` · **risk** · STANDARDS.md asks for one
  serialized write connection plus a reader pool, but `Open` returns one unbounded `*sql.DB` per
  file with deferred transactions. Nothing opens a multi-statement write transaction yet (all
  writes are single statements, covered by `busy_timeout`), so it cannot fail today. The first
  transaction that reads and then writes, run beside another writer, will get `SQLITE_BUSY`
  without any retry. Decide the writer model (a `_txlock=immediate` write handle with
  `SetMaxOpenConns(1)`, plus a read handle) before the first such transaction lands in chunk 3.

## `12508a8` Add config, HTTP gateway and CLI

- **#3** · `12508a8` · `config/config.go` · **risk** · YAML was parsed non-strictly, so a typo'd
  key (`data-dir`, `llm_enable`) silently kept the default: the mirror landed in `./data`, the LLM
  stayed off. Fixed with `yaml.Strict()`; test `TestUnknownKeyRejected`.
- **#4** · `12508a8` · `config/config.go` · **bug** · `listen` accepted ports outside 0-65535
  (`:99999`, `:-1`) and only failed later at bind time. Validation now range-checks the port;
  test `TestListenPortRange`.
- **#5** · `12508a8` · `config/config.go` · **risk** · `_ = godotenv.Load(...)` discarded every
  error, so a malformed `.env` dropped all account passwords with no message. A missing `.env` is
  still fine; any other error is returned. Test `TestMalformedEnvFileRejected`.
- **#6** · `12508a8` · `config/config.go` · **risk** · account ids `my-mail` and `my_mail` both
  map to `IVY_MY_MAIL_PASSWORD`, so two mailboxes would silently share a credential. Validation now
  rejects the collision. Test `TestAccountIDsCollidingOnPasswordEnvRejected`.
- **#7** · `12508a8` · `config/config_test.go` · **nit** · `TestLoadEnvFile` called
  `os.Unsetenv` and then `Load` exported `IVY_AUTUMN_PASSWORD` into the process for the rest of the
  run. It now registers a `t.Setenv` restore first.
- **#8** · `12508a8` · `cmd/cmd.go` · **risk** · `http.Server` had no `ReadHeaderTimeout`
  (gosec G112, slow-loris). Added a header and an idle timeout; deliberately no `WriteTimeout`
  because SSE streams are long-lived.
- **N2 (open)** · `.golangci.yml` · **standards** · STANDARDS.md says errorlint, gosec, bodyclose,
  noctx, contextcheck, exhaustive and revive are enforced, but the config only enables the
  `standard` set, so CI is green while 69 findings exist under the documented set (3 bodyclose,
  4 exhaustive, 23 gosec, 27 noctx, 12 revive; most in tests and the fakes). Real ones are fixed
  as the files come up in this review; the config switch happens at the end.

## `846bd1e`..`46880e3` dev stack (rails, control protocol, CLI, states, snapshots, scenarios)

- **#9** · `e04edda` · `internal/devstack/control.go`, `internal/mailworld/account.go` ·
  **bug** · the control server's `deliver` op called `Account.Deliver`, which panics on a missing
  mailbox, from a bare connection goroutine. One typo'd `ivy-dev deliver --mailbox Nope` crashed
  the whole dev stack (reproduced: the test binary died with the panic). Added
  `Account.Append(mailbox, raw) (uint32, error)`; `Deliver` is now a thin panicking wrapper for test
  setup, and the control op returns the error. Test `TestControlDeliverToMissingMailboxIsAnError`.
- **#10** · `5f0aa66` · `internal/mailworld/smtp.go` · **risk** · the SMTP fake had the same
  `Deliver` panic inside a go-smtp session goroutine (local delivery and the auto Sent copy). It
  now answers a transient `451 4.3.0` instead.
- **#11** · `e04edda` · `internal/devstack/control.go` · **risk** · a client that connected to
  the control socket and sent nothing pinned its goroutine forever, and `ControlServer.Close`
  waits on every goroutine, so shutdown hung. Added a 10 s request read deadline.
- **#12** · `a2e4e07` · `internal/devstack/state.go`, `internal/mailworld` · **bug** · the
  `unreachable` and `offline` states armed `DropConnection{After: 0}`, which `takeDropFault`
  consumes, so only the first IMAP connection dropped and every retry succeeded: the unreachable
  screen would have healed on its own. Added a sticky `mailworld.Unreachable` fault (closes each
  connection at accept until faults are cleared), used by both states and by the `unreachable`
  fault kind. `TestApplyStateUnreachable` now makes three attempts and failed on the second before
  the fix.
- **#13** · `846bd1e` · `internal/devstack/devstack.go` · **bug** · `BuildConfig` set
  `LLMEnabled: opts.LLM == LLMLive`, so `--llm fake` opted every account out of the LLM and the
  fake provider (and the `llm-cap-reached` state) could never be reached. Opt-in is now on for
  both providers; test `TestBuildConfigOptsAccountsIntoLLMForEitherProvider`.
- **#14** · `46880e3` · `internal/devstack/scenario.go` · **risk** · scenario YAML was parsed
  non-strictly, so a misspelt field (`subjct:`) silently produced a different step. Now strict;
  test `TestParseScenarioRejectsUnknownFields`.
- **N3 (nit, mailworld)** · `ef8382b` · `internal/mailworld/llm.go` · the fake `/systemone`
  models only choice-style `criteria` (a map); the real API's `score` questions send a list
  (see `spikes/s4-jev`), which the fake silently answers with an empty answer. Add score support
  when the real Jev client lands in chunk 4.
- **#15** · `53bac38` · `internal/devstack/supervisor.go` · **bug** · the restart-on-change
  supervisor scanned its "last known" file stamps after the build finished, so a Go file saved
  while the compiler was running was absorbed into the baseline and the stale binary kept running
  until the next edit (the existing test even slept to dodge this). The baseline is now taken
  before the build and also handed to `awaitChange` after a failed build or start. Test
  `TestSupervisorRebuildsAfterEditDuringBuild` timed out before the fix.
- **#16** · `bce5b0d` · `cmd/ivy-dev/main.go` · **bug** · `startWeb` used plain
  `exec.CommandContext` for `pnpm dev`, which on cancel kills only `pnpm`; Vite, its child, kept
  running and kept port 5173. Vite without `--strictPort` then silently moves to 5174 on the next
  `make dev`, so the printed URL and the `--expose` QR code pointed at the stale server (and
  `Wait()` could block on the orphan's pipe). Vite now runs in its own process group that is
  signalled as a whole on cancel, with `WaitDelay`, and `--strictPort` makes a port clash loud.
  Test `TestStartWebStopsTheWholeProcessTree` (a fake `pnpm` that spawns a grandchild) hung and
  then failed before the fix.
- **#17** · `53bac38` · `cmd/ivy-dev/main.go` · **bug** · `runWatched` located the Go module with
  `ModuleRoot(".")` instead of `--root`, so `ivy-dev --root /repo up` run from elsewhere built and
  watched the wrong tree. Uses `opts.Root` now.
- **#18** · `bce5b0d` · `cmd/ivy-dev/main.go` · **risk** · the in-process server in `runUp` had
  no `ReadHeaderTimeout` (gosec G112); added header and idle timeouts like `ivy run`.
- **#19** · `a2e4e07` · `cmd/ivy-dev/main.go` · **nit** · the `fault` command's help text did not
  list the new `unreachable` kind.

## `9c1e7c9`..`38be26b` compression, `5e46a02` embedded frontend

- **#20** · `be808b3` · `internal/compress/middleware.go` · **bug** · the compressor encoded any
  2xx body of a compressible type, including `206 Partial Content` and responses carrying
  `Content-Range`, so the range offsets described bytes the client never received and range
  clients would corrupt their reassembly. It also ignored `Cache-Control: no-transform`. Both now
  pass through untouched (`mustNotTransform`). Tests `TestMiddlewareLeavesPartialContentAlone`
  and `TestMiddlewareHonoursNoTransform` failed with `zstd` before the fix.
- **#21** · `2878911` · `internal/asset/precompress.go` · **risk** · when a variant was not
  smaller than its source the code skipped it but left any sibling from an earlier run, so a
  stale `.br`/`.zst`/`.gz` would be served for content it no longer matched. It now removes the
  sibling. (`make web-assets` wipes the directory first, so this only bit direct re-runs of
  `ivy-assets`.) Test `TestPrecompressRemovesStaleSiblings`.
- **#22** · `5e46a02` · `gateway/gateway.go` · **risk** · the gateway set no security headers.
  Added `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` (a link clicked in a
  mail would otherwise send the Ivy URL, message id included, to the sender's site),
  `X-Frame-Options: SAMEORIGIN` (not DENY, so 2d's reader can frame its own sanitised bodies),
  and `Cache-Control: no-store` by default on `/api/` (private, fast-changing mailbox data).
  The full CSP is left to 2d where its hashes are chosen. Tests
  `TestEveryResponseCarriesHardeningHeaders` and `TestAPIResponsesAreNotCached`.
- **N4 (open, perf)** · `d53dc23` · `internal/asset/server.go` · every static request copies the
  embedded file (`fs.ReadFile`) and SHA-256s it for the ETag. The embedded set is immutable per
  binary, so both could be computed once per file. Not changed without a number: measure on the
  potato first (PERFORMANCE.md, "measure, don't guess").
- **N5 (open, perf)** · `be808b3` · `internal/compress/middleware.go` · the pooled zstd encoder
  uses the library default concurrency (GOMAXPROCS goroutines and buffers per encoder). For
  one-response-at-a-time streaming `zstd.WithEncoderConcurrency(1)` is the usual setting and
  matters on a 4-core, ~800 MB box. Benchmark on the potato before changing.
- **N6 (open)** · `gateway/gateway.go` · unknown `/api/...` paths and wrong methods answer with
  the mux's plain-text 404/405, but the contract promises a JSON `Error` body. Lands with the
  2f handlers.

## CI pipeline and load budgets (`1d2ebbb`..`9e3f6d3`)

- **#23** · `9e3f6d3` · `web/scripts/size-budget.mjs` · **bug** · the budget scan matched only
  `"/_app/immutable/..."` literals in `index.html`. Switching SvelteKit to relative paths
  (`"./_app/..."`) made it match nothing, report `0.0 KiB`, and pass every budget (reproduced by
  rewriting the built `index.html`). The regex now accepts both forms, and the script fails
  outright when it finds no critical-path JS, so an empty scan can no longer look like a fast page.
- Reviewed with no change needed: `ci.yml` (read-only token, SHA-pinned actions, no secrets, fork
  PRs safe), `docs.yml`, `guard.sh`, the smoke spec and its config, `web-assets` in the Makefile.

## `42f86f3`..`c89c7ac` store (2a), `f7eb3f5` sync (2b)

- **#24** · `f7eb3f5` · `sync/sync.go` · **bug** · only a *missing* `Date` fell back to the
  internal date, but ARCHITECTURE.md 9a says clock-skewed mail sorts by internal date. A spam mail
  dated 2099 would sit at the top of the inbox forever. A `Date` more than 24 h ahead of now now
  falls back too. Test `TestFetchSortsFutureDatedMailByInternalDate`.
- **#25** · `f7eb3f5` · `sync/sync.go` · **bug** · `Fetch(ctx, ...)` ignored its context once
  connected: the IMAP client's commands take none, and `dial` had no timeout or context, so a
  server that went quiet (phone asleep, VPN down) blocked the sync forever and cancellation did
  nothing. `dial` now uses `DialContext` with a 15 s timeout and a context-bound TLS handshake,
  and `context.AfterFunc` closes the connection when the context ends. The error reports the
  cancellation, not the resulting I/O error. Test
  `TestFetchStopsWhenContextEndsWhileServerStalls` (timed out before the fix).
- **#26** · `f7eb3f5` · `sync/sync.go` · **standards** · `Account.TLS` defaulted to `false`, i.e.
  plaintext, so a caller that forgot the field would send the real password unencrypted. Inverted
  to `Insecure bool` (zero value = implicit TLS, TLS 1.2+, SNI = IMAP host); only the loopback
  fake sets it. Test `TestFetchDefaultsToTLS`.
- **#27** · `f7eb3f5` · `sync/sync.go` · **nit** · the read path `SELECT`ed read-write though it
  never writes; it now uses `EXAMINE` (`ReadOnly: true`), so it cannot touch server state.
- **#28** · `13411f5` · `internal/mailworld/imap.go` · **bug (fake)** · `slowConn.Read` used an
  uninterruptible `time.Sleep`, so a client hanging up left a sleeping server goroutine that
  goleak reported at exit. The delay now aborts when the connection closes.
- **N7 (open, for 2d/2e)** · `42f86f3` · `store/messages.go` · `UpsertMessage` overwrites
  `thread_id` and `body_html_sanitized` with whatever the caller holds on conflict. Today only new
  rows are upserted (a repeat sync skips known UIDs), but chunk 3's flag/move updates and any
  re-sync would blank the sanitised HTML 2d writes and the thread ids 2e writes. Give those
  columns targeted `UPDATE`s (or leave them out of the conflict set) when they land.
- **N8 (open, design)** · `efbeb2f` · `store/contentkey.go` · identical `Message-ID`s share a
  content key by design (a message in two folders is one message), but derived state
  (`needs_me`, tags) keys on `(account_id, content_key)`, so a hostile sender who copies a victim
  message's `Message-ID` inherits its verdict and tags. Fine for a single operator today; worth a
  line in the threat model before tags and triage write anything.
