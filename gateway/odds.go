package gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/jev"
	"github.com/AutumnsGrove/Ivy/store"
)

// OddsSource is where the odds sheet reads a message's cached answers. The Jev
// engine implements it; it is a read of the cache and never makes a request.
type OddsSource interface {
	Odds(ctx context.Context, accountID, contentKey string) (jev.Odds, error)
}

// WithOdds wires the odds sheet. Without it every message's sheet is empty, which is
// how a server with smart features absent behaves.
func (s *Server) WithOdds(src OddsSource) *Server {
	s.odds = src
	return s
}

// handleMessageOdds serves GET /messages/{id}/odds.
func (s *Server) handleMessageOdds(w http.ResponseWriter, r *http.Request) {
	m, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view := api.MessageOdds{Answers: []api.OddsAnswer{}, Unanswered: []api.OddsUnanswered{}}
	if s.odds != nil {
		odds, err := s.odds.Odds(r.Context(), m.AccountID, m.ContentKey)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		view.Model = odds.Model
		for _, v := range odds.Answers {
			view.Answers = append(view.Answers, api.OddsAnswer{
				QuestionId: v.QuestionID, Choice: v.Choice, Probabilities: v.Probabilities,
				Confidence: v.Confidence, Threshold: v.Threshold, QuietOption: v.QuietOption,
				Fires: v.Fires, Suppressed: v.Suppressed, Acts: v.Acts(),
			})
		}
		for _, u := range odds.Unanswered {
			view.Unanswered = append(view.Unanswered, api.OddsUnanswered{
				QuestionId: u.QuestionID, Reason: api.OddsUnansweredReason(u.Reason),
			})
		}
	}
	writeJSON(w, http.StatusOK, view)
}
