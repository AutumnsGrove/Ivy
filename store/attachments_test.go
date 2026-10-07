package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testDate() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }

func attachment(path, filename, cid, hash string, size int64) Attachment {
	return Attachment{
		Filename: filename, MIMEType: "application/octet-stream",
		Size: size, ContentHash: hash, CID: cid, StoragePath: path,
	}
}

func TestReplaceMessageAttachmentsIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m1", testDate(), false)

	rows := []Attachment{
		attachment("2", "note.txt", "", "hash-a", 16),
		attachment("3", "logo.png", "logo@example", "hash-b", 8),
	}
	if err := dbs.ReplaceMessageAttachments(ctx, "m1", rows); err != nil {
		t.Fatalf("ReplaceMessageAttachments: %v", err)
	}
	// A re-sync replaces the same rows rather than duplicating them.
	if err := dbs.ReplaceMessageAttachments(ctx, "m1", rows); err != nil {
		t.Fatalf("ReplaceMessageAttachments again: %v", err)
	}

	got, err := dbs.ListAttachments(ctx, "m1")
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d attachments, want 2", len(got))
	}
	// Document order is the order sync enumerated the parts.
	if got[0].StoragePath != "2" || got[1].StoragePath != "3" {
		t.Errorf("order = %s, %s; want 2 then 3", got[0].StoragePath, got[1].StoragePath)
	}
	if got[0].ID == "" || got[0].ID == got[1].ID {
		t.Errorf("ids = %q, %q; want distinct generated ids", got[0].ID, got[1].ID)
	}
	if got[0].MessageID != "m1" || got[0].Filename != "note.txt" || got[0].ContentHash != "hash-a" {
		t.Errorf("row = %+v", got[0])
	}
}

func TestReplaceMessageAttachmentsClearsWhenEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m1", testDate(), false)

	if err := dbs.ReplaceMessageAttachments(ctx, "m1", []Attachment{attachment("2", "a.txt", "", "h", 1)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := dbs.ReplaceMessageAttachments(ctx, "m1", nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err := dbs.ListAttachments(ctx, "m1")
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("attachments = %+v, want none", got)
	}
}

func TestAttachmentLookups(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m1", testDate(), false)
	seedMessage(t, dbs, "acct-1", "inbox-1", "m2", testDate(), false)

	// The same part path on two messages must resolve to each message's row.
	if err := dbs.ReplaceMessageAttachments(ctx, "m1", []Attachment{attachment("2", "one.txt", "cid-one", "h1", 1)}); err != nil {
		t.Fatalf("m1: %v", err)
	}
	if err := dbs.ReplaceMessageAttachments(ctx, "m2", []Attachment{attachment("2", "two.txt", "cid-two", "h2", 2)}); err != nil {
		t.Fatalf("m2: %v", err)
	}

	byPath, err := dbs.GetAttachmentByPath(ctx, "m2", "2")
	if err != nil {
		t.Fatalf("GetAttachmentByPath: %v", err)
	}
	if byPath.Filename != "two.txt" {
		t.Errorf("by path = %+v, want m2's row", byPath)
	}
	byCID, err := dbs.GetAttachmentByCID(ctx, "m1", "cid-one")
	if err != nil {
		t.Fatalf("GetAttachmentByCID: %v", err)
	}
	if byCID.Filename != "one.txt" {
		t.Errorf("by cid = %+v, want m1's row", byCID)
	}

	if _, err := dbs.GetAttachmentByPath(ctx, "m1", "9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing path = %v, want ErrNotFound", err)
	}
	if _, err := dbs.GetAttachmentByCID(ctx, "m1", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing cid = %v, want ErrNotFound", err)
	}
}

func TestAttachmentsSurviveADisabledMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m1", testDate(), false)
	if err := dbs.ReplaceMessageAttachments(ctx, "m1", []Attachment{attachment("2", "a.txt", "", "h", 1)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Disabling is not erasing: the attachment rows stay, as the raw does.
	m, err := dbs.GetMessage(ctx, "m1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	m.DisabledAt = testDate()
	if err := dbs.UpsertMessage(ctx, m); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, err := dbs.ListAttachments(ctx, "m1")
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("attachments = %+v, want the row kept", got)
	}
}

// The "From your mail" list is newest first, hides disabled mail, filters by
// name and is bounded.
func TestListRecentMailAttachments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedInbox(t, dbs, "acct-1", "inbox-1")
	seedInbox(t, dbs, "acct-2", "inbox-2")
	seedMessage(t, dbs, "acct-1", "inbox-1", "old", time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), false)
	seedMessage(t, dbs, "acct-1", "inbox-1", "new", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), false)
	seedDisabledMessage(t, dbs, "acct-1", "inbox-1", "gone", DisabledRemoved, 7, "")
	seedMessage(t, dbs, "acct-2", "inbox-2", "other", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), false)

	if err := dbs.ReplaceMessageAttachments(ctx, "old", []Attachment{attachment("2", "old.pdf", "", "h1", 10)}); err != nil {
		t.Fatalf("old: %v", err)
	}
	if err := dbs.ReplaceMessageAttachments(ctx, "new", []Attachment{
		attachment("2", "new.pdf", "", "h2", 20),
		attachment("3", "logo.png", "logo@x", "h3", 5),
	}); err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := dbs.ReplaceMessageAttachments(ctx, "gone", []Attachment{attachment("2", "hidden.pdf", "", "h4", 1)}); err != nil {
		t.Fatalf("gone: %v", err)
	}
	if err := dbs.ReplaceMessageAttachments(ctx, "other", []Attachment{attachment("2", "other.pdf", "", "h5", 1)}); err != nil {
		t.Fatalf("other: %v", err)
	}

	got, err := dbs.ListRecentMailAttachments(ctx, "acct-1", "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d attachments, want 3: %+v", len(got), got)
	}
	if got[0].Name != "new.pdf" || got[1].Name != "logo.png" || got[2].Name != "old.pdf" {
		t.Errorf("order = %s, %s, %s; want newest message first", got[0].Name, got[1].Name, got[2].Name)
	}
	if got[1].Inline != true {
		t.Errorf("inline = %v, want the cid part marked inline", got[1].Inline)
	}
	if got[0].MessageID != "new" || got[0].Path != "2" || got[0].Size != 20 {
		t.Errorf("row = %+v, want the new message's part", got[0])
	}

	filtered, err := dbs.ListRecentMailAttachments(ctx, "acct-1", "old", 10)
	if err != nil {
		t.Fatalf("filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Name != "old.pdf" {
		t.Errorf("filtered = %+v, want only old.pdf", filtered)
	}

	limited, err := dbs.ListRecentMailAttachments(ctx, "acct-1", "", 1)
	if err != nil {
		t.Fatalf("limited: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("limited = %d rows, want 1", len(limited))
	}
}
