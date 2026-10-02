package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
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

func TestMessageUIDsReturnsOnlyEnabledRowsInFolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedFolder(t, dbs, "acct-1", "folder-2")

	for _, uid := range []uint32{10, 20, 30} {
		m := Message{ID: "m" + string(rune('a'+uid/10)), AccountID: "acct-1", FolderID: "folder-1", UID: uid, ContentKey: "ck"}
		if uid == 20 {
			m.DisabledAt = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
		}
		if err := dbs.UpsertMessage(ctx, m); err != nil {
			t.Fatalf("upsert %d: %v", uid, err)
		}
	}
	// A message in another folder must not leak into this folder's set.
	if err := dbs.UpsertMessage(ctx, Message{ID: "other", AccountID: "acct-1", FolderID: "folder-2", UID: 10, ContentKey: "ck"}); err != nil {
		t.Fatalf("upsert other: %v", err)
	}

	uids, err := dbs.MessageUIDs(ctx, "folder-1")
	if err != nil {
		t.Fatalf("MessageUIDs: %v", err)
	}
	slices.Sort(uids)
	if !slices.Equal(uids, []uint32{10, 30}) {
		t.Errorf("uids = %v, want [10 30] (disabled and other-folder rows excluded)", uids)
	}
}

func TestGetMessageByUID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedFolder(t, dbs, "acct-1", "folder-2")

	if err := dbs.UpsertMessage(ctx, Message{ID: "m1", AccountID: "acct-1", FolderID: "folder-1", UID: 10, ContentKey: "ck", Subject: "Hi"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := dbs.GetMessageByUID(ctx, "folder-1", 10)
	if err != nil {
		t.Fatalf("GetMessageByUID: %v", err)
	}
	if got.Subject != "Hi" {
		t.Errorf("Subject = %q, want Hi", got.Subject)
	}
	if _, err := dbs.GetMessageByUID(ctx, "folder-2", 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("other folder err = %v, want ErrNotFound", err)
	}
	if _, err := dbs.GetMessageByUID(ctx, "folder-1", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing uid err = %v, want ErrNotFound", err)
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
