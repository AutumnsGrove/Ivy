package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// Bounds on the operator-supplied account profile (STANDARDS.md 4a limits).
const (
	maxDisplayNameRunes  = 120
	maxIconRunes         = 16
	maxAccountPhotoBytes = 5 << 20
	maxProfileBodyBytes  = 4 << 10
)

// photoTypes are the image formats an account photo may be. SVG is deliberately
// absent: served same-origin it can carry script, and there is no reason a
// badge needs it.
var photoTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// handleUpdateAccountProfile renames an account or sets its icon. Omitted
// fields keep their value, so a partial edit never clears the other.
func (s *Server) handleUpdateAccountProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	acct, err := s.dbs.GetAccount(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "account")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	var body api.AccountProfile
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxProfileBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That name or icon is not valid")
		return
	}
	displayName, icon := acct.DisplayName, acct.Icon
	if body.DisplayName != nil {
		displayName = strings.TrimSpace(*body.DisplayName)
	}
	if body.Icon != nil {
		icon = strings.TrimSpace(*body.Icon)
	}
	if utf8.RuneCountInString(displayName) > maxDisplayNameRunes || utf8.RuneCountInString(icon) > maxIconRunes {
		writeError(w, http.StatusBadRequest, "bad_request", "That name or icon is too long")
		return
	}
	if body.Icon != nil && !store.ValidAccountIcon(icon) {
		writeError(w, http.StatusBadRequest, "bad_request", "That icon is not one Ivy offers")
		return
	}

	if err := s.dbs.SetAccountProfile(r.Context(), id, displayName, icon); err != nil {
		s.serverError(w, r, err)
		return
	}
	view, err := s.accountByID(r, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleUploadAccountPhoto stores an operator-chosen photo. The declared type
// is not trusted: the bytes are sniffed and only a real raster image is kept.
func (s *Server) handleUploadAccountPhoto(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.dbs.GetAccount(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "account")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}

	photo, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAccountPhotoBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That photo is too big")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "That photo could not be read")
		return
	}
	if len(photo) == 0 || !photoTypes[http.DetectContentType(photo)] {
		writeError(w, http.StatusBadRequest, "bad_request", "That is not a JPEG, PNG, GIF or WebP image")
		return
	}
	if err := s.dbs.SetAccountPhoto(r.Context(), id, photo); err != nil {
		s.serverError(w, r, err)
		return
	}
	view, err := s.accountByID(r, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleDeleteAccountPhoto removes the photo, leaving the badge to fall back to
// the icon or the account colour.
func (s *Server) handleDeleteAccountPhoto(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.dbs.SetAccountPhoto(r.Context(), id, nil); errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "account")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	view, err := s.accountByID(r, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleAccountPhoto streams the stored photo with its sniffed type.
func (s *Server) handleAccountPhoto(w http.ResponseWriter, r *http.Request) {
	photo, err := s.dbs.GetAccountPhoto(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "photo")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(photo))
	_, _ = w.Write(photo) //nolint:gosec // G705: the bytes were sniffed on upload and are served as their sniffed image type, with nosniff
}

// accountByID builds one account's view, for the write handlers that return the
// updated row.
func (s *Server) accountByID(r *http.Request, id string) (api.Account, error) {
	a, err := s.dbs.GetAccount(r.Context(), id)
	if err != nil {
		return api.Account{}, err
	}
	stats, err := s.dbs.AccountStats(r.Context())
	if err != nil {
		return api.Account{}, err
	}
	hidden, err := s.dbs.DisabledStats(r.Context())
	if err != nil {
		return api.Account{}, err
	}
	smart, err := s.smartAccounts(r.Context())
	if err != nil {
		return api.Account{}, err
	}
	return accountView(a, stats[a.ID], hidden[a.ID], smartFor(a, smart)), nil
}
