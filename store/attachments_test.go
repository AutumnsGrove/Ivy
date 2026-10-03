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
