package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func mustSubscribe(t *testing.T, h *Hub) *Subscription {
	t.Helper()
	s, err := h.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// next reads one event with a deadline, so a hub that loses a hint fails the
// test instead of hanging it.
func next(t *testing.T, s *Subscription) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e, err := s.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return e
}

// nextErr is next for the cases where the answer is an error. It has a
// deadline too: a hub that forgets to wake a waiter must fail the test, not
// hang the suite.
func nextErr(s *Subscription) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.Next(ctx)
	return err
}

func TestEveryOpenSubscriberGetsEveryHintInOrder(t *testing.T) {
	t.Parallel()
	h := New()
	a, b := mustSubscribe(t, h), mustSubscribe(t, h)

	want := []Event{
		{Type: FolderChanged, AccountID: "acct-1", Folder: "INBOX"},
		{Type: SyncState, AccountID: "acct-1"},
		{Type: HealthAlert, AccountID: "acct-2", Code: "mass_disable"},
	}
	for _, e := range want {
		h.Publish(e)
	}
	for _, s := range []*Subscription{a, b} {
		for i, w := range want {
			if got := next(t, s); got != w {
				t.Errorf("event %d = %+v, want %+v", i, got, w)
			}
		}
	}
}

// A hint says "refetch what you are showing", so the same hint twice is the
// same instruction. Coalescing keeps a backfill's thousand message.changed
// events from filling a slow phone's queue.
func TestIdenticalPendingHintsCoalesce(t *testing.T) {
	t.Parallel()
	h := New()
	s := mustSubscribe(t, h)

	e := Event{Type: MessageChanged, AccountID: "acct-1", Folder: "INBOX"}
	for range 10 * QueueSize {
		h.Publish(e)
	}
	other := Event{Type: SyncState, AccountID: "acct-1"}
	h.Publish(other)

	if got := next(t, s); got != e {
		t.Errorf("first = %+v, want %+v", got, e)
	}
	if got := next(t, s); got != other {
		t.Errorf("second = %+v, want %+v", got, other)
	}
	if n := s.Dropped(); n != 0 {
		t.Errorf("Dropped = %d, want 0: a coalesced hint was not lost, it was redundant", n)
	}

	// Once delivered, the same hint is news again.
	h.Publish(e)
	if got := next(t, s); got != e {
		t.Errorf("after delivery = %+v, want %+v", got, e)
	}
}

func TestSlowSubscriberDropsOldestWithoutBlockingPublish(t *testing.T) {
	t.Parallel()
	h := New()
	slow := mustSubscribe(t, h)
	fast := mustSubscribe(t, h)

	total := QueueSize + 10
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range total {
			h.Publish(Event{Type: FolderChanged, AccountID: "acct-1", Folder: fmt.Sprintf("f%d", i)})
			if i < QueueSize { // the fast one keeps up
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, err := fast.Next(ctx)
				cancel()
				if err != nil {
					t.Errorf("fast subscriber Next: %v", err) // not Fatal: this is not the test goroutine
					return
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a subscriber that is not reading")
	}

	if n := slow.Dropped(); n != 10 {
		t.Errorf("Dropped = %d, want 10", n)
	}
	// What survives is the newest QueueSize hints: the oldest were shed.
	first := next(t, slow)
	if want := fmt.Sprintf("f%d", 10); first.Folder != want {
		t.Errorf("oldest surviving hint = %q, want %q", first.Folder, want)
	}
	if n := fast.Dropped(); n != 0 {
		t.Errorf("a reading subscriber lost %d hints", n)
	}
}

func TestNextWakesOnCancelAndOnClose(t *testing.T) {
	t.Parallel()
	h := New()
	s := mustSubscribe(t, h)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := s.Next(ctx); errc <- err }()
	cancel()
	if err := waitErr(t, errc); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Next: err = %v, want context.Canceled", err)
	}

	go func() { errc <- nextErr(s) }()
	h.Close()
	if err := waitErr(t, errc); !errors.Is(err, ErrClosed) {
		t.Errorf("Next after Close: err = %v, want ErrClosed", err)
	}
	if _, err := h.Subscribe(); !errors.Is(err, ErrClosed) {
		t.Errorf("Subscribe after Close: err = %v, want ErrClosed", err)
	}
	h.Publish(Event{Type: SyncState}) // must not panic or block
}

func waitErr(t *testing.T, errc <-chan error) error {
	t.Helper()
	select {
	case err := <-errc:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Next did not wake up")
		return nil
	}
}

// Hints queued before Close are still the subscriber's to read; only after
// they are drained does Next report the end.
func TestCloseDeliversWhatWasAlreadyQueued(t *testing.T) {
	t.Parallel()
	h := New()
	s := mustSubscribe(t, h)
	e := Event{Type: HealthAlert, AccountID: "acct-1", Code: "mass_disable"}
	h.Publish(e)
	h.Close()
	if got := next(t, s); got != e {
		t.Errorf("queued hint = %+v, want %+v", got, e)
	}
	if err := nextErr(s); !errors.Is(err, ErrClosed) {
		t.Errorf("after draining: err = %v, want ErrClosed", err)
	}
}

func TestSubscriberCountIsBoundedAndSlotsFree(t *testing.T) {
	t.Parallel()
	h := New()
	var subs []*Subscription
	for range MaxSubscribers {
		s, err := h.Subscribe()
		if err != nil {
			t.Fatalf("Subscribe within the limit: %v", err)
		}
		subs = append(subs, s)
	}
	if _, err := h.Subscribe(); !errors.Is(err, ErrTooManySubscribers) {
		t.Fatalf("Subscribe over the limit: err = %v, want ErrTooManySubscribers", err)
	}
	if n := h.Subscribers(); n != MaxSubscribers {
		t.Errorf("Subscribers = %d, want %d", n, MaxSubscribers)
	}

	subs[0].Close()
	subs[0].Close() // idempotent: a deferred Close after an explicit one is normal
	if n := h.Subscribers(); n != MaxSubscribers-1 {
		t.Errorf("Subscribers after Close = %d, want %d", n, MaxSubscribers-1)
	}
	again, err := h.Subscribe()
	if err != nil {
		t.Fatalf("second attempt after a slot freed: %v", err)
	}
	again.Close()
	for _, s := range subs[1:] {
		s.Close()
	}
}

func TestClosedSubscriptionIsDeaf(t *testing.T) {
	t.Parallel()
	h := New()
	s := mustSubscribe(t, h)
	s.Close()
	h.Publish(Event{Type: SyncState})
	if err := nextErr(s); !errors.Is(err, ErrClosed) {
		t.Errorf("Next on a closed subscription: err = %v, want ErrClosed", err)
	}
}

func TestConcurrentPublishAndSubscribe(t *testing.T) {
	t.Parallel()
	h := New()
	var wg sync.WaitGroup
	for p := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				h.Publish(Event{Type: MessageChanged, AccountID: fmt.Sprintf("a%d", p), Folder: fmt.Sprintf("f%d", i%5)})
			}
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := h.Subscribe()
			if err != nil {
				return // the limit is a legitimate answer under this load
			}
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			for {
				if _, err := s.Next(ctx); err != nil {
					return
				}
			}
		}()
	}
	wg.Wait()
	h.Close()
}
