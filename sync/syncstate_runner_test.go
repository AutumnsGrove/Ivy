package sync_test

import (
	"context"
	"testing"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// The runner is the only writer of an account's sync_state, so Mirror health can
// tell "never synced" and every failure state apart (ARCHITECTURE.md 9b).
func TestFetchRecordsSyncOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("hi").Build())

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := dbs.GetSyncState(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if got.Status != store.SyncOK {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if got.LastOKAt.IsZero() {
		t.Error("LastOKAt is zero after a successful sync")
	}
	if got.LastErrorCode != "" || got.LastErrorDetail != "" {
		t.Errorf("a successful sync kept an error: %+v", got)
	}
}

func TestFetchRecordsAuthFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	w.Fault(mailworld.AuthFail{})

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err == nil {
		t.Fatal("Fetch succeeded despite AuthFail")
	}
	got, err := dbs.GetSyncState(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if got.Status != store.SyncAuthFailed {
		t.Errorf("status = %q, want auth_failed", got.Status)
	}
	if got.LastErrorCode == "" || got.LastErrorDetail == "" {
		t.Errorf("auth failure lost its error: %+v", got)
	}
	if got.LastOKAt.IsZero() == false {
		t.Error("a first-sync auth failure invented a LastOKAt")
	}
}

func TestFetchRecordsFetchFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("hi").Build())
	w.Fault(mailworld.FailFetch{})

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err == nil {
		t.Fatal("Fetch succeeded despite FailFetch")
	}
	got, err := dbs.GetSyncState(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if got.Status != store.SyncError {
		t.Errorf("status = %q, want error", got.Status)
	}
}

// A failure must not erase when the account last worked, and recovery must clear
// the error text, so the banner disappears the moment the provider is back.
func TestFetchAuthFailureKeepsLastOKThenRecovers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("hi").Build())
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, acct); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	first, err := dbs.GetSyncState(ctx, "acct-1")
	if err != nil {
		t.Fatal(err)
	}

	w.Fault(mailworld.AuthFail{})
	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, acct); err == nil {
		t.Fatal("Fetch succeeded despite AuthFail")
	}
	failed, err := dbs.GetSyncState(ctx, "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if !failed.LastOKAt.Equal(first.LastOKAt) {
		t.Errorf("LastOKAt moved on failure: %v -> %v", first.LastOKAt, failed.LastOKAt)
	}

	w.ClearFaults()
	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, acct); err != nil {
		t.Fatalf("recovery Fetch: %v", err)
	}
	recovered, err := dbs.GetSyncState(ctx, "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != store.SyncOK || recovered.LastErrorCode != "" || recovered.LastErrorDetail != "" {
		t.Errorf("recovery kept the failure: %+v", recovered)
	}
}

// A dial to a refused port is the "host is down" state, not a generic error.
func TestFetchRecordsUnreachable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := newStore(t)
	// Port 1 on loopback has no listener, so dial fails with ECONNREFUSED.
	acct := ivysync.Account{
		ID: "acct-1", Address: "me@grove.test",
		IMAPHost: "127.0.0.1", IMAPPort: 1,
		Username: "me@grove.test", Password: "secret", Insecure: true,
	}
	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, acct); err == nil {
		t.Fatal("Fetch succeeded against a closed port")
	}
	got, err := dbs.GetSyncState(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if got.Status != store.SyncUnreachable {
		t.Errorf("status = %q, want unreachable", got.Status)
	}
}
