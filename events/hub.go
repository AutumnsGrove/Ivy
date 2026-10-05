// Package events is the hub that tells open browsers something changed. It
// carries hints only (ARCHITECTURE.md 4, round 37): small typed events that say
// what to refetch, with no replay and no event ids. A client that reconnects
// refetches what it is showing, so nothing here needs to remember the past.
package events

import (
	"context"
	"errors"
	"slices"
	"sync"
)

// Type names what kind of change a hint is about.
type Type string

// The hint types a stream carries (ARCHITECTURE.md 4); the wire names are the
// contract's `Event.type` values.
const (
	MessageChanged Type = "message.changed"
	FolderChanged  Type = "folder.changed"
	SyncState      Type = "sync.state"
	OutboxState    Type = "outbox.state"
	HealthAlert    Type = "health.alert"
	UpdateState    Type = "update.state"
)

// Event is one hint. It is comparable on purpose: an identical hint already
// waiting in a queue is redundant, so the hub coalesces it.
type Event struct {
	Type      Type   `json:"type"`
	AccountID string `json:"accountId,omitempty"`
	Folder    string `json:"folder,omitempty"`
	// Code is a stable machine-readable reason, used by health.alert.
	Code string `json:"code,omitempty"`
}

// Limits (STANDARDS.md 4a). One operator opens a handful of tabs and devices, so
// the subscriber bound is generous; the queue bound is what stops a stalled
// phone from holding memory.
const (
	MaxSubscribers = 16
	QueueSize      = 64
)

var (
	// ErrClosed is returned once a subscription or the whole hub has ended and
	// nothing more will arrive.
	ErrClosed = errors.New("events: closed")
	// ErrTooManySubscribers is returned when MaxSubscribers streams are open.
	ErrTooManySubscribers = errors.New("events: too many subscribers")
)

// Hub fans events out to subscribers. Publish never blocks, whatever a
// subscriber is doing, so a stalled client cannot slow sync down.
type Hub struct {
	mu     sync.Mutex
	subs   map[*Subscription]struct{}
	closed bool
}

// New returns an empty Hub.
func New() *Hub { return &Hub{subs: make(map[*Subscription]struct{})} }

// Publish offers e to every subscriber. It is safe for concurrent use and a
// no-op once the hub is closed.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		s.push(e)
	}
}

// Subscribe opens a subscription. The caller must Close it.
func (h *Hub) Subscribe() (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrClosed
	}
	if len(h.subs) >= MaxSubscribers {
		return nil, ErrTooManySubscribers
	}
	s := &Subscription{hub: h, wake: make(chan struct{}, 1)}
	h.subs[s] = struct{}{}
	return s, nil
}

// Subscribers reports how many subscriptions are open.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Close ends every subscription. A subscriber can still read what was already
// queued; after that Next reports ErrClosed. It is how a server shutdown ends
// its open streams, which never go idle on their own.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for s := range h.subs {
		s.finish(false)
	}
	h.subs = nil
}

// Subscription is one client's bounded queue of hints. It is read by one
// goroutine at a time.
type Subscription struct {
	hub  *Hub
	wake chan struct{} // capacity 1: "the queue or the state changed"

	mu      sync.Mutex
	queue   []Event
	dropped uint64
	closed  bool
}

// push queues e unless an equal hint is already waiting. A full queue sheds its
// oldest hint: the client refetches what it shows anyway, so the freshest hint
// is the one worth keeping.
func (s *Subscription) push(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || slices.Contains(s.queue, e) {
		return
	}
	if len(s.queue) >= QueueSize {
		s.queue = s.queue[1:]
		s.dropped++
	}
	s.queue = append(s.queue, e)
	s.signal()
}

// signal wakes a waiting Next. The channel holds one token, so repeated
// signals while nobody waits collapse into one.
func (s *Subscription) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// finish marks the subscription over. With discard it also empties the queue,
// for an owner that has stopped listening.
func (s *Subscription) finish(discard bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if discard {
		s.queue = nil
	}
	s.signal()
}

// Next returns the oldest queued hint, waiting for one if there is none. It
// returns ctx's error when ctx ends and ErrClosed once the subscription is over
// and its queue is drained.
func (s *Subscription) Next(ctx context.Context) (Event, error) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			e := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return e, nil
		}
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return Event{}, ErrClosed
		}
		select {
		case <-s.wake:
		case <-ctx.Done():
			return Event{}, ctx.Err()
		}
	}
}

// Dropped counts hints shed because the queue was full: how far behind this
// client has been.
func (s *Subscription) Dropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// Close ends the subscription and frees its slot. It is safe to call twice.
func (s *Subscription) Close() {
	s.finish(true)
	s.hub.mu.Lock()
	delete(s.hub.subs, s) // a no-op on a nil map after hub.Close
	s.hub.mu.Unlock()
}
