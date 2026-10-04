package sync_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// The worker idles on INBOX and refreshes when mail arrives. The short idle
// timeout is the design's fallback: the fake server can register its IDLE
// listener just after it acknowledges the command, so a notification sent in
// that window is not guaranteed to arrive, and the periodic poll covers it.
func TestWorkerSyncsOnIdleNotification(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")

	idling := make(chan struct{}, 4)
	worker := ivysync.NewWorker(ivysync.NewFetcher(dbs), acct,
		ivysync.WithIdleTimeout(200*time.Millisecond),
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

	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("hi").Build())
	waitForCondition(t, 5*time.Second, "the idle worker to mirror the new message", func() bool {
		return messageCount(t, dbs) == 1
	})
}

// A provider outage must not stop the worker or spin it: it backs off, records
// the failure, and converges once the provider is back.
func TestWorkerRetriesAfterAnOutage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	w.Fault(mailworld.Unreachable{})

	synced := make(chan struct{}, 8)
	worker := ivysync.NewWorker(ivysync.NewFetcher(dbs), acct,
		ivysync.WithIdleTimeout(50*time.Millisecond),
		ivysync.WithBackoff(5*time.Millisecond, 25*time.Millisecond),
		ivysync.WithJitter(func(d time.Duration) time.Duration { return d }),
		ivysync.WithWorkerSyncFunc(func(_ ivysync.Result, err error) {
			if err == nil {
				select {
				case synced <- struct{}{}:
				default:
				}
			}
		}))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer stopWorker(t, cancel, done)

	waitForCondition(t, 5*time.Second, "the outage to be recorded", func() bool {
		st, err := dbs.GetSyncState(context.Background(), "acct-1")
		return err == nil && st.Status != store.SyncOK
	})

	w.ClearFaults()
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("after the outage").Build())
	select {
	case <-synced:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never recovered after the outage")
	}
	waitForCondition(t, 5*time.Second, "the message delivered after recovery", func() bool {
		return messageCount(t, dbs) == 1
	})
}

func TestWorkerStopsPromptlyWhenCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")

	idling := make(chan struct{}, 1)
	worker := ivysync.NewWorker(ivysync.NewFetcher(dbs), acct,
		ivysync.WithIdleTimeout(time.Minute),
		ivysync.WithWorkerIdleFunc(func() {
			select {
			case idling <- struct{}{}:
			default:
			}
		}))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	select {
	case <-idling:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("the worker never started idling")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("the worker did not stop on cancellation")
	}
}

func messageCount(t *testing.T, dbs *store.DBs) int {
	t.Helper()
	f, err := dbs.GetFolderByName(context.Background(), "acct-1", "INBOX")
	if err != nil {
		return 0
	}
	uids, err := dbs.MessageUIDs(context.Background(), f.ID)
	if err != nil {
		return 0
	}
	return len(uids)
}

func stopWorker(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("the worker did not stop")
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(timeout)
	for {
		if cond() {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// A server that answers the IDLE connection's login slowly must not hold the
// worker past cancellation: the client's commands take no context, so only
// closing the connection unblocks them.
func TestWorkerStopsPromptlyWhenTheIdleConnectionStalls(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")

	armed := make(chan struct{}, 1)
	worker := ivysync.NewWorker(ivysync.NewFetcher(dbs), acct,
		ivysync.WithIdleTimeout(time.Minute),
		ivysync.WithWorkerSyncFunc(func(_ ivysync.Result, err error) {
			if err == nil {
				// The reconcile is done; the next connection is the IDLE one.
				w.Fault(mailworld.Latency{Delay: 5 * time.Second})
				select {
				case armed <- struct{}{}:
				default:
				}
			}
		}))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	select {
	case <-armed:
	case <-time.After(5 * time.Second):
		t.Fatal("the first reconcile never finished")
	}
	time.Sleep(200 * time.Millisecond) // let the worker reach the stalled login
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("the worker did not stop while its idle connection was stalled")
	}
}
