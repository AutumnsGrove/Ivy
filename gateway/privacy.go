package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/jev"
	"github.com/AutumnsGrove/Ivy/store"
)

// PrivacySource says where a message stands with the safety checks. The Jev vetter
// implements it; it reads the cache and never makes a request.
type PrivacySource interface {
	Status(ctx context.Context, accountID, contentKey string) (jev.VetStatus, error)
}

// WithPrivacy wires the withheld marker. Without it every message reads as
// unchecked, which is how a server with smart features absent behaves.
func (s *Server) WithPrivacy(src PrivacySource) *Server {
	s.privacy = src
	return s
}

// handleMessagePrivacy serves GET /messages/{id}/privacy.
func (s *Server) handleMessagePrivacy(w http.ResponseWriter, r *http.Request) {
	m, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view := api.MessagePrivacy{State: api.Unchecked, Reasons: []string{}}
	if s.privacy != nil {
		st, err := s.privacy.Status(r.Context(), m.AccountID, m.ContentKey)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		view.State = api.MessagePrivacyState(st.State)
		view.Overridden = st.Overridden
		view.Reasons = append(view.Reasons, st.Reasons...)
	}
	writeJSON(w, http.StatusOK, view)
}

// handleAllowForHelpers serves PUT /messages/{id}/privacy/override.
func (s *Server) handleAllowForHelpers(w http.ResponseWriter, r *http.Request) {
	s.setOverride(w, r, func(ctx context.Context, m store.Message) error {
		return s.dbs.SetVettingOverride(ctx, m.AccountID, m.ContentKey, time.Now())
	})
}

// handleWithholdFromHelpers serves DELETE /messages/{id}/privacy/override.
func (s *Server) handleWithholdFromHelpers(w http.ResponseWriter, r *http.Request) {
	s.setOverride(w, r, func(ctx context.Context, m store.Message) error {
		return s.dbs.ClearVettingOverride(ctx, m.AccountID, m.ContentKey)
	})
}

func (s *Server) setOverride(w http.ResponseWriter, r *http.Request, apply func(context.Context, store.Message) error) {
	m, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := apply(r.Context(), m); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
