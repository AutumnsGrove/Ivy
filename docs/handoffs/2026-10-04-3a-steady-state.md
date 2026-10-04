# 3a steady state: sync_state, QRESYNC, IDLE (2026-10-04)

Continuation of 3a after the C2 checkpoint. The runner's Go scope is now complete: it writes
`sync_state`, uses a QRESYNC delta when the server offers it, and a per-account worker idles on
INBOX and re-syncs on a notification or the idle timeout. `ivy run` owns one worker per configured
account. All of it is committed in small stages (`b1ca426`, `d95bebe`, `2c6fe0a`, `14c927a`).

Still open and deferred to 3b: the mass-disable alert and the Restore/Purge endpoints. The
frontend `EventSource` client landed after this note was first written (commit `2d39e8d`), so 3a's
list is complete.

## 1. sync_state (commit b1ca426)

`Fetcher.Fetch` records every run: `syncing` on entry, then `ok` with a fresh `last_ok_at`, or a
failure with a stable code. `classifySyncError` puts an `AUTHENTICATIONFAILED`/`AUTHORIZATIONFAILED`
IMAP response in `auth_failed`, a `*net.OpError` in `unreachable`, and everything else in `error`;
the detail is truncated to `store.MaxSyncErrorDetail` on a rune boundary. The write is best effort
and never masks the sync result. Tests: `sync/syncstate_runner_test.go`,
`sync/classify_test.go`.

## 2. QRESYNC deltas (commit d95bebe)

`fetchAll` enables QRESYNC if the account advertises it and each folder has a stored UIDVALIDITY and
modseq. `snapshotFolder` SELECTs with `QResyncOptions{UIDValidity, ModSeq}`; a changed validity comes
back with no VANISHED, so it falls through to the full scan. The metadata FETCH adds
`ChangedSince` so the server returns only changed and new messages; `VANISHED` supplies the
expunges. A server without CONDSTORE takes the same full scan as before, so the no-CONDSTORE
convergence run is unchanged.

Move vs removal is now decided **after** the account pass: rows disabled during the pass are
provisionally `server_removed`, then `reclassifyDisabled` flips any whose Message-ID is still live
elsewhere to `moved`. That is what lets a delta skip a full-account scan while keeping the round 37
rule.

A folder's modseq advances only after every body in it is stored; a partial run keeps the old
modseq. This is a real bug found while testing: an early advance made the next delta skip the
messages the interrupted run never fetched. `TestDeltaResumeKeepsUnfetchedMessages` was watched
failing on the early-advance version (it then held `[1 3]`, missing UID 2).

Tests: the convergence harness on both variants (24 seeds, 640 operations each, deltas on every
sync after the first), `sync/qresync_test.go`, and the pure `canUseDelta` table.

## 3. IDLE worker and `ivy run` (commits 2c6fe0a, 14c927a)

`sync.Worker` reconciles, then holds IDLE on INBOX until a notification, the idle timeout, or
cancellation, and loops. The work and the IDLE use separate connections (never more than two). A
failed reconcile or IDLE backs off exponentially with ±25% jitter; a server without the IDLE
extension is polled on the idle timeout. The go-imap client only surfaces unilateral data through a
`UnilateralDataHandler`, so `dial` grew a `dialWith` variant and the worker wakes on
Mailbox/Expunge/Fetch.

`ivy run` starts one worker per configured account and publishes a `sync.state` hint (plus
`message.changed` when mail was stored) through the SSE hub; shutdown cancels the workers and waits
for them. A configured account can now say `insecure: true` (default off, named for the unsafe
thing) so the real binary can use the loopback dev fake; `devstack` sets it and still refuses any
non-loopback host.

Tests: `sync/worker_test.go` (idle refresh, outage recovery, prompt cancellation, goleak) and
`TestRunSyncsAConfiguredAccount` in `cmd/cmd_test.go`. The fast dev seeder records the same
`sync_state` the runner does, with `last_ok_at` masked as volatile in the agreement test.

Note on the idle test: the fake server can register its IDLE listener just after it acknowledges
the command, so a notification sent in that window can be lost; the test uses a short idle timeout
so the design's periodic fallback covers it. The notification path is still exercised.

## 4. Where the work stands

Full suite `CGO_ENABLED=1 go test -race ./...` is green; `make check` (drift, fmt, vet, staticcheck,
Go `-race`, `pnpm check`, 209 Vitest) is green; `govulncheck` clean (local Go 1.26.6); the mock
Playwright suite is 242 passed / 10 skipped; the real-binary smoke slice is 8/8. `BenchmarkSyncBackfill`
(200-message cold backfill) and `BenchmarkSyncDelta` (steady-state QRESYNC) live in
`sync/bench_test.go`. The frontend `EventSource` client is in `web/src/lib/api/events.ts` with a
Vitest test and a Playwright refetch test (`web/e2e/events.spec.ts`).

The 3a Go scope in `next_steps.md` (runner, resumable backfill, IDLE, QRESYNC/fallback, UIDVALIDITY,
SSE hub, convergence test) is done. The next gate is **C3** (write down the outbox op states before
coding 3d). The real-mailbox live check and the potato numbers are operator follow-ups, and C2 wants
Claude's fresh-session review of the tests and design.
