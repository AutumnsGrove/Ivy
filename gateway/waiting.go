package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
	"github.com/AutumnsGrove/Ivy/waiting"
)

// WaitingSource is where the Waiting on list comes from. The waiting service implements
// it; opening the list may ask the helper decision model about a few sent messages it
// has not judged, and answers from what it kept after that.
type WaitingSource interface {
	List(ctx context.Context, accountID string) ([]waiting.Item, error)
}

// WithWaiting wires the Waiting on list. Without it the list is always empty, which is
// how a server with smart features absent behaves.
func (s *Server) WithWaiting(src WaitingSource) *Server {
	s.waitingOn = src
	return s
}

// handleListWaitingOn serves GET /waiting. It is always a 200: the list is a nicety, so a
// failure behind it is logged and the screen shows what it has.
func (s *Server) handleListWaitingOn(w http.ResponseWriter, r *http.Request) {
	view := api.WaitingOn{Items: []api.WaitingItem{}}
	if s.waitingOn != nil {
		items, err := s.waitingOn.List(r.Context(), r.URL.Query().Get("account_id"))
		if err != nil {
			slog.WarnContext(r.Context(), "waiting on: the list is incomplete", "error", err)
		}
		for _, it := range items {
			to := make([]string, 0, len(it.To))
			for _, a := range it.To {
				to = append(to, displayName(a))
			}
			view.Items = append(view.Items, api.WaitingItem{
				Id: it.MessageID, AccountId: it.AccountID, To: to, Subject: it.Subject,
				SentAt: it.SentAt, Days: it.Days,
			})
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// handleStopWaiting serves POST /waiting/{id}/stop. It is the operator's own call, kept
// locally, and works on any live message: stopping one nobody was waiting on is not an
// error.
func (s *Server) handleStopWaiting(w http.ResponseWriter, r *http.Request) {
	m, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.dbs.StopWaiting(r.Context(), m.AccountID, m.ContentKey, s.now()); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
