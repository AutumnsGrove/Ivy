package store

import (
	"context"
	"errors"
	"hash/crc32"
	"testing"
	"time"
)

func TestListInboxCombinedNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")
	seedInbox(t, dbs, "acct-2", "inbox-2")
	seedOther(t, dbs, "acct-1", "archive-1")

	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	seedMessage(t, dbs, "acct-1", "inbox-1", "m1", base.Add(1*time.Hour), false)
	seedMessage(t, dbs, "acct-1", "inbox-1", "m2", base.Add(2*time.Hour), true)
	seedMessage(t, dbs, "acct-2", "inbox-2", "m3", base.Add(3*time.Hour), true)
	seedMessage(t, dbs, "acct-1", "archive-1", "m4", base.Add(4*time.Hour), true)

	page, err := dbs.ListInbox(ctx, InboxQuery{})
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if got := summaryIDs(page.Items); len(got) != 3 || got[0] != "m3" || got[2] != "m1" {
		t.Errorf("items = %v, want [m3 m2 m1] newest first", got)
	}
	if page.UnreadCount != 1 {
		t.Errorf("UnreadCount = %d, want 1", page.UnreadCount)
	}
}

func TestListInboxScopedToAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")
	seedInbox(t, dbs, "acct-2", "inbox-2")

	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	seedMessage(t, dbs, "acct-1", "inbox-1", "m1", base, false)
	seedMessage(t, dbs, "acct-2", "inbox-2", "m2", base, true)

	page, err := dbs.ListInbox(ctx, InboxQuery{AccountID: "acct-1"})
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if got := summaryIDs(page.Items); len(got) != 1 || got[0] != "m1" {
		t.Errorf("items = %v, want [m1]", got)
	}
}

func TestListInboxPaging(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")

	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		seedMessage(t, dbs, "acct-1", "inbox-1", id, base.Add(time.Duration(i)*time.Hour), false)
	}

	first, err := dbs.ListInbox(ctx, InboxQuery{Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if got := summaryIDs(first.Items); len(got) != 2 || got[0] != "m5" || got[1] != "m4" {
		t.Fatalf("first page = %v, want [m5 m4]", got)
	}
	if first.NextCursor == "" {
		t.Fatal("NextCursor empty on a full page")
	}

	second, err := dbs.ListInbox(ctx, InboxQuery{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if got := summaryIDs(second.Items); len(got) != 2 || got[0] != "m3" || got[1] != "m2" {
		t.Fatalf("second page = %v, want [m3 m2]", got)
	}

	third, err := dbs.ListInbox(ctx, InboxQuery{Limit: 2, Cursor: second.NextCursor})
	if err != nil {
		t.Fatalf("third page: %v", err)
	}
	if got := summaryIDs(third.Items); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("third page = %v, want [m1]", got)
	}
	if third.NextCursor != "" {
		t.Errorf("NextCursor = %q on the last page, want empty", third.NextCursor)
	}
}

func TestListInboxExcludesDisabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")

	m := Message{
		ID: "m1", AccountID: "acct-1", FolderID: "inbox-1", UID: 1,
		ContentKey: "ck", Date: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := dbs.UpsertMessage(ctx, m); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	m.DisabledAt = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if err := dbs.UpsertMessage(ctx, m); err != nil {
		t.Fatalf("disable: %v", err)
	}

	page, err := dbs.ListInbox(ctx, InboxQuery{})
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("items = %v, want none", summaryIDs(page.Items))
	}
}

func TestListInboxCountsNeeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	seedMessage(t, dbs, "acct-1", "inbox-1", "m1", base.Add(1*time.Hour), false)
	seedMessage(t, dbs, "acct-1", "inbox-1", "m2", base, false)

	_, err := dbs.Mirror.Write.ExecContext(ctx,
		`INSERT INTO needs_me (account_id, content_key, verdict, reason, state) VALUES (?,?,?,?,?)`,
		"acct-1", "ck:m1", "needs", "asks a question", "new")
	if err != nil {
		t.Fatalf("insert needs_me: %v", err)
	}

	page, err := dbs.ListInbox(ctx, InboxQuery{})
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if page.NeedCount != 1 {
		t.Errorf("NeedCount = %d, want 1", page.NeedCount)
	}
	if len(page.Items) == 0 {
		t.Fatal("no items returned")
	}
	if !page.Items[0].Needs {
		t.Error("first item Needs = false, want true")
	}
}

// A cursor is opaque; a client that invents one gets a defined client error,
// not a 500 or an empty page that looks like the end of the mailbox.
func TestListInboxRejectsBadCursor(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	_, err := dbs.ListInbox(context.Background(), InboxQuery{Cursor: "!!!not-base64!!!"})
	if !errors.Is(err, ErrBadCursor) {
		t.Fatalf("err = %v, want ErrBadCursor", err)
	}
}

func seedInbox(t *testing.T, dbs *DBs, accountID, folderID string) {
	t.Helper()
	seedAccount(t, dbs, accountID)
	err := dbs.UpsertFolder(context.Background(), Folder{
		ID: folderID, AccountID: accountID, Name: folderID, Role: RoleInbox,
	})
	if err != nil {
		t.Fatalf("seedInbox(%s): %v", folderID, err)
	}
}

func seedOther(t *testing.T, dbs *DBs, accountID, folderID string) {
	t.Helper()
	seedFolder(t, dbs, accountID, folderID)
}

func seedMessage(t *testing.T, dbs *DBs, accountID, folderID, id string, date time.Time, seen bool) {
	t.Helper()
	flags := []string{}
	if seen {
		flags = append(flags, `\Seen`)
	}
	err := dbs.UpsertMessage(context.Background(), Message{
		ID: id, AccountID: accountID, FolderID: folderID, UID: crc32.ChecksumIEEE([]byte(id)),
		ContentKey: "ck:" + id, Date: date, Flags: flags,
	})
	if err != nil {
		t.Fatalf("seedMessage(%s): %v", id, err)
	}
}

func summaryIDs(items []MessageSummary) []string {
	out := make([]string, len(items))
	for i, m := range items {
		out[i] = m.ID
	}
	return out
}
