package store

import (
	"context"
	"testing"
	"time"
)

var overrideNow = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

// An override is the operator's "let helpers read this one": it is stored per
// account and content key, and reading it back says so.
func TestVettingOverrideRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if on, err := dbs.VettingOverridden(ctx, "acct-1", "key-1"); err != nil || on {
		t.Fatalf("before any override = %v, %v; want false, nil", on, err)
	}
	if err := dbs.SetVettingOverride(ctx, "acct-1", "key-1", overrideNow); err != nil {
		t.Fatalf("set: %v", err)
	}
	if on, err := dbs.VettingOverridden(ctx, "acct-1", "key-1"); err != nil || !on {
		t.Fatalf("after set = %v, %v; want true, nil", on, err)
	}

	// Setting it twice is the same fact, not an error: a double tap must not fail.
	if err := dbs.SetVettingOverride(ctx, "acct-1", "key-1", overrideNow.Add(time.Hour)); err != nil {
		t.Fatalf("second set: %v", err)
	}
}

// Another account's mail with the same content key is a different message to
// vet, so one account's override never lets helpers read the other's copy.
func TestVettingOverrideIsPerAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if err := dbs.SetVettingOverride(ctx, "acct-1", "key-1", overrideNow); err != nil {
		t.Fatalf("set: %v", err)
	}
	if on, _ := dbs.VettingOverridden(ctx, "acct-2", "key-1"); on {
		t.Error("acct-2 is overridden by acct-1's override")
	}
	if on, _ := dbs.VettingOverridden(ctx, "acct-1", "key-2"); on {
		t.Error("key-2 is overridden by key-1's override")
	}
}

// Clearing puts the message back under the checks, and clearing one that was
// never set is not an error.
func TestVettingOverrideClears(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if err := dbs.ClearVettingOverride(ctx, "acct-1", "key-1"); err != nil {
		t.Fatalf("clear of an unset override: %v", err)
	}
	if err := dbs.SetVettingOverride(ctx, "acct-1", "key-1", overrideNow); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := dbs.ClearVettingOverride(ctx, "acct-1", "key-1"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if on, _ := dbs.VettingOverridden(ctx, "acct-1", "key-1"); on {
		t.Error("override still set after clear")
	}
}

// An override needs both halves of the key; an empty one would match nothing and
// hide a caller bug.
func TestVettingOverrideRefusesAnEmptyKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if err := dbs.SetVettingOverride(ctx, "", "key-1", overrideNow); err == nil {
		t.Error("an empty account was accepted")
	}
	if err := dbs.SetVettingOverride(ctx, "acct-1", "", overrideNow); err == nil {
		t.Error("an empty content key was accepted")
	}
}
