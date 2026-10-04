package sync_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// After the first full sync, the folder holds a modseq, so the next sync on a
// QRESYNC server is a delta: the server sends only changed flags, new UIDs and
// VANISHED, and the mirror must still converge (including a move relabelled
// from the provisional server_removed to moved).
func TestQResyncDeltaConvergesAfterChanges(t *testing.T) {
	t.Parallel()
	script := []op{
		{kind: opAppend, n: 0},
		{kind: opAppend, n: 1},
		{kind: opAppend, n: 2},
		{kind: opSync}, // full backfill, stores the modseq
		{kind: opFlag, a: 0, b: 0},
		{kind: opExpunge, a: 1},
		{kind: opAppend, n: 3},
		{kind: opCreateFolder},
		{kind: opMove, a: 2},
		{kind: opSync}, // delta: flag, VANISHED, new UID, move
	}
	cfg := defaultConfig(true)
	out := cfg.replay(script)
	if out.fail != nil {
		t.Fatalf("%s: %s\n%s", out.fail.kind, out.fail.detail, strings.Join(out.trace, "\n"))
	}
}

// A delta sync that stops after some but not all of its new messages must keep
// the folder's old modseq, or the next delta would skip the messages it never
// fetched. This is why the runner advances a folder's modseq only after every
// body in it is stored.
func TestDeltaResumeKeepsUnfetchedMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("one").Date(base).Build())
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	f := ivysync.NewFetcher(dbs, ivysync.WithBatchSize(1))

	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	// Two messages arrive; the next sync is a QRESYNC delta with two new bodies.
	acc.Deliver("INBOX", mailworld.Msg().From("b@example.com").Subject("two").Date(base.Add(time.Hour)).Build())
	acc.Deliver("INBOX", mailworld.Msg().From("c@example.com").Subject("three").Date(base.Add(2*time.Hour)).Build())

	// One message per batch, so the drop can land after the newest new message
	// has landed but before the older one: a resume that lost the old modseq
	// would never fetch the older one.
	w.Fault(mailworld.DropConnection{After: 10})
	if _, err := f.Fetch(ctx, acct); err == nil {
		t.Fatal("the delta sync survived the armed drop")
	}
	w.ClearFaults()
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("resume Fetch: %v", err)
	}

	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	uids, err := dbs.MessageUIDs(ctx, inbox.ID)
	if err != nil {
		t.Fatalf("MessageUIDs: %v", err)
	}
	if len(uids) != 3 {
		t.Errorf("after resume the folder holds %v, want all 3 messages", uids)
	}
}
