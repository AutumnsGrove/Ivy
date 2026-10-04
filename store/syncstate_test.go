package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSyncStateRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")

	if _, err := dbs.GetSyncState(ctx, "acct-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before any write: err = %v, want ErrNotFound (never synced is not the same as healthy)", err)
	}

	ok := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	want := SyncState{
		AccountID: "acct-1", Status: SyncSyncing, LastOKAt: ok,
		BackfillDone: 120, BackfillTotal: 5000, UpdatedAt: ok.Add(time.Minute),
	}
	if err := dbs.SetSyncState(ctx, want); err != nil {
		t.Fatalf("SetSyncState: %v", err)
	}
	got, err := dbs.GetSyncState(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A failure must not erase when the account last worked: that instant is what
// Mirror health shows while the account is broken.
func TestSyncStateErrorKeepsLastOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")

	ok := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	if err := dbs.SetSyncState(ctx, SyncState{AccountID: "acct-1", Status: SyncOK, LastOKAt: ok, UpdatedAt: ok}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	failed := SyncState{
		AccountID: "acct-1", Status: SyncAuthFailed, UpdatedAt: ok.Add(time.Hour),
		LastErrorCode: "auth_failed", LastErrorDetail: "login rejected",
	}
	if err := dbs.SetSyncState(ctx, failed); err != nil {
		t.Fatalf("failure write: %v", err)
	}
	got, err := dbs.GetSyncState(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if !got.LastOKAt.Equal(ok) {
		t.Errorf("LastOKAt = %v, want %v kept across the failure", got.LastOKAt, ok)
	}
	if got.Status != SyncAuthFailed || got.LastErrorCode != "auth_failed" || got.LastErrorDetail != "login rejected" {
		t.Errorf("failure not recorded: %+v", got)
	}

	// Recovery clears the error text, which would otherwise outlive the outage.
	if err := dbs.SetSyncState(ctx, SyncState{AccountID: "acct-1", Status: SyncOK, LastOKAt: ok.Add(2 * time.Hour), UpdatedAt: ok.Add(2 * time.Hour)}); err != nil {
		t.Fatalf("recovery write: %v", err)
	}
	got, err = dbs.GetSyncState(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetSyncState after recovery: %v", err)
	}
	if got.LastErrorCode != "" || got.LastErrorDetail != "" {
		t.Errorf("error survived recovery: %+v", got)
	}
}

func TestSyncStateRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")

	for name, s := range map[string]SyncState{
		"unknown status":    {AccountID: "acct-1", Status: "borked"},
		"empty status":      {AccountID: "acct-1"},
		"negative done":     {AccountID: "acct-1", Status: SyncOK, BackfillDone: -1},
		"done above total":  {AccountID: "acct-1", Status: SyncSyncing, BackfillDone: 11, BackfillTotal: 10},
		"huge error detail": {AccountID: "acct-1", Status: SyncError, LastErrorDetail: string(make([]byte, MaxSyncErrorDetail+1))},
	} {
		if err := dbs.SetSyncState(ctx, s); err == nil {
			t.Errorf("%s: SetSyncState accepted %+v", name, s)
		}
	}
	if _, err := dbs.GetSyncState(ctx, "acct-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a rejected write left a row behind: err = %v", err)
	}
}

func TestSyncStateNeedsTheAccount(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	if err := dbs.SetSyncState(context.Background(), SyncState{AccountID: "ghost", Status: SyncOK}); err == nil {
		t.Error("SetSyncState accepted an account that does not exist")
	}
}
