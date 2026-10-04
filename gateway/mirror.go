package gateway

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/store"
)

// The mirror endpoints are the operator's control over hidden mail. They are
// local-only by construction: none of them talks to IMAP, so a restore or a
// purge can never be mistaken for a mail action (the write path through the
// outbox arrives in 3d).

// handleRestoreMessage makes one hidden message visible again.
func (s *Server) handleRestoreMessage(w http.ResponseWriter, r *http.Request) {
	err := s.dbs.RestoreMessage(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "message")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
	s.hintMessageChanged("")
}

// handleRestoreAccountHidden is the one-click answer to a mass-disable alert:
// every settled hidden row of the account becomes visible again.
func (s *Server) handleRestoreAccountHidden(w http.ResponseWriter, r *http.Request) {
	n, err := s.dbs.RestoreAccountDisabled(r.Context(), r.PathValue("id"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.hintMessageChanged(r.PathValue("id"))
	writeJSON(w, http.StatusOK, api.RestoreResult{Restored: n})
}

// handlePurgeMessage permanently erases one hidden message. It is the only
// erasure Ivy has, so a message that is not hidden is refused rather than
// destroyed: the reader's ordinary delete can never reach this.
func (s *Server) handlePurgeMessage(w http.ResponseWriter, r *http.Request) {
	rawPath, err := s.dbs.PurgeMessage(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "message")
		return
	case errors.Is(err, store.ErrNotDisabled):
		writeError(w, http.StatusConflict, "not_disabled", "Only hidden mail can be purged")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	// The row is already gone, so an unlink failure leaves an orphan the spool
	// sweep collects; it must not fail the request.
	if err := s.removeSpool(rawPath); err != nil {
		slog.WarnContext(r.Context(), "gateway: cannot unlink the purged spool file", "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
	s.hintMessageChanged("")
}

// hintMessageChanged tells open browsers to refetch. Restore and purge change
// what a screen shows, so they publish the same hint sync does. A server without
// a hub (a test, or a build that never wired events) is a no-op.
func (s *Server) hintMessageChanged(accountID string) {
	if s.events == nil {
		return
	}
	s.events.Publish(events.Event{Type: events.MessageChanged, AccountID: accountID})
}

// removeSpool unlinks one spool file, refusing any path that escapes the spool
// directory. rawPath is a slash-separated path relative to the data dir, as
// sync stored it.
func (s *Server) removeSpool(rawPath string) error {
	if rawPath == "" {
		return nil
	}
	root := filepath.Join(s.dbs.Dir, "spool")
	path := filepath.Join(s.dbs.Dir, filepath.FromSlash(rawPath))
	if rel, err := filepath.Rel(root, path); err != nil || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("spool path %q is outside the spool directory", rawPath)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) { //nolint:gosec // G703: the containment check above keeps the path inside the spool directory
		return fmt.Errorf("remove spool file %q: %w", rawPath, err)
	}
	return nil
}
