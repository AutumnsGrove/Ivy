# 4d — the drafts design (the outbox gate)

Stage 4d is "DeepSeek, with a gate on the outbox invariants" (`next_steps.md`, round 60), so a short
design is settled **before any code**, in the spirit of `docs/CHUNK4-BRIEF.md` section 5. The four
open choices were put to the operator before this file and are recorded in `docs/qa-log.md` round 62.
**Nothing here is implemented yet.**

`send_queue` owns **SMTP**; the outbox owns **IMAP** (`docs/handoffs/2026-10-06-G2-send-queue-design.md`).
Drafts are IMAP writes, so they belong to the outbox. The draft's *content* is locally owned state
(CLAUDE.md rule 5), so it lives in `state.db` and is backed up. The two queues keep their
responsibilities apart.

## 1. What 4d must do

1. Autosave a compose request to the server's Drafts folder as an `APPEND` with `\Draft`, visible in
   Apple Mail (round 12: server Drafts folder, not a local-only store).
2. A replace is: file the new version, then remove the version it supersedes.
3. List drafts so the compose screen can resume one.
4. A draft that was sent leaves Drafts.
5. Two tabs editing one draft must not corrupt it: the last write wins and the loser is told.

## 2. The two constraints that shape it

- **The `expunge` outbox op is Trash-only** (`sync/outbox.go`, `not_trash`). A draft replace must not
  reuse it and must not widen it: "a single tap must never erase mail". A draft version is not mail.
- **The `append` op loads its body from `send_queue` by `SendID` and dedupes by Message-ID.** A draft
  needs a different body source, and a fresh Message-ID per save: a reused one would make the second
  save's dedupe search find the first copy and silently skip the new version.

## 3. Settled decisions (operator, round 62)

1. **A dedicated outbox op kind, `draft`, owns a whole save** — append the new version then remove the
   superseded one, in one op. No new expunge path, no dependence on the mirror holding the old
   version, one op per autosave.
2. **The list merges the mirror's Drafts folder with local drafts not yet synced** — server truth
   (including drafts made in Apple Mail) plus immediate feedback for an autosave.
3. **Optimistic version:** a save carries the version it began from; the later save wins and bumps the
   version; a stale save is refused `409 draft_conflict` with the newer content.
4. **The sent-draft linkage is in 4d:** a send records the draft version it came from, and the send
   worker removes that version from Drafts after the `250`. An undo, a cancel or a permanent failure
   keeps the draft.

## 4. Rows (`state.db`, append-only migrations 12 and 13)

### `drafts` — one immutable row per saved version; the head is the highest live version

| column | meaning |
|---|---|
| `id` | version-row id (injected per save); the outbox op references this |
| `draft_id` | the stable draft identity (client-supplied on the first save) |
| `account_id` | whose mailbox |
| `version` | 1, 2, 3 … monotonic per `draft_id` |
| `message_id` | this version's injected `<id@domain>`; a fresh one every save |
| `content_key` | `store.ContentKey(message_id)` |
| `supersedes` | the previous head's Message-ID (empty on version 1) |
| `subject` / `to_addrs` | denormalised for the list |
| `compose_json` | the caller's request verbatim, for resume |
| `body` | the built MIME bytes (Bcc kept; `\Draft` applied at append) |
| `state` | `saving` (the op is live) \| `saved` (the op is done) \| `sent` \| `discarded` |
| `created_at` / `updated_at` | times |

One row per version, for the same reason `send_queue` stores its body: the op and its bytes are
committed together and the body is never mutated, so two racing autosaves cannot make one op file the
other's bytes.

### `send_queue.draft_message_id` (migration 13)

The Message-ID of the draft version the operator sent, so the worker removes exactly that server copy
and never a newer edit.

## 5. The `draft` op (`store.OutboxDraft`)

`OutboxExpect` gains `DraftID`, `DraftVersionID`, `Supersedes []string` and `Remove bool`. The op's
`ContentKey` is the version's content key and `SourceFolderID` is the Drafts folder, so each save has
its own idempotency key and a repeat of one save collapses to one op.

```
dispatch (expect.Remove == false):
  resolve + select the Drafts folder            -> role guard: not_drafts
  load the body by DraftVersionID               -> draft_gone if the version is missing
  if SEARCH new Message-ID present:             -> the append already applied; skip it
  SetOutboxInFlight, then APPEND body with \Draft
  for each superseded Message-ID: SEARCH, then STORE \Deleted + UIDExpunge
  MarkDraftSaved(DraftVersionID)
  done

dispatch (expect.Remove == true):               # sent or discarded
  select Drafts; for each Message-ID in Supersedes: SEARCH, EXPUNGE; done

recover (in flight):
  Remove:      expunge whatever Supersedes is still present; done
  otherwise:   SEARCH the new Message-ID
                 present -> expunge supersedes, MarkDraftSaved, done
                 absent  -> requeue (the append never landed; a re-append is safe)
```

- **Never two copies of one version:** the per-version Message-ID plus search-before-append.
- **Never a lost version:** the new copy is appended before the old is expunged, and a crash between
  them is healed by recovery (finding the new, expunging the old).
- **No mirror dependence:** both copies are located by `Message-ID`, so two quick autosaves before a
  sync pass are still correct.
- **UIDPLUS is required** to expunge the superseded copy precisely, exactly as the existing expunge
  op requires it. Without it the op fails `unsupported`; it never falls back to a bare `EXPUNGE`,
  which would erase whatever else was marked `\Deleted`.

## 6. The save (`store.SaveDraft`), one transaction

1. Resolve the Drafts folder (`FolderByRole`); a missing folder is a clear error (no auto-create in
   4d).
2. If `draft_id` has a head, require `base_version == head.version`, else `ErrDraftConflict` carrying
   the head. No head creates version 1.
3. Insert the new version row (`saving`) and enqueue the `draft` op with `supersedes = head.message_id`.
   An `outbox_full` rolls the whole save back, so nothing is half-written.
4. Prune superseded rows for this `draft_id` whose state is terminal and that are older than the head.
   A `saving` row is kept because a live op still needs its bytes.

## 7. List, resume, discard

- **List** merges local heads (max version per `draft_id`, excluding `sent`/`discarded`) with the
  mirror's Drafts-role messages whose content key has no local head. Newest first, bounded.
- **Resume** returns the local `compose_json` when a local head exists, otherwise parses the mirror
  row's stored MIME back into To/Cc/Subject/text. No sanitiser is involved either way: compose is the
  operator's own content and never goes through the inbound path (CHUNK4-BRIEF section 3).
- **Discard** marks the local head `discarded` and enqueues a `draft` op with `Remove: true`.

## 8. Crash and race table

| event | result | recovery |
|---|---|---|
| crash after insert + op commit, before APPEND | head `saving`, op pending | the op appends on the next pass |
| crash after APPEND, before `MarkDraftSaved` | head `saving`, op in flight | recovery finds the Message-ID, expunges the old, marks `saved` |
| crash after APPEND and after expunging the old | head `saving`, op in flight | recovery finds the new, finds no old, marks `saved` |
| two autosaves before either op runs | two version rows, two ops; each op's bytes are its own | FIFO settles both; the head is the newest |
| two tabs, stale save | refused `draft_conflict` | the loser refetches the head |
| send accepted, crash before the draft remove | `send_queue.draft_message_id` set | the send worker enqueues the remove on the next pass |

## 9. Limits (added to `STANDARDS.md` 4a when this lands)

- draft body: `compose.MaxBodyBytes` (already); a compose/large-upload request body a new
  `maxDraftBodyBytes`;
- one draft's live versions: the head plus any `saving`; superseded terminal rows pruned on save;
- drafts per account listed: a bounded page (`MaxDraftListLimit`);
- `\Draft` and no `\Seen`; a `draft` op needs UIDPLUS to remove a superseded version;
- a draft request body bounded like the send request.

## 10. Scope of 4d

`store` (migrations 12–13, the drafts state machine), the outbox `draft` kind in `sync/outbox.go`, the
send worker's draft removal, the `/drafts` API and the resume parse, and the mirror merge. No compose
screen (4f); no attachments (4g).

## 11. Plan of small commits

1. this design, the qa-log round, and `next_steps.md`;
2. `store`: migration 12, the drafts state machine, and its tests;
3. `sync`: the `draft` outbox kind, dispatch and recovery, and its tests (including the crash
   windows);
4. `send` + migration 13: the draft linkage and removal after the `250`;
5. `gateway`: the `/drafts` API, resume, discard, and the mirror merge, with tests;
6. docs (`ARCHITECTURE.md`, `STANDARDS.md`, `openapi.yaml`, `BUILD-LOG.md`) and `next_steps.md`.
