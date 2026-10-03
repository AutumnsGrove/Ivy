package store

import (
	"context"
	"testing"
	"time"
)

// The account list and mirror health both need, per account, how much mail is
// unread and how much of the mailbox has synced, without N+1 queries per row.
func TestAccountStatsRollsUpUnreadAndSync(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	seedInbox(t, dbs, "acct-1", "inbox-1")
	seedOther(t, dbs, "acct-1", "archive-1")
	seedInbox(t, dbs, "acct-2", "inbox-2")

	synced := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	for _, f := range []struct {
		id, account string
		role        string
		lastSync    time.Time
	}{{"inbox-1", "acct-1", RoleInbox, synced}, {"archive-1", "acct-1", RoleOther, synced.Add(-time.Hour)}} {
		err := dbs.UpsertFolder(ctx, Folder{
			ID: f.id, AccountID: f.account, Name: f.id, Role: f.role, LastSyncAt: f.lastSync,
		})
		if err != nil {
			t.Fatalf("folder %s: %v", f.id, err)
		}
	}

	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	seedMessage(t, dbs, "acct-1", "inbox-1", "a1", base, false)
	seedMessage(t, dbs, "acct-1", "inbox-1", "a2", base.Add(time.Hour), false)
	seedMessage(t, dbs, "acct-1", "inbox-1", "a3", base.Add(2*time.Hour), true)
	seedMessage(t, dbs, "acct-1", "archive-1", "a4", base.Add(3*time.Hour), false)
	seedMessage(t, dbs, "acct-2", "inbox-2", "b1", base, false)

	// A disabled message is hidden everywhere, so it must not count as unread.
	disabled := Message{ID: "a5", AccountID: "acct-1", FolderID: "inbox-1", UID: 99, ContentKey: "ck:a5", Date: base}
	if err := dbs.UpsertMessage(ctx, disabled); err != nil {
		t.Fatalf("upsert disabled: %v", err)
	}
	disabled.DisabledAt = base
	if err := dbs.UpsertMessage(ctx, disabled); err != nil {
		t.Fatalf("disable: %v", err)
	}

	stats, err := dbs.AccountStats(ctx)
	if err != nil {
		t.Fatalf("AccountStats: %v", err)
	}
	one := stats["acct-1"]
	if one.Unread != 2 {
		t.Errorf("acct-1 unread = %d, want 2 (inbox only, disabled excluded)", one.Unread)
	}
	if one.Folders != 2 || one.SyncedFolders != 2 {
		t.Errorf("acct-1 folders = %d synced = %d, want 2/2", one.Folders, one.SyncedFolders)
	}
	if !one.LastSync.Equal(synced) {
		t.Errorf("acct-1 LastSync = %v, want the newest folder sync %v", one.LastSync, synced)
	}

	two := stats["acct-2"]
	if two.Unread != 1 || two.Folders != 1 || two.SyncedFolders != 0 {
		t.Errorf("acct-2 = %+v, want unread 1, 1 folder, 0 synced", two)
	}
	if !two.LastSync.IsZero() {
		t.Errorf("acct-2 LastSync = %v, want the zero time", two.LastSync)
	}
}

func TestAccountStatsEmpty(t *testing.T) {
	t.Parallel()
	stats, err := openTemp(t).AccountStats(context.Background())
	if err != nil {
		t.Fatalf("AccountStats: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("stats = %v, want empty", stats)
	}
}

func TestMessageNeeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")

	_, err := dbs.Mirror.Write.ExecContext(ctx,
		`INSERT INTO needs_me (account_id, content_key, verdict, reason, state) VALUES (?,?,?,?,?)`,
		"acct-1", "ck:yes", "needs", "asks a question", "new")
	if err != nil {
		t.Fatalf("insert needs_me: %v", err)
	}
	_, err = dbs.Mirror.Write.ExecContext(ctx,
		`INSERT INTO needs_me (account_id, content_key, verdict, reason, state) VALUES (?,?,?,?,?)`,
		"acct-1", "ck:no", "calm", "", "new")
	if err != nil {
		t.Fatalf("insert needs_me: %v", err)
	}

	needs, err := dbs.MessageNeeds(ctx, "acct-1", "ck:yes")
	if err != nil {
		t.Fatalf("MessageNeeds: %v", err)
	}
	if !needs {
		t.Error("ck:yes = false, want true")
	}
	if needs, err = dbs.MessageNeeds(ctx, "acct-1", "ck:no"); err != nil || needs {
		t.Errorf("ck:no = %v, %v; want false, nil", needs, err)
	}
	if needs, err = dbs.MessageNeeds(ctx, "acct-1", "ck:missing"); err != nil || needs {
		t.Errorf("missing = %v, %v; want false, nil", needs, err)
	}
	if needs, err = dbs.MessageNeeds(ctx, "acct-2", "ck:yes"); err != nil || needs {
		t.Errorf("other account = %v, %v; want false, nil", needs, err)
	}
}

func TestMirrorBytesReportsTheFileSize(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	n, err := dbs.MirrorBytes(context.Background())
	if err != nil {
		t.Fatalf("MirrorBytes: %v", err)
	}
	if n <= 0 {
		t.Errorf("MirrorBytes = %d, want the migrated schema's size", n)
	}
}
