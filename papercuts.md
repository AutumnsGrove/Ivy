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
- **N32 (partly verified 2026-10-05, remainder open)** · Playwright wants `webkit-2359` and
  `chromium_headless_shell-1243`, which this container lacks (it has Chromium 1194 only). With a
  throwaway config (deleted afterwards) pointing at that Chromium, the **whole e2e suite passed on both
  projects (258 passed, 10 skipped by design)** and the **real-binary `make smoke` passed (8/8)** against
  the embedded, precompressed build. The "phone" project there is the iPhone 14 profile (viewport, touch,
  user agent) on Chromium, **not WebKit**. Still unverified: true WebKit/Safari rendering on iPhone and
  iPad, and everything that needs the Le Potato or a real Purelymail mailbox (real MOVE/UIDPLUS/COPYUID
  behaviour for #94, `expunge` against the live Trash). Run `make e2e` and `make smoke` once somewhere
  with the pinned browsers, and do a live archive, undo and Empty Trash on the potato.

### Reviewed with no finding

`ea78a63` (design revision: its six fixes match what was built), `dbbdb61`, `ef4d42a`, `0c7556f`,
`28ed913` (crash test, per-folder ownership re-check and docs: read, no defect). The Message-ID search is a
substring match, but the mirror stores the id with its angle brackets (`wrapMessageID`), so a hit cannot be
a different message's id.

### Gate at the tip of this review

`go build`, `go vet`, `staticcheck`, `gofumpt -l`, golangci-lint v2.12.1 (CI's pin, 0 issues),
`CGO_ENABLED=1 go test -race ./...`, arm64 cross-compile, `govulncheck` v1.1.4 on go1.26.8 (none),
`make drift`, `pnpm check` and `pnpm test` all pass.

## Review of `25a9896..3d26e01` (3f search, 3g rules/People/Reading, 3h update; 2026-10-05)

Baseline before any change: `go build`, `go vet`, `staticcheck`, `gofumpt -l` and
`CGO_ENABLED=1 go test -race ./...` were green; **golangci-lint v2.12.1 (CI's pin) reported 13 issues**
that CI would fail on (fixed under the commit that introduced each, see below).

### `e6b76e9` Add the rule engine, store and ingest pass

- **#95** · `e6b76e9` · `store/rules.go`, `rules/applier.go` · **standards** · STANDARDS 4a documents
  that an apply-to-existing reads 500 messages per account, and `rules.applyBatch` was 500, but
  `RuleMessages` clamped every caller to `MaxRuleDryRun` (200), so the constant was dead and an apply
  silently skipped everything older than the newest 200. Reproduced with
  `TestApplyToExistingReadsItsDocumentedBatch` (300 matching messages: applied 200, failed before the
  fix). `store.MaxRuleApply` is now the ceiling and `applyBatch` uses it.
- **#96** · `e6b76e9`, `b6e447d`, `dac8acc` · `store/rules.go`, `store/people.go`, `store/inbox.go` ·
  **bug** · the "newest copy of each content key" filter `GROUP BY content_key HAVING m.date =
  MAX(m.date)` is never true for a message whose `date` is NULL (no Date header), so such mail was
  never offered to the rule pass or the dry run, never counted in People, never listed in a person's
  conversations, and dropped from `MessagesByContentKeys`. Reproduced with
  `TestRuleMessagesIncludesUndatedMail` (0 rows, failed before the fix). All four queries now use
  `HAVING m.date IS MAX(m.date)`. Letting undated mail through exposed a second fault behind it:
  `RebuildPeople` and `ConversationsForAddresses` parsed the empty date with `parseTime` and failed
  (`TestUndatedMailIsListedEverywhere` failed with `parsing time ""`), which would have failed every
  Settle. They now use `parseOptionalTime`; an undated message is counted but has no place on the
  first/last-seen timeline.

### `901c0e3` Add the self-update resolve, API and UI

- **#97** · `901c0e3` · `update/update.go` · **bug** · the host watcher writes `update-signal/result`
  once and never clears it, and `WriteSignal` left the previous run's file in place. If the watcher was
  not installed, stopped or hung past the 15-minute wait, the UI read the last run's `ok` as this
  request's outcome and reported an update that never happened. Reproduced with
  `TestWriteSignalDropsTheLastRunsResult` (the old result was still readable after a new request; failed
  before the fix). `WriteSignal` now removes `result` first, so "no result" (already worded for the UI)
  means the watcher has not finished this request.

### `7da4aef` Add the container image and host update watcher

The watcher script had no test at all, so the three defects below were found by driving the real
`update.sh` with stubbed `docker`, `git`, `timeout`, `flock`, `sudo` and `sleep` (new
`compose/watcher/watcher_test.go`, which runs on a laptop and in CI alike).

- **#98** · `7da4aef` · `compose/watcher/update.sh` · **bug** · `json_escape` only escaped `\` and `"` and
  deleted newlines, but docker prints tabs, carriage returns and colour escapes in its errors. Those are
  illegal raw inside a JSON string, so on exactly the failures the operator needs to read, `result` was
  invalid JSON, `update.ReadResult` returned nil and the panel said only "the host update watcher
  reported no result". Reproduced with `TestFailureResultStaysValidJSONWhateverDockerPrints` (result
  unreadable before the fix). Every control character now becomes a space.
- **#99** · `7da4aef` · `compose/watcher/update.sh` · **bug** · `requested` was removed by hand on each
  expected failure path only. Under `set -euo pipefail` any other failing command (`docker compose ps
  -q`, an unwritable `.env`, a full disk) ended the script with the file still there, and
  `ivy-update.path` fires whenever that file exists, so the watcher re-ran the whole `git pull`, pull
  and force-recreate in a loop, restarting Ivy each time. Reproduced with
  `TestRequestIsClearedEvenWhenTheScriptDies` (`requested` survived; failed before the fix). An `EXIT`
  trap now always removes the request and, if no result was written, records that the script stopped
  early.
- **#100** · `7da4aef` · `compose/watcher/update.sh` · **risk** · the health poll gave a new container
  120 s (40 x 3 s) and treated anything but `healthy` as a failed update, including `starting`. The
  first start after an update runs migrations and can be slow on the potato, and the failure path rolls
  back, discarding a good update. Reproduced with `TestSlowStartingContainerIsNotRolledBack` (60
  `starting` polls, then healthy: reported `failed` before the fix). The poll is now 300 s and the unit's
  `TimeoutStartSec` comment arithmetic (780 s of 1800 s) is updated.
- **#101** · `7da4aef` · `compose/watcher/update.sh` · **risk** · the watcher accepted any
  `<repo>@sha256:<digest>` in `requested`. `install.sh` makes the signal directory `0777` so the
  container's uid can write through the bind mount, so any local user can drop a file there, and the
  script then runs `docker compose pull`/`up` as the deploy user with the data directory mounted.
  Reproduced with `TestForeignImageIsRefused` (`registry.example.net/someone/else@sha256:...` was pulled
  and reported `ok`; failed before the fix). The pattern is now pinned to `ghcr.io/autumnsgrove/ivy`,
  the same constant as `update.DefaultRepo`.

### `d570eeb` Serve /search and wire it into the app

- **#102** · `d570eeb` · `gateway/search.go` · **bug** · the handler only embedded the query when the
  request named an `account_id`, but the search screen always shows "All accounts" and sends none (its
  loader calls `api.search(q)`). Meaning-based search therefore never ran from the app at all, while the
  embed worker kept paying to embed every message for it, and no test covered the semantic path through
  the handler. Reproduced with `TestSearchWithoutAnAccountStillSearchesByMeaning` (a query sharing no
  word with either message returned 0 hits; failed before the fix). With no account named, accounts are
  now offered to the gate in turn until one will take the query, so at most one paid call is made; a
  provider outage or vector-scan failure is logged and search stays keyword-only.

### `e34fc80` Index mail into FTS and embed it once in the background

- **#103** · `e34fc80` · `search/search.go` · **bug** · attachments are embedded under their content
  hash, but `VectorSearch` returned that hash as a hit's content key, which resolves to no message, so
  `handleSearch` dropped every attachment match as "hidden by the time the page was built". Everything
  paid for attachment meaning was unreachable, and the dead hits also took up result slots.
  Reproduced with `TestSearchByMeaningFindsTheMessageCarryingAnAttachment` (0 hits; failed before the
  fix). Attachment matches now fan back out to every visible message that carries the file
  (`store.ContentRefsForAttachment`, bounded by `MaxAttachmentRefs`), best match first. The scan's
  dead no-op loop and its key-collision between body and attachment refs went with it.
- **#104** · `b74c0c2` · `sync/extract.go` · **bug** · extracted attachment text is keyed by the file's
  hash and shared by every message carrying it, but `ExtractTexts` reindexed only the one message the
  pending query picked, so the text was searchable through one message and silently missing from the
  rest (a forwarded PDF, a re-sent invoice). Reproduced with
  `TestSharedAttachmentTextIsIndexedForEveryMessageCarryingIt` (1 of 2 messages found; failed before the
  fix). It now reindexes every message with that hash.

### `71fd522` Add tier 0-1 attachment text extraction

Attachments are sender-controlled and extraction runs inside the sync worker's Settle, so these are
denial-of-service paths on the board. Sizes were measured at several scales with a hard `timeout`
before fixing: an `.ics` with 10k/20k/40k/80k folded lines took 11/39/171/556 ms (quadratic, so a
1 MiB file is tens of seconds and a 32 MiB one effectively forever); an OOXML archive of 8 KiB parts
inflating to 8 MiB of markup each took 0.28/1.1/4.5 s for 1/4/16 parts (linear in parts, with no cap
on parts and no use of the caller's deadline).

- **#105** · `71fd522` · `extract/extract.go` · **bug** · `unfold` appended each continuation line to the
  previous one with `+=`, copying the growing line on every fold. Reproduced with
  `TestExtractCalendarManyFoldsIsLinear` (200k folds: 2.3-2.6 s, asserted under 1 s; failed before the
  fix). The line is now built in a `strings.Builder`.
- **#106** · `71fd522` · `extract/extract.go` · **bug** · the OOXML walk never looked at `ctx`, and each
  part could inflate to `MaxInputBytes` (32 MiB) with no limit on the total or on the number of parts,
  while output limits never fire for markup with no text. So the 20 s `extractTimeout` did nothing for
  `.docx/.xlsx/.pptx`, and a zip of a few KiB held the settle pass for minutes. Reproduced with
  `TestExtractOOXMLHonoursItsDeadline` (ran ~7 s past a 150 ms deadline) and
  `TestExtractOOXMLInflationIsBounded` (a part after the budget was still read). The walk now polls the
  context and shares a 64 MiB inflation budget across the archive (`maxOOXMLInflate`).
- **#107** · `71fd522` · `extract/extract.go` · **bug** · `appendXMLText` stopped a part when the
  builder's total length reached the room *left*, so once earlier parts had used a little over half of
  `MaxOutputBytes`, every later part was cut off after its first run of text (a big shared-strings
  table starved every worksheet). Reproduced with `TestExtractOOXMLLaterPartsGetTheRemainingRoom` (the
  second part's text was missing; failed before the fix). The ceiling is now computed per part.

### `e34fc80` (embed worker)

- **#108** · `e34fc80` · `search/search.go`, `store/embeddings.go` · **bug** · a message whose body is only
  whitespace and which has no subject (or an attachment whose extracted text trims to nothing) yields no
  chunks, and the worker skipped it with `continue` without recording anything, so it stayed pending and
  sat at the head of the newest-first, `Batch`-sized queue forever. Enough of them at the head starved
  every older message of its embedding for good. Reproduced with
  `TestEmbedWorkerDoesNotStallBehindMailWithNothingToEmbed` (batch 2, three blank messages newer than a
  real one: the real one was never embedded; failed before the fix). Such a document is now recorded as
  done with an empty zero-dimension row (`MarkEmbeddingEmpty`) that the vector scan already skips.
- **#109** · `e34fc80` · `search/search.go` · **bug** · any provider error on one document ended the whole
  pass, and the next pass met the same document first again, so a single refused document blocked
  everything behind it (and paid for a failed call every minute). Reproduced with
  `TestEmbedWorkerSkipsOneRefusedDocument` (nothing embedded; failed before the fix). One failed
  document is now logged and skipped; three in a row (a real outage) end the pass. A refused document is
  still retried each pass: see N33.
- **N33 (open, needs a decision)** · `e34fc80` · `search/search.go` · a document the provider refuses
  every time is skipped but not remembered, so it costs one failed call and one `error` ledger row per
  pass (once a minute) for as long as it exists. Recommend counting failures per ref in the ledger and
  tombstoning after, say, five, once there is a real provider to see what a refusal looks like.

### `0ac3c61` Add the embeddings gate, providers and vectors

- **#110** · `0ac3c61` · `llm/gate.go` · **standards** · `record` discarded the error of
  `RecordAPICalls` (`_ = ...`). The ledger is the cap's only record of spend, so a failed write meant
  money left the account with no row and no trace, and the cap could drift below reality in silence.
  Reproduced with `TestGateReportsALedgerWriteFailure` (state DB write handle closed: the paid-for vector
  came back and nothing was logged; failed before the fix). The vectors are still returned (the call was
  paid for); the failure is now logged at error level with provider, endpoint, row count and cost, and no
  mail content.
- **N34 (open, needs a decision)** · `0ac3c61` · `llm/embedder.go`, `llm/gate.go` · when OpenRouter
  returns no `usage.cost` the call is ledgered with `cost_usd = 0` and `cost_estimated = 1`, so the
  monthly cap (`spent_usd >= cap`) never sees that spend and can never trip. The default provider is
  expected to report a cost, so this is latent. Recommend computing an estimate from tokens and a
  per-model price table when the cost is missing, as the ledger comment already promises, and checking a
  real response from `perplexity/pplx-embed-v1-0.6b` once on the potato to confirm it reports `cost`.

### `d570eeb` Serve /search and wire it into the app

- **#111** · `d570eeb` · `gateway/search.go` · **risk** · the query embedding and vector scan had no
  deadline beyond the provider client's 60 s, though the design says an outage falls back quietly to
  keyword search. A provider that hangs held every search open for the full minute. Reproduced with
  `TestSearchFallsBackQuicklyWhenTheProviderHangs` (no answer within 3 s while the provider hung; failed
  before the fix). The meaning half now has its own 5 s deadline (`semanticTimeout`) and falls back.
- **#112** · `d570eeb` · `gateway/search.go` · **standards** · the query had no documented maximum, yet it
  goes verbatim to a paid provider (and into an FTS expression). Reproduced with
  `TestSearchRejectsAnOversizeQuery` (a 2049-byte query returned 200; failed before the fix). It is now
  capped at 2048 bytes with a 400.

### `d570eeb` (startup wiring)

- **#113** · `d570eeb` · `cmd/embed.go` · **standards** · `buildEmbedders` dropped an account without a
  word when `embed_provider: openrouter` had no `OPENROUTER_API_KEY` (the key lives in `data/.env`, easy
  to forget on a fresh install) or `embed_provider: ollama` had no `llm.ollama_url`, so search stayed
  keyword-only and nothing said why. Reproduced with `TestBuildEmbeddersSaysWhyAnAccountIsLeftOut` (empty
  log; failed before the fix). Each case now logs a warning naming the account and the missing setting.

### `b6e447d` Derive People and serve them with merges

- **#114** · `b6e447d` · `store/people.go` · **bug** · `LinkPerson` only refused `address == person`. After
  a was merged into b, merging b into a stored a second link, so a resolved to b and b to a: the gateway's
  bounded resolver returned each as the other's canonical id, the pair split into two people with swapped
  ids, and the merge the operator had just asked for did nothing. Reproduced with
  `TestLinkingAPersonBackNeverMakesACycle` (a and b resolved to different people; failed before the fix).
  `LinkPerson` now links to the person's canonical address (also `TestLinkingToAMergedPersonFollows...`, so
  chains cannot outgrow the resolver's 8 hops) and, when that address is the one being merged, drops the
  old link first.
- **N35 (open, needs a decision)** · `b6e447d` · `gateway/people.go` · `GET /people` returns every address
  ever seen in a From, To or Cc, newsletters and one-off senders included, as one unpaged list, and each
  person page reloads the whole table to find one person. STANDARDS 4a wants paged lists. Fine for a small
  mailbox, but a real one has tens of thousands of addresses. Recommend a keyset-paged list sorted by
  count, a minimum message count, and a lookup by address for the person page; it needs a UI paging
  decision for the People screen.
- **N36 (open, measure first)** · `b6e447d` · `sync/sync.go` · `RebuildPeople` runs inside every `Settle`
  and re-reads every visible message of the account, decodes three header columns, deletes and reinserts
  every row. `threadAccount` already does a similar full pass, so this matches the existing shape, but it is
  O(mailbox) work on every sync that stores anything, on the potato. Not changed without a number: time
  one rebuild at 5k messages on the board and extrapolate (do not run the 100k profile). If it matters,
  rebuild only when a pass stored or hid mail, or update the touched addresses incrementally.

### Baseline: golangci-lint

- **#115** · `0ac3c61`, `b6e447d`, `901c0e3`, `b74c0c2` and others · **standards** · the 13 issues
  golangci-lint v2.12.1 (CI's pin) reported at the start of this review, which would have failed the `go`
  job. Fixed in one commit because they are one-line each: `errors.Is` for the `io.EOF` and `ErrNotFound`
  comparisons (`extract`, `store/search_test.go`), `ctx` first in `Server.WithUpdate` (plus `_` for the
  unused request, and `cmd` now builds the client with the existing `newUpdateClient` hook rather than a
  second copy), and the builtin names `cap` and `max` no longer shadowed (`devstack`, `llm`,
  `update_test`). Four gosec findings are deliberate and carry a same-line reason: the int8/byte
  reinterpretation in the vector codec (G115, two sites), the fixed-fragment `LIKE ?` join in
  `ConversationsForAddresses` (G202, every value is bound) and the operator-configured signal directory
  (G304). The gate at the tip is clean: `golangci-lint` 0 issues, `staticcheck`, `go vet`, `gofumpt` and
  `CGO_ENABLED=1 go test -race ./...` all pass.

### `dac8acc` Hide snoozed and Reading mail, serve both locally

- **#116** · `dac8acc` · `store/inbox.go`, `store/snooze.go`, `store/tags.go`, `gateway/snooze.go` ·
  **bug** · a content key is the hash of the Message-ID, so a mailing-list post delivered to two of the
  operator's accounts has the same key in both. Snoozes and Reading membership are stored per account, but
  the lists of hidden keys passed to the inbox query were bare keys and `ContentKeysForTag` ignored the
  account, so snoozing or tagging one account's copy hid the other account's copy: in the combined inbox
  for a snooze, and in the other account's own inbox for Reading (with a wrong "reading waiting" count).
  Reproduced with `TestSnoozingOneAccountsCopyKeepsTheOthersInTheCombinedInbox` and
  `TestReadingOneAccountsCopyKeepsTheOthersInItsInbox` (both failed before the fix). The hidden lists are
  now (account, key) pairs end to end (`ActiveSnoozeRefs`, `ContentRefsForTag`, `MessagesByContentRefs`,
  `InboxQuery.Hide`), compared as `account || char(31) || key`.
- **N37 (open, needs a decision)** · `dac8acc` · `gateway/snooze.go` · the Snoozed, Reading and tag views
  are not paged: each answers with at most `MaxInboxLimit` (200) rows and no cursor, though up to 2000
  messages may be snoozed or in Reading. A message in Reading or Snoozed is also hidden from the inbox, so
  past the newest 200 it is listed nowhere (search still finds it). A rule that sends newsletters to
  Reading makes this reachable in weeks. Fixing it means a cursor in the `Reading` and `Inbox` contract
  and the screens, so it is not done here. Recommend keyset paging on (date, id) like the inbox, with the
  Reading feed taking `cursor` and returning `nextCursor`.

### `0c7abc0` Add the FTS index, extracted text and cost ledger schemas

- **#117** · `0c7abc0`, `b74c0c2` · `store/search.go`, `sync/sync.go` · **bug** · migration 13 creates
  `search_index` empty and `DerivedVersion` was not bumped, and a message is only indexed when it is
  derived. Any mirror that already held mail when this version first ran (an existing dev mirror, or the
  potato's if it has synced before) had no search document for any of it, and nothing would ever build
  one, so old mail was invisible to keyword search for good. Reproduced with
  `TestIndexMissingSearchDocsBackfillsAnExistingMirror` and
  `TestSettleIndexesMailThatPredatesTheSearchIndex` (the second: index emptied, `Settle` run, 0 hits;
  failed before the fix). `Settle` now builds the missing documents in batches of 500
  (`IndexMissingSearchDocs`), newest first. A fresh install is unaffected.
- **N38 (open, measure first)** · `0c7abc0` · `store/search.go` · the backfill query has to scan the
  account's messages and probe `search_docs` on every settle to prove nothing is missing, so it costs a
  full pass in the steady state, like `RebuildPeople` (N36). Measure both at 5k messages on the potato; if
  they matter, keep a per-account "backfill complete" marker in `state.db` and skip once it is set.
- **N39 (open, needs a decision)** · `d570eeb` · `cmd/ivy-dev/main.go`, `internal/devstack/stack.go` ·
  `ivy-dev` builds its gateway without `WithSearch` and never starts the embed worker, so `make dev` and
  `make dev-fake` search by keyword only and never embed, although `docs/DEV.md` says embeddings follow
  `--llm` and `stack.go` points the config at the fake embedder (nothing reads it). Production wiring lives
  in `cmd/cmd.go` and `cmd/embed.go` (unexported, package `cmd`). Recommend one exported function that
  builds the gate, embedders, query embedder and worker from a `config.Config`, used by both `ivy run` and
  `ivy-dev`, so the dev stack and the e2e suite exercise the code that ships.
- **N40 (open, measure first)** · `e6b76e9`, `b6e447d`, `0c7abc0` · every `Settle` now runs three passes
  that touch the whole account to prove there is little to do: the rule pass (`RuleMessages` over
  unevaluated mail, `GROUP BY content_key` and a sort), `RebuildPeople` (N36) and the search backfill
  (N38); the embed worker repeats the same shape once a minute (`PendingBodyRefs`,
  `PendingAttachmentRefs`). None is a defect at a few thousand messages and all are O(mailbox) per run on
  the Le Potato. Before the real mailbox syncs: time one `Settle` at 5k messages on the board (do not run the
  100k profile, extrapolate), then decide whether `Settle` should skip the people/rule/index passes when it
  stored and hid nothing.
- **N41 (open, needs a decision)** · `7da4aef` · `store/migrations.go`, `compose/watcher/update.sh` · the
  watcher rolls back to the previous image when the new one is unhealthy, but the new image may already have
  applied migrations. `migrate` accepts a database whose `user_version` is newer than the binary knows
  (it only runs what is missing), so the old image will open the upgraded schema and run on it. That is safe
  while migrations stay additive (they are append-only), but nothing enforces it. Recommend refusing to open
  a database newer than the binary in `ivy run` with a clear message, or documenting that a rollback relies
  on additive migrations and testing the previous image against the new schema before each release.

### Gate at the tip of this review (`25a9896..3d26e01`, 2026-10-05)

`go build`, `go vet`, `staticcheck`, `gofumpt -l`, `golangci-lint` v2.12.1 (CI's pin, **0 issues**, was 13),
`CGO_ENABLED=1 go test -race ./...` (all packages, and `-count=3 -race` on every package touched),
arm64 cross-compile, `govulncheck` v1.1.4 on go1.26.8 (none), `make drift`, `shellcheck` on `update.sh`,
`pnpm check` and `pnpm test` (254), the full Playwright suite (**270 passed, 10 skipped by design**,
WebKit phone and Chromium desktop, run on the operator's laptop where the pinned browsers exist), and
`make smoke` (10/10 against the real embedded binary). The image was built from this branch and booted
with an empty data directory: healthy, `/search` answers, the daily backup ran. The real registry exchange
(`ghcr.io/token` then the manifest HEAD for `autumnsgrove/ivy:latest`) was run by hand and returns 200
with a `Docker-Content-Digest`, so the package is public and `ivy update` can resolve it.

**Not verified anywhere in this review (needs the potato or a real account):** `sudo ./install.sh` and
the systemd path/timer/service on the board; a real `ivy update` end to end (the watcher script is proven
only against stubs: `compose/watcher/watcher_test.go`); real Purelymail behaviour (MOVE, UIDPLUS, COPYUID,
expunge against the live Trash, N32); a real OpenRouter embeddings response (does
`perplexity/pplx-embed-v1-0.6b` report `usage.cost`? N34); timings on the potato (N36, N38, N40) and the
vector scan time over a real mailbox; the update rollback against a real unhealthy image. The published
`:latest` image predates this branch, so the potato only gets these fixes after the branch is merged to
`main` and `docker-publish.yml` has run.

## Resolution of the open items (round 58)

- **N41 resolved** · `store/migrations.go` · `migrate` refuses a database whose `user_version` is newer than
  the newest migration the binary knows (`ErrSchemaNewer`, with a message naming both versions and the way
  out), for the mirror and the state database alike. A rollback onto a migrated schema now fails loudly and
  the watcher reports the container's log. Reproduced with `TestOpenRefusesADatabaseNewerThanTheBinary`
  (opened without error; failed before the fix).
- **N34 resolved** · `llm/embedder.go` · a response with no `usage.cost` is now priced from its tokens at the
  model's listed rate (`perplexity/pplx-embed-v1-0.6b` $0.000000004/token, `-4b` $0.00000003/token, read from
  OpenRouter's public embeddings model list on 2026-10-05) and flagged `cost_estimated`; a response with no
  usage has its tokens estimated at 4 bytes each, and a model with no listed price is priced at the dearest
  listed rate so the cap errs early rather than never. Reproduced with
  `TestOpenRouterEstimatesTheCostWhenNoneIsReported`, `TestAnUnpricedModelIsEstimatedConservatively` and
  `TestTokensAreEstimatedWhenTheProviderReportsNone` (all $0; failed before the fix). Still worth one look at
  a real response from the potato to see that the provider does report `cost`.
- **N33 resolved** · `search/search.go`, `llm/gate.go`, `llm/embedder.go`, `store/ledger.go` · a document
  the provider refuses five separate times is recorded as skipped and leaves the queue. Provider errors are
  now a typed `llm.StatusError`, and the gate records HTTP 400, 413 and 422 as outcome `rejected`; 401, 403,
  429, 5xx and network failures stay `error`, so an outage or a bad key can never count against a document
  (`TestEmbedWorkerNeverGivesUpOnADocumentBecauseOfAnOutage`). Calls are counted by a new per-call id the
  gate now writes to `api_calls.call_id` (the column existed and was never filled), not by timestamp: the
  ledger's timestamps are one second wide and my first attempt, counting distinct instants, counted ten
  passes inside a second as one. Reproduced with `TestEmbedWorkerGivesUpOnADocumentAfterFiveRefusals` (10
  calls, still queued; failed before the fix), `TestGateRecordsADocumentRefusalDistinctlyFromAnOutage` and
  `TestCountRejectedCallsCountsCallsNotRows`.
- **N37 resolved (backend)** · `store/inbox.go`, `gateway/snooze.go`, `api/openapi.yaml` · the Snoozed, tag and
  Reading views now page with the inbox's keyset cursor (`MessagesByContentRefs` takes a cursor and returns a
  page; 50 a page, 200 at most). The Reading feed gains `cursor` and `nextCursor`, and its digest counts the
  whole feed rather than the page. A malformed cursor is a 400 even for an empty view. Reproduced with
  `TestTagViewPagesThroughEveryMessage`, `TestSnoozedViewPagesThroughEveryMessage` and
  `TestReadingFeedPagesThroughEveryIssue` (120 messages: these were already satisfied once the store paged;
  the failing evidence was the old 200-row cap and `TestMessagesByContentRefsPages`, which could not compile
  against a function with no cursor), plus `TestAnInvalidCursorIsABadRequest` (200 before the fix).
- **#118** · `dac8acc`, `d570eeb` · `store/inbox.go` · **bug** · found while paging: the keyset comparison
  `(m.date, m.id) < (?, ?)` is never true for a message with a NULL date, so an undated message was listed
  only if it fitted on the first page of the inbox, and the cursor for an undated last row was a year-1 date.
  Reproduced with `TestInboxPagesReachUndatedMail` (3 of 5 messages reached; failed before the fix). The
  inbox and the local views now order and compare on `COALESCE(date, '')`, and the cursor's presence is
  decided by its id, not its date.
- **N35 resolved (backend)** · `gateway/people.go` · `GET /people` returns a `PeoplePage` (`items`,
  `nextCursor`), 100 a page, most correspondence first, with an offset cursor over the in-memory grouping and
  a fixed tie-break (map order made equal people swap places between requests, which would have shown a
  person twice or never across a page boundary). Reproduced with `TestPeoplePagesInHundreds` (the endpoint
  returned a bare array; failed before the fix). The contract change is breaking, so the web client moves with
  it.
- **#119** · inbox wiring (chunk 2) · `web/src/routes/(mail)/+layout.ts`, `PhoneInbox.svelte`,
  `DesktopInbox.svelte` · **bug** · found while paging: the inbox screens loaded the first page and never
  followed `nextCursor`, so a real mailbox showed its newest 50 messages and no way to reach the rest except
  search. (Only the spend log, built later, had a "Show older".) Fixed with the shared `Pager`
  (`web/src/lib/pager.svelte.ts`, six unit tests, including that a page requested for one folder is dropped
  when the reader switches to another): the inbox, the Reading feed and People now load the first page in the
  route and append the rest with a "Show older" / "Show more people" button. The e2e fake serves two pages
  under `?scenario=paged` and `web/e2e/paging.spec.ts` covers all four lists on phone and desktop. Search is
  still one page: its handler ignores the `cursor` and `limit` the contract declares (see N42).
- **N42 (open, needs a decision)** · `d570eeb` · `gateway/search.go`, `api/openapi.yaml` · `GET /search` takes
  `limit` (default 50, at most 200) and a `cursor`, but the handler ignores the cursor and never returns a
  `nextCursor`, so a search shows at most the top 200 fused hits. That is probably enough for a person, so it
  is not changed here; if wanted, an offset cursor over the fused ranking (fetch `offset + limit`, slice) is
  the cheap way.
- **N39 resolved** · `cmd/embed.go`, `cmd/cmd.go`, `cmd/ivy-dev/main.go`, `internal/devstack/devstack.go` ·
  the search stack (gate, embedders, query embedder, embed worker) is built by one exported
  `cmd.NewEmbedding(cfg, dbs, apiKey)`, used by `ivy run` and by the in-process dev server, so dev and the e2e
  suite run the code that ships. Reproduced with `TestUpEmbedsTheSeededMailAndSearchesByMeaning` (the dev
  server found nothing by meaning after 15 s; failed before the fix) and
  `TestNewEmbeddingEmbedsAndAnswersQueriesAgainstTheFakeProvider`. Two things DEV.md already promised and the
  code did not do came with it: `devstack.ResolveLLM` makes `--llm live` with no `OPENROUTER_API_KEY` (in the
  environment or the repo `.env`) start on the fake and say so, and the dev Ivy is handed a throwaway key
  when it talks to the fake, so a real key in the operator's environment never reaches the fake endpoint. The
  existing in-process `up` tests now pass `--llm fake` explicitly: with the stack wired, a real key in a
  developer's environment would otherwise have sent them to the live provider.
- **N36 / N38 / N40, measured on the laptop, decision pending the potato numbers** · `sync/settle_bench_test.go`
  (`BenchmarkSettleSteadyState`, 1k and 5k messages, backlogs drained first) on an Apple M2: People 4.7 ms to
  25.8 ms, rule pass 1.0 ms to 5.1 ms, search backfill 0.9 ms to 4.9 ms, the whole `Settle` 30.6 ms to
  162.7 ms. Each is linear (5.0-5.5x for 5x the messages), so about 0.7 s of the three together at 100k on the
  laptop; but they are only about a fifth of a settle, and the remaining ~125 ms at 5k is the older passes (thread
  rebuild, re-derive, extraction queue). My first run of the benchmark showed 116x growth for the backfill and
  17x for the rules: that was the benchmark, not the code, because the backfill takes 500 messages a settle and
  the rule pass 200, so two warm-up settles never drained a 5k mailbox and it was timing real work. It now
  drains first and asserts it did. Run it on the board (see `docs/PERFORMANCE.md` "Settle at rest") and decide
  with those numbers; nothing is changed without them, as agreed.
- **Runbook written** · `docs/DEPLOY.md` (linked from `README.md` and the `CLAUDE.md` doc map): first install,
  `ivy.yaml` and `data/.env` templates (Purelymail's IMAP 993 / SMTP 465 from spike S1), `allowed_hosts`, first
  start, an update and a manual rollback, backup and restore, a troubleshooting table and the live checklist.
  Writing it turned up one thing to change rather than document: narrowing the published port meant editing the
  tracked `docker-compose.yml`, which the watcher's `git pull` would then trip over, so the bind address is now
  `IVY_BIND` in `.env` (default `0.0.0.0`, unchanged behaviour).

## Review of `d703629..839bf05` (chunk 4a-4c: compose, SMTP, send queue, send API; 2026-10-06)

Baseline at `839bf05`: `go build`, `go vet`, `staticcheck`, `gofumpt -l` clean and `go test -race ./...` green
(CGO on); nothing was broken before the review began.

### `d08120d` Build outgoing messages in compose

- **#120** · `d08120d` · `compose/header.go`, `compose/compose.go` · **bug** · display names that needed RFC 2047
  encoding (any non-ASCII name, or an ASCII one that looks like an encoded word) were encoded and then handed to
  net/mail, which wrapped the encoded words in a quoted-string: `From: "=?utf-8?B?Wm/Dqw==?=" <a@b>`. RFC 2047
  section 5 forbids an encoded word inside a quoted-string, so a compliant client shows the raw `=?utf-8?B?...`
  text. The existing test passed because its `parseAddress` helper decodes a quoted name itself, a lenient
  oracle. Reproduced with `TestBuildEncodedDisplayNamesAreNotQuoted` (net/mail's own parser, no extra decoding;
  failed before the fix for From, To and Bcc, three names). `compose` now writes From, To, Cc, Reply-To and the
  Sent copy's Bcc itself (`formatAddress`): ASCII names still go through net/mail, anything else is bare
  encoded words.
- **#121** · `d08120d` · `compose/wire.go`, `compose/compose.go` · **bug** · an all-ASCII body is sent by enmime as
  7bit exactly as given, so the operator's lone LF or CR and any line over 998 bytes reached the wire (a paragraph
  typed on a phone is one long line) and the Sent copy was written with bare LFs. Non-ASCII bodies were fine
  (quoted-printable). Reproduced with `TestBuildBodyLinesAreWireSafe`, plain and markdown (failed: bare LF at
  byte 237; the long line check is behind it). New `wireText` normalises to CRLF and breaks any line over 900
  bytes at a space (plain text also hard-cuts at a rune boundary when a line has none; HTML only breaks at spaces,
  so a tag is never split). The markdown test now compares the text part line-ending-insensitively.
- **#122** · `d08120d` · `compose/header.go` · **bug** · `Validate` accepted a message with no recipients, and `Build`
  then failed with enmime's bare "no recipients" error instead of a `*ValidationError`, so a caller that maps
  validation errors to a refusal would see a server fault. Reproduced with `TestNoRecipientsIsAValidationError`
  (failed for both `Validate` and `Build` before the fix); `validate` now refuses it with field `recipients`.

### `54913ce` Submit mail over implicit TLS in smtp

- **#123** · `54913ce` · `smtp/smtp.go` · **bug** · every failure at `RCPT` became `KindRecipientRefused`, which is
  permanent: a 4xx (greylisting, "try again") and a dropped connection or timeout during RCPT failed the send
  for good, though nothing had been accepted and a retry was safe. Reproduced with
  `TestSubmitTransientRecipientRefusalIsRetryable` (a 451 at RCPT via `SMTPRejectRcpt`; failed: kind
  `recipient_refused`, not transient). Only a 5xx is a recipient verdict now; anything else goes through
  `classify` with `Recipient` set.
- **#124** · `a12971f` · `docs/ARCHITECTURE.md`, `docs/STANDARDS.md` · **standards** · the 4a docs said the text part
  goes out "exactly as typed" and the 4a limits table had no row for line length; both now describe `wireText`
  (CRLF, 900-byte lines). `docs/qa-log.md` round 61 keeps its original wording, as a record of the answer.

### `56660d6` Store the send queue in state.db

- **#125** · `56660d6` · `store/sendqueue.go` · **bug** · `EnqueueSend` was idempotent on `(account, message_id)`, but the
  send handler mints a new Message-ID for every request, so a double tap carrying the same client id never
  matched and the second insert failed on the primary key: the operator got a 500 for a message that was queued.
  Reproduced with `TestEnqueueSendIsIdempotentOnTheRowID` (failed with `UNIQUE constraint failed:
  send_queue.id`). A repeat of the same row id now returns the existing row (`created=false`); an id owned by
  another account is refused rather than handed back across accounts.
- **#126** · `56660d6` · `store/sendqueue.go` · **risk** · `RetrySend`, `FailSend`, `MarkSendUnconfirmed`,
  `MarkSendAppended`, `MarkSendDone` and `SetSendAppendID` updated by id alone, so a late write could move any
  row anywhere, including reviving a terminal one: a worker that had chosen a row just before the operator undid
  it would, on its `beforeData` refusal, call `RetrySend` and put the **cancelled** row back to `queued`, and the
  message the operator took back would be sent (CHUNK4-BRIEF T11). It cannot happen while one clock orders the
  worker and the undo, but a clock step is enough. Reproduced with `TestSendStateWritesNeverReviveATerminalRow`
  (all five writes succeeded on a cancelled row, which ended `done`). Every transition now names the states it may
  leave (`queued`/`submitting` for retry and fail, `submitting` for unconfirmed, `submitted` for appended, done and
  the append id) and a wrong-state write is `ErrNotFound`. `TestPruneSendQueueRemovesOnlyOldTerminalRows` took a
  `queued` row straight to `done`; it now walks the real path.
- **N43 (open, latent)** · `56660d6` · `store/migrations.go` · the partial unique index
  `idx_send_queue_live_message` (migration 10) excludes `appended`, `done`, `failed` and `unconfirmed` but not
  `cancelled`, which the code treats as terminal everywhere else. A cancelled row therefore still blocks a new row
  with the same Message-ID with a constraint error. Nothing reaches it today (every request mints a fresh
  Message-ID and the client-id repeat is answered earlier), so no migration is added; if stage 4d reuses the
  Message-ID when a draft is resent after an undo, add migration 12 that recreates the index with `'cancelled'`
  (migrations are append-only).

### `677fe49` Drain the send queue and file Sent copies

- **#127** · `677fe49` · `send/worker.go` · **risk** · the writes that record an accepted message (`submitting` to
  `submitted`, the Sent append op) ran under the worker's own context, so a shutdown landing in the few
  milliseconds after the server's 250 made them fail and left the row `submitting`; the next start then called a
  message that had in fact been accepted `unconfirmed` and never filed its Sent copy. Reproduced with
  `TestSendShutdownAfterTheServerAcceptsStillRecordsIt` (the clock hook cancels at the first read after the fake
  records the message; failed: state `submitting`, no append id). `submitted` now runs under
  `context.WithoutCancel` bounded by a 10 s `settleTimeout`.

### `839bf05` Add the send API and undo send

- **#128** · `839bf05` · `store/sendqueue.go`, `gateway/send.go` · **standards** · `GET /send?limit=` passed the
  caller's number straight into `LIMIT`, so `limit=1000000` read every send row with both message bodies and the
  stored draft (STANDARDS 4a: no unbounded reads). Reproduced with `TestSendsByAccountClampsTheLimit` (a limit of a
  million returned 205 rows; failed). `SendsByAccount` now clamps to `store.MaxSendListLimit` (200, default 50);
  the row is in the STANDARDS limits table.
- **#129** · `839bf05` · `docs/STANDARDS.md` · **standards** · the 4a limits table said a refused outgoing field is
  "400 `bad_request`"; the handler answers 400 `invalid_message` (and `bad_request` only for a malformed or
  oversized request). The five compose rows now say `invalid_message`.
- **N44 (open, measure first)** · `839bf05` · `store/sendqueue.go` · every read of a send row (`GET /send`, `GET
  /send/{id}`, the worker's `notify`) selects `wire_body`, `sent_body` and `compose_json`, three copies of the
  message, though the list and status need none of the first two. Today that is at most about 3 MiB a row and 200
  rows; with attachments (4g) the bodies grow to tens of MiB and the "stream sender-sized data through disk" rule
  applies. Not changed without a number from the potato: when 4g lands, split the metadata read from the body
  read (and keep bodies out of the row), then benchmark `GET /send`.
- **#130** · `839bf05` · `gateway/send.go` · **standards** · the client's idempotency `id`, which becomes the row's
  primary key and a URL segment, had no maximum (only the 1 MiB body bound it). Reproduced with
  `TestSendRefusesAnOversizedClientID` (a 129-byte id was accepted with 202; failed). It is now refused with 400
  `bad_request` above 128 bytes (`maxSendIDBytes`, in the limits table).

### Lint at the CI-pinned version (golangci-lint v2.12.1, `.golangci.yml`)

- **#131** · `839bf05` · `gateway/send_test.go` · **nit** · `err != store.ErrNotFound` (errorlint); now `errors.Is`.
- **N45 (open, outside this range)** · `84533c5`, `40d5779`, `128e582` · `internal/accountsvc/connector.go:127,148`,
  `internal/secrets/secrets.go:95`, `store/accountconfigs.go:63` · the pinned linter reports four more findings on
  `main`, all from commits before `d703629`: `contextcheck` on `Supervisor.Start(acct)` (twice, the supervisor
  deliberately owns its own context), and `errcheck` on `defer f.Close()` / `defer rows.Close()` (the config
  excludes `io.Closer` interface calls but not these concrete ones). Decide whether CI is meant to be clean at the
  pin; the cheap fix is `//nolint:contextcheck // reason` on the two calls and `_ = rows.Close()` style closes.
- **#132** · `56660d6` · `store/sendqueue.go` · **risk** (operator decision) · `NextQueuedSend` took the lowest-sequence
  queued row and returned "none" if it was not yet due, so one message in a retry backoff (a greylisted
  recipient: 5 s doubling to 15 min over up to 8 tries) held every later send for hours. The operator chose
  independent rows. `TestNextQueuedSendRespectsUndoAndBackoff` was rewritten for the new rule and failed before
  the change (send-2 was held behind send-1's window); the query now filters on `undo_deadline` and
  `next_attempt_at` and orders the due rows by sequence. STANDARDS row updated.
- **N45 resolved** (operator asked for it) · `internal/accountsvc/connector.go`, `internal/secrets/secrets.go`,
  `store/accountconfigs.go` · the four findings above are fixed: `//nolint:contextcheck` with the reason on both
  `Supervisor.Start` calls, and explicit discarded closes on the two read-only defers. `golangci-lint run` at
  v2.12.1 now reports 0 issues.

## Review of `2cabdd0..4f34628` (chunk 4d-4h: drafts, identities, attachments, rich text; 2026-10-06)

Baseline on `4f34628`: `go build`, `go vet` and `go test -count=1 ./...` all green; nothing pre-broken.

### `8d637d3`..`fa052ad` drafts

- **#133** · `fa052ad` · `gateway/drafts.go` · **bug** · `GET /drafts?limit=N` bounded the local heads and the mirrored
  Drafts rows each to N and truncated the merge only at the 200 maximum, so `limit=1` could return two rows.
  Reproduced with `TestListDraftsLimitBoundsTheMergedList` (2 rows for limit=1; failed before). The merged list is
  now cut to the same effective limit (default 50, max 200).
- **#134** · `fa052ad` · `gateway/drafts.go` · **risk** · `DELETE /drafts/{id}` accepted the id of any visible message
  and queued a remove op. The worker's `draftFolder` guard refuses it later (`not_drafts`), so the caller got 204
  for an op that could only fail. Reproduced with `TestDeleteDraftRefusesAMessageOutsideDrafts` (204, op queued;
  failed before). It is now 404 and nothing is queued unless the message is in the account's Drafts folder.

### `0d219c2` attachment staging

- **#135** · `0d219c2` · `gateway/uploads.go` · **bug** · the deny list was dodged by a trailing space, dot or tab on the
  name (`evil.exe `, which Windows runs) and by a malformed Content-Type parameter (`text/html; =x`:
  `ParseMediaType` returns the type with an error, and the `err == nil` guard skipped the check). Reproduced with
  `TestAttachmentDenyListSurvivesDisguises` (five disguises allowed; failed before). The name is trimmed before
  `path.Ext` and the media type is split on `;` by hand.
- **#136** · `0d219c2` · `gateway/uploads.go` · **bug** · `POST .../uploads/from-mail` staged any mirrored attachment
  without the deny list, so a received `.exe` could be forwarded though the documented policy refuses it.
  Reproduced with `TestUploadFromMailAppliesTheDenyList` (201; failed before). It now answers 400 `bad_type`; the
  check runs before the copy goroutine starts so an early return cannot leave it blocked on the pipe.

### `c16a0fc`, `53a2ff7` rich-text bodies

- **#137** · `c16a0fc` · `compose/markdown.go`, `web/src/lib/compose/sanitize.ts` · **bug** · `div` was off the outgoing
  allow-list on both the server and the paste walker, and a stripped tag leaves no separator, so
  `<div>one</div><div>two</div>` (what Apple Notes and Safari paste) was sent as "onetwo" in both parts. Reproduced
  with `TestBuildHTMLBodyKeepsDivParagraphsApart` (failed before). `div` is now allowed on both sides and is a block
  in `htmlToText`. The editor itself uses `blockTag: 'P'`, so typed text was never affected.
- **#138** · `c16a0fc` · `compose/html.go` · **bug** · the text/plain alternative dropped every link target, so a
  text-only recipient got "the plan" with no URL. Reproduced with `TestBuildHTMLBodyPlainTextKeepsLinkTargets`
  (failed before). `htmlToText` now appends ` (url)` after the link text unless the text already is the address.
  Verified by `vitest` (47 pass) for the walker; the editor in a real browser is unverified here.

### Open items from the store and attachment review

- **N46 (open, needs a design decision)** · `67cfb5e`, `0d219c2` · `store/uploads.go` · uploads are content-addressed, and
  `StageUpload` writes the blob (`Uploads.Put`) before it inserts the row, while `DeleteUpload` and `SweepUploads`
  decide to remove a blob by counting rows. A delete or sweep that runs between another stage's `Put` and its
  insert sees zero rows, removes the shared file and leaves the new row pointing at nothing. Not reproduced
  deterministically (the window is two statements wide), so not changed. Recommend one `sync.Mutex` in `DBs`
  held across Put-to-insert and across the count-then-remove in delete and sweep.
- **N47 (open, needs a decision)** · `67cfb5e` · `store/uploads.go`, `internal/blobstore` · nothing collects a blob that never
  got a row (a crash or failed insert after `Put`) or a stale `.tmp` from an interrupted `Put`; `SweepUploads` only
  walks rows. Disk use of hostile or crashed uploads grows without bound. Recommend a sweep that lists
  `data/uploads`, skips files younger than an hour, and removes any hash with no row.
- **N48 (open, measure first)** · `05d3fb2`, `2c47e8e` · `store/drafts.go` · every autosave stores the whole built MIME,
  attachments included (up to about 33 MiB base64), as a new `drafts.body` BLOB in `state.db`, which is the file
  that is backed up daily and kept 15 days. An autosave every few seconds with a large attachment is heavy write
  amplification and backup growth on the potato's storage. Recommend measuring a 25 MiB attachment draft on the
  board, then keeping bodies in the blob store by hash with the row holding only the key.

### `919588b` identities

- **#139** · `919588b` · `gateway/identities.go` · **risk** · an identity display name was bounded in length but not
  checked for CR, LF or NUL, so a name with a line break was stored (200) and then made every draft and send as
  that address fail in the builder with a message about the From header. Reproduced with
  `TestSaveIdentityRejectsAHostileName` (200; failed before). It is now a 400 `bad_request` at entry.

### `0aff6ca` reply and forward prefills

- **#140** · `0aff6ca` · `compose/reply.go` · **bug** · reply-all with a Reply-To (a list, a contact form) put only the
  Reply-To in To and the original To and Cc in Cc, so the person who wrote the message dropped out of the
  conversation. Reproduced with `TestReplyAllKeepsTheOriginalSenderWhenReplyToRedirects` (failed before).
  `TestReplyAllPrefillKeepsTheOthers` asserted the old Cc verbatim and now expects the sender first.
- **#141** · `0aff6ca` · `compose/reply.go` · **bug** · replying to a message the operator sent (Sent folder, or a thread
  view) addressed the reply to the operator, because the target was From, and sent it as the account default even
  when an alias wrote it. Reproduced with `TestReplyToOwnMessageGoesToItsRecipients` (to was the operator's own
  address; failed before). The target is now the first recipient who is not the operator, the rest go to reply-all's
  Cc, and a message written by one of the identities is continued as that identity.

### `0186dbd` draft to send link

- **#142** · `0186dbd` · `gateway/send.go` · **standards** · `draftMessageId` is client-supplied, is stored on the send row and
  becomes the Message-ID the worker searches for in Drafts, but had no maximum (only the 1 MiB body bound it;
  4a). Reproduced with `TestSendRefusesAnOversizedDraftMessageID` (a 400-byte id was accepted with 202; failed
  before). It is now refused with 400 above `compose.MaxMessageIDBytes`.

### `bba47fc` compose screen

- **#143** · `bba47fc` · `web/src/routes/compose/+page.svelte`, `web/src/lib/ids.ts` · **bug** · the compose screen called
  `crypto.randomUUID()` for every draft save, send and upload handle, and that function does not exist outside a
  secure context. `docs/DEPLOY.md` has the operator open `http://potato.<tailnet>.ts.net:8418` (plain http), so on
  the phone every save, send and attachment would throw a `TypeError` before a request left. The Playwright runs
  use localhost, which is a secure context, so no existing test could see it. Reproduced with `ids.test.ts`
  ("still works when crypto.randomUUID is unavailable", with the global stubbed away); the module did not exist,
  so the suite failed to load. All three call sites now use `newId()`, which falls back to `getRandomValues`.
  Unverified here: Safari over http on a real tailnet host.
- **#144** · `bba47fc` · `web/src/lib/compose/autosave.ts` · **bug** · the server minted the draft id on the first save, so a
  first save whose reply was lost (a phone on a flaky link) was retried with no `draftId` and the server filed a
  second draft beside the first, orphaning it in Drafts. Reproduced with the new autosave test "names the draft
  before the first save so a retry cannot fork it" (`draftId` was `undefined` on both attempts; failed before).
  The autosaver mints the id before the first attempt. The test that asserted `draftId: undefined` verbatim now
  expects a string.
- **N49 (open, needs a design decision)** · `fa052ad`, `bba47fc` · `store/drafts.go`, `sync/outbox.go` · a draft op that fails
  permanently (over quota, `draft_gone`, `not_drafts`, no UIDPLUS) leaves its version in state `saving` for
  good: the list shows it as saved, `MarkDraftSaved` never runs, and `SaveDraft`'s prune keeps `saving`
  rows, so the body (up to about 33 MiB) is never freed. Nothing in the draft summary exposes the state. Recommend
  a `failed` state set from the worker's `fail`, surfaced as a "not on the server" mark in the list, and pruned
  with the other terminal rows.
- **N50 (open, needs a decision)** · `bba47fc` · `web/src/routes/compose/+page.svelte` · a retried save after a lost reply
  now keeps its draft but still meets `409 draft_conflict` against its own committed version (the retry carries
  a fresh version id and the old base), so the operator sees "This draft changed somewhere else" for their own
  edit. It is safe (the conflict adopts the head and saves again) but misleading. Recommend reusing one save id
  while the revision is unchanged, and having the conflict body carry the head's version id so the client can
  tell its own save from another tab's.

### Docs and measure-first items

- **#145** · `d4734d5` · `docs/STANDARDS.md` · **nit** · the 4a limits table had no rows for the draft Message-ID, the merged drafts
  list limit or the identity name rule (#133, #139, #142); added.
- **N51 (open, measure first)** · `6b78c73` · `web/src/lib/api/client.ts` · `uploadAttachment` reads the file with
  `await file.arrayBuffer()` before sending, so a 25 MiB photo is held twice in the phone's memory; passing the
  `Blob` as the body lets the browser stream it. Not changed without a number from a real iPhone.

### Gate at the end of this review

`go tool gofumpt -l .` clean, `go vet ./...` clean, `go tool staticcheck ./...` clean, `golangci-lint run ./...`
(v2.12.1) 0 issues, `CGO_ENABLED=1 go test -race -count=1 ./...` all packages pass, `pnpm test` (358) and
`pnpm check` (0 errors) pass. Not run here: `govulncheck`, the Playwright suites, a smoke run of the binary, any
real browser or the potato.

## Resolution of the open items from `2cabdd0..4f34628` (operator answers, 2026-10-06)

- **N51 resolved** (operator chose "pass the Blob") · `web/src/lib/api/client.ts` · `uploadAttachment` now hands `fetch` the
  `Blob` itself. Reproduced with the `uploadAttachment` test in `send.test.ts` (the body was an `ArrayBuffer`;
  failed before). The memory saving on a real iPhone is unmeasured and remains the operator's live check.
- **N50 resolved** (operator chose "reuse the save id while unchanged") · `web/src/lib/compose/autosave.ts`,
  `web/src/routes/compose/+page.svelte` · the autosaver now supplies each attempt's `saveId` and keeps it for a
  retry of unchanged content, so the server's idempotent path (`TestSaveDraftIsIdempotentOnTheClientID`) returns
  the stored version instead of a conflict with the operator's own save; an edit mints a new id. Reproduced with
  "reuses the save id for a retry of unchanged content" (`saveId` did not exist; failed before). The 409 body is
  unchanged, so a genuine second-tab conflict still shows its toast.
- **N46 resolved** (operator chose "one mutex in DBs") · `store/uploads.go`, `store/store.go` · a `sync.RWMutex`: `StageUpload`
  holds the read lock from before the blob is written until the row is inserted, and delete and sweep release a
  blob through `releaseUploadBlob`, which takes the write lock with `TryLock` and recounts rows under it. A removal
  that finds a stage in flight skips the file instead of waiting (a delete must not hang behind a 25 MiB phone
  upload); the orphan sweep (N47) collects it. Reproduced with
  `TestDeleteUploadKeepsABlobAStageIsStillUsing` (the blob was removed mid-stage; failed before). That test proves
  the ordering the lock gives, not the two-statement window itself, which cannot be paused from a test. The
  in-transaction row counts in delete and sweep were dropped because they went stale before the file removal.
