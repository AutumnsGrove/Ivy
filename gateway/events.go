package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/AutumnsGrove/Ivy/events"
)

const (
	// defaultHeartbeat keeps an idle stream alive through proxies that close a
	// silent connection (Tailscale serve and most reverse proxies allow 60 s) and
	// is what notices a peer that has gone: the keepalive write fails.
	defaultHeartbeat = 20 * time.Second
	// eventWriteTimeout bounds one write. The server has no WriteTimeout because
	// streams are long-lived, so a phone that sleeps with its socket half-open
	// would otherwise hold this goroutine and a hub slot forever.
	eventWriteTimeout = 10 * time.Second
	// eventRetry is how long the browser waits before reconnecting. A reconnect
	// refetches what is on screen, so a short delay is right.
	eventRetry = 3000
)

// WithEvents serves the hub's hints at /api/v1/events.
func (s *Server) WithEvents(h *events.Hub) *Server {
	s.events = h
	return s
}

// WithEventsHeartbeat sets how often an idle stream sends a keepalive comment.
func (s *Server) WithEventsHeartbeat(d time.Duration) *Server {
	s.heartbeat = d
	return s
}

// handleEvents streams hints as server-sent events (ARCHITECTURE.md 4, round 37).
// There are no event ids and no replay: after any reconnect the client refetches
// what it is showing.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	sub, err := s.events.Subscribe()
	switch {
	case errors.Is(err, events.ErrTooManySubscribers):
		writeError(w, http.StatusServiceUnavailable, "too_many_streams", "Too many Ivy windows are open; close one and try again")
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Ivy is shutting down")
		return
	}
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	send := func(frame string) bool {
		// A writer that cannot take a deadline (a test recorder) is not an error:
		// the deadline is protection against a stalled peer, not part of the data.
		if err := rc.SetWriteDeadline(time.Now().Add(eventWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false
		}
		if _, err := w.Write([]byte(frame)); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send("retry: " + strconv.Itoa(eventRetry) + "\n\n") {
		return
	}

	heartbeat := s.heartbeat
	if heartbeat <= 0 {
		heartbeat = defaultHeartbeat
	}
	for {
		e, idle, err := nextOrIdle(r.Context(), sub, heartbeat)
		switch {
		case err != nil:
			return // the client left, or the hub closed
		case idle:
			if !send(": keepalive\n\n") {
				return
			}
			continue
		}
		// The event is JSON-encoded onto one line, so a folder name the server
		// chose cannot add a line or a frame of its own.
		data, err := json.Marshal(e)
		if err != nil {
			continue // an Event is plain strings; this cannot happen
		}
		if !send("event: " + string(e.Type) + "\ndata: " + string(data) + "\n\n") {
			return
		}
	}
}

// nextOrIdle waits for the next hint. It reports idle, with no error, when the
// heartbeat interval passes first; a request that ended or a hub that closed is
// an error, whatever deadline the request's own context carries.
func nextOrIdle(ctx context.Context, sub *events.Subscription, every time.Duration) (e events.Event, idle bool, err error) {
	waitCtx, cancel := context.WithTimeout(ctx, every)
	defer cancel()
	e, err = sub.Next(waitCtx)
	switch {
	case err == nil:
		return e, false, nil
	case ctx.Err() != nil:
		return events.Event{}, false, ctx.Err()
	case errors.Is(err, context.DeadlineExceeded):
		return events.Event{}, true, nil
	}
	return events.Event{}, false, err
}
