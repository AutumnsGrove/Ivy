package store

import (
	"context"
	"testing"
	"time"
)

// Content hashes are 64 hex characters, as the blob store returns them: a hash
// becomes a file name, so a malformed one is refused.
const (
	hashA     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hashOther = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// A purged blob is recorded before its row is removed, so a crash between the
// two cannot leave bytes in an offline target with nothing left to retry them.
func TestPendingBlobDeletionRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if err := dbs.RecordPendingBlobDeletion(ctx, hashA); err != nil {
		t.Fatalf("RecordPendingBlobDeletion: %v", err)
	}
	if err := dbs.RecordPendingBlobDeletion(ctx, hashA); err != nil {
		t.Fatalf("second RecordPendingBlobDeletion: %v", err)
	}
	if err := dbs.RecordPendingBlobDeletion(ctx, hashB); err != nil {
		t.Fatalf("RecordPendingBlobDeletion b: %v", err)
	}
	// An empty hash is not a blob and must not create a row.
	if err := dbs.RecordPendingBlobDeletion(ctx, ""); err != nil {
		t.Fatalf("RecordPendingBlobDeletion empty: %v", err)
	}

	got, err := dbs.PendingBlobDeletions(ctx)
	if err != nil {
		t.Fatalf("PendingBlobDeletions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("pending = %v, want two deduped hashes", got)
	}

	if err := dbs.ClearPendingBlobDeletion(ctx, hashA); err != nil {
		t.Fatalf("ClearPendingBlobDeletion: %v", err)
	}
	got, err = dbs.PendingBlobDeletions(ctx)
	if err != nil {
		t.Fatalf("PendingBlobDeletions after clear: %v", err)
	}
	if len(got) != 1 || got[0] != hashB {
		t.Errorf("pending after clear = %v, want [hash-b]", got)
	}
	// Clearing an unknown hash is a no-op, not an error.
	if err := dbs.ClearPendingBlobDeletion(ctx, "missing"); err != nil {
		t.Errorf("ClearPendingBlobDeletion(unknown) = %v", err)
	}
}

// The pending list is locally owned state, so it survives a clean reopen the way
// the rest of state.db does.
func TestPendingBlobDeletionSurvivesReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbs, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := dbs.RecordPendingBlobDeletion(ctx, hashA); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := dbs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.PendingBlobDeletions(ctx)
	if err != nil {
		t.Fatalf("PendingBlobDeletions: %v", err)
	}
	if len(got) != 1 || got[0] != hashA {
		t.Errorf("pending after reopen = %v, want [hash-a]", got)
	}
}

// BlobReferenced answers whether any hidden row still needs the bytes, so a
// purge of one of several rows that share a blob does not evict the others.
func TestBlobReferenced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledBlob(t, dbs, "m1", "folder-1", 1, hashA)
	seedDisabledBlob(t, dbs, "m2", "folder-1", 2, hashA)

	ref, err := dbs.BlobReferenced(ctx, hashA)
	if err != nil {
		t.Fatalf("BlobReferenced: %v", err)
	}
	if !ref {
		t.Error("BlobReferenced(hash-a) = false with two rows pointing at it")
	}
	ref, err = dbs.BlobReferenced(ctx, hashOther)
	if err != nil {
		t.Fatalf("BlobReferenced: %v", err)
	}
	if ref {
		t.Error("BlobReferenced(hash-other) = true with no row pointing at it")
	}
	// An empty hash is never a reference.
	ref, err = dbs.BlobReferenced(ctx, "")
	if err != nil {
		t.Fatalf("BlobReferenced empty: %v", err)
	}
	if ref {
		t.Error("BlobReferenced(\"\") = true")
	}
}

// A purge of a message whose blob no row shares records the deletion, so the
// backup can erase it from every target including one that is offline.
func TestPurgeRecordsAnUnreferencedBlob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledBlob(t, dbs, "m1", "folder-1", 1, hashA)

	got, err := dbs.PurgeMessage(ctx, "m1")
	if err != nil {
		t.Fatalf("PurgeMessage: %v", err)
	}
	if got.BlobHash != hashA || !got.BlobUnreferenced {
		t.Errorf("purged = %+v, want hash-a unreferenced", got)
	}
	pending, err := dbs.PendingBlobDeletions(ctx)
	if err != nil {
		t.Fatalf("PendingBlobDeletions: %v", err)
	}
	if len(pending) != 1 || pending[0] != hashA {
		t.Errorf("pending = %v, want [hash-a]", pending)
	}
}

// Purging one of two rows that share a blob must keep the bytes for the other,
// and only record the deletion once the last reference is gone.
func TestPurgeKeepsASharedBlob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledBlob(t, dbs, "m1", "folder-1", 1, hashA)
	seedDisabledBlob(t, dbs, "m2", "folder-1", 2, hashA)

	first, err := dbs.PurgeMessage(ctx, "m1")
	if err != nil {
		t.Fatalf("PurgeMessage m1: %v", err)
	}
	if first.BlobUnreferenced {
		t.Errorf("first purge = %+v, want the blob still referenced", first)
	}
	if pending, err := dbs.PendingBlobDeletions(ctx); err != nil || len(pending) != 0 {
		t.Errorf("pending after first purge = %v (%v), want none", pending, err)
	}

	second, err := dbs.PurgeMessage(ctx, "m2")
	if err != nil {
		t.Fatalf("PurgeMessage m2: %v", err)
	}
	if !second.BlobUnreferenced {
		t.Errorf("second purge = %+v, want the blob now unreferenced", second)
	}
	if pending, err := dbs.PendingBlobDeletions(ctx); err != nil || len(pending) != 1 {
		t.Errorf("pending after second purge = %v (%v), want [hash-a]", pending, err)
	}
}

// A message never downloaded has no blob, so a purge records nothing.
func TestPurgeWithoutABlobRecordsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledBlob(t, dbs, "m1", "folder-1", 1, "")

	got, err := dbs.PurgeMessage(ctx, "m1")
	if err != nil {
		t.Fatalf("PurgeMessage: %v", err)
	}
	if got.BlobHash != "" || got.BlobUnreferenced {
		t.Errorf("purged = %+v, want no blob", got)
	}
	if pending, err := dbs.PendingBlobDeletions(ctx); err != nil || len(pending) != 0 {
		t.Errorf("pending = %v (%v), want none", pending, err)
	}
}

func seedDisabledBlob(t *testing.T, dbs *DBs, id, folderID string, uid uint32, hash string) {
	t.Helper()
	if err := dbs.UpsertMessage(context.Background(), Message{
		ID: id, AccountID: "acct-1", FolderID: folderID, UID: uid, ContentKey: id,
		RawBlob:    []byte("raw " + id),
		DisabledAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), DisabledReason: DisabledRemoved,
		DisabledBlob: hash,
	}); err != nil {
		t.Fatalf("seed disabled blob %s: %v", id, err)
	}
}
