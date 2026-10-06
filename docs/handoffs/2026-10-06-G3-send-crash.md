# G3 — the send crash-window tests pass (2026-10-06)

Gate **G3** of `docs/CHUNK4-BRIEF.md` section 5: the failure-injection tests for the send queue are
green. The design they were built from is `docs/handoffs/2026-10-06-G2-send-queue-design.md`.

## What was tested

The dangerous property is: **no message is ever submitted twice, and a message whose outcome is
unknown is never auto-resent.** SMTP cannot be asked what happened after `DATA`, so the queue records
what it can and treats the rest as unknown.

| test | kill point | asserts |
|---|---|---|
| `send/worker_test.go:TestSendAcceptThenDropNeverResends` | after `DATA`, before the 250 — repeated **12** times | every server acceptance is recorded (12), every row is `unconfirmed`, and a second pass with the fault cleared sends **zero** more |
| `send/worker_test.go:TestSendCrashDuringDataIsUnconfirmed` | during `DATA` (the durable `submitting` point, process gone) | the row is `unconfirmed`; nothing was sent; a second pass never retries it |
| `send/worker_test.go:TestSendCrashAfterSubmitBeforeAppend` | after the 250, before the append op exists | the worker re-enqueues the append idempotently and the Sent copy exists **exactly once** |
| `sync/outbox_test.go:TestOutboxAppendFilesTheSentCopyOnce` | successful append, run twice | one copy, `\Seen`, op `done` |
| `sync/outbox_test.go:TestOutboxAppendSkipsWhenAlreadyFiled` | the acknowledgement was lost but the copy landed | the search by `Message-ID` finishes the op with no second `APPEND` |
| `sync/outbox_test.go:TestOutboxAppendRecoversAfterInFlight` | after the `APPEND` command, before its ack | recovery finds it absent, requeues and files once |
| `send/worker_test.go:TestSendSentCopyFailureIsNotASendFailure` | the copy fails permanently | the send is `done` with `sent_copy_failed`; it is not marked failed and never resent |
| `store/sendqueue_test.go:TestRecoverSubmittingSendsIsUnconfirmedAndNeverRetried` | restart with a `submitting` row | the row is `unconfirmed` and invisible to the worker |

The "crash" is modelled by the durable point the design names: a real `kill -9` leaves exactly the
state the tests set (`submitting` after the commit, `submitted` before the append id), and a fresh
worker is then run against the same database. The accept-then-drop case uses the fake's real
`SMTPAcceptThenDrop` fault over a real socket, so it is not a simulated state.

## The command and its result

```
$ CGO_ENABLED=1 go test -race -count=1 ./send/ ./sync/ -run 'TestSend|TestOutboxAppend|TestRecoverSubmitting'
ok  	github.com/AutumnsGrove/Ivy/send	2.583s
ok  	github.com/AutumnsGrove/Ivy/sync	6.084s
```

`store/sendqueue_test.go` covers the durable points and the recovery rule directly; `go build ./...`,
`go vet`, `staticcheck` and `gofumpt` are clean.

## The invariant each test protects

- **Nothing twice** (CHUNK4-BRIEF 1.2): the only automatic retries are from `queued` (never sent) and
  `failed`→explicit. `submitting` never returns to `queued`; it becomes `unconfirmed`.
- **Nothing lost silently** (1.2, 1.10): every row ends `appended`, `done`, `failed` or
  `unconfirmed`, all visible, and a failed Sent copy is surfaced rather than dropped.
- **One Sent copy** (1.6): the outbox append is idempotent by send id and by `Message-ID` search.
- **The 250 is the only success** (1.1): `submitted` is written after `w.Close()` returns nil, and
  only a `submitting` row can enter it.

## Outcome

4b is implemented (`store/sendqueue.go`, the outbox `append` kind, `send/worker.go`, the supervisor's
third worker) and green. No stop-trigger fired. The API (`POST /send`, undo) and the compose screen
are 4c and 4f; 4b exposes the queue those build on.
