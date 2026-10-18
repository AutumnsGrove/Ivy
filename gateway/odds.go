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
	row, found, err := s.dbs.GetNeedsMe(r.Context(), m.AccountID, m.ContentKey)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if found {
		stage2 := api.OddsStage2{Verdict: api.OddsStage2Verdict(row.Verdict), Model: row.Model, Dismissed: row.State == store.NeedsDismissed}
		if row.Reason != "" {
			stage2.Reason = &row.Reason
		}
		view.Stage2 = &stage2
	}
	writeJSON(w, http.StatusOK, view)
}

// looksRealHint says whether a quiet "looks real" chip may show on a message. It needs
// the message to be in Junk now (a stale answer must not outlive a move out of Junk)
// and the kept junk_rescue answer to act. A failure to read either is a message with
// no chip, never a failed page: the chip is only a hint.
func (s *Server) looksRealHint(ctx context.Context, m store.Message) bool {
	if s.odds == nil {
		return false
	}
	folder, err := s.dbs.GetFolderByID(ctx, m.FolderID)
	if err != nil || folder.Role != store.RoleJunk {
		return false
	}
	return s.keptAnswerLooksReal(ctx, m.AccountID, m.ContentKey)
}

// readerWarnings are the quiet hints from the kept answers that the reader may show above
// a message: the phishing banner level and the contact-form note, each "" for none. Like
// the chip they are only hints: a failure to read the answers means none, never a failed
// page. One read serves both.
func (s *Server) readerWarnings(ctx context.Context, m store.Message) (phishing, contactForm string) {
	if s.odds == nil {
		return "", ""
	}
	odds, err := s.odds.Odds(ctx, m.AccountID, m.ContentKey)
	if err != nil {
		return "", ""
	}
	return jev.PhishingLevel(odds.Answers, m.AuthResults), jev.ContactFormNote(odds.Answers)
}

// markLooksReal sets the hint on the rows of a Junk page. The caller has already
// established that every row is in Junk. One indexed read per row, and a page is
// bounded by the inbox page size.
func (s *Server) markLooksReal(ctx context.Context, page store.InboxPage, out *api.Inbox) {
	if s.odds == nil {
		return
	}
	for i, m := range page.Items {
		if s.keptAnswerLooksReal(ctx, m.AccountID, m.ContentKey) {
			out.Items[i].LooksReal = boolPtr(true)
		}
	}
}

// keptAnswerLooksReal is the one rule for the chip, shared by the reader and the list:
// the kept junk_rescue answer is looks_real and clears its bar unsuppressed.
func (s *Server) keptAnswerLooksReal(ctx context.Context, accountID, contentKey string) bool {
	odds, err := s.odds.Odds(ctx, accountID, contentKey)
	if err != nil {
		return false
	}
	for _, v := range odds.Answers {
		if v.QuestionID == jev.JunkRescueQuestion && v.Choice == jev.LooksReal && v.Acts() {
			return true
		}
	}
	return false
}
