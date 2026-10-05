package sync_test

import (
	"context"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// A connection that dies mid-command must not be reused by the next attempt:
// the retry has to dial afresh, or a healthy server still fails every attempt
// until the op exhausts its retries.
func TestOutboxRetriesOnAFreshConnectionAfterADrop(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if err := acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	acc.Deliver("INBOX", rawFor(1))
	f := ivysync.NewFetcher(dbs)
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("sync: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	archive := mustFolder(t, dbs, "acct-1", "Archive")
	op, _, err := dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: "op-1", AccountID: "acct-1", Kind: store.OutboxMove,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect:    store.OutboxExpect{DestFolderID: archive.ID},
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Only the worker's first connection is dropped, mid-command.
	w.Fault(mailworld.DropConnection{After: 4})
	worker := ivysync.NewOutboxWorker(f, acct,
		ivysync.WithOutboxPoll(10*time.Millisecond),
		ivysync.WithOutboxJitter(func(time.Duration) time.Duration { return 20 * time.Millisecond }))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	deadline := time.Now().Add(15 * time.Second)
	var got store.OutboxOp
	for time.Now().Before(deadline) {
		got, err = dbs.GetOutbox(ctx, op.ID)
		if err != nil {
			t.Fatalf("get op: %v", err)
		}
		if got.State == store.OutboxDone || got.State == store.OutboxFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if got.State != store.OutboxDone {
		t.Fatalf("op state = %q after %d attempts (%s: %s), want done on a fresh connection",
			got.State, got.Attempts, got.LastErrorCode, got.LastErrorDetail)
	}
}

// Shutdown must not wait out a server that has gone quiet: cancelling the
// context closes the worker's connection, as sync and the IDLE worker do.
func TestOutboxRunStopsPromptlyWhenCancelledMidCommand(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if err := acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	acc.Deliver("INBOX", rawFor(1))
	f := ivysync.NewFetcher(dbs)
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("sync: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	archive := mustFolder(t, dbs, "acct-1", "Archive")
	if _, _, err := dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: "op-1", AccountID: "acct-1", Kind: store.OutboxMove,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect:    store.OutboxExpect{DestFolderID: archive.ID},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w.Fault(mailworld.Latency{Delay: time.Minute})
	worker := ivysync.NewOutboxWorker(f, acct, ivysync.WithOutboxPoll(10*time.Millisecond))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	time.Sleep(500 * time.Millisecond) // the worker is now waiting on a silent server
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run was still blocked 5s after cancellation; it waits out the stall timeout")
	}
}
