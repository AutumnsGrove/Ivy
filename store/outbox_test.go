package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// baseTime keeps every outbox timestamp deterministic so a test can reason
// about due times and retention without sleeping.
var outboxNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func flagOp(id, contentKey, folderID string) OutboxOp {
	return OutboxOp{
		ID:             id,
		AccountID:      "acct-1",
		Kind:           OutboxFlags,
		ContentKey:     contentKey,
		SourceFolderID: folderID,
		Expect:         OutboxExpect{FlagsAdd: []string{`\Seen`}},
		CreatedAt:      outboxNow,
	}
}

func moveOp(id, contentKey, folderID, dest string) OutboxOp {
	return OutboxOp{
		ID:             id,
		AccountID:      "acct-1",
		Kind:           OutboxMove,
		ContentKey:     contentKey,
		SourceFolderID: folderID,
		Expect:         OutboxExpect{DestFolderID: dest},
		CreatedAt:      outboxNow,
	}
}

// The op row is committed before the API reports the action accepted, so a
// fresh enqueue must be durable, pending, and carry a monotonic sequence the
// worker can order on.
func TestEnqueueOutboxStoresAPendingOpInSequence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	first, created, err := dbs.EnqueueOutbox(ctx, flagOp("op-1", "key-a", "folder-inbox"))
	if err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	if !created {
		t.Fatal("first enqueue reported it already existed")
	}
	if first.State != OutboxPending {
		t.Errorf("state = %q, want %q", first.State, OutboxPending)
	}
	if first.Seq != 1 {
		t.Errorf("seq = %d, want 1", first.Seq)
	}
	if first.IdempotencyKey == "" {
		t.Error("idempotency key was not filled in")
	}

	second, created, err := dbs.EnqueueOutbox(ctx, moveOp("op-2", "key-b", "folder-inbox", "folder-archive"))
	if err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	if !created {
		t.Fatal("second enqueue reported it already existed")
	}
	if second.Seq != 2 {
		t.Errorf("second seq = %d, want 2", second.Seq)
	}
}

// A double-tap or a retried HTTP request is the same action, not two. The
// partial-unique idempotency key makes the second enqueue return the first op.
func TestEnqueueOutboxIsIdempotentWhileNonTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	first, _, err := dbs.EnqueueOutbox(ctx, flagOp("op-1", "key-a", "folder-inbox"))
	if err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	again, created, err := dbs.EnqueueOutbox(ctx, flagOp("op-2", "key-a", "folder-inbox"))
	if err != nil {
		t.Fatalf("enqueue again: %v", err)
	}
	if created {
		t.Error("the repeat enqueue created a second op")
	}
	if again.ID != first.ID {
		t.Errorf("repeat returned %s, want the existing %s", again.ID, first.ID)
	}
	if n := countOutbox(t, dbs); n != 1 {
		t.Errorf("outbox holds %d rows, want 1", n)
	}
}

// A terminal row must never block a later action: flag, unflag, flag is three
// real changes, and a whole-row unique index would silently swallow the third.
func TestOutboxTerminalOpDoesNotBlockARepeatAction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	first, _, err := dbs.EnqueueOutbox(ctx, flagOp("op-1", "key-a", "folder-inbox"))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := dbs.SetOutboxDone(ctx, first.ID, outboxNow); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	_, created, err := dbs.EnqueueOutbox(ctx, flagOp("op-2", "key-a", "folder-inbox"))
	if err != nil {
		t.Fatalf("enqueue after done: %v", err)
	}
	if !created {
		t.Error("the repeat after a terminal op was swallowed")
	}
	if n := countOutbox(t, dbs); n != 2 {
		t.Errorf("outbox holds %d rows, want 2", n)
	}
}

// A queue that grows without bound is a hostile-input path; the cap refuses the
// enqueue with a visible error instead.
func TestEnqueueOutboxRefusesBeyondTheQueueCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	for i := 0; i < MaxQueuedOps; i++ {
		op := flagOp("op-"+itoa(i), "key-"+itoa(i), "folder-inbox")
		if _, _, err := dbs.EnqueueOutbox(ctx, op); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	_, _, err := dbs.EnqueueOutbox(ctx, flagOp("overflow", "key-overflow", "folder-inbox"))
	if !errors.Is(err, ErrOutboxFull) {
		t.Fatalf("enqueue past the cap = %v, want ErrOutboxFull", err)
	}
}

// FIFO is by sequence, and an op that failed transiently waits until its
// backoff is due, so a flapping server cannot spin.
func TestNextOutboxReturnsTheLowestSeqDueOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, _, err := dbs.EnqueueOutbox(ctx, flagOp("op-1", "key-a", "folder-inbox")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, _, err := dbs.EnqueueOutbox(ctx, moveOp("op-2", "key-b", "folder-inbox", "folder-archive")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := dbs.SetOutboxPending(ctx, "op-1", 1, outboxNow.Add(time.Minute), "unavailable", "try later", outboxNow); err != nil {
		t.Fatalf("set pending: %v", err)
	}

	if _, err := dbs.NextOutbox(ctx, "acct-1", outboxNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("next before the backoff = %v, want ErrNotFound", err)
	}
	got, err := dbs.NextOutbox(ctx, "acct-1", outboxNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("next after the backoff: %v", err)
	}
	if got.ID != "op-1" {
		t.Errorf("next = %s, want op-1 (lowest seq)", got.ID)
	}
}

// An in-flight op means "may have been sent, ack unknown", so recovery must see
// it even though it is not pending.
func TestNextOutboxReturnsInFlightOpsForRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, _, err := dbs.EnqueueOutbox(ctx, moveOp("op-1", "key-a", "folder-inbox", "folder-archive")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := dbs.SetOutboxInFlight(ctx, "op-1", 7, 42, outboxNow); err != nil {
		t.Fatalf("set in flight: %v", err)
	}
	got, err := dbs.NextOutbox(ctx, "acct-1", outboxNow)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if got.ID != "op-1" || got.State != OutboxInFlight {
		t.Errorf("next = %+v, want op-1 in_flight", got)
	}
	if got.SourceUIDValidity != 7 || got.SourceUID != 42 {
		t.Errorf("resolved identity = %d/%d, want 7/42", got.SourceUIDValidity, got.SourceUID)
	}
}

// A flag followed by its own inverse while both are still pending is nothing at
// all; both rows end cancelled, so the worker never sends either.
func TestOutboxFlagAndItsInverseCancelBoth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	add := flagOp("op-1", "key-a", "folder-inbox")
	add.Expect = OutboxExpect{FlagsAdd: []string{`\Flagged`}}
	if _, _, err := dbs.EnqueueOutbox(ctx, add); err != nil {
		t.Fatalf("enqueue add: %v", err)
	}
	remove := flagOp("op-2", "key-a", "folder-inbox")
	remove.Expect = OutboxExpect{FlagsClear: []string{`\Flagged`}}
	got, created, err := dbs.EnqueueOutbox(ctx, remove)
	if err != nil {
		t.Fatalf("enqueue remove: %v", err)
	}
	if !created {
		t.Fatal("the inverse was swallowed instead of recorded")
	}
	if got.State != OutboxCancelled {
		t.Errorf("inverse state = %q, want %q", got.State, OutboxCancelled)
	}
	for _, id := range []string{"op-1", "op-2"} {
		op, err := dbs.GetOutbox(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if op.State != OutboxCancelled {
			t.Errorf("%s state = %q, want %q", id, op.State, OutboxCancelled)
		}
	}
}

// Sync and the read overlay need only the live keys, never a terminal op.
func TestOutboxActiveKeysListsOnlyNonTerminalOps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, _, err := dbs.EnqueueOutbox(ctx, flagOp("op-1", "key-a", "folder-inbox")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, _, err := dbs.EnqueueOutbox(ctx, moveOp("op-2", "key-b", "folder-inbox", "folder-archive")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := dbs.SetOutboxDone(ctx, "op-2", outboxNow); err != nil {
		t.Fatalf("done: %v", err)
	}

	active, err := dbs.OutboxActiveKeys(ctx, "acct-1")
	if err != nil {
		t.Fatalf("active keys: %v", err)
	}
	if !active[OutboxKey{ContentKey: "key-a", SourceFolderID: "folder-inbox"}] {
		t.Error("the pending op's key is missing")
	}
	if active[OutboxKey{ContentKey: "key-b", SourceFolderID: "folder-inbox"}] {
		t.Error("a terminal op was reported active")
	}
}

// Terminal rows stay seven days so the undo toast and a short history survive a
// restart, then they are pruned; a pending op is never pruned.
func TestPruneOutboxRemovesOnlyOldTerminalRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	old := outboxNow.Add(-8 * 24 * time.Hour)
	for _, id := range []string{"op-done", "op-failed", "op-cancelled"} {
		op := flagOp(id, "key-"+id, "folder-inbox")
		op.CreatedAt = old
		if _, _, err := dbs.EnqueueOutbox(ctx, op); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}
	if err := dbs.SetOutboxDone(ctx, "op-done", old); err != nil {
		t.Fatalf("done: %v", err)
	}
	if err := dbs.SetOutboxFailed(ctx, "op-failed", "noperm", "no", old); err != nil {
		t.Fatalf("failed: %v", err)
	}
	if err := dbs.CancelOutbox(ctx, "op-cancelled", old); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, _, err := dbs.EnqueueOutbox(ctx, flagOp("op-pending", "key-pending", "folder-inbox")); err != nil {
		t.Fatalf("enqueue pending: %v", err)
	}

	n, err := dbs.PruneOutbox(ctx, outboxNow)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 3 {
		t.Errorf("pruned %d rows, want 3", n)
	}
	if _, err := dbs.GetOutbox(ctx, "op-pending"); err != nil {
		t.Errorf("pending op was pruned: %v", err)
	}
	if _, err := dbs.GetOutbox(ctx, "op-done"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old done op survived: %v", err)
	}
}

// A terminal op that is still inside the retention window must survive, so the
// undo toast keeps working right after the action.
func TestPruneOutboxKeepsRecentTerminalRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, _, err := dbs.EnqueueOutbox(ctx, flagOp("op-1", "key-a", "folder-inbox")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := dbs.SetOutboxDone(ctx, "op-1", outboxNow); err != nil {
		t.Fatalf("done: %v", err)
	}
	n, err := dbs.PruneOutbox(ctx, outboxNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 0 {
		t.Errorf("pruned %d recent rows, want 0", n)
	}
	if _, err := dbs.GetOutbox(ctx, "op-1"); err != nil {
		t.Errorf("recent done op was pruned: %v", err)
	}
}

// An op row lives in the backed-up state database and must not be lost by a
// mirror rebuild, exactly like a tag or a setting.
func TestOutboxSurvivesAMirrorRebuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	dbs, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, _, err := dbs.EnqueueOutbox(ctx, flagOp("op-1", "key-a", "folder-inbox")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := dbs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A mirror rebuild replaces mirror.db only.
	for _, name := range []string{"mirror.db", "mirror.db-wal", "mirror.db-shm"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remove %s: %v", name, err)
		}
	}

	dbs, err = Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = dbs.Close() }()

	if _, err := dbs.GetOutbox(ctx, "op-1"); err != nil {
		t.Errorf("outbox op did not survive the mirror rebuild: %v", err)
	}
}

func countOutbox(t *testing.T, dbs *DBs) int {
	t.Helper()
	var n int
	if err := dbs.State.Read.QueryRow(`SELECT count(*) FROM outbox`).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

// itoa avoids importing strconv into the test's shared namespace only to build
// the cap test's per-op keys.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
