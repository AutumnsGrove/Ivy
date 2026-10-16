package gateway

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/AutumnsGrove/Ivy/api"

	"github.com/AutumnsGrove/Ivy/jev"
)

// Backfiller is the Jev worker's backfill, declared here at the point of use.
type Backfiller interface {
	EstimateBackfill(ctx context.Context, account string, win jev.Window) (jev.BackfillEstimate, error)
	StartBackfill(ctx context.Context, account string, win jev.Window) error
}

// WithBackfill wires the Smart screen's "look at existing mail" button.
func (s *Server) WithBackfill(b Backfiller) *Server {
	s.backfill = b
	return s
}

// maxBackfillBodyBytes bounds the start request: an account id and a window.
const maxBackfillBodyBytes = 1 << 10

// windows is every reach the screen offers. A closed set keeps an odd value from
// quietly meaning "all mail", which is the expensive one.
var windows = map[api.BackfillWindow]jev.Window{
	"7d":  jev.Days(7),
	"30d": jev.Days(30),
	"90d": jev.Days(90),
	"all": jev.AllMail,
}

func (s *Server) handleSmartBackfillEstimate(w http.ResponseWriter, r *http.Request) {
	account, win, ok := s.backfillTarget(w, r, r.URL.Query().Get("account"), api.BackfillWindow(r.URL.Query().Get("window")))
	if !ok {
		return
	}
	s.writeBackfillEstimate(w, r, account.Id, api.BackfillWindow(r.URL.Query().Get("window")), win)
}

func (s *Server) handleSmartBackfillStart(w http.ResponseWriter, r *http.Request) {
	var body api.SmartBackfillRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBackfillBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That is not a request Ivy understands")
		return
	}
	account, win, ok := s.backfillTarget(w, r, body.Account, body.Window)
	if !ok {
		return
	}
	if !account.Smart {
		writeError(w, http.StatusConflict, "not_ready", "Smart features are off for this account, so there is nothing to look over mail with.")
		return
	}
	if problems := s.controls().Problems(r.Context(), account.Id); len(problems) > 0 {
		writeError(w, http.StatusConflict, "not_ready", problems[0].Message)
		return
	}
	if s.backfill == nil {
		writeError(w, http.StatusConflict, "not_ready", "Smart features are not running on this server.")
		return
	}
	if err := s.backfill.StartBackfill(r.Context(), account.Id, win); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.writeBackfillEstimate(w, r, account.Id, body.Window, win)
}

// backfillTarget resolves the account and the window, or answers the request itself.
func (s *Server) backfillTarget(w http.ResponseWriter, r *http.Request, accountID string, window api.BackfillWindow) (api.Account, jev.Window, bool) {
	win, known := windows[window]
	if !known {
		writeError(w, http.StatusBadRequest, "bad_request", "Pick 7 days, 30 days, 90 days or all mail")
		return api.Account{}, 0, false
	}
	views, err := s.accountViews(r)
	if err != nil {
		s.serverError(w, r, err)
		return api.Account{}, 0, false
	}
	for _, v := range views {
		if v.Id == accountID {
			return v, win, true
		}
	}
	s.notFound(w, r, "account")
	return api.Account{}, 0, false
}

func (s *Server) writeBackfillEstimate(w http.ResponseWriter, r *http.Request, accountID string, window api.BackfillWindow, win jev.Window) {
	if s.backfill == nil {
		writeError(w, http.StatusConflict, "not_ready", "Smart features are not running on this server.")
		return
	}
	est, err := s.backfill.EstimateBackfill(r.Context(), accountID, win)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.SmartBackfill{
		Account: accountID, Window: window, Items: est.Items, Usd: est.Estimate.USD, Fits: est.Estimate.Fits,
		AccountHeadroomUsd: est.Estimate.AccountHeadroomUSD, GlobalHeadroomUsd: est.Estimate.GlobalHeadroomUSD,
	})
}
