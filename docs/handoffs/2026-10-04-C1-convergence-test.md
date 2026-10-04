# C1: the convergence test is written and red (2026-10-04)

Checkpoint C1 of `docs/CHUNK3-BRIEF.md` 5: the model-based convergence test exists and fails for the
right reason, **before the runner is implemented**. Nothing in `sync/` production code changed.
Waiting for an explicit "go" before the runner is written.

Last commits before this note: `bf7eafb` (SSE hub), `039a34e` (`sync_state`), `bd87542` (mailworld
unflag/rename/delete/`Messages`). The test files are `sync/converge_model_test.go` (harness) and
`sync/converge_test.go` (tests).

## Command that shows the failure

    CGO_ENABLED=1 go test -race -count=1 -run TestSyncConvergesToTheServer ./sync

Output on seed 1, both server variants (with QRESYNC and without), shrunk from 38 operations to 3:

    kind: mirror differs from server
      on the server but not visible in the mirror: INBOX|1|<m7@sync.test>|\seen
      visible in the mirror but not on the server: INBOX|1|<m7@sync.test>|
    1. append m7 -> INBOX    2. sync    3. flag m7 +\Seen    4. sync   <-- fails here

The system under test today is `Fetcher.Fetch` (chunk 2b), which only adds UIDs it has not seen, so
it never refreshes flags, never disables an expunged message and never handles a move. That is the
whole reason for the red. Every other test in the repo is green.

## The operation set

`append`, `copy` (the same Message-ID appended to another folder, so duplicate content keys),
`flag`, `unflag`, `move`, `expunge`, `create-folder`, `rename-folder`, `delete-folder`,
`bump-uidvalidity`, `sync`. Weights are `defaultMix`; scripts are 12 to 40 steps; the end of every
script is an implicit sync. INBOX is never renamed or deleted (the in-memory server would leave no
INBOX). Folders are a flat namespace (no hierarchy). Every fourth message is a few KiB, and the
fetcher runs with 2 KiB / 64 KiB tiers, so the spool path is exercised.

## The generator

`generate(seed, mix)`: `math/rand/v2` PCG, same seed gives the same script. Operations are **index
picks** ("the 3rd message, the 2nd folder") resolved against a pure-Go model when they run, not UIDs,
so a script stays valid after the shrinker deletes steps. Seeds `IVY_SYNC_SEED` (pin one),
`IVY_SYNC_SEEDS` (count, default 24, 6 with `-short`), `IVY_SYNC_SEED_BASE` (move the window).

## The oracle: what "expected mirror" is computed from

**The server, not the model.** After the quiet sync point the oracle reads the fake server through
`mailworld.Account.Messages`/`Mailboxes` (a fresh IMAP connection, no Ivy code between) and compares
the mirror, read by plain SQL so a disabled row cannot hide, against it. The model is used only to
choose valid steps; if it ever disagrees with the server the failure kind is `harness`, never a sync
failure (`TestHarnessModelMatchesTheServer` runs scripts with no sync at all and checks after every step).

Checked at every sync point:

1. visible rows == the server's messages, as `folder|uid|Message-ID|flags` (flags compared
   case-insensitively; the fake lower-cases them);
2. `seen` column agrees with the flags; `content_key == store.ContentKey(Message-ID)`; every visible
   message has a `thread_id`;
3. **nothing erased (invariant 1):** no row id ever disappears, and every Message-ID ever mirrored
   still has a row, disabled or not, whose bytes (blob **or spool file**) equal what was delivered;
4. **moved vs removed (round 37):** a row disabled in this pass whose Message-ID is still on the
   server must carry `disabled_reason = 'moved'`; one that is nowhere must carry some other non-empty
   reason (the removal string is not fixed by any doc, so any other value passes);
5. every server folder has a mirror folder row with the same UIDVALIDITY;
6. **the second attempt:** running the system under test again changes no row.

Rows with a pending outbox op are excluded from 1 once the outbox exists (3d); there is none yet.

## Proof the harness is sound (all green)

The model matches the server for all operation kinds; the oracle **accepts** an append-only history
the one-shot fetch handles correctly (no false positives); it **rejects** stale flags and the shrinker
cuts a padded script to exactly `append, sync, flag`; sabotaged syncs that erase raw bytes or are not
idempotent are caught on the real path; each check has a unit test that makes it fail; the generator is
deterministic and covers every operation; the shrinker (delta debugging, then zeroing the picks, budget
150 replays) is tested on its own.

## The seam for 3a

`oneShotFetch` in `converge_model_test.go`, the default of `config.sut`. When the runner exists, its
single-pass entry point replaces that one function; nothing else changes.

## Traps this harness already knows about (for whoever writes the runner)

- **UID reuse.** After `bump-uidvalidity` (and after a folder is deleted and a new one created under
  the same name) the server restarts UIDs at 1. `messages` has `UNIQUE(folder_id, uid)`, the row id is
  `messageRowID(folderID, uid)` and the spool file is `spool/<folderID>/<uid>.eml`. A new message at an
  old UID collides on all three while the old, disabled message must keep its row and its bytes.
  Re-parenting old rows alone is not enough: the new row's derived id and the spool path must not
  reuse the old ones. Check 3 fails if this is wrong.
- A **rename** keeps UIDs and UIDVALIDITY, so it is per-message "moved" under invariant 5.
- `Fetch` reads folders with EXAMINE; the runner must still work with the harness's two connections.

## Open questions (T4: the docs do not answer them)

1. What happens to the **folder row** of a folder the server deleted or renamed away? Not asserted
   here. Recommendation: keep the row, mark it gone (a new `gone_at` column, append-only migration),
   and hide it from folder lists, consistent with "nothing is ever erased".
2. The string for the **removal** reason (only `moved` is fixed). Recommendation: `server_removed`.
3. `store.SyncStatus` has `error` beside the API's `ok | syncing | auth-failed | unreachable`; the
   gateway must map it when `/accounts` starts reading `sync_state` (suggest `unreachable` is wrong
   for it; add an `error` value to the contract instead).

## Answers (operator, 2026-10-04; also in qa-log round 38)

1. Folder row of a deleted or renamed-away folder: **keep and mark gone** (`gone_at`, new migration).
2. Removal reason: **`server_removed`**. The oracle (check 4) now requires exactly that string.
3. `error` status: **add it to the API contract** as a fifth `SyncState` value.

## Also delivered in this session (3a groundwork, all green, see the commits)

`sync_state` table and `store.SetSyncState`/`GetSyncState`; the `events` hub and `/api/v1/events`
(hints only, wired into `ivy run` and `ivy-dev up`, shutdown-safe); `gateway.errorWriter.Unwrap`
(a wrapper writer had hidden `Flush`); mailworld `Unflag`, `RenameMailbox`, `DeleteMailbox`,
`Messages`. Not done: the frontend `EventSource` client in `web/src/lib/api/` and its Playwright test.

## Next

On "go": C2 is the runner until `TestSyncConvergesToTheServer` passes, then the seeds and sequence
count run, the operation mix executed (the test logs it), and a demonstration that breaking the code
fails and shrinks.
