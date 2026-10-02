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
