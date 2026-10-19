package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/insight"
	"github.com/AutumnsGrove/Ivy/store"
)

// InsightSource is where the reader's summary chip comes from. The insight service
// implements it; it may make one chat call the first time a worthwhile thread is
// opened, and answers from its cache after that.
type InsightSource interface {
	ForMessage(ctx context.Context, accountID, messageID string) (insight.Result, error)
}

// WithInsight wires the summary chip. Without it every thread has no summary, which is
// how a server with smart features absent behaves.
func (s *Server) WithInsight(src InsightSource) *Server {
	s.insight = src
	return s
}

// handleMessageInsight serves GET /messages/{id}/insight. It is always a 200 for a
// message that exists: a chip is a nicety, so a failure behind it is logged and the
// reader simply shows none.
func (s *Server) handleMessageInsight(w http.ResponseWriter, r *http.Request) {
	m, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view := api.MessageInsight{}
	if s.insight != nil {
		res, err := s.insight.ForMessage(r.Context(), m.AccountID, m.ID)
		switch {
		case err != nil:
			slog.WarnContext(r.Context(), "insight: no summary for an opened message", "error", err)
		case res.Summary != "":
			view.Summary = &res.Summary
			if res.Earlier > 0 {
				view.Earlier = &res.Earlier
			}
		}
	}
	writeJSON(w, http.StatusOK, view)
}
