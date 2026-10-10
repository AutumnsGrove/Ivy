package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/AutumnsGrove/Ivy/store"
)

// dealsWithMessage reports whether an outbox action is the operator dealing with a
// message (moving it out of the Inbox or deleting it), as opposed to organising it:
// flagging, marking seen and tagging leave a needs-attention flag alone, and "not
// junk" moves mail back to where it needs attention.
func dealsWithMessage(action string) bool {
	switch action {
	case "archive", "trash", "spam", "move", "expunge":
		return true
	}
	return false
}

// clearNeed drops a message's needs-attention flag once the operator has dealt with
// it. The action that earned it has already been accepted and the flag is only a
// local hint, so a failure here is logged and never turned into a failed request.
func (s *Server) clearNeed(ctx context.Context, accountID, contentKey string) {
	if err := s.dbs.DismissNeedsMe(ctx, accountID, contentKey); err != nil {
		slog.WarnContext(ctx, "gateway: could not clear a needs-attention flag", "account", accountID, "error", err)
	}
}

// handleDismissNeeds serves POST /messages/{id}/needs/dismiss. It works on any live
// message, judged or not: the click is the operator's, and a message nobody judged
// simply has nothing to clear.
func (s *Server) handleDismissNeeds(w http.ResponseWriter, r *http.Request) {
	m, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.dbs.DismissNeedsMe(r.Context(), m.AccountID, m.ContentKey); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// clearNeedForReply clears the flag of the message a reply answers. A message's
// content key is a hash of its Message-ID, which is exactly what a reply carries as
// In-Reply-To, so no lookup is needed.
func (s *Server) clearNeedForReply(ctx context.Context, accountID, inReplyTo string) {
	if inReplyTo == "" {
		return
	}
	s.clearNeed(ctx, accountID, store.ContentKey(inReplyTo, nil))
}
