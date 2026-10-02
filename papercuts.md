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
