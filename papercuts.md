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
  when the real Jev client lands in chunk 5.
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
- **N6 (resolved, 2f)** · `gateway/gateway.go` · unknown `/api/...` paths and wrong methods
  answered with the mux's plain-text 404/405, but the contract promises a JSON `Error` body. The
  read handlers wrap the API mux in a rewriter that turns only the mux's own `text/plain` errors
  into `{code, message}` and leaves a handler's JSON 404 alone; `TestUnknownAPIPathsAreJSON`
  covers both the 404 and the 405.

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
- **N8 (decided in round 37: accept for tags; chunk 5 verdict rows bind to a body hash and are
  ignored on mismatch)** · `efbeb2f` · `store/contentkey.go` · identical `Message-ID`s share a
  content key by design (a message in two folders is one message), but derived state
  (`needs_me`, tags) keys on `(account_id, content_key)`, so a hostile sender who copies a victim
  message's `Message-ID` inherits its verdict and tags. Fine for a single operator today; worth a
  line in the threat model before tags and triage write anything.
- **#29** · `4eefb89` · `sync/roles.go` · **bug** · the name table keys are ASCII-folded
  (`entwurfe`, `envoyes`, `courrier indesirable`) but the lookup only lower-cased, so the real
  folders `Entwürfe`, `Envoyés`, `Courrier indésirable` and `Archivés` all resolved to `other`
  (no Drafts/Sent/Junk role for a German or French account). Lookup now folds common Latin
  diacritics with a small replacer (no new dependency). Test `TestRoleForFoldsDiacritics`.

## `12b1017`..`0a7dc96` mime (2c) — review paused here

- **N9 (open, security, needs a decision)** · `12b1017` · `mime/authresults.go` · **risk** ·
  `ParseAuthResults` trusts "the first header that carries a method", on the theory that the
  receiving server's header is on top. If the receiver adds none (local delivery, an unchecked
  relay), a sender-supplied `Authentication-Results: x; spf=pass; dkim=pass; dmarc=pass` becomes
  the only and therefore trusted header, and the spoofed-sender discount (qa-log round on LLM
  safety) would be switched off by the attacker. RFC 8601 section 5 says to trust a header only
  when its `authserv-id` is the receiving server's. Recommendation: record the `authserv-id`
  with each verdict and accept only ids matching a per-account allowlist (default: the IMAP
  host's registrable domain). Blocked on the Purelymail header capture the qa-log lists as "not
  yet confirmed", so it was not implemented.
- **N10 (open, unverified)** · `12b1017` · `mime/mime.go` · whether a deeply nested multipart
  message can exhaust the stack or CPU. `recover()` does not catch Go's fatal stack-overflow error.
  A first experiment (50 to 20000 nested levels) did not finish within 40 s even at the smaller
  depths, which was not diagnosed (the harness may have been at fault). Re-run with `-timeout` at
  depths 1, 3, 6, 10, 14 to find the growth curve; if it is super-linear in depth, bound nesting
  depth and part count before `Parse` hands the message to enmime.
- Not yet reviewed: `92ef9fe` (store parsed header fields), `0a7dc96` (body parse in the fetch),
  `fd7f73c`/`48be660`/`a049bfb` (next_steps.md), the remaining `mime` tests and fuzzers, the
  `store` tests and query-plan guard, the golangci-lint wider-set switch (N2), and the
  `web/` changes in `68e5fce`.
- **#30 (resolves N10)** · `12b1017` · `mime/mime.go`, `mime/depth.go` · **bug (DoS)** · verified:
  enmime's cost on *unterminated* nested multipart doubles with every level (measured:
  1.6 ms at depth 12, 20 ms at 16, 307 ms at 20, 4.9 s at 24, no result at 40; well-formed nesting
  stays linear). A 1.4 KB truncated or hostile message could therefore pin the sync worker for
  seconds to forever, and `recover()` cannot help because nothing panics. `Parse` now pre-scans
  the raw lines, tracking open multipart boundaries (quoted, unquoted and folded forms), and past
  `MaxMultipartDepth = 16` parses only the headers (so threading, addresses and auth survive) and
  records "multipart nesting deeper than 16: body not parsed". Tests
  `TestParseBoundsUnterminatedNesting` (hung until its 2 s guard before the fix),
  `TestParseBoundsFoldedUnquotedNesting`, and `TestParseKeepsRealisticNesting`.

## Single-writer model (resolves N1)

- **#31 (resolves N1)** · `aa0e438` · `store/store.go` and every store query · **risk → fixed at
  the operator's request** · each database is now a `store.DB{Read, Write}`: a read pool (4
  connections, opened `query_only`, so a stray write through it fails) and exactly one write
  connection with `_txlock=immediate`. Writers queue in Go rather than racing for SQLite's write
  lock. Before the change a pooled handle with deferred transactions failed 7 of 8 workers
  immediately with `SQLITE_BUSY` on a read-then-write transaction, ignoring the 5 s
  `busy_timeout`; `TestConcurrentReadModifyWriteNeverBusy` runs 8 workers x 25 read-modify-writes
  and checks no increment is lost. Also covered: `TestReadHandleRejectsWrites` and
  `TestReadsDoNotWaitForAnOpenWriteTransaction` (WAL readers do not queue behind a sync). The
  health check pings the read side only, since a long batch holds the writer. `Open` and
  `migrate` now take a context (STANDARDS: context first on I/O), so no store call lacks one. The
  one rule for callers: never keep a `Rows` open on `Write`.

## Linter set (resolves N2)

- **#32** · `e7c955b` · `store/messages.go` · **risk** · `uint32(uid)` on an `int64` read from the
  database wrapped silently: a corrupt row with `uid = -5` came back as UID 4294967291, which would
  make sync skip or refetch the wrong message. A checked `uidFromDB` returns an error for
  anything outside 0..2^32-1. Test `TestCorruptUIDIsAnErrorNotAWrap` (returned the wrapped UID
  before the fix).
- **#33 (resolves N2)** · `eb5288b` · `.golangci.yml` and the tree · **standards** · STANDARDS.md
  promised errorlint, gosec, bodyclose, noctx, contextcheck, exhaustive and revive, but the config
  enabled only the `standard` set, so CI was green over 120 findings. The config now enables the
  documented set and the tree reports **0 issues** under the CI-pinned `golangci-lint` v2.12.1.
  Findings were fixed at the source:
  - noctx: `store.Open`/`migrate` take a context; the control-socket dial uses a `net.Dialer` with
    a timeout, and each call now has a 30 s deadline (a stack that accepts but never answers can
    no longer hang the CLI); mailworld listens through `net.ListenConfig`; the dev supervisor's
    child uses `CommandContext`.
  - exhaustive/revive: `FileStamp` exported (an exported func returned an unexported type); a
    `max` constant shadowing the builtin renamed; doc comments on exported constants; a package
    comment on `compress`; unused `cmd` parameters renamed; `++`/`--`; a missing `Identity` case.
  - gosec: kept only where the code is deliberate, each with a stated reason on the line:
    operator-chosen paths (`--config`, `--file`, scenario files), the harness running its own
    toolchain and build output, a build-time tool over its own output (public `0644` assets),
    and the seeded math/rand that makes a profile byte-reproducible.
  - Test files are excluded from `gosec`, `noctx` and `bodyclose` only (loopback `httptest`
    servers, background contexts and `t.Cleanup`-closed bodies the analyzers cannot follow);
    `revive`, `exhaustive` and the standard set still apply to tests.

## Frontend toolchain

- **#34** · baseline (`web/`) · `web/package.json`, `web/pnpm-lock.yaml` · **bug (CI)** ·
  `pnpm check` failed with 9 errors on a clean `pnpm install --frozen-lockfile`: `vite.config.ts`
  and `src/lib/tokens.test.ts` use Node globals (`process`, `node:fs`, `__dirname`) but
  `@types/node` was never a dependency, so the CI `web` job (`make web-check`) could not pass.
  Added `@types/node@^22` (matching the `engines` range). The lockfile was regenerated; the
  `libc:` selectors on the rolldown and lightningcss native bindings, which this container's pnpm
  drops, were restored by hand so they are byte-identical to before.
- Verified here: the production-binary smoke suite (`web/e2e/smoke.spec.ts`) passes 8/8 on
  Chromium with a desktop viewport and an iPhone 14 viewport emulated on Chromium. **Not**
  verified: WebKit (not installed in this container), so the real phone project, and the mocked
  `e2e` suite and visual baselines.

## `12b1017` mime tests

- **#35** · `12b1017` · `mime/mime_test.go` · **nit (test gap)** · `FuzzParse` asserted only the
  snippet length, so it could never have found a slow input, which is how the exponential
  nesting bug (#30) went unseen. It now fails any input that takes over 2 s and is seeded with
  unterminated nests at and just past `MaxMultipartDepth`. After the guard, 60 s (about 600k
  runs) and a 75 s run at a 250 ms threshold found nothing else slow.
- **N11 (open, design, memory)** · `f7eb3f5` · `sync/sync.go` · `fetchBatch` asks for
  `RFC822Size` and `BODY.PEEK[]` in one FETCH and `Collect()`s each message whole, so there is no
  per-message size cap: a 100 MB mail is downloaded, held in memory (raw plus decoded
  attachments) and stored in `raw_blob`, on a board with ~800 MB free. STANDARDS.md says no
  unbounded reads. Recommendation: fetch envelope and size first, then the body only when
  `size <= cap` (say 50 MiB, a setting); above it store the headers with a `body_status` of
  `too_large` and show the "open in webmail" state. Needs a decision on the cap and the UI copy.

## Dependencies and toolchain

- **#36** · baseline · `go.mod` · **risk (supply chain)** · `go 1.26.1` let any build use a
  toolchain with known standard-library CVEs. `govulncheck` (a CI gate) reported 16 affected
  vulnerabilities on 1.26.1, 12 on 1.26.2 and 8 on 1.26.3 (net/http, crypto/tls, crypto/x509,
  mime, net/textproto, encoding/asn1), none in Ivy's own code or third-party modules; on 1.26.6 it
  reports none. CI's `check-latest` already hides this, but a local build, the future container
  image or a contributor would not. The `go` directive now says `1.26.6`, so the toolchain is
  fetched at or above the patched version everywhere. `golangci-lint` v2.12.1, vet, staticcheck,
  the race suite and `make drift` were re-run on it. **Operator note:** this will need a bump
  every time `govulncheck` reports a newer stdlib fix; consider a scheduled job that opens that PR.
- **#37** · `53bac38` · `internal/devstack/supervisor_test.go` · **nit** · a
  `time.Sleep(3 * PollInterval)` existed only to dodge the baseline race fixed in #15; removed.
  Ten repeated `-race` runs of the supervisor tests pass.

## Standards: failure paths are first-class

- **S1** · `docs/STANDARDS.md` 4a, `CLAUDE.md` non-negotiable 8, `docs/TESTING.md` 9 · the audit's
  recurring finding, written down as a rule so new code is built to it from the start: the happy
  path was well covered while the second attempt, the stalled peer, the huge or hostile input and
  the silent failure were not. The rule bounds size/count/depth/time with a limits table, forbids
  blocking without a deadline and silent failure, requires sender-sized data to stream through disk,
  and lists the boundary tests every change owes. The limits table is the source of truth for the
  caps implemented in N11 and N7's follow-up commits.

## N7 resolved

- **#38 (resolves N7)** · `42f86f3` · `store/messages.go` · **risk** · `UpsertMessage`'s conflict
  clause overwrote `thread_id` and `body_html_sanitized` with the caller's values. Sync, which
  knows neither, would blank the threads 2e and the sanitised HTML 2d write whenever it sees a
  known message again (chunk 3's flag and move updates, or any re-sync). Both columns are now
  written on the first insert only, and owned by `SetMessageThread` and `SetMessageBodyHTML`
  (each `ErrNotFound` on a missing id). Tests `TestUpsertKeepsDerivedColumns` (failed with both
  fields empty before the fix) and `TestSetDerivedColumnsOnMissingMessage`.

## N11 resolved: big messages and attachments never sit in memory

- **#39 (resolves N11)** · `f7eb3f5` · `sync/`, `mime/`, `store/` · **risk (memory)** · the fetch
  asked for the size and the body in one command and collected each message whole, so a 100 MB
  mail was downloaded, held in memory (raw plus decoded attachments) and stored as a database
  blob, on a board with ~800 MB free. Now, at the operator's direction (large attachments must
  never go into memory):
  - Sync fetches envelope, flags and size first (small whatever the messages weigh), then routes
    each message by size: up to `InlineMessageBytes` (2 MiB) as before; up to `MaxMessageBytes`
    (64 MiB) streamed from the IMAP literal to `spool/<folder row id>/<uid>.eml` through an
    atomic temp-file-and-rename writer that cleans up on any failure and enforces the limit on
    the bytes that actually arrive (a server may send more than it announced); above that the
    body is never requested and the row is `body_status = too_large`.
  - `mime.ParseStream` walks a spooled message once in a fixed buffer, keeping headers, inline
    text up to 2 MiB, other parts up to 64 KiB and at most 8 MiB in total; every bigger part is
    read through and recorded as a `PartInfo` (path, name, type, size). Depth, part count and
    header size are capped; a body that cannot be walked yields the headers with `BodySkipped`.
  - `mime.CopyPart` decodes one part from the file straight to a writer, so an attachment is
    read from disk each time it is served.
  - Store migration 4 adds `raw_path` and `body_status`; `DBs.Dir` locates the spool. The spool
    path is built from the folder row id (a hash) and the UID, never the account id, which the
    operator can set to anything.
  - Tests: a 100 MiB message parses and a 100 MiB attachment is served while allocating under
    32 MiB (they would need over 100 MiB if buffered); mid-size, oversize, mixed-tier and
    path-escape cases through the real IMAP client; the spool writer's partial-failure,
    over-limit and replace cases; a time-bounded fuzz target for the new parser (60 s clean).
  - Limits are in the table in `STANDARDS.md` 4a.
- **N12 (open, for chunk 3)** · `sync/spool.go` · spool files are never deleted by sync. When
  chunk 3 disables or expunges a message it must also remove `raw_path`; until then an expunged
  message's file stays on disk (bounded by the 64 MiB cap per message, and the target has 256 GB).

## N12 resolved (and my own guidance corrected)

- **#40 (resolves N12; corrects #39)** · `sync/spool.go`, `store/messages.go`, `next_steps.md` ·
  **risk, plus an error in my earlier note** · N12 said chunk 3 must delete a spool file when it
  disables or expunges a message. That was wrong: CLAUDE.md 5 and ARCHITECTURE 9a say disabled
  mail is kept forever, so a disabled message keeps its file as it keeps its row. The note (and
  the matching line in `next_steps.md`, which DeepSeek reads) is corrected. The real leaks were
  smaller: a crash between `CreateTemp` and the rename left a `.spool-*` file forever, and a
  download whose row never landed left an unowned `.eml`. `sync.SweepSpool` now removes exactly
  those: files no row owns (`store.SpooledPaths` includes disabled rows) and that are over an
  hour old, so an in-flight download is never taken; it runs at the start of every `Fetch`.
  Tests: `TestSweepSpoolRemovesOnlyOrphans` (keeps owned, disabled-owned, in-flight and
  non-spool files; removes the orphan and the stale temp), `TestSweepSpoolWithNoSpoolIsFine`,
  `TestFetchSweepsOrphansFirst`, `TestSpooledPathsIncludesDisabledMessages`. The sweep's
  stub-then-implement red step was observed; `SpooledPaths` and its test were written together.

## Spike code removed

- **#41** · `aa0e438`..`a049bfb` · `spikes/` · **standards / CI cost** · nine finished spikes were
  committed to `main` as nine separate Go modules (264 KB), against STANDARDS.md section 1 (spikes
  live on a `spike/` branch and are never merged) and by a phase-specific exception. They were
  invisible to the main build, vet, linters and tests, but CodeQL's Go autobuild walks every
  `go.mod`, and the PDF and image libraries only they used showed up in the dependency graph and
  Dependabot once the repo went public. Removed at the operator's direction; their findings stay
  in `docs/spikes/` and the code is recoverable from history (`git show a049bfb:spikes/<name>/`,
  the last commit that has it). Each finding doc and `docs/SPIKES.md` now say so, and
  `docs/qa-log.md` records the decision.

## N9 resolved

- **#42 (resolves N9)** · `12b1017` · `mime/authresults.go`, `sync/sync.go`, `config/config.go`,
  `store/messages.go` · **risk (security)** · `ParseAuthResults` believed the first header that
  carried each method. Purelymail adds **no** SPF/DKIM/DMARC verdicts (spike S1), so a
  sender-supplied header was the only one, and a forged `spf=pass; dkim=pass; dmarc=pass` switched
  off the spoofed-sender discount. Fixed per RFC 8601 sections 2.5/4.1: believe only the **topmost**
  header whose `authserv-id` is in the account's new `trusted_authserv_ids`, and **only that one
  header** (a later header cannot add a method the trusted one omitted, which is the same forgery
  by another route). The trusted id is recorded with the verdicts, the raw headers are kept for
  debugging, and the default is an empty list, so nothing is trusted — the audit's suggested
  registrable-domain default was rejected because a forged header could spell the provider's domain.
  Tests: `TestParseAuthResultsTrustsOnlyConfiguredAuthservID` (the forged cases failed with the
  verdicts accepted before the fix), `TestFetchIgnoresForgedAuthResults` (end to end through
  mailworld), and the updated `TestParseAuthResults`/`TestParseAuthResultsHeader`.
  On Purelymail the signal is now honestly empty and the discount stays off; verifying DKIM
  ourselves is the later feature (`ARCHITECTURE.md` section 5, `next_steps.md`).

## N4 and N5 resolved (measured, then changed)

Both were held for a potato measurement. No potato was available, so these numbers are from a
laptop (Apple M2, 8 cores) and only the direction is claimed; the potato re-measure is noted in
`PERFORMANCE.md` and remains a follow-up, not a gate.

- **#43 (resolves N4)** · `d53dc23` · `internal/asset/server.go` · **perf** · every static request
  copied the whole embedded file (`fs.ReadFile`) and SHA-256'd it for the ETag. The file is now
  opened and streamed straight through `http.ServeContent` (embed and `fstest.MapFS` files are
  `io.ReadSeeker`; `TestFileServerServesRanges` proves Range still works), and the ETag is hashed
  once per served path into a `sync.Map`. A 270 KB identity asset: **245 µs → 145 µs/op, 1.33 MB →
  1.05 MB/op, 29 → 25 allocs** (the remainder is the `httptest.ResponseRecorder` buffer); the
  precompressed path went 1.6 µs → 1.44 µs. New `BenchmarkFileServerIdentity`/`BenchmarkFileServerZstd` hold it.
- **#44 (resolves N5)** · `be808b3` · `internal/compress/middleware.go` · **perf (memory)** · the
  pooled zstd encoder used the library default concurrency (GOMAXPROCS goroutines and buffers per
  encoder). One response streams at a time, so the pool now builds encoders with
  `zstd.WithEncoderConcurrency(1)`. One encoder: **2.33 MB / 30 allocs → 1.76 MB / 17 allocs**, and
  ~35% faster to create (128 µs → 87 µs). Single-stream throughput cost **12.6 µs → 13.9 µs/op**
  (~10%) on this 8-core laptop; on the potato's four slow cores the parallel path helps less and
  the work is I/O-bound. `BenchmarkZstdEncoderAlloc` records the footprint.

## Review of 6f29d15..HEAD (2d render through account customization)

Second pass over the unreviewed range, security-sensitive slice first (round 32).

- **#45** · `6f29d15` · `render/render.go` · **risk (security)** · `allowSafeStyles` allowed the
  `list-style` shorthand, which accepts `url()` (it is `list-style-image`), and bluemonday does not
  judge a declared value. A sender could therefore load a remote image through a style, bypassing the
  remote-image block when an allow-listed sender turns remote images on and the tracking-pixel strip
  in every case; the doc comment claimed no fetching property was on the list. Reproduced by
  `TestStyleValuesNeverFetch` (failed with `list-style: url(https://t.example/b.gif)` kept). Fixed:
  `list-style` is gone (the `-type`/`-position` longhands stay) and every allowed property now goes
  through `safeStyleValue`, which refuses `url`, `image`, `expression`, `@`, `<`, `>` and any
  backslash (CSS escapes can spell `url(` without the text); the escaped forms are in the test.
- **#46** · `6f29d15` · `render/render.go` · **risk (security)** · `isOurInline` accepted any URL
  with the message's inline prefix, so sender-written `/api/v1/messages/<id>/inline/../../x` (or its
  `%2e%2e` form, or extra segments) survived the sanitizer and the browser normalised it to any
  same-origin URL, contradicting the stated invariant that a sender cannot make the reader fetch an
  Ivy URL. No state-changing GET exists today, so the reach was reads and navigation, but the first
  one added would have inherited it. Reproduced by `TestSenderWrittenInlineURLCannotClimbOut` (all
  four cases survived before the fix). Fixed: after the prefix the remainder must be one segment
  that is not empty, `.` or `..` once percent-decoded and has no `/` or `\`; the renderer's own
  `pathEscape` output is unchanged.
- **#47** · `fe20330` · `gateway/body.go` · **risk (security)** · `/messages/{id}/inline/{cid}`
  served the part under the sender's declared `Content-Type` with `Content-Disposition: inline`,
  from the operator's own origin. A part with a Content-ID but type `text/html` or `image/svg+xml`
  could be opened as a sender-authored page (the deny-all API CSP blocks script but not a phishing
  form, because `form-action` does not fall back to `default-src`). Reproduced by
  `TestInlineServesOnlyRasterImagesAsThemselves` (both parts came back as themselves, inline).
  Fixed: only PNG, JPEG, GIF, WebP, AVIF and BMP go out as themselves; anything else is served as
  `application/octet-stream` with `Content-Disposition: attachment`. Attachments were already
  forced to download. `TestWriteWithNullOriginIsForbidden` was added as coverage for the Origin
  guard (`null`, a foreign host, a suffix-spoofed host); it passed before any change, so that guard
  is sound for what it claims.
- **N10 (open, needs a design decision)** · `2fc22b1` · `sync/sync.go`, `gateway/body.go` ·
  `body_html_sanitized` is written once at sync and served as-is, with no record of which sanitizer
  produced it. Fixes like #45 and #46 therefore never reach mail that is already mirrored, and a
  stored body keeps whatever policy was current the day it arrived. Recommendation: store a
  `sanitizer_version` beside the HTML (bumped with any policy change) and re-render rows whose
  version is behind, from the raw message, in a bounded background pass; the CSP header is the
  second layer meanwhile. Not done here because the schema and the re-render pass are a decision
  (the mirror is seeded dev data today, so nothing real is exposed yet).
- **N11 (open, needs a design decision)** · `46be4c0` · `gateway/gateway.go` · the Origin guard
  compares `Origin` to `Host`, and both come from the attacker in a DNS-rebinding attack (the
  attacker's own name resolving to the tailnet address), so a rebound page is same-origin and can
  write. Reads are equally open to it. Recommendation: an allowed-hosts list in config (the
  tailnet name, `localhost`, the dev address) checked on every API request, defaulting to loopback
  plus the configured listen address. Not fixed because the list needs a config key and a
  decision about how Tailscale hostnames are discovered.
- **N12 (open, needs a design decision)** · `4e103a3` · `thread/thread.go` · a thread's id is the
  content key of its root, or of its earliest message when the root is a placeholder. Sync is
  newest-first, so as history backfills an older message arrives, becomes the root, and the
  thread's id changes. The id is stable across moves and UID changes (what the docs promise) but
  not across backfill, so anything later keyed on it (a snooze or mute on a conversation, a thread
  tag) would silently detach. Recommendation: make the id sticky: when a rebuilt thread contains
  messages that already carry a `thread_id`, keep the oldest existing id and only mint a new one
  for a thread with none. Not done because `ReplaceThreads` is delete-and-rewrite today and the
  rule needs choosing for merges (two old ids become one) before chunk 3 hangs state on it.
- **N13 (open, measure-first, needs a decision)** · `4e103a3` · `thread/thread.go` ·
  `References` is parsed without a cap; a header can be 1 MiB (`MaxHeaderBytes`), so one message can
  name ~250k ids. Measured on a laptop: `Build` is linear at about 20 µs per id per message
  (50 messages x 200k ids: 4.2 s), run for the whole account after every fetch. Not super-linear,
  so not a hang, but `STANDARDS.md` 4a has no row for it. Recommendation: honour at most the
  last 128 ids of a References header (the nearest ancestors; the true root is usually also the
  first, so keep the first id as well) and add the row; the choice of which ids to keep is the
  decision. Re-measure on the potato before choosing the number.
- **N14 (open, same fix as N10)** · `2fc22b1`, `a965999` · `sync/sync.go` · `UpsertMessage` writes
  the row, then `storeAttachments` and `storeRendered` write derived data in separate statements.
  A crash or error between them leaves a mirrored row with no sanitised HTML or attachment rows,
  and because the checkpoint is the set of live UIDs the next run skips it, so the gap never
  heals (the body falls back to plain text; attachments have a raw-walk fallback, the HTML does
  not). Recommendation: a `derived_version` column set last, in the same transaction as the derived
  data, and a bounded boot/sync pass that re-derives rows behind the current version from the raw
  message. That is also the mechanism N10 needs, so one design covers both.
- **#48** · `a965999` (walk from `fe20330`) · `mime/stream.go`, `mime/parts.go` · **bug** · the
  part-hashing walk returned the decoder's error, so one attachment with malformed base64 (routine in
  real mail) aborted `BuildSkeleton`, and `ParseStream` then reported the whole message as
  `body not parsed`: no text, no snippet, no parts. It was a regression: before 2f the in-memory path
  used the tolerant `Parse`, and `a965999` moved every message onto `ParseStream`. `ListParts` had
  the same flaw, which made the gateway's raw-walk fallback 404 every part of such a message.
  Reproduced by `TestParseStreamSurvivesAMalformedAttachment` (`body skipped ... illegal base64 data
  at input byte 4`) and `TestListPartsSurvivesAMalformedAttachment`, both failing before the fix.
  Fixed: on a decode error the rest of the part is drained raw (so the skeleton keeps it whole and
  a failure of the stream itself still surfaces), the part is listed with the size that decoded and
  no hash, and the walk continues.
- **N15 (open, needs a design decision)** · `46be4c0` · `gateway/format.go` · `humanTime` renders
  "15:04", "Yesterday" and weekday names server-side in `now.Location()`, which is the server's
  zone, while its comment promises the viewer's. A container on the potato defaults to UTC, so the
  operator's phone would show inbox times in UTC and the today/yesterday boundary would fall at UTC
  midnight. The tests pin `testNow` in UTC, so they cannot see it. Recommendation: send the
  timestamp (RFC 3339) in the API and format it in the browser, which knows the viewer's zone and
  locale; if the API must keep pre-rendered strings, add a `timezone` setting and load it into the
  formatter. Not fixed because it changes the `MailSummary`/`MailMessage` contract.

## N11 resolved: the Host allow-list

- **#49 (resolves N11)** · `46be4c0` · `gateway/gateway.go`, `config/config.go`, `cmd/cmd.go` ·
  **risk (security)** · `STANDARDS.md` section 8 required rejecting requests whose `Host` is not on an
  allow-list, but nothing enforced it; the Origin guard compares `Origin` to `Host`, and in a
  DNS-rebinding attack both are the attacker's name, so a rebound page could read and write the
  whole API. Decided in round 32b. Reproduced by `TestAPIRejectsUnknownHosts` (every foreign Host,
  including `127.0.0.1.evil.example`, `sub.localhost` and an empty Host, answered 200) and
  `TestRunRefusesAForeignHostUnlessAllowed` against the real `ivy run`. Fixed: a `hostGuard` on
  every `/api/` request admits exact loopback names, the config's new `allowed_hosts` (validated as
  bare names or IPs, so a port or wildcard fails loudly) and the listen host (a wildcard listen adds
  none); everything else is `403 forbidden`. `ivy init` and `ivy doctor` print the effective list and
  a hint when a network listen address has no configured name. `ivy init` writes no config file, so
  the "prompt for the Tailscale name" in the decision became that guidance. The static shell is not
  guarded by design (tested).

## N10 and N14 resolved: versioned, atomic derived data

- **#50 (resolves N10 and N14)** · `2fc22b1`, `a965999` · `store/derived.go`, `sync/sync.go` ·
  **risk** · derived data was written in separate statements after the row (HTML, then attachment
  rows) with no record of which sanitizer or parser produced it. A crash between them left a row
  sync would skip forever, and a fix like #45/#46/#48 never reached mail already mirrored. Decided in
  round 32b. Reproduced by `TestSetMessageDerivedIsAllOrNothing` (a failing attachment insert left
  `BodyText:new BodyHTML:<p>new</p>` at version 0, the half-written state of N14) and the sync
  integration tests below. Fixed: migration 6 adds `messages.derived_version`; `SetMessageDerived`
  writes the text columns, sanitised HTML, attachment rows and the version in one transaction;
  `Fetcher.Rederive` re-derives rows behind `sync.DerivedVersion` from the raw message (blob or
  spool file, one message in memory at a time), newest first, at most 200 per `Fetch`, which runs it
  before threading. A message whose raw bytes cannot be read is logged and left behind without
  stopping the pass (`TestRederiveSkipsAnUnreadableMessage`; a pile of such rows at the top of the
  list would starve older ones, noted below). `TestRederiveHealsRowsBehindInBoundedPasses` shows a
  mangled-attachment message getting its body back, and `TestDerivedVersionMatchesTheCorpusOutput`
  fails if the pipeline's output for a fixed corpus changes without a `DerivedVersion` bump
  (mutation-checked: weakening a style rule fails it). The old `SetMessageBodyHTML` is gone.
- **#51** · `d9dd8fc`, `2fc22b1` · `store/messages.go` · **bug (latent)** · `UpsertMessage`'s conflict
  clause overwrote `body_text`, `snippet`, `has_attachments`, `parse_errors`, `body_status`,
  `raw_blob` and `raw_path` with whatever the incoming row carried. A re-upsert built from the
  envelope alone (what a chunk 3 flag refresh would do) would blank a healed message's text and,
  worse, **erase the raw message it is derived from**, against "nothing is ever erased". Found
  while moving derived data into one writer. Reproduced by `TestUpsertKeepsDerivedColumns` and
  `TestUpsertKeepsTheRawPath` (text, snippet, attachments flag, raw blob and raw path all came back
  empty). Fixed: the derived columns are written on the first insert only, and `raw_blob`/`raw_path`
  keep their stored value when the incoming row has none. `TestUpsertMessageUpdatesInPlace` pinned
  the old overwrite of `body_text` and now asserts the new rule.
- **N16 (open, minor)** · `Fetcher.Rederive` · a message whose spool file is missing or unreadable is
  skipped each pass but stays behind, so enough of them at the top of the newest-first list would
  starve older rows within the 200-per-run limit. None can occur today (nothing deletes a spool
  file). When chunk 3 can disable and restore messages, either record the failure on the row or
  page past rows that failed.

## N12 resolved: sticky thread ids

- **#52** · `d9dd8fc` · `store/threads.go` · **bug** · `threads.id` is a global primary key and a
  thread's id was its root's content key, which is not account-scoped. The same email delivered to
  two of the operator's addresses (routine on one domain) has one Message-ID, so the second
  account's thread write failed with `UNIQUE constraint failed: threads.id` and that account's
  whole sync run returned an error. Reproduced through the real fetch path by
  `TestFetchThreadsTheSameMessageInTwoAccounts` and at the store by
  `TestReplaceThreadsSameProposedIdInTwoAccounts`, both failing before the fix. Fixed with the
  change below: a minted id is `<account>:<proposed key>`.
- **#53 (resolves N12)** · `4e103a3` · `store/threads.go` · **risk** · a thread's id was the root's
  content key, and sync is newest-first, so as history backfilled the root arrived, the id changed,
  and anything later keyed on it (a snooze, a mute, a thread tag) would detach. Decided in round 32b.
  Reproduced by `TestFetchKeepsAThreadIdWhenTheRootArrivesLater` (mutation-checked: switching the
  rule off fails it) and the store tests for backfill, merge and split. Fixed: `ReplaceThreads`
  assigns ids in two passes, so stickiness always beats minting: a rebuilt thread keeps the existing
  id carried by its oldest member that has one (each id claimed once, in the caller's order), and
  only a thread with none mints a scoped id, with a `~n` suffix if one is already claimed. A merge
  collapses onto the older conversation; a split leaves the id with the half holding the oldest
  message. `thread.Thread.ID` is now documented as the *proposed* id. `ListThreads` and
  `messages.thread_id` report the stored one, so the old tests pinning unscoped ids and
  replace-on-rebuild now assert the new rule.

## N15 resolved: instants on the wire

- **#54 (resolves N15)** · `46be4c0` · `gateway/format.go`, `api/openapi.yaml`, `web/src/lib/time.ts` ·
  **bug** · `humanTime` rendered "15:04", "Yesterday" and weekday names server-side in
  `now.Location()`, the server's zone, while its comment promised the viewer's. A container on the
  potato is UTC, so the phone showed UTC clock times and the today/yesterday boundary fell at UTC
  midnight; the tests pinned `testNow` in UTC and could not see it. Decided in round 32b:
  `MailSummary`, `MailMessage` and `SearchHit` now carry `date` (RFC 3339, `format: date-time`,
  UTC) instead of `time`, a contract change. `TestMessageDatesAreRFC3339InUTC` fails on the old
  shape (the field is `time` and a zone-offset instant is not normalised); the server's
  `humanTime` and its tests are gone. The browser formats with `formatMessageTime`
  (`web/src/lib/time.ts`), which takes an injectable clock and IANA zone: its tests include the
  case that motivated this (03:00 UTC on the 3rd is "Yesterday" in Los Angeles and in Tokyo),
  locale-following output (`en-GB` is 24-hour, German says "Gestern") and future and zero
  instants. `MessageList.test.ts` pins that the row shows the formatted date (mutation-checked:
  passing the raw instant fails it). The mock data builds instants relative to now so the designed
  rows still read "9:41 / Yesterday / Mon". A message with no Date header carries the zero
  instant and shows no time.
- **N17 (open, minor)** · `b5bfc78` · `gateway/read.go` · the account `syncNote` ("synced 4 min ago",
  "on Jan 2") is still rendered server-side. The relative part is zone-independent, but the
  "on Jan 2" form, used after 24 hours, takes the date in the server's zone, and the whole note is
  English only. Send `lastSyncAt` as an instant and format the note in the browser when the
  settings and health screens are designed (the health screen already needs a design pass).

## N13 resolved: bounded References

- **#55 (resolves N13)** · `4e103a3` · `thread/thread.go` · **risk (performance)** · `References` was
  parsed without a cap: a header can be 1 MiB (about 250k ids) and `Build` ran over the whole account
  after every fetch. N13 measured it as linear in the ids and guessed the graph work was the cost.
  **That diagnosis was wrong.** A first fix that only capped the parsed ids (first id plus the last
  128) barely moved the number (51 messages x 200k ids: 4.2 s to 3.8 s), because the time was the
  regexp scanning the 1.4 MB header, about 75 ms per message, before any cap applied. The real fix
  is `scanMessageIDs`, one linear pass that keeps the first id and a ring of the last
  `MaxReferences` (128), so memory does not grow with the header. Same probe afterwards: 148 ms
  (28x), and 10k ids 193 ms to 7 ms. Tests: `TestScanMessageIDsMatchesTheRegexp` (fixed awkward
  inputs plus 2000 random ones: it finds exactly the ids the regexp did),
  `TestScanMessageIDsDoesNotMaterialiseEveryID` (a handful of allocations for 200k ids; asserted on
  allocations because a timing would flake), and three `Build` tests: an unrelated real message named
  only in the dropped middle of a long header no longer links into the conversation, ordinary mail
  below the cap threads as before, and the boundary (first id plus exactly 128 more) is kept whole.
  The row is in the `STANDARDS.md` 4a limits table. Laptop numbers only; the potato is slower, so
  the ratio is the claim, not the milliseconds. The choice (first plus last 128) was the recommendation
  in N13, taken as the default when the work was started.

## N16 resolved: a broken message cannot starve the re-derive pass

- **#56 (resolves N16)** · `a965999` · `sync/sync.go`, `store/derived.go` · **risk** · `Rederive`
  skipped a message whose spool file was gone, but left it behind, so it came back first in the
  newest-first list on every pass; enough of them would take all 200 slots and no healthy message
  behind them would ever heal. Nothing deletes a spool file today (hence minor), but chunk 3's
  disable and restore will. Reproduced by `TestRederiveMarksAMissingSpoolFileAndHealsPastIt` (limit 1,
  the broken newest message held the only slot: `first pass = 0`). Fixed: migration 7 adds
  `messages.derive_failed_version`; a missing file (`fs.ErrNotExist`) is recorded with
  `MarkDeriveFailed` and `MessageIDsBehind` skips rows failed at the current version, so the next
  `DerivedVersion` bump retries them; the pass keeps going until it has healed its quota, so a broken
  row does not cost the healthy ones their turn. `TestRederiveDoesNotMarkATransientFailure` pins the
  other half: a permission error is skipped without a mark and the message heals once the file is
  readable again. The first draft of that test was nondeterministic (the two messages had no `Date`,
  so "newest" was a tiebreak on a hashed id); it now sets explicit dates.

## N17 resolved: the sync note carries no time

- **#57 (resolves N17)** · `b5bfc78` · `gateway/read.go`, `api/openapi.yaml`, `web/src/lib/accounts.ts` ·
  **bug** · the account's `syncNote` was rendered server-side with a time in it ("Up to date · synced
  4 min ago", and "on Jan 2" after a day, in the server's zone), the same N15 defect as the message
  times and English-only. `Account` now carries `syncedAt` (RFC 3339, UTC, absent until the first
  folder has synced) and `syncNote` is the state phrase alone. The browser composes the line:
  `syncLine` adds "synced 4 minutes ago" for an account that is up to date, "last synced" for one
  that is failing, and nothing while it is still reading, using `formatSince` (relative in the
  viewer's language, a date after a month). Reproduced by `TestListAccounts` (the note still held
  the time and `SyncedAt` was missing). With no view left that renders a time, `gateway.since`, the
  `Server.now` clock and its test hook were removed. Web tests: `formatSince` (units, German, the
  month cut-over, skew) and `syncLine` (three cases).

## `9f0f4ef..7d99f92` Review of the 3a runner (C2 and the steady state)

- **#58** · `d95bebe` · `sync/sync.go` · **bug** · a folder renamed away and then back kept its
  stored modseq (the row is only marked gone), and the rename keeps UIDVALIDITY, so on a QRESYNC
  server the next sync was a delta that answered "nothing changed": the messages disabled while the
  name was gone never came back and the folder showed 0 of 2. Only the condstore variant failed, and
  the 24 default seeds ran just 3 renames, so the convergence test never composed the sequence.
  Reproduced by `TestRenamedAwayAndBackKeepsItsMessages` (`sync/revive_test.go`: create, append x2,
  sync, rename away, sync, rename back, sync; failed before the fix with `the server holds 2 messages
  and the mirror shows 0 visible`). `canUseDelta` now refuses a folder with `gone_at` set, so a revived
  name gets a full read and its disabled rows are upserted back to life.

- **#59** · `2c6fe0a` · `sync/worker.go` · **bug** · `idleOnInbox` dialled its own connection but,
  unlike `fetch`, never closed it on cancellation, and the client's commands take no context, so a
  server that went quiet during the IDLE connection's login, CAPABILITY or SELECT held the worker (and
  `ivy run`'s shutdown) indefinitely, against STANDARDS 4a rule 2. Reproduced by
  `TestWorkerStopsPromptlyWhenTheIdleConnectionStalls` (a 5 s `Latency` armed after the first
  reconcile, then cancel; failed before the fix with `the worker did not stop while its idle
  connection was stalled`). Fixed with the same `context.AfterFunc` close `fetch` uses.

- **#60** · `2d39e8d` · `web/src/lib/api/events.ts`, `web/src/routes/+layout.svelte` · **bug** · the
  events client promised "a reconnect means refetch what is on screen" but did nothing on reopen, and
  the hub sends no hint on connect and has no replay. A suspended Safari tab or a Tailscale blip
  therefore left the screen stale until the next unrelated hint. Reproduced by the Vitest case
  `asks for a refetch when the stream reopens, but not on the first open` (failed before the fix:
  `expected vi.fn() to be called 1 times, but got 0`). `connectEvents` takes an `onReconnect` called
  on every `open` after the first; the layout passes `invalidateAll`. The browser-level behaviour
  (iOS Safari really resuming the stream) is not verified here.

- **#63 (resolves N18)** · `9f0f4ef` · `sync/sync.go` · **risk** · `fetchAll` holds every folder's
  snapshot until the whole account is reconciled, and the snapshot asked for envelope, internal date
  and size as well as flags. Measured on the laptop (`BenchmarkSnapshotRetainedHeap`, 500 to 4000
  messages, linear and stable): **775 B per message retained**, so a 100k mailbox is about 78 MB for
  the listing alone before Go's GC headroom, against PERFORMANCE.md's 250 MB budget during backfill
  and 100 MB idle. `reconcileFolder` reads only UID and flags from it (`fetchBatch` fetches its own
  envelopes for new messages), so the snapshot now asks for exactly those: **190 B per message**
  (about 19 MB at 100k), and about 40% faster to read. Reproduced by
  `TestSnapshotHoldsLittlePerMessage` (783 B against a 300 B budget before the change, 192 after;
  internal test, `sync/snapshot_mem_test.go`). Convergence is unchanged: the default suite plus 100
  extra seeds on each of the default and churn mixes pass. **Not done, on purpose:** reconciling each
  folder straight after its snapshot (the N18 recommendation). With the listing at 190 B the gain is
  small, and it would let a connection drop between folders leave a moved message labelled
  `server_removed` for good, because `reclassifyDisabled` runs only at the end of a successful pass.
  `BenchmarkSyncFullScanHeap` is kept as an upper bound (it includes the in-process fake's garbage).
  The potato figure is still an operator measurement; the laptop slope is what is recorded.
- **#64 (resolves N22)** · `9f0f4ef` · `sync/sync.go`, `store/messages.go` · **bug** · a pass that
  died after disabling a source folder's row but before the destination's new copy was read left the
  row labelled `server_removed` although the message had only moved, and no later pass relabelled it
  (the first disable wins; the provisional list lived in memory and was lost). Reproduced by
  `TestAnInterruptedPassStillCallsAMoveAMove` (the connection dropped after each of 1 to 40 commands,
  both folder orders): 3 of the 80 drop points failed with `disabled as "server_removed", want
  "moved"`. Fixed by keeping the provisional label in the row (`disabled_reason =
  'pending_classification'`, `store.DisabledPending`) and settling every pending row in one
  set-based UPDATE at the end of a pass that completed (`store.SettlePendingDisabled`: moved if the
  same Message-ID is live in another row, else `server_removed`). This replaces the in-memory
  `disabledRef` list, `LiveMessageIDs` and `SetMessageDisabledReason`, so the account's Message-IDs
  are never loaded either. It deliberately does not relabel rows that were already settled, so a
  message deleted and later re-received stays `server_removed`
  (`TestAReceivedAgainMessageDoesNotMakeAnOldRemovalAMove`), which is why no decision was needed. For
  3b: a row can read `pending_classification` between an interrupted pass and the next completed
  one; treat it as not yet restorable and not yet counted by the mass-disable alert.
  Two small behaviour notes: a message with no Message-ID is never called a move (the old map treated
  every empty id as live), and the constants `DisabledPending/Moved/Removed` now live in `store`.
  Convergence: the default suite and 100 extra seeds on each of the default and churn mixes pass.
- **#62 (resolves N19)** · `9f0f4ef`, `2c6fe0a` · `sync/stall.go`, `sync/sync.go`, `sync/worker.go` ·
  **risk** · nothing but a cancelled `ctx` ended a command to a server that accepted the connection
  and then stopped answering. The premise of N19 was partly wrong: our go-imap fork already bounds a
  response once its first byte arrives (30 s) and bounds writes (30 s), but deliberately waits for a
  response to *begin* with no deadline (an IDLE is silent by design), and that gap is where a wedged
  server hangs a command. `stallConn` closes it: a read deadline of 2 minutes of silence, restarting on
  every byte, armed only while a command is in flight (around login/list/capability, each folder
  snapshot, each reselect plus body batch, and the IDLE connection's setup and its DONE) and off
  between commands and during IDLE, so local database work and a quiet IDLE are never counted. The
  client sets and clears read deadlines on the same connection, so `SetReadDeadline` is intercepted
  and the earlier of the two applies (a first attempt that ignored this was silently cleared after
  every response and never fired). A stall ends the pass as `unreachable` (`errServerStalled`, since
  the client formats read errors with `%v` and the type is lost) and the worker's backoff retries.
  Reproduced by `TestFetchGivesUpOnAServerThatStopsAnswering` and
  `TestWorkerRecoversFromAStalledIdleConnection` (both ran to the test's own 20 s / 10 s limit before).
  `TestStallGuardLeavesASlowButAnsweringServerAlone` and
  `TestStallGuardLeavesAQuietIdleConnectionAlone` prove it does not fire on healthy traffic; the second
  fails if the guard is never disarmed (mutation-checked). Limits row added to STANDARDS 4a. The
  value (2 min) is a judgement, not measured against Purelymail; `WithStallTimeout` changes it.
- **N21 (resolved in 3b)** · `b1ca426` · `sync/sync.go` · **nit** · `Fetch` recorded the final
  `sync_state` with the pass's own `ctx`, so a pass that ended because `ctx` was cancelled or timed
  out logged `cannot record sync state: context canceled` and left the row at `syncing` until the
  next start overwrote it. Fixed with the recommendation: the outcome is written on a
  `context.WithoutCancel` context with a 5 s bound (`syncStateWriteTimeout`), so a cancelled or
  timed-out pass settles its own row and an operator-visible `syncing` after a deadline cannot
  mislead.
- **#61 (resolves N20)** · `5a34ca8` · `sync/churn_test.go` · **standards** · the default 24 seeds
  execute 3 renames in 640 operations, which is why #58 survived the C2 gate (300 extra default-mix
  seeds were clean). `TestSyncConvergesUnderFolderChurn` runs the same oracle, seeds and variants on
  `churnMix` (create, rename, delete and rebuild folders weighted up: 44 renames per 24 sequences, 14
  times the default). With the #58 fix reverted, a 200-seed sweep failed at seed 153 and shrank to
  exactly create, rename away, rename back, sync, so that seed is pinned in `churnRegressionSeeds` and
  always runs. With the fix, the default run and 200 further seeds (base 5000) pass on both variants.
  The oracle file is untouched (gate T1). Known nit: the failure's "reproduce:" line names
  `TestSyncConvergesToTheServer`; set `IVY_SYNC_SEED` and run the churn test instead.

- **#65** · `9f0f4ef..2d39e8d` · `.golangci.yml`, `sync/`, `store/`, `events/`, `internal/devstack/` ·
  **standards** · `make check` runs only staticcheck, but CI's `go` job runs the pinned golangci-lint
  (v2.12.1, the set STANDARDS 1 documents), so the 3a work would have gone green locally and red in
  CI: 13 findings (two contextcheck, four errcheck, two exhaustive, five revive; two were in the stall
  guard written in this review). Found by running the documented linter set once, as the review
  skill asks. Fixed at the source: a cancelled-context-safe `WithoutCancel` for the migration's
  foreign-key restore, settled `Close` errors, an unused `ctx` dropped from `snapshotFolder`, a
  shadowed builtin renamed, the exported constants documented, an `if` in place of a switch over
  `imap.ResponseCode`, and the convergence harness's `numOpKinds` sentinel excluded in the config
  (the oracle file itself stays untouched). One `//nolint:contextcheck` with its reason, for the
  fake mail server, which takes no context. Recommendation (open, your call): add `golangci-lint
  run` to `make check`, or the gap reopens with the next stage.

## 3c backups (agent, 2026-10-04)

- **N24 (resolved 2026-10-04, operator).** Purge now erases everywhere: when no other hidden row
  shares the bytes (reference-counted), `PurgeMessage` records the blob hash in `state.db`
  (`pending_blob_deletions`, state migration 3) before it deletes the row, and the gateway erases the
  local blob and the copy in every backup target. A target that is offline is not treated as clean:
  the record stays and the daily backup retries it (`backup.PurgeBlob`, `processPendingBlobDeletions`),
  so the erasure also survives a restore. A blob another row still needs is never evicted, and the
  stale record is cleared. Tests: `store/blobdeletion_test.go`, `backup/backup_test.go`
  (`TestPurgeBlobErasesLocalAndEveryTarget`, `TestPurgeBlobRetriesAnOfflineTarget`,
  `TestPurgeBlobKeepsASharedBlob`, `TestRunProcessesPendingBlobDeletions`),
  `gateway/mirror_test.go`. The crash window is closed by recording the pending deletion before the
  row goes: if the delete then fails it is cleared, and if a crash leaves a still-referenced blob the
  backup's reference check skips it until the last row is purged.
- **#66** · `c510612..e8d8c6f` · `sync/sync.go` · **risk** · the disable path copies a message's
  bytes to the blob store through `MessageRawReader`, which opens the spool file while the mirror's
  read cursor (for the reference list) is done and the write connection is free; the copy is bounded
  by the existing spool size limits. No defect found, recorded because it is the first code that
  reads mail bytes on the write path. The blob copy failure path warns and leaves the hash empty,
  and the next `backup.Run` reconciles it (`TestRunReconcilesDisabledBlobs`), so it is never silent
  and never permanent.
- **#67** · `e8d8c6f` · `internal/devstack/devstack.go`, `config/config.go` · **bug (caught by CI
  smoke)** · strict `backup.at` validation (a new setting) broke the dev stack: `BuildConfig` built a
  `Config` without the field, `WriteConfig` marshalled `at: ""`, and the spawned `ivy` failed at
  startup on `backup.at "": must be HH:MM` (`make smoke` timed out waiting for the WebServer). Fixed
  by setting `DefaultBackupAt` in `BuildConfig`; `TestWrittenConfigReloads` marshals and reloads the
  generated config so the next new setting cannot do this silently. This is why the definition of
  done runs the real-binary smoke slice even for a backend-only change.

## `8b5daa8..1c0c8b9` Review of 3b (hidden mail) and 3c (backups, blob store)

- **#68** · `8cfcbb8` · `sync/sync.go`, `store/disabled.go` · **bug** · the mass-disable alert counted
  only what the current pass hid, so a pass that died after sweeping a folder (connection drop while
  fetching a later folder) left the rows pending, and the recovery pass hid nothing, settled them as
  removed and alerted on nothing: a mailbox wipe went by in silence, the one case the alert exists
  for. Reproduced with `TestMassDisableAlertSurvivesAPassThatDiesAfterTheSweep` (connection dropped
  after every command count; drops 8 to 10 gave 0 alerts, want 1; the first draft of the test never
  failed because the folders after INBOX had nothing to fetch, so there was no command to drop in
  the window). `fetchAll` now reads the sweep from the rows (`PendingDisabledByFolder`: pending rows
  per folder, held = live + pending) just before settling, and the in-pass tally is gone.
- **#69** · `e8d8c6f` · `backup/backup.go` · **bug** · `mirrorTree` skipped any blob whose name
  already existed at the target and wrote new ones straight to the final name, so a copy that died
  half-way left a truncated file that every later run (and a restore, which uses the same walk)
  treated as present: the only off-device copy of a server-deleted message stayed short for good.
  `TestRunHealsATruncatedBlobInATarget` failed before the fix. It now compares sizes and copies
  through a `.tmp-` name and a rename.
- **#70** · `8b5daa8` · `store/disabled.go` · **bug** · `PurgeMessage` checked that the row was
  hidden in one statement and deleted it in a later transaction, so a Restore landing between them
  (two tabs, or a double tap) made the one erasure Ivy has delete a row that was visible by then.
  `TestPurgeMessageRefusesARowRestoredBeforeTheDelete` (a trigger stands in for the restore) failed
  before the fix: purge returned nil and both rows were gone. The delete is now guarded by
  `disabled_at IS NOT NULL`, and a miss rolls the transaction back with `ErrNotDisabled`.
- **#71** · `27492df` · `backup/backup.go` · **risk** · `Restore` deleted the old `state.db-wal` while
  keeping the old database "moved aside", so after a crash the kept copy lost every row only the
  log held, the operator's newest tags. `TestRestoreMovesTheOldWriteAheadLogAsideWithTheDatabase`
  failed before the fix; the log now moves to `<aside>-wal`.
- **#72** · `96d033c` · `sync/sync.go` · **nit** · the blob-copy warning logged the message id under
  the key `account`.
- **N25 (resolved, operator chose the recommendation)** · `8cfcbb8` · `sync/massdisable.go` · the alert counts moved rows as
  hidden, so archiving more than 50 messages (or more than 20% of a 10+ message INBOX) raises a
  `mass_disable` alert, and its one-click restore (`RestoreAccountDisabled`) then makes the moved
  copies visible beside the live ones in the other folder. The qa-log wording ("a sweep that
  disables") allows it. Recommendation: count only rows that settled as `server_removed`, and have
  the account restore skip `moved`. It needs the operator's call because it changes round 42.
- **N26 (resolved: fixed in the fork, `v2.0.0-beta.8-ivy.3`)** · `go-imap` fork `v2.0.0-beta.8-ivy.2` `imapserver/conn.go` · the fake
  server's serve loop can block forever on `encMutex` after `DropConnection` cuts the connection
  mid-response, stranding a goroutine and failing goleak at exit (the churn-alert test at drop 10 does
  it every time). Test-only (only mailworld uses `imapserver`). Fix in the fork, then widen the sweep
  in `TestMassDisableAlertSurvivesAPassThatDiesAfterTheSweep` back past drop 9.
- **N27 (resolved)** · `e8d8c6f` · `cmd/cmd.go` · `backupLoop` waits for the next 03:00 and never
  catches up: a potato that was off at 03:00 goes a day or more without a snapshot. Recommendation:
  on start, run a backup when the newest snapshot is older than a day.
- **N28 (resolved, operator: refuse it)** · `22091dc` · `api/openapi.yaml`, `store/disabled.go` · the contract says a
  pending row is "not restorable", but the single-message `RestoreMessage` restores any hidden row,
  pending included (only the account bulk skips them). Harmless (the next pass hides it again if
  the server lacks it), but one of the two should change; recommendation is to say "bulk restore
  skips pending rows" in the contract.
- **N29 (resolved, operator: keep them outside state.db)** · `354d14b` · `backup/backup.go` · `pending_blob_deletions` lives in
  `state.db`, so restoring a snapshot taken before a purge forgets an erasure that an offline target
  still owes, and the blob stays there. Recommendation: have `ivy restore` list the targets' blobs
  that no mirror row references only after a rebuild (a later chunk), and say so in the restore
  output until then.

## Resolution of N25-N29 and the port churn (2026-10-04)

- **#73 (resolves N25)** · `8cfcbb8` · `store/disabled.go`, `sync/sync.go` · **bug** · archiving 12 of 12
  INBOX messages raised `mass_disable`, and its one-click restore then showed each moved message beside
  its live copy. `TestMassDisableAlertStaysQuietWhenTheMailWasOnlyMoved` and
  `TestPendingDisabledByFolderLeavesOutRowsThatWillSettleAsMoved` failed first. The sweep now counts
  only pending rows that will settle as removed (`provablyMovedSQL`), and `RestoreAccountDisabled`
  restores only `server_removed` rows. Two existing tests that encoded the old restore-everything
  behaviour were changed with it.
- **#74 (resolves N26)** · fork `imapmemserver/message.go` · **bug** · `fetch` returned on a failed body
  write without `w.Close()`, so the connection's encoder lock was never released and the serve loop
  blocked on the tagged response. The fork's `TestFetchWriteErrorReleasesTheEncoderLock` failed first
  (its first draft closed from the client side and never failed: the leak needs the server's own end
  to close, and a body bigger than the write buffer). Pushed as `v2.0.0-beta.8-ivy.3`; the churn-alert
  sweep is back to drops 1-14.
- **#75 (resolves N27)** · `e8d8c6f` · `backup/backup.go`, `cmd/cmd.go` · **risk** · a device that was off at
  03:00 waited a day for the next slot. `TestBackupLoopCatchesUpAMissedBackupOnStart` failed first
  (no snapshot was written on start); `Manager.Due` and a catch-up in `backupLoop` fix it.
- **#76 (resolves N28)** · `22091dc` · `store/disabled.go`, `gateway/mirror.go` · **standards** · single
  restore restored a pending row although the contract said it was not restorable. It now returns
  `ErrPendingClassification` (409 `pending_classification`); the generated contract was regenerated.
- **#77 (resolves N29)** · `354d14b` · `store/blobdeletion.go`, `store/migrations.go` · **risk** ·
  `TestRestoreOfAnOlderSnapshotKeepsAnOwedBlobErasure` failed first: the pending-erasure table rolled back
  with the snapshot. The debt is one synced marker file per hash under `data/pending-blob-deletions/`
  (hash validated as 64 hex before it becomes a name), and state migration 4 drops the table. Erasures
  recorded in the table before this change are not carried over (the feature was a day old).
- **#78** · the sync test harness (`sync/`, `internal/mailworld`) · **standards** · one run of the sync suite parked ~9,000
  temporary ports in TIME_WAIT, so the next run on macOS failed with `can't assign requested address`.
  `TestTestConnectionsToTheFakeDoNotLeaveTimeWaitSockets` counted 40 lingering sockets after 80
  connections before the fix; loopback test dials (`Fetcher.dial` for insecure accounts and the
  mailworld client) now set `SO_LINGER` 0, and a whole run leaves ~144.

## C3 outbox design review (2026-10-04)

All six are defects in the design document, found before any code existed and fixed in
`docs/handoffs/2026-10-04-C3-outbox.md`; qa-log round 46 has the operator's decisions.

- **#79** · C3 draft · **bug (design)** · the idempotency key was unique over every row, so flag, unflag,
  flag again made the third tap a no-op against the finished first op. Now a partial unique index over
  `pending` and `in_flight` ops, with a C4 test for the sequence.
- **#80** · C3 draft · **bug (design)** · "the mirror update and `in_flight → done` are one
  transaction" cannot hold across `mirror.db` and `state.db`. The order is now ack, idempotent mirror
  update, then `done`, and recovery re-applies the update.
- **#81** · C3 draft · **bug (design)** · `source_folder_id` was only a hint, so with N8 duplicates (mail
  sent to yourself, Bcc) an Archive could act on the Sent copy. It is part of the op's identity and
  resolution looks only inside that folder.
- **#82** · C3 draft · **bug (design)** · recovery by content key could be fooled by a pre-existing copy,
  and a UIDVALIDITY change could aim a stored UID at a different message. The resolved
  `(uidvalidity, uid)` is persisted at `in_flight`; recovery checks that message first and re-resolves by
  Message-ID only after a UIDVALIDITY change, failing as `ambiguous` rather than guessing.
- **#83** · C3 draft · **standards** · retry had no attempt, age or queue bound and every IMAP `NO` was
  retried. Caps added (8 attempts, 24 h, 500 ops) with a response-code table.
- **#84** · C3 draft · **standards** · `cancelled` was nearly unreachable, undo was undefined, and nothing
  satisfied CLAUDE.md rule 6 for moves. Undo is an inverse op, `cancelled` means superseded, and moves
  and deletes need a confirmation modal (operator decision).
- Also corrected in the doc, not numbered: the `in_flight` definition, sharing the sync connection
  (the outbox owns its own), `MOVE`/`UIDPLUS` requirements instead of emulation, and reusing sync's
  `disableRef` so a hidden row keeps its blob-store copy.

## Second-opinion review of `ea78a63`..`28ed913` (2026-10-05)

Baseline before any change: `go build`, `go vet`, `staticcheck`, `gofumpt -l`, the full suite and
`-race` on store, sync and gateway were all green.

### `4d23d76` Add the outbox worker and sync deferral

- **#85** · `4d23d76` · `sync/outbox.go` · **bug** · after a failure that killed the connection (a drop
  mid-command, a stall, BYE) the worker kept its dead `*session`, so every retry wrote to a closed
  socket and the op exhausted its 8 attempts as `retries_exhausted` against a healthy server. Every
  existing test used `RunOnce`, which closes the connection, so none could see it. Reproduced with
  `TestOutboxRetriesOnAFreshConnectionAfterADrop` (`DropConnection{After: 4}` on the worker's first
  connection, `Run` with a short jitter): it failed with `failed after 7 attempts ... use of closed
  network connection` before the fix. `transient` now closes the connection so the next attempt dials
  afresh.
- **#86** · `4d23d76` · `sync/outbox.go` · **bug** · the worker's IMAP commands take no context and the
  worker, unlike sync and the IDLE worker, never closed the connection on cancellation, so shutdown
  waited out the 2-minute stall timeout behind a server that had gone quiet. Reproduced with
  `TestOutboxRunStopsPromptlyWhenCancelledMidCommand` (`Latency{1m}`, cancel after 500 ms): `Run` was
  still blocked 5 s later before the fix. The connection now has a `context.AfterFunc` close hook from
  the moment it is dialled, and a failure caused by that cancellation returns the context error instead
  of costing the op an attempt.

### `5611a5b` Add the outbox table and its state machine

- **#87** · `5611a5b` · `store/outbox.go` · **bug** · `EnqueueOutbox` checked the 500-op cap before looking
  for an existing live op with the same idempotency key, so a double tap on a full queue was refused as
  `outbox_full` instead of returning the op already queued. Reproduced with
  `TestEnqueueOutboxIsIdempotentEvenWhenTheQueueIsFull` (failed with `outbox full`). The idempotent
  lookup now runs first.

### `0be4836` Add the outbox HTTP surface and run wiring

- **#88** · `0be4836` · `gateway/outbox.go` · **bug** · a `move` whose destination was a folder the server no
  longer lists (`gone_at` set) was accepted with 202 and could only fail at dispatch. Reproduced with
  `TestEnqueueMoveToAGoneFolderIsRefused` (got 202, wanted 409 `bad_destination`); the destination
  check now refuses a gone folder.
- **#89** · `5611a5b`/`0be4836` · `store/outbox.go`, `gateway/outbox.go` · **standards** · STANDARDS.md
  requires an injected clock, but `EnqueueOutbox` fell back to `time.Now()` when the caller passed no time
  and the retry handler called `time.Now()` directly, so a test (or a replay) could not control an op's
  timestamps. Reproduced with `TestOutboxTimestampsComeFromTheInjectedClock` (did not build: no
  `WithClock`) and `TestEnqueueOutboxRequiresATimestamp` (an untimed enqueue succeeded). The gateway now
  has `WithClock` (default `time.Now`, stamped on enqueue and retry) and the store refuses an untimed op.
  Two gateway tests that leaned on the fallback now pass a time.

### `960cea5` Add the reader's flag, junk and outbox queue

- **#90** · `960cea5` · `store/migrations.go` · **bug** · mirror migration 11 backfilled the new `flagged`
  column with `flags_json LIKE '%Flagged%'`, which also matches a keyword that merely contains the word
  (`$notflagged`, a tag-style label), so such mail would show a star it never had. Reproduced with
  `TestFlaggedBackfillMatchesOnlyTheFlaggedFlag` (a v10 database upgraded through `Open`: the keyword row
  came out `flagged = true`). Migrations are append-only, so v11 is untouched and migration 12 recomputes
  the column from the parsed flag list (`json_each`, case-insensitive, skipping unparsable JSON).
- **#91** · `6b0135f` · `gateway/outbox.go` · **bug** · the reader's Undo toast sends the id of the row it just
  acted on, but once the worker has moved the message that row is hidden as moved and `GetMessage` filters
  hidden rows, so Undo answered 404 "not found" in the normal case (the worker drains in about a second,
  the toast lasts four). The e2e only checks that the Undo button appears. Reproduced with
  `TestUndoOfASettledMoveActsOnTheCopyInTheDestination` (404, wanted 202) and
  `TestUndoBeforeTheArrivalIsMirroredSaysSoPlainly` (404, wanted 409 `not_synced`). The gateway now
  follows a row hidden as moved through the newest *finished move op* for that mail
  (`store.SettledMoveDestination`) to the copy in the folder it delivered it to, so a same-Message-ID copy
  in another folder (N8) is never mistaken for it; when sync has not mirrored the arrival yet it answers
  409 `not_synced`. The web client knows the new code (before it was coerced to `internal_error`).
- **#94 (was N30, decided by the operator 2026-10-05: mirror via COPYUID)** · `6b0135f` · `sync/outbox.go` ·
  **bug** · Undo of a move could only act once sync had mirrored the arrival, so within the first seconds
  (the toast's window) it answered `not_synced`. Reproduced with
  `TestOutboxMoveMirrorsTheArrivalImmediately` (no row in the destination mirror after the op was done).
  After a MOVE the worker now uses the `COPYUID` destination UIDs to fetch and store the arrived message
  with sync's own `fetchBatch` (peeking, so nothing is marked seen). It is best effort and logged: the
  move already happened, and sync still mirrors the arrival if this fails (`not_synced` is then the honest
  answer). The same test proves the next sync pass adopts that row rather than listing the message twice.
  A crash between the ack and this step is covered by sync, as before.
- **#92** · `4d23d76`/`0be4836` · `sync/outbox.go`, `gateway/outbox.go` · **standards** · the CI step
  `golangci-lint` (pinned v2.12.1, `.golangci.yml`) failed on the outbox code with 8 findings the local
  gates (`vet`, `staticcheck`, `gofumpt`) do not run: three non-exhaustive IMAP `switch`es, the builtins
  `clear` and `cap` shadowed, three unused parameters. Reproduced by building the pinned linter (the
  preinstalled v2.5 refuses a Go 1.26 module) and running it: 8 issues before, 0 after. The switches got
  explicit `default` branches with a reason, the builtins were renamed, and `ctx` was removed from the
  five IMAP helpers that never used it (the commands take no context; cancellation closes the connection,
  #86), rather than renamed to `_`.
- **#93 (was N31, decided by the operator 2026-10-05: do not count them)** · `4d23d76` · `sync/outbox.go` · a connect or login failure counts
  as an attempt against the op at the head of the queue (`transient`), so with the 5 s to 15 min backoff
  an outage of roughly 20 minutes ends every queued action as `failed (retries_exhausted)` and the
  operator must retry each by hand. That is what STANDARDS 4a documents, but it treats "the server is
  unreachable" like "the server rejected this op". Reproduced with
  `TestOutboxOutageDoesNotExhaustAttempts` (`Unreachable` for 1.5 s: op `failed` after 7 attempts). Now
  only a server NO to the op counts; a failed dial, login, drop or stall leaves the attempt count alone,
  backs off on its own consecutive-failure counter, and the 24 h age cap still bounds the op. The
  STANDARDS limits row says so.
- **N32 (open, unverified here)** · the web e2e could run only on Chromium through a throwaway config
  pointing at the container's older build (WebKit and the real-binary smoke cannot launch in this
  environment: `webkit-2359` and `chromium_headless_shell-1243` are missing). The phone (WebKit)
  project, `make smoke` and everything on the potato (real Purelymail MOVE/UIDPLUS behaviour, the
  `COPYUID` question in N30) remain unverified.

### Reviewed with no finding

`ea78a63` (design revision: its six fixes match what was built), `dbbdb61`, `ef4d42a`, `0c7556f`,
`28ed913` (crash test, per-folder ownership re-check and docs: read, no defect). The Message-ID search is a
substring match, but the mirror stores the id with its angle brackets (`wrapMessageID`), so a hit cannot be
a different message's id.

### Gate at the tip of this review

`go build`, `go vet`, `staticcheck`, `gofumpt -l`, golangci-lint v2.12.1 (CI's pin, 0 issues),
`CGO_ENABLED=1 go test -race ./...`, arm64 cross-compile, `govulncheck` v1.1.4 on go1.26.8 (none),
`make drift`, `pnpm check` and `pnpm test` all pass.
