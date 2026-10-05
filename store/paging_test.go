package store

import (
	"context"
	"testing"
	"time"
)

// pagingFixture mirrors five messages in the inbox: three dated, two with no
// Date header, plus a second copy of one of them in another folder (the same
// content key, as after a move or a copy to an archive).
func pagingFixture(t *testing.T) (*DBs, []ContentRef) {
	t.Helper()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct")
	for id, role := range map[string]string{"f1": RoleInbox, "f2": RoleArchive} {
		if err := dbs.UpsertFolder(ctx, Folder{ID: id, AccountID: "acct", Name: id, Role: role}); err != nil {
			t.Fatal(err)
		}
	}
	put := func(id, folder string, uid uint32, at time.Time) {
		t.Helper()
		if err := dbs.UpsertMessage(ctx, Message{
			ID: id, AccountID: "acct", FolderID: folder, UID: uid, ContentKey: "ck-" + id[:2],
			Subject: id, Date: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	day := func(d int) time.Time { return time.Date(2026, 9, d, 9, 0, 0, 0, time.UTC) }
	put("m1-dated", "f1", 1, day(1))
	put("m2-dated", "f1", 2, day(2))
	put("m3-dated", "f1", 3, day(3))
	put("m4-undated", "f1", 4, time.Time{})
	put("m5-undated", "f1", 5, time.Time{})
	put("m3-copy", "f2", 1, day(3)) // same content key as m3-dated
	var refs []ContentRef
	for _, k := range []string{"ck-m1", "ck-m2", "ck-m3", "ck-m4", "ck-m5"} {
		refs = append(refs, ContentRef{AccountID: "acct", ContentKey: k})
	}
	return dbs, refs
}

// The keyset compared (date, id) with a NULL date, which is never true, so an
// undated message was listed only if it happened to fit on the first page.
func TestInboxPagesReachUndatedMail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs, _ := pagingFixture(t)

	seen := map[string]int{}
	cursor := ""
	for range 10 {
		page, err := dbs.ListInbox(ctx, InboxQuery{Role: RoleInbox, Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page.Items {
			seen[m.ID]++
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("paged through %v, want all five messages of the inbox folder", seen)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s was listed %d times", id, n)
		}
	}
}

// The local views (snoozed, Reading, a tag) page the same way, one row per
// message even when a copy sits in two folders, and reach undated mail.
func TestMessagesByContentRefsPages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs, refs := pagingFixture(t)

	seen := map[string]int{}
	cursor := ""
	pages := 0
	for range 10 {
		page, err := dbs.MessagesByContentRefs(ctx, "", refs, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, m := range page.Items {
			seen[m.ContentKey]++
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("paged through %v, want five messages", seen)
	}
	for key, n := range seen {
		if n != 1 {
			t.Errorf("%s was listed %d times", key, n)
		}
	}
	if pages != 3 {
		t.Errorf("%d pages of 2 for 5 messages, want 3", pages)
	}
}
