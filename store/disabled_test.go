package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func seedDisabledMessage(t *testing.T, dbs *DBs, accountID, folderID, id, reason string, uid uint32, rawPath string) {
	t.Helper()
	if err := dbs.UpsertMessage(context.Background(), Message{
		ID: id, AccountID: accountID, FolderID: folderID, UID: uid, ContentKey: id,
		RawBlob: []byte("raw " + id), RawPath: rawPath,
		DisabledAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), DisabledReason: reason,
	}); err != nil {
		t.Fatalf("seed disabled %s: %v", id, err)
	}
}

// Hidden mail is grouped per account so Mirror health can say how much is kept
// but not shown, and the pending rows a pass has not classified yet are counted
// apart from the settled ones (N22).
func TestDisabledStatsCountsHiddenMail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedAccount(t, dbs, "acct-2")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedFolder(t, dbs, "acct-2", "folder-2")

	seedDisabledMessage(t, dbs, "acct-1", "folder-1", "m-removed", DisabledRemoved, 1, "")
	seedDisabledMessage(t, dbs, "acct-1", "folder-1", "m-moved", DisabledMoved, 2, "")
	seedDisabledMessage(t, dbs, "acct-1", "folder-1", "m-pending", DisabledPending, 3, "")
	seedDisabledMessage(t, dbs, "acct-2", "folder-2", "m-other", DisabledRemoved, 1, "")
	// A live row is not hidden and must not be counted.
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "live", AccountID: "acct-1", FolderID: "folder-1", UID: 99, ContentKey: "live",
	}); err != nil {
		t.Fatalf("seed live: %v", err)
	}

	stats, err := dbs.DisabledStats(ctx)
	if err != nil {
		t.Fatalf("DisabledStats: %v", err)
	}
	got := stats["acct-1"]
	if got.Hidden != 3 || got.Removed != 1 || got.Moved != 1 || got.Pending != 1 {
		t.Errorf("acct-1 stats = %+v, want hidden 3, removed 1, moved 1, pending 1", got)
	}
	if other := stats["acct-2"]; other.Hidden != 1 || other.Removed != 1 {
		t.Errorf("acct-2 stats = %+v, want hidden 1, removed 1", other)
	}
	if _, ok := stats["acct-3"]; ok {
		t.Errorf("an account with no hidden mail should be absent, got present")
	}
}

// Restore is the operator's way back from a wrong hide: the row becomes visible
// again with its bytes untouched. Restoring a live row is harmless, and an
// unknown id is a client error, not a silent success.
func TestRestoreMessageMakesItVisible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledMessage(t, dbs, "acct-1", "folder-1", "m1", DisabledRemoved, 1, "spool/folder-1/1.eml")

	if err := dbs.RestoreMessage(ctx, "m1"); err != nil {
		t.Fatalf("RestoreMessage: %v", err)
	}
	got, err := dbs.GetMessage(ctx, "m1")
	if err != nil {
		t.Fatalf("GetMessage after restore: %v", err)
	}
	if !got.DisabledAt.IsZero() || got.DisabledReason != "" {
		t.Errorf("restored row still hidden: %+v", got)
	}
	if string(got.RawBlob) != "raw m1" || got.RawPath != "spool/folder-1/1.eml" {
		t.Errorf("restore erased the bytes: blob %q path %q", got.RawBlob, got.RawPath)
	}

	if err := dbs.RestoreMessage(ctx, "m1"); err != nil {
		t.Errorf("restoring a live row: %v, want nil", err)
	}
	if err := dbs.RestoreMessage(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
}

// Restoring a whole account is the one-click answer to a mass-disable alert. It
// leaves rows a pass has not classified alone, because their true reason is not
// known yet (N22).
func TestRestoreAccountSkipsPendingRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledMessage(t, dbs, "acct-1", "folder-1", "m-removed", DisabledRemoved, 1, "")
	seedDisabledMessage(t, dbs, "acct-1", "folder-1", "m-moved", DisabledMoved, 2, "")
	seedDisabledMessage(t, dbs, "acct-1", "folder-1", "m-pending", DisabledPending, 3, "")
	// A hidden row with no reason still has to be restorable; only the pending
	// label is special.
	if _, err := dbs.Mirror.Write.ExecContext(ctx,
		`INSERT INTO messages (id, account_id, folder_id, uid, content_key, disabled_at)
		 VALUES ('m-no-reason', 'acct-1', 'folder-1', 4, 'ck-no-reason', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert row without a reason: %v", err)
	}

	n, err := dbs.RestoreAccountDisabled(ctx, "acct-1")
	if err != nil {
		t.Fatalf("RestoreAccountDisabled: %v", err)
	}
	if n != 3 {
		t.Errorf("restored %d rows, want 3 (pending is not restorable)", n)
	}
	for id, wantHidden := range map[string]bool{"m-removed": false, "m-moved": false, "m-pending": true, "m-no-reason": false} {
		var disabledAt string
		if err := dbs.Mirror.Read.QueryRowContext(ctx,
			`SELECT COALESCE(disabled_at, '') FROM messages WHERE id = ?`, id).Scan(&disabledAt); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if hidden := disabledAt != ""; hidden != wantHidden {
			t.Errorf("%s hidden = %v, want %v", id, hidden, wantHidden)
		}
	}
}

// Purge is the one explicit erasure. It removes the row and its attachment
// rows and hands back the spool path so the caller can unlink the file. It
// refuses a live message, so the reader's ordinary delete can never reach it.
func TestPurgeMessageErasesRowAndAttachments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledMessage(t, dbs, "acct-1", "folder-1", "m1", DisabledRemoved, 1, "spool/folder-1/1.eml")
	if err := dbs.ReplaceMessageAttachments(ctx, "m1", []Attachment{
		{ID: "att-1", MessageID: "m1", Filename: "a.pdf", StoragePath: "1"},
		{ID: "att-2", MessageID: "m1", Filename: "b.pdf", StoragePath: "2"},
	}); err != nil {
		t.Fatalf("seed attachments: %v", err)
	}

	rawPath, err := dbs.PurgeMessage(ctx, "m1")
	if err != nil {
		t.Fatalf("PurgeMessage: %v", err)
	}
	if rawPath != "spool/folder-1/1.eml" {
		t.Errorf("rawPath = %q, want the spool path", rawPath)
	}
	if _, err := dbs.GetMessage(ctx, "m1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("purged message still readable: %v", err)
	}
	var n int
	if err := dbs.Mirror.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM attachments WHERE message_id = 'm1'`).Scan(&n); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if n != 0 {
		t.Errorf("%d attachment rows survived a purge", n)
	}

	if _, err := dbs.PurgeMessage(ctx, "m1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("purge unknown err = %v, want ErrNotFound", err)
	}
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "live", AccountID: "acct-1", FolderID: "folder-1", UID: 3, ContentKey: "live",
	}); err != nil {
		t.Fatalf("seed live: %v", err)
	}
	if _, err := dbs.PurgeMessage(ctx, "live"); !errors.Is(err, ErrNotDisabled) {
		t.Errorf("purge live err = %v, want ErrNotDisabled", err)
	}
}
