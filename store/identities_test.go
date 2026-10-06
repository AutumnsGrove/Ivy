package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// identityNow fixes every timestamp so ordering and preservation are asserted
// without sleeping.
var identityNow = time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)

// identitiesForTest reads every stored identity row for an account, including
// the raw column values the public API would hide.
func (d *DBs) identitiesForTest(ctx context.Context, accountID string) ([]Identity, error) {
	rows, err := d.State.Read.QueryContext(ctx,
		`SELECT id, account_id, address, display_name, signature, created_at, updated_at
		 FROM identities WHERE account_id=? ORDER BY address`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Identity
	for rows.Next() {
		var (
			id, account, address, displayName, signature string
			createdAt, updatedAt                         string
		)
		if err := rows.Scan(&id, &account, &address, &displayName, &signature, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		created, err := parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		updated, err := parseTime(updatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, Identity{
			ID: id, AccountID: account, Address: address, DisplayName: displayName,
			Signature: signature, CreatedAt: created, UpdatedAt: updated,
		})
	}
	return out, rows.Err()
}

func identityIn(id, address string) IdentityInput {
	return IdentityInput{
		ID: id, AccountID: "acct-1", Address: address,
		DisplayName: "Autumn", Signature: "Autumn · Grove", Now: identityNow,
	}
}

// An identity is created once and read back by its address, case-insensitively,
// with its signature intact.
func TestUpsertIdentityCreatesAndReadsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	got, err := dbs.UpsertIdentity(ctx, identityIn("id-1", "hello@example.test"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.ID != "id-1" || got.Address != "hello@example.test" || got.DisplayName != "Autumn" {
		t.Fatalf("identity = %+v, want the stored row", got)
	}
	if got.Signature != "Autumn · Grove" {
		t.Errorf("signature = %q, want it preserved verbatim", got.Signature)
	}
	if !got.CreatedAt.Equal(identityNow) || !got.UpdatedAt.Equal(identityNow) {
		t.Errorf("times = %v/%v, want the injected clock", got.CreatedAt, got.UpdatedAt)
	}

	// The reply/send path looks a From up by address; the match is
	// case-insensitive because a mail domain is.
	byAddr, err := dbs.GetIdentity(ctx, "acct-1", "HELLO@example.test")
	if err != nil {
		t.Fatalf("get by address: %v", err)
	}
	if byAddr.ID != got.ID {
		t.Errorf("case-insensitive get = %q, want %q", byAddr.ID, got.ID)
	}
}

// A second upsert of the same address edits the row in place: it keeps the row
// id and created_at and only moves updated_at. Editing the primary identity is
// the same call as adding an alias.
func TestUpsertIdentityEditsInPlace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	first, err := dbs.UpsertIdentity(ctx, identityIn("id-1", "hello@example.test"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	later := identityNow.Add(time.Hour)
	edited, err := dbs.UpsertIdentity(ctx, IdentityInput{
		ID: "id-2", AccountID: "acct-1", Address: "Hello@example.test",
		DisplayName: "Autumn Grove", Signature: "— Autumn", Now: later,
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if edited.ID != first.ID {
		t.Errorf("edit id = %q, want the original %q", edited.ID, first.ID)
	}
	if !edited.CreatedAt.Equal(first.CreatedAt) || !edited.UpdatedAt.Equal(later) {
		t.Errorf("edit times = %v/%v, want created preserved and updated bumped", edited.CreatedAt, edited.UpdatedAt)
	}
	if edited.DisplayName != "Autumn Grove" || edited.Signature != "— Autumn" {
		t.Errorf("edit = %+v, want the new name and signature", edited)
	}
	rows, err := dbs.identitiesForTest(ctx, "acct-1")
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want the case-insensitive match to edit one row", len(rows))
	}
}

// A hostile addr-spec is refused, never stored: a CR, LF or NUL could forge a
// header, and Purelymail has no SMTPUTF8 so a non-ASCII address cannot send.
func TestUpsertIdentityRefusesBadAddresses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	bad := []string{
		"",
		"has space@example.test",
		"line\nbreak@example.test",
		"carriage\rreturn@example.test",
		"nul\x00@example.test",
		"Autumn <hello@example.test>",
		"héllo@example.test",
		"no-at-sign",
		"<hello@example.test>",
		strings.Repeat("a", 400) + "@example.test",
	}
	for _, addr := range bad {
		_, err := dbs.UpsertIdentity(ctx, identityIn("id-x", addr))
		if !errors.Is(err, ErrIdentityAddress) {
			t.Errorf("upsert(%q) = %v, want ErrIdentityAddress", addr, err)
		}
	}
}

// The account has a bounded number of identities, and a refused over-limit
// insert leaves the table untouched. An edit at the limit still works.
func TestUpsertIdentityEnforcesLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	for i := range MaxIdentitiesPerAccount {
		if _, err := dbs.UpsertIdentity(ctx, identityIn("id-"+strconv.Itoa(i), "a"+strconv.Itoa(i)+"@example.test")); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := dbs.UpsertIdentity(ctx, identityIn("overflow", "one-too-many@example.test")); !errors.Is(err, ErrIdentityLimit) {
		t.Fatalf("over-limit = %v, want ErrIdentityLimit", err)
	}
	if _, err := dbs.UpsertIdentity(ctx, identityIn("edit", "a0@example.test")); err != nil {
		t.Fatalf("edit at the limit: %v", err)
	}
	rows, err := dbs.identitiesForTest(ctx, "acct-1")
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(rows) != MaxIdentitiesPerAccount {
		t.Fatalf("rows = %d, want %d", len(rows), MaxIdentitiesPerAccount)
	}
}

// Identities belong to one account: a second account never sees the first's.
func TestListIdentitiesIsScopedToTheAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	in := identityIn("id-1", "hello@example.test")
	if _, err := dbs.UpsertIdentity(ctx, in); err != nil {
		t.Fatalf("acct-1: %v", err)
	}
	other := in
	other.ID, other.AccountID, other.Address = "id-2", "acct-2", "hi@example.test"
	if _, err := dbs.UpsertIdentity(ctx, other); err != nil {
		t.Fatalf("acct-2: %v", err)
	}

	got, err := dbs.ListIdentities(ctx, "acct-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Address != "hello@example.test" {
		t.Fatalf("list = %+v, want only the account's own identity", got)
	}
}

// Deleting an identity is idempotent to the caller as a not-found on the second
// call, and a missing row is ErrNotFound rather than a silent success.
func TestDeleteIdentityRemovesAndReportsMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	got, err := dbs.UpsertIdentity(ctx, identityIn("id-1", "hello@example.test"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := dbs.DeleteIdentity(ctx, got.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := dbs.GetIdentity(ctx, "acct-1", "hello@example.test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
	if err := dbs.DeleteIdentity(ctx, got.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

// Identities are locally owned: they live in state.db, which is the only
// database backed up, so they must survive a process restart.
func TestIdentitiesSurviveAReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	first, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := first.UpsertIdentity(ctx, identityIn("id-1", "hello@example.test")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = second.Close() }()
	if _, err := second.GetIdentity(ctx, "acct-1", "hello@example.test"); err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
}
