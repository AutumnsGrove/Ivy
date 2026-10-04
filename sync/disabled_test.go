package sync_test

import (
	"context"
	"testing"

	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// The alert is the whole point of the threshold: a pass that hides a small
// folder's mail must be visible in the Result even though sync keeps going.
func TestMassDisableAlertFiresOnAWholeFolderSweep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	for i := 0; i < 12; i++ {
		acc.Deliver("INBOX", rawFor(i))
	}
	f := ivysync.NewFetcher(dbs)
	res, err := f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret"))
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if len(res.MassDisabled) != 0 {
		t.Fatalf("a first sync hid nothing but reported %+v", res.MassDisabled)
	}

	for uid := uint32(1); uid <= 12; uid++ {
		if err := acc.Expunge("INBOX", uid); err != nil {
			t.Fatalf("expunge %d: %v", uid, err)
		}
	}
	res, err = f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret"))
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if len(res.MassDisabled) != 1 {
		t.Fatalf("MassDisabled = %+v, want one folder", res.MassDisabled)
	}
	got := res.MassDisabled[0]
	if got.Folder != "INBOX" || got.Hidden != 12 || got.Held != 12 {
		t.Errorf("alert = %+v, want INBOX held 12 hidden 12", got)
	}
}

// One message out of twelve is an edit, not a sweep, and must stay quiet.
func TestMassDisableAlertStaysQuietForASingleExpunge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	for i := 0; i < 12; i++ {
		acc.Deliver("INBOX", rawFor(i))
	}
	f := ivysync.NewFetcher(dbs)
	if _, err := f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if err := acc.Expunge("INBOX", 1); err != nil {
		t.Fatalf("expunge: %v", err)
	}
	res, err := f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret"))
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if len(res.MassDisabled) != 0 {
		t.Errorf("one of twelve raised %+v, want none", res.MassDisabled)
	}
}

// A provider rebuild (UIDVALIDITY change) hides every old row at once, which is
// exactly the case the alert exists for.
func TestMassDisableAlertFiresOnAUIDValidityReset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	for i := 0; i < 11; i++ {
		acc.Deliver("INBOX", rawFor(i))
	}
	f := ivysync.NewFetcher(dbs)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if err := acc.BumpUIDValidity("INBOX"); err != nil {
		t.Fatalf("bump uidvalidity: %v", err)
	}
	res, err := f.Fetch(ctx, acct)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if len(res.MassDisabled) != 1 || res.MassDisabled[0].Folder != "INBOX" {
		t.Errorf("MassDisabled = %+v, want INBOX", res.MassDisabled)
	}
}
