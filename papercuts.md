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
