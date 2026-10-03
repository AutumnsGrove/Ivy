package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		Color:         "fern",
		SortOrder:     2,
		LLMEnabled:    true,
		VisionEnabled: false,
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
	// A fresh account has no profile; DisplayName and Icon are the operator's.
	if got.DisplayName != "" || got.Icon != "" {
		t.Errorf("fresh profile = %q/%q, want empty", got.DisplayName, got.Icon)
	}
	if got.Address != in.Address ||
		got.Color != in.Color || got.SortOrder != in.SortOrder || got.LLMEnabled != in.LLMEnabled ||
		got.IMAPPort != in.IMAPPort || got.Username != in.Username {
		t.Errorf("GetAccount = %+v, want %+v", got, in)
	}
	if got.HasPhoto {
		t.Error("HasPhoto = true, want false before a photo is set")
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

	acct.Address = "moved@example.test"
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
	if accounts[0].Address != "moved@example.test" {
		t.Errorf("Address = %q, want the updated one", accounts[0].Address)
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

func TestSetAccountProfile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	run := Account{ID: "acct-1", Address: "me@example.test", SortOrder: 3, LLMEnabled: true, IMAPHost: "imap.test", CreatedAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}
	if err := dbs.UpsertAccount(ctx, run); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}

	if err := dbs.SetAccountProfile(ctx, "acct-1", "Autumn", "\U0001F33F"); err != nil {
		t.Fatalf("SetAccountProfile: %v", err)
	}
	got, err := dbs.GetAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.DisplayName != "Autumn" || got.Icon != "\U0001F33F" {
		t.Errorf("profile = %q/%q, want Autumn/leaf", got.DisplayName, got.Icon)
	}
	// The connection fields the sync owns are untouched by a rename.
	if got.Address != run.Address || got.SortOrder != run.SortOrder || !got.LLMEnabled || got.IMAPHost != run.IMAPHost {
		t.Errorf("rename disturbed sync-owned fields: %+v", got)
	}

	err = dbs.SetAccountProfile(ctx, "nope", "x", "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account err = %v, want ErrNotFound", err)
	}
}

func TestSetAccountPhoto(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if err := dbs.UpsertAccount(ctx, Account{ID: "acct-1", Address: "me@example.test", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47}
	if err := dbs.SetAccountPhoto(ctx, "acct-1", png); err != nil {
		t.Fatalf("SetAccountPhoto: %v", err)
	}
	got, err := dbs.GetAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if !got.HasPhoto {
		t.Error("HasPhoto = false after a photo was set")
	}
	stored, err := dbs.GetAccountPhoto(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetAccountPhoto: %v", err)
	}
	if string(stored) != string(png) {
		t.Errorf("photo = %v, want %v", stored, png)
	}

	if err := dbs.SetAccountPhoto(ctx, "acct-1", nil); err != nil {
		t.Fatalf("clear photo: %v", err)
	}
	got, _ = dbs.GetAccount(ctx, "acct-1")
	if got.HasPhoto {
		t.Error("HasPhoto = true after clear")
	}
	if _, err := dbs.GetAccountPhoto(ctx, "acct-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAccountPhoto after clear = %v, want ErrNotFound", err)
	}

	if err := dbs.SetAccountPhoto(ctx, "nope", png); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account err = %v, want ErrNotFound", err)
	}
}

func openTemp(t *testing.T) *DBs {
	t.Helper()
	dbs, err := Open(context.Background(), t.TempDir())
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

// The profile is the operator's own state (CLAUDE.md non-negotiable 5): the
// mirror is rebuilt from IMAP and never backed up, so a rename or a photo must
// survive losing mirror.db entirely.
func TestAccountProfileSurvivesAMirrorRebuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	acct := Account{ID: "acct-1", Address: "me@example.test", CreatedAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}
	png := []byte{0x89, 0x50, 0x4e, 0x47}

	dbs, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := dbs.UpsertAccount(ctx, acct); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	if err := dbs.SetAccountProfile(ctx, "acct-1", "Autumn", "leaf"); err != nil {
		t.Fatalf("SetAccountProfile: %v", err)
	}
	if err := dbs.SetAccountPhoto(ctx, "acct-1", png); err != nil {
		t.Fatalf("SetAccountPhoto: %v", err)
	}
	if err := dbs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, name := range []string{"mirror.db", "mirror.db-wal", "mirror.db-shm"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remove %s: %v", name, err)
		}
	}

	dbs, err = Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = dbs.Close() }()
	if err := dbs.UpsertAccount(ctx, acct); err != nil {
		t.Fatalf("UpsertAccount after rebuild: %v", err)
	}
	got, err := dbs.GetAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.DisplayName != "Autumn" || got.Icon != "leaf" || !got.HasPhoto {
		t.Errorf("profile after rebuild = %q/%q photo=%v, want Autumn/leaf/true", got.DisplayName, got.Icon, got.HasPhoto)
	}
	if stored, err := dbs.GetAccountPhoto(ctx, "acct-1"); err != nil || string(stored) != string(png) {
		t.Errorf("photo after rebuild = %v, %v", stored, err)
	}
}

// A sync or config reload upserts the account row; it must not reach the
// operator's profile in either direction.
func TestUpsertAccountNeverTouchesTheProfile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	acct := Account{ID: "acct-1", Address: "me@example.test", CreatedAt: time.Now()}
	if err := dbs.UpsertAccount(ctx, acct); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	if err := dbs.SetAccountProfile(ctx, "acct-1", "Autumn", "leaf"); err != nil {
		t.Fatalf("SetAccountProfile: %v", err)
	}

	acct.DisplayName, acct.Icon = "From config", "x"
	if err := dbs.UpsertAccount(ctx, acct); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, err := dbs.GetAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.DisplayName != "Autumn" || got.Icon != "leaf" {
		t.Errorf("profile after upsert = %q/%q, want Autumn/leaf", got.DisplayName, got.Icon)
	}
}

// Before round 32 the profile lived on the mirror row. Opening such a data
// directory copies it into state.db once and empties the mirror columns, and a
// profile already in state wins over a stale mirror value.
func TestLegacyMirrorProfileMovesToState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	png := []byte{0x89, 0x50, 0x4e, 0x47}

	dbs, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, id := range []string{"acct-1", "acct-2"} {
		if err := dbs.UpsertAccount(ctx, Account{ID: id, Address: id + "@example.test", CreatedAt: time.Now()}); err != nil {
			t.Fatalf("UpsertAccount %s: %v", id, err)
		}
	}
	if err := dbs.SetAccountProfile(ctx, "acct-2", "Chosen", "leaf"); err != nil {
		t.Fatalf("SetAccountProfile: %v", err)
	}
	legacy := `UPDATE accounts SET display_name = ?, icon = ?, photo_blob = ? WHERE id = ?`
	if _, err := dbs.Mirror.Write.ExecContext(ctx, legacy, "Old", "moss", png, "acct-1"); err != nil {
		t.Fatalf("seed legacy acct-1: %v", err)
	}
	if _, err := dbs.Mirror.Write.ExecContext(ctx, legacy, "Stale", "old", nil, "acct-2"); err != nil {
		t.Fatalf("seed legacy acct-2: %v", err)
	}
	if err := dbs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	dbs, err = Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = dbs.Close() }()

	one, err := dbs.GetAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetAccount acct-1: %v", err)
	}
	if one.DisplayName != "Old" || one.Icon != "moss" || !one.HasPhoto {
		t.Errorf("acct-1 = %q/%q photo=%v, want the legacy profile", one.DisplayName, one.Icon, one.HasPhoto)
	}
	two, err := dbs.GetAccount(ctx, "acct-2")
	if err != nil {
		t.Fatalf("GetAccount acct-2: %v", err)
	}
	if two.DisplayName != "Chosen" || two.Icon != "leaf" {
		t.Errorf("acct-2 = %q/%q, want the state profile to win", two.DisplayName, two.Icon)
	}

	var left int
	err = dbs.Mirror.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM accounts WHERE display_name != '' OR icon != '' OR photo_blob IS NOT NULL`).Scan(&left)
	if err != nil || left != 0 {
		t.Errorf("mirror rows still holding a profile = %d (%v), want 0", left, err)
	}
}
