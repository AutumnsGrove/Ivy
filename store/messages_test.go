package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMessageRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")

	date := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	in := Message{
		ID:             "msg-1",
		AccountID:      "acct-1",
		FolderID:       "folder-1",
		UID:            10,
		ContentKey:     ContentKey("<m1@example.test>", nil),
		MessageID:      "<m1@example.test>",
		InReplyTo:      "<m0@example.test>",
		References:     "<m0@example.test>",
		Subject:        "Hello",
		From:           Address{Name: "Alice", Address: "alice@example.test"},
		To:             []Address{{Name: "Me", Address: "me@example.test"}},
		CC:             []Address{},
		Date:           date,
		Size:           1234,
		Flags:          []string{`\Seen`, `\Flagged`},
		InternalDate:   date.Add(time.Minute),
		HasAttachments: true,
		RawBlob:        []byte("raw bytes"),
		BodyText:       "hi",
		BodyHTML:       "<p>hi</p>",
		ThreadID:       "thread-1",
		Snippet:        "hi",
	}
	if err := dbs.UpsertMessage(ctx, in); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	got, err := dbs.GetMessage(ctx, "msg-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if !got.Seen {
		t.Error("Seen = false, want true (derived from \\Seen flag)")
	}
	in.Seen = true
	if !reflect.DeepEqual(got, in) {
		t.Errorf("message round trip mismatch:\n got %+v\nwant %+v", got, in)
	}
}

func TestUpsertMessageUpdatesInPlace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedFolder(t, dbs, "acct-1", "folder-2")

	m := Message{
		ID: "msg-1", AccountID: "acct-1", FolderID: "folder-1", UID: 10,
		ContentKey: "ck", Subject: "First", BodyText: "old",
	}
	if err := dbs.UpsertMessage(ctx, m); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	m.Subject = "Second"
	m.BodyText = "new"
	m.Flags = []string{`\Seen`}
	if err := dbs.UpsertMessage(ctx, m); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := dbs.GetMessage(ctx, "msg-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Subject != "Second" || got.BodyText != "new" || !got.Seen {
		t.Errorf("update not applied: %+v", got)
	}
}

func TestUpsertMessageReenablesDisabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")

	m := Message{
		ID: "msg-1", AccountID: "acct-1", FolderID: "folder-1", UID: 10,
		ContentKey: "ck", DisabledAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		DisabledReason: "server removed",
	}
	if err := dbs.UpsertMessage(ctx, m); err != nil {
		t.Fatalf("upsert disabled: %v", err)
	}
	if _, err := dbs.GetMessage(ctx, "msg-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled message visible: err = %v, want ErrNotFound", err)
	}

	m.DisabledAt = time.Time{}
	m.DisabledReason = ""
	if err := dbs.UpsertMessage(ctx, m); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, err := dbs.GetMessage(ctx, "msg-1")
	if err != nil {
		t.Fatalf("GetMessage after reappear: %v", err)
	}
	if !got.DisabledAt.IsZero() || got.DisabledReason != "" {
		t.Errorf("still disabled: %+v", got)
	}
}

func TestGetMessageMissing(t *testing.T) {
	t.Parallel()
	if _, err := openTemp(t).GetMessage(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func seedFolder(t *testing.T, dbs *DBs, accountID, folderID string) {
	t.Helper()
	err := dbs.UpsertFolder(context.Background(), Folder{
		ID: folderID, AccountID: accountID, Name: folderID, Role: RoleOther,
	})
	if err != nil {
		t.Fatalf("seedFolder(%s): %v", folderID, err)
	}
}
