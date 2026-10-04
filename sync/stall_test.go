package sync_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// A server that accepts the connection and then stops answering is a stall, not
// a slow sync: with nobody cancelling, the pass must still end, say so in
// sync_state, and leave the retry to the worker's backoff (STANDARDS 4a rule 2).
func TestFetchGivesUpOnAServerThatStopsAnswering(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	w.Fault(mailworld.Latency{Delay: 5 * time.Second})

	f := ivysync.NewFetcher(dbs, ivysync.WithStallTimeout(300*time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	_, err := f.Fetch(ctx, acct)
	if err == nil {
		t.Fatal("Fetch succeeded against a server that answers nothing")
	}
	if ctx.Err() != nil {
		t.Fatalf("Fetch only ended because the test's own deadline fired: %v", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("Fetch took %v to give up on a stalled server, want about the 300ms stall timeout", took)
	}
	st, gerr := dbs.GetSyncState(context.Background(), "acct-1")
	if gerr != nil {
		t.Fatalf("GetSyncState: %v", gerr)
	}
	if st.Status != store.SyncUnreachable {
		t.Errorf("sync_state status = %q, want %q for a host that stopped answering", st.Status, store.SyncUnreachable)
	}
}

// The worker must survive the same stall on its IDLE connection: no cancel, the
// idle connection's login never answers, and it still comes back to reconcile
// (which, with the server still stalled, fails and backs off rather than hangs).
func TestWorkerRecoversFromAStalledIdleConnection(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")

	failed := make(chan struct{}, 1)
	armed := false
	worker := ivysync.NewWorker(ivysync.NewFetcher(dbs, ivysync.WithStallTimeout(300*time.Millisecond)), acct,
		ivysync.WithIdleTimeout(time.Minute),
		ivysync.WithBackoff(10*time.Millisecond, 20*time.Millisecond),
		ivysync.WithWorkerSyncFunc(func(_ ivysync.Result, err error) {
			switch {
			case err == nil && !armed:
				armed = true
				// The reconcile is done; the next connection is the IDLE one.
				w.Fault(mailworld.Latency{Delay: 5 * time.Second})
			case err != nil:
				select {
				case failed <- struct{}{}:
				default:
				}
			}
		}))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	select {
	case <-failed:
	case <-time.After(10 * time.Second):
		t.Error("the worker never got past its stalled idle connection")
	}
	stopWorker(t, cancel, done)
}

// The guard is silence, not duration: a server that answers every command a
// little slowly, across many commands, takes far longer than the stall timeout
// in total and must still sync.
func TestStallGuardLeavesASlowButAnsweringServerAlone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	for i := range 6 {
		acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("m").Date(base.Add(time.Duration(i)*time.Hour)).Build())
	}
	// Every command waits 60ms; the stall timeout is 400ms; one message per batch
	// makes the pass many commands long, well past 400ms in total.
	w.Fault(mailworld.Latency{Delay: 60 * time.Millisecond})
	f := ivysync.NewFetcher(dbs, ivysync.WithStallTimeout(400*time.Millisecond), ivysync.WithBatchSize(1))

	start := time.Now()
	res, err := f.Fetch(context.Background(), acct)
	if err != nil {
		t.Fatalf("Fetch against a slow server: %v", err)
	}
	if res.Stored != 6 {
		t.Errorf("stored %d messages, want 6", res.Stored)
	}
	if took := time.Since(start); took < 400*time.Millisecond {
		t.Logf("the pass took only %v; it should outlast the stall timeout for this test to mean anything", took)
	}
}

// An IDLE connection is silent by design: staying quiet for longer than the
// stall timeout must not drop it, or the worker would miss the notification
// that follows.
func TestStallGuardLeavesAQuietIdleConnectionAlone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")

	idling := make(chan struct{}, 4)
	var syncs atomic.Int32
	worker := ivysync.NewWorker(ivysync.NewFetcher(dbs, ivysync.WithStallTimeout(150*time.Millisecond)), acct,
		ivysync.WithIdleTimeout(time.Minute),
		ivysync.WithWorkerSyncFunc(func(ivysync.Result, error) { syncs.Add(1) }),
		ivysync.WithWorkerIdleFunc(func() {
			select {
			case idling <- struct{}{}:
			default:
			}
		}))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer stopWorker(t, cancel, done)

	select {
	case <-idling:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never started idling")
	}
	time.Sleep(600 * time.Millisecond) // four stall timeouts of silence
	before := syncs.Load()
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("hi").Build())
	waitForCondition(t, 5*time.Second, "the idle worker to mirror the message after a long silence", func() bool {
		return messageCount(t, dbs) == 1
	})
	waitForCondition(t, 5*time.Second, "the reconcile to be reported", func() bool { return syncs.Load() > before })
	time.Sleep(300 * time.Millisecond)
	if got := syncs.Load(); got != before+1 {
		t.Errorf("the worker reconciled %d times for one notification, want 1 (a dropped idle connection would add a retry)", got-before)
	}
}
