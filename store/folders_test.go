package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFolderRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")

	synced := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	f := Folder{
		ID:            "folder-1",
		AccountID:     "acct-1",
		Name:          "INBOX",
		Role:          RoleInbox,
		UIDValidity:   4242,
		HighestModSeq: 9001,
		LastSyncAt:    synced,
	}
	if err := dbs.UpsertFolder(ctx, f); err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}

	got, err := dbs.GetFolderByName(ctx, "acct-1", "INBOX")
	if err != nil {
		t.Fatalf("GetFolderByName: %v", err)
	}
	if got != f {
		t.Errorf("got %+v, want %+v", got, f)
	}
}

func TestUpsertFolderUpdatesInPlace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")

	base := Folder{ID: "folder-1", AccountID: "acct-1", Name: "INBOX", Role: RoleOther}
	if err := dbs.UpsertFolder(ctx, base); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	base.Role = RoleInbox
	base.UIDValidity = 7
	base.HighestModSeq = 99
	if err := dbs.UpsertFolder(ctx, base); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	folders, err := dbs.ListFolders(ctx, "acct-1")
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("got %d folders, want 1 (upsert must not insert)", len(folders))
	}
	if folders[0].Role != RoleInbox || folders[0].UIDValidity != 7 || folders[0].HighestModSeq != 99 {
		t.Errorf("folder = %+v, want updated role/uidvalidity/modseq", folders[0])
	}
}

func TestListFoldersIsScopedToAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedAccount(t, dbs, "acct-2")

	for _, f := range []Folder{
		{ID: "f1", AccountID: "acct-1", Name: "INBOX"},
		{ID: "f2", AccountID: "acct-1", Name: "Archive"},
		{ID: "f3", AccountID: "acct-2", Name: "INBOX"},
	} {
		if err := dbs.UpsertFolder(ctx, f); err != nil {
			t.Fatalf("UpsertFolder(%s): %v", f.ID, err)
		}
	}

	folders, err := dbs.ListFolders(ctx, "acct-1")
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 2 || folders[0].Name != "Archive" || folders[1].Name != "INBOX" {
		t.Errorf("folders = %+v, want Archive then INBOX for acct-1 only", folders)
	}
}

func TestGetFolderByNameMissing(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	if _, err := dbs.GetFolderByName(context.Background(), "acct-1", "Nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func seedAccount(t *testing.T, dbs *DBs, id string) {
	t.Helper()
	err := dbs.UpsertAccount(context.Background(), Account{
		ID:        id,
		Address:   id + "@example.test",
		CreatedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("seedAccount(%s): %v", id, err)
	}
}
