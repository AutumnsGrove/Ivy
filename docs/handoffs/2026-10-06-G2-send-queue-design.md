# G2 — the send queue design (4b)

Gate **G2** of `docs/CHUNK4-BRIEF.md` section 5: the half-page the brief asks for **before coding the
queue**. It answers the four questions — the states, which step is durable, the idempotency key, and
exactly what happens on a crash before `MAIL FROM`, during `DATA`, after `250` and before the DB
write, and after the submit and before the `APPEND` — plus the `unconfirmed` state and what the
operator sees. Nothing here is implemented yet.

`send_queue` owns **SMTP**. The outbox owns **IMAP**; the Sent copy is an outbox `append` op, so the
two queues never overlap. The invariant that drives every choice: SMTP cannot be asked what happened
after the last `DATA` byte, so an unknown outcome is never retried automatically.

## The row (`state.db`, migration 10, append-only)

| column | meaning |
|---|---|
| `id` | queue row id (injected) |
| `account_id` | whose mailbox |
| `message_id` | the injected `<id@domain>`; one per row, reused by every retry and the Sent copy |
| `content_key` | `store.ContentKey(message_id)`; the key the `append` op and its recovery search use |
| `envelope_from` | reverse-path |
| `recipients` | JSON array, To then Cc then Bcc (Bcc included; the wire body has no header) |
| `wire_body` | the transmitted bytes (no `Bcc`) |
| `sent_body` | the copy for Sent (keeps `Bcc`) |
| `state` | `queued` \| `submitting` \| `submitted` \| `appended` \| `done` \| `failed` \| `unconfirmed` |
| `attempts` | SMTP attempts; bounded like the outbox |
| `last_error_code` / `last_error_detail` | the stable reason the UI shows |
| `undo_deadline` | when the undo window ends; the worker never submits before it (4c builds the API) |
| `sent_append_id` | the outbox op id for the Sent copy, stored in the same commit as `submitted` |
| `created_at` / `updated_at` / `completed_at` | times |

`wire_body` and `sent_body` are both stored so a retry is byte-identical and the Sent copy is exactly
the transmitted message plus `Bcc`; rebuilding them from inputs would re-fold and re-boundary.

## States and the durable points

```
queued ──(undo deadline passed)──▶ [MAIL/RCPT ok] ──DURABLE─▶ submitting ──DATA──▶ 250
   ▲                                                                                │
   │                                                        DURABLE: submitted + append op
   │                                                                                ▼
   └──transient 4xx/timeout/reach fail──┐                                    submitted
   │                                    │                                            │
   failed ◀──5xx/auth/too large─────────┘                     append ack ──▶ appended ──┐
                                                                                        ▼
   submitting (crash/restart) ──▶ unconfirmed                                         done
```

1. **Enqueue** (`queued`) is durable before the API answers; it changes nothing on the server.
2. **`submitting` is written after the last `RCPT` and immediately before `DATA`.** The SMTP client
   exposes a `BeforeData` hook for this. Before this commit, the message cannot have been accepted —
   `MAIL`/`RCPT`/auth failures leave the row `queued` and are simply retried. After it, `DATA` may
   have been sent and the row means **may have been sent**.
3. **`submitted` is written only after the server answers `250` for the whole transaction**, in the
   same transaction that enqueues the `append` op and stores `sent_append_id`. There is no window in
   which the message was accepted but the queue does not know it received.
4. **`appended`/`done`** are terminal successes: `appended` once the outbox confirms the Sent copy,
   `done` when there was no Sent folder to file into. **`failed`** is a permanent SMTP rejection or
   the retry cap (the message was not sent). **`unconfirmed`** is unknown.

## The attempt, and what is ambiguous

`smtp.Submit` classifies a failure into a `*SendError{Kind, Transient, Ambiguous}`:

- auth, `MAIL`, `RCPT`, or an explicit `4xx`/`5xx` reply **before `DATA`**: definitive; `queued`
  (transient) or `failed` (permanent).
- a body-write failure **before** the terminating dot: the server cannot have accepted an incomplete
  message; definitive, retried when transient.
- a network, timeout or cancellation failure **after the terminating dot was written and before the
  `250`**, or an explicit `4xx`/`5xx` reply at the end: an explicit reply is definitive, anything
  else is **`Ambiguous`**. The worker turns `Ambiguous` into `unconfirmed`.

`BeforeData` is where the queue writes `submitting`; if it fails, `Submit` aborts before `DATA`, so
the abort is never ambiguous.

## The idempotency key

- **One SMTP send per row.** The row id is the unit; a `queued`/`submitting`/`submitted` row is never
  enqueued twice, and a terminal row never blocks a different message (4c's `POST /send` key handles a
  client double-tap at the API edge).
- **One Sent copy per send.** The `append` op's idempotency key is the outbox's
  `SHA-256(account ‖ 'append' ‖ content_key ‖ '' ‖ canonical(expect))` with
  `expect = {dest_folder_id, flags_add:["\\Seen"], send_id}`; the unique index over non-terminal ops
  makes a second enqueue return the first op. Before appending, the outbox worker searches the Sent
  folder by `Message-ID`: if a copy is already there, the op is `done` without a second `APPEND`, so a
  lost acknowledgement cannot file the message twice.

## What happens on a crash, point by point

| crash point | row after restart | recovery |
|---|---|---|
| before `MAIL FROM` | `queued` (or `submitting` if the crash was after the `BeforeData` commit) | `queued`: submit again. `submitting`: `unconfirmed` |
| during `DATA` | `submitting` | `unconfirmed` — never auto-resent |
| after `250`, before the DB write | `submitting` | `unconfirmed` — the server has it, the queue does not know |
| after `submitted`, before the `APPEND` | `submitted`, `sent_append_id` set | the outbox op is already durable; the worker tracks it to `appended`/`done`. No resend |
| during the `APPEND` | `submitted` + a live/failed op | the outbox's own recovery (`search Sent by Message-ID`) decides, then the row settles |

On startup every `submitting` row becomes `unconfirmed`; nothing else is touched. Recovery never asks
SMTP anything, because it cannot.

## `unconfirmed`, what the operator sees

The row is terminal and is **never auto-resent**. The API (4c) returns it with a plain message: "This
may have been sent; Ivy cannot tell. Check Sent before sending again." The UI (4f) shows it as a
distinct state with a **Send again** button that creates a *new* row, so the operator's explicit tap
is the only way a second copy can exist. It counts as neither `failed` nor `done`.

## The Sent copy

The `append` outbox op carries `send_id` in its `expect` and `\Seen` in `flags_add`; the worker loads
`sent_body` from the send row and `APPEND`s it to the account's Sent-role folder. A failed append is
retried by the outbox **without resending**; if it fails permanently the send row becomes `done` with
`last_error_code = 'sent_copy_failed'` (the mail was sent; only Ivy's own copy is missing), and the
failure stays visible in the outbox history.

## Bounds (added to the `STANDARDS.md` 4a table when this lands)

- attempts per row: `store.MaxSendAttempts` (8), backoff 5 s → 15 min with jitter;
- row age: `store.MaxSendAge` (24 h) then `failed` (`expired`);
- non-terminal rows per account: `store.MaxQueuedSends` (500);
- terminal retention: `store.MaxSendTerminalRetention` (7 days);
- error detail: `store.MaxSendErrorDetail` (500 bytes, rune-safe);
- body size is already bounded by `compose.MaxBodyBytes`; the `SIZE` check bounds the wire.

## Scope of 4b

`store` (migration + state machine), the outbox `append` kind in `sync/outbox.go`, the `send` worker,
`mailworld`'s accept-then-drop fault, and the crash-window tests (gate G3). No HTTP endpoint and no
undo API; those are 4c. The `undo_deadline` column exists now so 4c adds no migration.
