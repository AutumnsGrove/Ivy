package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/internal/secrets"
	"github.com/AutumnsGrove/Ivy/store"
)

// Bounds on what the connect screen may send (STANDARDS.md 4a limits).
const (
	maxAddressBytes     = 254 // RFC 5321 path limit
	maxConnectBodyBytes = 8 << 10
)

// ErrAlreadyConnected means the address is already mirrored, from the app or
// from ivy.yaml; connecting it twice would sync one mailbox into two accounts.
var ErrAlreadyConnected = errors.New("address is already connected")

// ConnectError is a failed login test. Code is "auth_failed", "unreachable" or
// "error"; the message never carries the password.
type ConnectError struct {
	Code string
	Err  error
}

func (e *ConnectError) Error() string {
	if e.Err == nil {
		return "connect failed (" + e.Code + ")"
	}
	return "connect failed (" + e.Code + "): " + e.Err.Error()
}
func (e *ConnectError) Unwrap() error { return e.Err }

// AccountConnector adds an account the operator typed into the app. Connect
// tests the login first and stores nothing unless it works; UpdatePassword does
// the same for an existing account and keeps the old password on failure. It
// reports store.ErrNotFound for an unknown account id.
type AccountConnector interface {
	Connect(ctx context.Context, address, password string, smart bool) (id string, err error)
	UpdatePassword(ctx context.Context, id, password string) error
}

// WithAccountConnector enables connecting accounts from the app.
func (s *Server) WithAccountConnector(c AccountConnector) *Server {
	s.connector = c
	return s
}

// validPassword applies the same bounds the password file enforces, so a bad
// one is a 400 here rather than a 500 after a successful provider login.
func validPassword(p string) bool {
	return p != "" && len(p) <= secrets.MaxPasswordBytes && !strings.ContainsAny(p, "\r\n\x00")
}

// validAddress accepts a bare address only. mail.ParseAddress also takes
// "Name <a@b>", which would put a display name into a login.
func validAddress(a string) bool {
	if a == "" || len(a) > maxAddressBytes {
		return false
	}
	parsed, err := mail.ParseAddress(a)
	return err == nil && parsed.Address == a
}

func (s *Server) handleConnectAccount(w http.ResponseWriter, r *http.Request) {
	if s.connector == nil {
		writeError(w, http.StatusServiceUnavailable, "connect_unavailable", "This Ivy cannot connect accounts from the app")
		return
	}
	var body api.ConnectAccount
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That request was not valid")
		return
	}
	address := strings.TrimSpace(body.Address)
	password := ""
	if body.Password != nil {
		password = *body.Password
	}
	if !validAddress(address) || !validPassword(password) {
		writeError(w, http.StatusBadRequest, "bad_request", "Enter your email address and password")
		return
	}
	id, err := s.connector.Connect(r.Context(), address, password, body.Smart != nil && *body.Smart)
	if err != nil {
		s.connectFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.ConnectedAccount{Id: id})
}

func (s *Server) handleUpdateAccountPassword(w http.ResponseWriter, r *http.Request) {
	if s.connector == nil {
		writeError(w, http.StatusServiceUnavailable, "connect_unavailable", "This Ivy cannot connect accounts from the app")
		return
	}
	var body api.UpdatePassword
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectBodyBytes)).Decode(&body); err != nil ||
		body.Password == nil || !validPassword(*body.Password) {
		writeError(w, http.StatusBadRequest, "bad_request", "Enter your password")
		return
	}
	if err := s.connector.UpdatePassword(r.Context(), r.PathValue("id"), *body.Password); err != nil {
		s.connectFailure(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// connectFailure maps the connector's typed errors to the contract. Anything
// untyped is logged and answered generically, since its text may name a host.
func (s *Server) connectFailure(w http.ResponseWriter, r *http.Request, err error) {
	var ce *ConnectError
	switch {
	case errors.Is(err, ErrAlreadyConnected):
		writeError(w, http.StatusConflict, "already_connected", "That address is already connected")
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "account")
	case errors.As(err, &ce) && ce.Code == "auth_failed":
		writeError(w, http.StatusUnprocessableEntity, "auth_failed", "Your provider refused that email address and password")
	case errors.As(err, &ce) && ce.Code == "unreachable":
		writeError(w, http.StatusBadGateway, "unreachable", "Ivy could not reach your mail provider")
	case errors.As(err, &ce):
		writeError(w, http.StatusBadGateway, "connect_failed", "Ivy could not sign in to your mail provider")
	default:
		s.serverError(w, r, err)
	}
}
