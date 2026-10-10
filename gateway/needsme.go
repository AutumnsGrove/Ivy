package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

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

// removesPlacedTag reports whether an action takes the local "Needs you" tag off a
// message. That is the same act as dismissing the flag (decision 5), so the caller
// clears it. A tag that cannot be read is not the placed tag.
func (s *Server) removesPlacedTag(ctx context.Context, action string, tagID *string) bool {
	if action != "untag" || tagID == nil || *tagID == "" {
		return false
	}
	tag, err := s.dbs.GetTag(ctx, *tagID)
	return err == nil && tag.LocalOnly()
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

// UrgencySource says how the Needs attention view recognises an urgent message: the
// live urgency question's wording, model and bar. The Jev registry implements it, so
// retuning the bar reorders the view with no migration.
type UrgencySource interface {
	UrgencyBar() store.UrgencyBar
}

// WithNeedsAttention wires the urgency ordering. Without it the view is plainly
// newest first, which is how a server with smart features absent behaves.
func (s *Server) WithNeedsAttention(src UrgencySource) *Server {
	s.urgency = src
	return s
}

// handleNeedsAttention serves GET /needs-attention.
func (s *Server) handleNeedsAttention(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	accountID := q.Get("account_id")
	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > store.MaxInboxLimit {
			writeError(w, http.StatusBadRequest, "bad_request", "That page size is not valid")
			return
		}
		limit = n
	}
	hidden, _, err := s.hiddenKeys(r.Context(), accountID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	query := store.NeedsAttentionQuery{AccountID: accountID, Cursor: q.Get("cursor"), Limit: limit, Hide: hidden}
	if s.urgency != nil {
		query.Urgency = s.urgency.UrgencyBar()
	}
	page, err := s.dbs.NeedsAttention(r.Context(), query)
	if errors.Is(err, store.ErrBadCursor) {
		writeError(w, http.StatusBadRequest, "bad_request", "That page link is not valid")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out, err := s.inboxResponse(r, page, 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
