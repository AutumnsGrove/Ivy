package sync_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// A pass that dies part-way must not decide a message was removed when it was
// only moved. The source folder's row is disabled before the destination's new
// copy has been read, so if the connection drops in between, the next pass sees
// the row already disabled and (with the first reason winning) never revisits it.
// The connection is dropped after every possible command count, in both folder
// orders, so wherever the pass dies the settled reason must be "moved".
func TestAnInterruptedPassStillCallsAMoveAMove(t *testing.T) {
	t.Parallel()
	for _, dir := range []struct{ from, to string }{{"Archive", "INBOX"}, {"INBOX", "Archive"}} {
		for drop := 1; drop <= 40; drop++ {
			t.Run(fmt.Sprintf("%s-to-%s/drop-after-%d", dir.from, dir.to, drop), func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				w := newWorld(t)
				acc := w.Account("me@grove.test", "secret")
				dbs := newStore(t)
				acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
				if err := acc.CreateMailbox("Archive"); err != nil {
					t.Fatalf("create Archive: %v", err)
				}
				base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
				uid := acc.Deliver(dir.from, mailworld.Msg().From("a@example.com").Subject("moved-one").Date(base).Build())
				acc.Deliver(dir.to, mailworld.Msg().From("b@example.com").Subject("bystander").Date(base.Add(time.Hour)).Build())
				f := ivysync.NewFetcher(dbs)
				if _, err := f.Fetch(ctx, acct); err != nil {
					t.Fatalf("first Fetch: %v", err)
				}
				if err := acc.Move(dir.from, uid, dir.to); err != nil {
					t.Fatalf("move: %v", err)
				}

				w.Fault(mailworld.DropConnection{After: drop})
				_, _ = f.Fetch(ctx, acct) // may die anywhere, or finish if drop is past its last command
				w.ClearFaults()
				if _, err := f.Fetch(ctx, acct); err != nil {
					t.Fatalf("recovery Fetch: %v", err)
				}

				var reason string
				err := dbs.Mirror.Read.QueryRowContext(ctx,
					`SELECT COALESCE(disabled_reason, '') FROM messages WHERE subject = 'moved-one' AND disabled_at IS NOT NULL`).Scan(&reason)
				if err != nil {
					t.Fatalf("read the disabled row: %v", err)
				}
				if reason != "moved" {
					t.Errorf("after a pass dropped after %d commands the moved message is disabled as %q, want \"moved\"", drop, reason)
				}
			})
		}
	}
}

// A message the server deleted stays "server_removed" even if a message with the
// same Message-ID arrives later: only a move is a move, and settling must not
// rewrite the history of a row that was already settled.
func TestAReceivedAgainMessageDoesNotMakeAnOldRemovalAMove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	f := ivysync.NewFetcher(dbs)
	raw := func() []byte {
		return mailworld.Msg().From("a@example.com").Subject("again").MessageID("<again@sync.test>").Build()
	}

	uid := acc.Deliver("INBOX", raw())
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if err := acc.Expunge("INBOX", uid); err != nil {
		t.Fatalf("expunge: %v", err)
	}
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("Fetch after the delete: %v", err)
	}
	acc.Deliver("INBOX", raw()) // the same Message-ID, a new message
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("Fetch after the re-delivery: %v", err)
	}

	var reason string
	if err := dbs.Mirror.Read.QueryRowContext(ctx,
		`SELECT COALESCE(disabled_reason, '') FROM messages WHERE subject = 'again' AND disabled_at IS NOT NULL`).Scan(&reason); err != nil {
		t.Fatalf("read the disabled row: %v", err)
	}
	if reason != "server_removed" {
		t.Errorf("the deleted message is disabled as %q after a re-delivery, want \"server_removed\"", reason)
	}
}
