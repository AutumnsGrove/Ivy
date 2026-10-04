# C4: the outbox crash window recovers exactly once (2026-10-04)

Checkpoint C4 of `docs/CHUNK3-BRIEF.md` 5, for stage **3d (outbox + write path)**: the
failure-injection test that kills the worker between the IMAP ack and the DB write passes, repeated
over many messages, with no duplicate and no lost write. This note is written for a fresh reviewer
who has seen none of the session: it names the files, the tests, the exact commands and the output,
and it is honest about what 3d still owes.

**This is not "3d done".** The outbox core (table, worker, recovery, sync deferral) is implemented
and tested; the HTTP surface, the reader actions and the optimistic UI are not yet built. They are
listed under "What is not here". The design followed is `docs/handoffs/2026-10-04-C3-outbox.md`
(reviewed, six defects corrected, operator answers in `qa-log.md` round 46), which the operator
released with an explicit "go".

## 1. The green run

    CGO_ENABLED=1 go test -race -count=1 -run 'CrashWindow|AckThenDrop' ./sync

    --- PASS: TestOutboxFlagCrashWindowRecovers (0.24s)
    --- PASS: TestOutboxExpungeCrashWindowRecovers (0.25s)
    --- PASS: TestOutboxMoveCrashWindowRecoversExactlyOnce (2.74s)   # 16 messages
    --- PASS: TestOutboxMoveAckThenDropConverges (1.47s)             # 8 messages
    ok  	github.com/AutumnsGrove/Ivy/sync	4.283s

The whole suite is green under `-race`:

    CGO_ENABLED=1 go test -race ./store/ ./sync/ ./internal/mailworld/
    ok  	github.com/AutumnsGrove/Ivy/store	17.3s
    ok  	github.com/AutumnsGrove/Ivy/sync	44.9s
    ok  	github.com/AutumnsGrove/Ivy/internal/mailworld	2.0s

`go vet ./...`, `go tool gofumpt -l` and `go tool staticcheck` are clean. `make check`'s web half
and the Playwright suite were **not** run: no screen changed yet (the gateway and UI are not built).

## 2. What the crash test actually does

The window is between "the server acknowledged the command" and "the DB write finished". It is
simulated two ways, because each catches a different half of the race:

- **`TestOutboxMoveCrashWindowRecoversExactlyOnce`** (16 messages). A white-box seam
  `OutboxWorker.afterAck` (`sync/outbox.go`) runs after the ack and before the mirror update; the
  test arms it to return `errSimulatedCrash`, so the op is left `in_flight` exactly as a `kill -9`
  would leave it. A **fresh** worker on a **fresh** connection then recovers. For a move the
  assertions are: the op ends `done`, the destination holds **exactly one** copy, the source has no
  live row, and the source row is hidden with `disabled_reason = 'moved'` (`sync/outbox_crash_test.go`).
- **`TestOutboxFlagCrashWindowRecovers`** and **`TestOutboxExpungeCrashWindowRecovers`**: the same
  seam after the STORE / UID EXPUNGE ack. Recovery reads the server's state (flag set present; UID
  absent) and finishes without re-sending blind.
- **`TestOutboxMoveAckThenDropConverges`** (8 messages). A new `mailworld.AckThenDrop` fault
  (`internal/mailworld/world.go`) writes the successful response and closes the connection a moment
  later, so the client either reads the ack and finishes or sees a drop and retries. The op's clock
  is advanced past any backoff and a second pass finishes it; the destination still holds exactly
  one copy.

The recovery is deliberately not "guess and re-send" (C3, "Crash after the IMAP ack"). A fresh
worker asks the server: for a move, if the stored `(uidvalidity, uid)` is still in the source the
command did not apply and the op is requeued; if it is gone it searches the destination by
Message-ID; on a UIDVALIDITY change it asks both folders and fails `ambiguous` rather than guessing.
For flags it checks the postcondition (`UID FETCH FLAGS`); for expunge, the UID absent.

## 3. What was built

**Store (`store/`).**

- **State migration 5** (`store/migrations.go`): the `outbox` table. `expect` is canonical JSON; the
  idempotency key is partial-unique over non-terminal rows only
  (`CREATE UNIQUE INDEX … WHERE state IN ('pending','in_flight')`).
- **`store/outbox.go`**: `OutboxOp`, `OutboxExpect`, `OutboxKey`, the kinds and states, the limit
  constants (8 attempts, 24 h, 500 queued, 7-day retention), and `EnqueueOutbox` (idempotent while
  live, refuses past the cap, collapses a flag and its exact inverse to two `cancelled` rows),
  `NextOutbox` (strict FIFO; a not-yet-due op blocks the ones behind it), `OutboxByAccount`,
  `OutboxHistory`, `OutboxActiveKeys`, the state transitions (`SetOutboxInFlight` records the
  resolved identity; `RequeueOutbox`; `SetOutboxPending`; `SetOutboxDone`; `SetOutboxFailed`;
  `CancelOutbox`) and `PruneOutbox`.
- `store/outbox_test.go`: 10 tests, written first and seen failing (undefined symbols, then two
  real failures: FIFO overtaking and the cancelled-inverse row not being returned).
- Two small lookups the worker needs: `store.GetFolderByID` (`store/folders.go`) and
  `store.MessageRowRef` (`store/messages.go`, hidden rows included, live row preferred).
  `SyncMessageRef` gained `ContentKey` for the deferral check.

**Worker (`sync/outbox.go`, package `sync`, reusing the unexported session/stall/dial plumbing and
`Fetcher.disableRefReason`).**

- One `OutboxWorker` per account, its **own** connection, one in-flight op at a time, FIFO by `seq`,
  dialled lazily and closed after 60 s idle, carrying the 2-minute stall guard. `RunOnce` drains the
  due ops (tests); `Run` polls and prunes.
- Dispatch resolves the UID at dispatch (never at enqueue) and writes it to the op in the same
  statement that sets `in_flight`, before any command goes on the wire.
- `move` requires `MOVE` and `expunge` requires `UIDPLUS`; without them the op fails `unsupported`
  rather than emulating with COPY/STORE/EXPUNGE.
- Error classification by response code (permanent: `NONEXISTENT`, `TRYCREATE`, `OVERQUOTA`,
  `NOPERM`, `CANNOT`, `CLIENTBUG`, `BAD`; transient: drops, stalls, `BYE`, `UNAVAILABLE`,
  `SERVERBUG`, a bare `NO`), with 5 s→15 min backoff and ±20 % jitter.
- The mirror update reuses sync's disable path (`disableRefReason`, so the blob-store copy still
  happens first) with reason `moved` for a move and `server_removed` for an expunge; a flag ack
  writes the server's flags with `SetMessageFlags`.

**Sync deferral (`sync/sync.go`, invariant 4).** `fetchAll` loads `OutboxActiveKeys` once per pass;
`reconcileFolder` and `markGoneFolders` skip a `(content_key, folder)` with a live op for both flag
writes and disables. Two tests: `TestSyncDefersToAPendingMove` (sync leaves a row live that another
client moved, then the op finishes it) and `TestSyncDefersToAPendingFlag`.

**Fake world (`internal/mailworld/world.go`).** `AckThenDrop`, plus `Store`/`Expunge`/`Move`
overrides on `faultSession`. Existing `DropConnection`, `Unreachable`, `AuthFail`, `FetchFail`,
`Latency` are untouched, and the package's other tests still pass.

Test files added: `store/outbox_test.go`, `sync/outbox_test.go`, `sync/outbox_crash_test.go`.

## 4. Reproduce

    # The C4 gate itself.
    CGO_ENABLED=1 go test -race -count=1 -run 'CrashWindow|AckThenDrop' ./sync

    # The outbox state machine and the deferral.
    go test -count=1 -run 'Outbox|SyncDefers' ./store ./sync

    # Everything touched, under the race detector.
    CGO_ENABLED=1 go test -race ./store/ ./sync/ ./internal/mailworld/

Commits on `main` (present tense, small stages): `Add the outbox table and its state machine`,
`Add the outbox worker and sync deferral`, plus the C4 commit carrying this note.

## 5. What is not here (3d still owes)

- **The HTTP surface.** No enqueue/list/retry/dismiss endpoints and no `api/openapi.yaml` schemas
  yet. The worker is inert until something can enqueue an op (today only tests and the store API
  can). This is the next piece and does not change the recovery logic under review.
- **The reader actions and the UI.** Archive / delete / flag / mark-spam / not-junk, the
  confirmation modal for moves and deletes (operator decision, round 46), the stronger Empty-Trash
  modal with the count, the optimistic overlay that keeps its state until an `outbox.state` hint
  says the op is terminal, and the undo toast that enqueues the inverse op. The `outbox.state` SSE
  hint is not published by the gateway yet.
- **Wiring into `ivy run`.** `cmd/cmd.go` does not start an `OutboxWorker` per account yet, and the
  gateway has no reference to one. The worker is constructed only in tests.
- **Op id generation.** Production paths need injected ids (C3); there is no generator yet.
- **Docs.** `ARCHITECTURE.md` 4, `TESTING.md` 2/6, the limits table in `STANDARDS.md` 4a and
  `openapi.yaml` do not describe the outbox yet. The design lives only in the C3 handoff until the
  stage finishes.
- **Not run:** `make check`'s web half, `pnpm check/test`, Playwright, `govulncheck`, and the
  potato. They follow with the UI and the docs fold.

## 6. For the reviewer

The two things worth a hard look are the ones the C3 review flagged as the hard parts:

1. **Exactly-once across the ack.** Read `recoverMove`/`recoverFlags`/`recoverExpunge` in
   `sync/outbox.go` against C3's "Crash after the IMAP ack" section, and the crash test's
   assertions. The seam (`afterAck`) is the only non-production code path; it is nil in production.
2. **Sync vs the outbox.** The active-key set is read once at the top of `fetchAll`
   (`sync/sync.go`), so a key enqueued mid-pass is not yet deferred. Two databases cannot share a
   transaction, so this window is closed only by the outbox overwriting the row after its own ack;
   say if that is not good enough and it needs a second check per folder.

Open questions I did not answer on my own (T4):

- Should `RunOnce`/`Run` be exported to `cmd`, or should `cmd` build the worker through a small
  constructor? Today `NewOutboxWorker` is exported and `cmd` wiring is absent.
- The `afterAck` seam is unexported and used only by a white-box test. If the reviewer prefers no
  test seam in production code, the alternative is a mailworld fault that drops exactly after the
  response; `AckThenDrop` cannot guarantee the client read the ack, which is why the seam exists.
