package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func seedRawMessage(t *testing.T, dbs *DBs, id string, uid uint32, date time.Time) {
	t.Helper()
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	err := dbs.UpsertMessage(context.Background(), Message{
		ID: id, AccountID: "acct-1", FolderID: "folder-1", UID: uid,
		ContentKey: ContentKey("<"+id+"@example.test>", nil), Date: date,
		RawBlob: []byte("From: a@example.test\r\n\r\nbody"), BodyStatus: BodyOK,
	})
	if err != nil {
		t.Fatalf("UpsertMessage %s: %v", id, err)
	}
}

// Everything computed from a message's raw bytes lands together with the version
// that says which pipeline produced it, so a reader never sees half of it.
func TestSetMessageDerivedWritesEverythingTogether(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedRawMessage(t, dbs, "m1", 1, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))

	err := dbs.SetMessageDerived(ctx, "m1", Derived{
		Version: 3, BodyText: "hello", Snippet: "hello", HasAttachments: true,
		ParseErrors: []string{"[W] odd"}, BodyStatus: BodyOK, BodyHTML: "<p>hello</p>",
		Attachments: []Attachment{{Filename: "a.txt", MIMEType: "text/plain", Size: 5, StoragePath: "2"}},
	})
	if err != nil {
		t.Fatalf("SetMessageDerived: %v", err)
	}
	got, err := dbs.GetMessage(ctx, "m1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.DerivedVersion != 3 || got.BodyHTML != "<p>hello</p>" || got.BodyText != "hello" ||
		got.Snippet != "hello" || !got.HasAttachments || !slices.Equal(got.ParseErrors, []string{"[W] odd"}) {
		t.Errorf("derived columns = %+v", got)
	}
	atts, err := dbs.ListAttachments(ctx, "m1")
	if err != nil || len(atts) != 1 || atts[0].Filename != "a.txt" {
		t.Errorf("attachments = %+v, %v", atts, err)
	}

	// A second pass replaces rather than duplicates, and an empty HTML clears the
	// old one (a stricter policy may sanitize a body down to nothing).
	if err := dbs.SetMessageDerived(ctx, "m1", Derived{Version: 4, BodyStatus: BodyOK}); err != nil {
		t.Fatalf("second SetMessageDerived: %v", err)
	}
	got, _ = dbs.GetMessage(ctx, "m1")
	atts, _ = dbs.ListAttachments(ctx, "m1")
	if got.DerivedVersion != 4 || got.BodyHTML != "" || got.HasAttachments || len(atts) != 0 {
		t.Errorf("after the second pass = %+v, attachments %+v; want it all replaced", got, atts)
	}
}

// The crash window of N14: the body was written and the attachments were not.
// A failure part-way must leave the previous derived data and version intact,
// or a row can claim a version whose data is half there.
func TestSetMessageDerivedIsAllOrNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedRawMessage(t, dbs, "m1", 1, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))

	// Two parts at one path collide on the attachment key, so the second insert
	// fails after the body and the first attachment were already written.
	dup := Attachment{Filename: "a", StoragePath: "2"}
	err := dbs.SetMessageDerived(ctx, "m1", Derived{
		Version: 2, BodyText: "new", BodyStatus: BodyOK, BodyHTML: "<p>new</p>",
		Attachments: []Attachment{dup, dup},
	})
	if err == nil {
		t.Fatal("SetMessageDerived accepted two parts at one path")
	}
	got, _ := dbs.GetMessage(ctx, "m1")
	atts, _ := dbs.ListAttachments(ctx, "m1")
	if got.DerivedVersion != 0 || got.BodyHTML != "" || got.BodyText != "" || len(atts) != 0 {
		t.Errorf("a failed write left %+v and attachments %+v behind", got, atts)
	}
}

func TestSetMessageDerivedOnMissingMessage(t *testing.T) {
	t.Parallel()
	err := openTemp(t).SetMessageDerived(context.Background(), "nope", Derived{Version: 1})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// A re-sync upserts the row again; it must not reset the version, or every
// message would be re-derived on every fetch.
func TestUpsertKeepsTheDerivedVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedRawMessage(t, dbs, "m1", 1, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err := dbs.SetMessageDerived(ctx, "m1", Derived{Version: 5, BodyStatus: BodyOK}); err != nil {
		t.Fatalf("SetMessageDerived: %v", err)
	}
	seedRawMessage(t, dbs, "m1", 1, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	got, _ := dbs.GetMessage(ctx, "m1")
	if got.DerivedVersion != 5 {
		t.Errorf("DerivedVersion after a re-upsert = %d, want 5", got.DerivedVersion)
	}
}

func TestMessageIDsBehindIsBoundedNewestFirstAndOnlyRealWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"old", "mid", "new", "current", "disabled"} {
		seedRawMessage(t, dbs, id, uint32(i+1), base.Add(time.Duration(i)*time.Hour))
	}
	// "new" is the newest behind row; "current" is newer still but already derived.
	if err := dbs.SetMessageDerived(ctx, "current", Derived{Version: 2, BodyStatus: BodyOK}); err != nil {
		t.Fatalf("SetMessageDerived: %v", err)
	}
	// "disabled" is hidden everywhere, so it is not worth deriving.
	if _, err := dbs.Mirror.Write.ExecContext(ctx,
		`UPDATE messages SET disabled_at = ? WHERE id = 'disabled'`, formatTime(base)); err != nil {
		t.Fatalf("disable: %v", err)
	}
	// A message that was never downloaded has no raw to derive from.
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "undownloaded", AccountID: "acct-1", FolderID: "folder-1", UID: 99,
		ContentKey: "ck:u", Date: base.Add(100 * time.Hour), BodyStatus: BodyTooLarge,
	}); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	got, err := dbs.MessageIDsBehind(ctx, "acct-1", 2, 10)
	if err != nil {
		t.Fatalf("MessageIDsBehind: %v", err)
	}
	if want := []string{"new", "mid", "old"}; !slices.Equal(got, want) {
		t.Errorf("behind = %v, want %v (newest first, no current, disabled or undownloaded rows)", got, want)
	}
	got, _ = dbs.MessageIDsBehind(ctx, "acct-1", 2, 2)
	if want := []string{"new", "mid"}; !slices.Equal(got, want) {
		t.Errorf("limit 2 = %v, want %v", got, want)
	}
	if got, _ = dbs.MessageIDsBehind(ctx, "other", 2, 10); len(got) != 0 {
		t.Errorf("another account's behind = %v, want none", got)
	}
}
