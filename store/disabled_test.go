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
	if n != 2 {
		t.Errorf("restored %d rows, want 2 (pending is unclassified and moved mail is still live elsewhere)", n)
	}
	// A moved message has a live copy in another folder, so un-hiding it would
	// show the same mail twice; only mail the server dropped comes back.
	for id, wantHidden := range map[string]bool{"m-removed": false, "m-moved": true, "m-pending": true, "m-no-reason": false} {
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

	purged, err := dbs.PurgeMessage(ctx, "m1")
	if err != nil {
		t.Fatalf("PurgeMessage: %v", err)
	}
	if purged.RawPath != "spool/folder-1/1.eml" {
		t.Errorf("rawPath = %q, want the spool path", purged.RawPath)
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

// A restore can land between PurgeMessage's check and its delete. A trigger that
// un-hides the row as soon as its attachments are deleted stands in for that
// restore: the one erasure Ivy has must still refuse a row that is visible by the
// time it deletes, and must leave it (and its attachments) whole.
func TestPurgeMessageRefusesARowRestoredBeforeTheDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledMessage(t, dbs, "acct-1", "folder-1", "m1", DisabledRemoved, 1, "")
	if _, err := dbs.Mirror.Write.ExecContext(ctx,
		`INSERT INTO attachments (id, message_id, filename) VALUES ('a1', 'm1', 'f.txt')`); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
	if _, err := dbs.Mirror.Write.ExecContext(ctx, `
		CREATE TRIGGER restore_mid_purge AFTER DELETE ON attachments
		BEGIN UPDATE messages SET disabled_at = NULL, disabled_reason = NULL WHERE id = OLD.message_id; END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	if _, err := dbs.PurgeMessage(ctx, "m1"); !errors.Is(err, ErrNotDisabled) {
		t.Errorf("PurgeMessage of a row restored mid-purge = %v, want ErrNotDisabled", err)
	}
	var rows, atts int
	if err := dbs.Mirror.Read.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE id = 'm1'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Mirror.Read.QueryRowContext(ctx, `SELECT count(*) FROM attachments WHERE message_id = 'm1'`).Scan(&atts); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || atts != 1 {
		t.Errorf("after the refused purge: %d message rows and %d attachment rows, want 1 and 1", rows, atts)
	}
}

// A sweep is what the server dropped. A pending row whose Message-ID is live in
// another row is going to settle as a move, so it is not part of any folder's
// sweep; one with no Message-ID can never be proven moved and counts.
func TestPendingDisabledByFolderLeavesOutRowsThatWillSettleAsMoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedFolder(t, dbs, "acct-1", "folder-2")
	add := func(id, folder string, uid uint32, msgID, reason string) {
		t.Helper()
		m := Message{ID: id, AccountID: "acct-1", FolderID: folder, UID: uid, ContentKey: id, MessageID: msgID}
		if reason != "" {
			m.DisabledAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			m.DisabledReason = reason
		}
		if err := dbs.UpsertMessage(ctx, m); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	add("gone", "folder-1", 1, "<gone@x>", DisabledPending)
	add("moved", "folder-1", 2, "<moved@x>", DisabledPending)
	add("moved-copy", "folder-2", 1, "<moved@x>", "") // live elsewhere
	add("anon", "folder-1", 3, "", DisabledPending)
	add("live", "folder-1", 4, "<live@x>", "")

	got, err := dbs.PendingDisabledByFolder(ctx, "acct-1")
	if err != nil {
		t.Fatalf("PendingDisabledByFolder: %v", err)
	}
	if len(got) != 1 || got[0].Hidden != 2 || got[0].Held != 4 {
		t.Errorf("sweeps = %+v, want one folder with hidden 2 (gone, anon) of held 4 (live, gone, moved, anon)", got)
	}
}

// A pending row has no verdict yet: the next completed pass decides whether it
// moved or was removed. Restoring it by hand would race that verdict, so single
// restore refuses it the way the account restore skips it, and leaves it hidden.
func TestRestoreMessageRefusesAPendingRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledMessage(t, dbs, "acct-1", "folder-1", "m1", DisabledPending, 1, "")

	if err := dbs.RestoreMessage(ctx, "m1"); !errors.Is(err, ErrPendingClassification) {
		t.Fatalf("RestoreMessage of a pending row = %v, want ErrPendingClassification", err)
	}
	var hidden int
	if err := dbs.Mirror.Read.QueryRowContext(ctx,
		`SELECT disabled_at IS NOT NULL FROM messages WHERE id = 'm1'`).Scan(&hidden); err != nil {
		t.Fatalf("read m1: %v", err)
	}
	if hidden != 1 {
		t.Error("the refused restore made the pending row visible")
	}
}
