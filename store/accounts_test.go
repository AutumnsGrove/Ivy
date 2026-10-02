package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAccountRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	created := time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)
	in := Account{
		ID:            "acct-1",
		Address:       "me@example.test",
		DisplayName:   "Me",
		Icon:          "leaf",
		Color:         "fern",
		SortOrder:     2,
		LLMEnabled:    true,
		VisionEnabled: false,
		Photo:         []byte{0x89, 0x50, 0x4e, 0x47},
		IMAPHost:      "imap.example.test",
		IMAPPort:      993,
		SMTPHost:      "smtp.example.test",
		SMTPPort:      465,
		Username:      "me@example.test",
		CreatedAt:     created,
	}
	if err := dbs.UpsertAccount(ctx, in); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}

	got, err := dbs.GetAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.Address != in.Address || got.DisplayName != in.DisplayName || got.Icon != in.Icon ||
		got.Color != in.Color || got.SortOrder != in.SortOrder || got.LLMEnabled != in.LLMEnabled ||
		got.IMAPPort != in.IMAPPort || got.Username != in.Username {
		t.Errorf("GetAccount = %+v, want %+v", got, in)
	}
	if string(got.Photo) != string(in.Photo) {
		t.Errorf("Photo = %v, want %v", got.Photo, in.Photo)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %s, want %s", got.CreatedAt, created)
	}
}

func TestUpsertAccountPreservesCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	created := time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)
	acct := Account{ID: "acct-1", Address: "me@example.test", CreatedAt: created}
	if err := dbs.UpsertAccount(ctx, acct); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	acct.DisplayName = "Renamed"
	acct.CreatedAt = created.Add(48 * time.Hour)
	if err := dbs.UpsertAccount(ctx, acct); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	accounts, err := dbs.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	if accounts[0].DisplayName != "Renamed" {
		t.Errorf("DisplayName = %q, want Renamed", accounts[0].DisplayName)
	}
	if !accounts[0].CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %s, want the original %s", accounts[0].CreatedAt, created)
	}
}

func TestListAccountsOrdersBySortOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	for _, a := range []Account{
		{ID: "b", Address: "b@example.test", SortOrder: 2},
		{ID: "a", Address: "a@example.test", SortOrder: 1},
	} {
		if err := dbs.UpsertAccount(ctx, a); err != nil {
			t.Fatalf("UpsertAccount(%s): %v", a.ID, err)
		}
	}

	accounts, err := dbs.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 2 || accounts[0].ID != "a" || accounts[1].ID != "b" {
		t.Errorf("order = %v, want a then b", ids(accounts))
	}
}

func TestGetAccountMissing(t *testing.T) {
	t.Parallel()
	_, err := openTemp(t).GetAccount(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func openTemp(t *testing.T) *DBs {
	t.Helper()
	dbs, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	return dbs
}

func ids(accounts []Account) []string {
	out := make([]string, len(accounts))
	for i, a := range accounts {
		out[i] = a.ID
	}
	return out
}
