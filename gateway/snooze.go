package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/rules"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxHiddenKeys bounds how many locally hidden keys the inbox view loads:
// snoozes and Reading membership. Above it, the remainder would still show in
// the inbox, which is a bounded, honest degradation (STANDARDS.md 4a).
const maxHiddenKeys = 2000

// hiddenKeys returns the content keys the inbox hides for one account (or every
// account when accountID is empty): the active snoozes plus everything in
// Reading. It also returns how many are in Reading, for the empty-inbox hint.
func (s *Server) hiddenKeys(ctx context.Context, accountID string) ([]string, int, error) {
	snoozed, err := s.dbs.ActiveSnoozeKeys(ctx, accountID, s.now())
	if err != nil {
		return nil, 0, err
	}
	tag, err := s.dbs.TagBySlug(ctx, store.ReadingSlug)
	if errors.Is(err, store.ErrNotFound) {
		return snoozed, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	reading, err := s.dbs.ContentKeysForTag(ctx, tag.ID, maxHiddenKeys)
	if err != nil {
		return nil, 0, err
	}
	return append(snoozed, reading...), len(reading), nil
}

// handleSnoozedInbox lists the local hidden-until mail, soonest to wake first
// is not kept, so it reads like any other list (newest first).
func (s *Server) handleSnoozedInbox(w http.ResponseWriter, r *http.Request, accountID string) {
	keys, err := s.dbs.ActiveSnoozeKeys(r.Context(), accountID, s.now())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	items, err := s.dbs.MessagesByContentKeys(r.Context(), accountID, keys, store.MaxRuleDryRun)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out, err := s.inboxResponse(r, store.InboxPage{Items: items}, 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTaggedInbox shows only the mail carrying one tag. Tag membership is
// state, so the keys are loaded and the mirror is read by content key.
func (s *Server) handleTaggedInbox(w http.ResponseWriter, r *http.Request, accountID, tagID string) {
	tag, err := s.dbs.GetTag(r.Context(), tagID)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "tag")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	keys, err := s.dbs.ContentKeysForTag(r.Context(), tag.ID, maxHiddenKeys)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	items, err := s.dbs.MessagesByContentKeys(r.Context(), accountID, keys, store.MaxInboxLimit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out, err := s.inboxResponse(r, store.InboxPage{Items: items}, 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSnoozeMessage hides one message until a preset time. It is local state,
// so it never touches IMAP.
func (s *Server) handleSnoozeMessage(w http.ResponseWriter, r *http.Request) {
	var body api.SnoozeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTagBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That is not a valid request")
		return
	}
	msg, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	preset := string(body.Preset)
	if !store.ValidSnoozePreset(preset) {
		writeError(w, http.StatusBadRequest, "bad_request", "That snooze time is not available")
		return
	}
	now := s.now()
	if err := s.dbs.SnoozeMessage(r.Context(), msg.AccountID, msg.ContentKey, rules.SnoozeUntil(preset, now), now); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.hintMessageChanged(msg.AccountID)
	w.WriteHeader(http.StatusNoContent)
}

// handleUnsnoozeMessage wakes a message now.
func (s *Server) handleUnsnoozeMessage(w http.ResponseWriter, r *http.Request) {
	msg, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.dbs.ClearSnooze(r.Context(), msg.AccountID, msg.ContentKey); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.hintMessageChanged(msg.AccountID)
	w.WriteHeader(http.StatusNoContent)
}

// handleListReading serves the messages the reserved Reading tag holds, as the
// feed the Reading screen shows. The digest is a plain count, never text from a
// model; newsletters and unsubscribe arrive in chunk 5.
func (s *Server) handleListReading(w http.ResponseWriter, r *http.Request) {
	tag, err := s.dbs.TagBySlug(r.Context(), store.ReadingSlug)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, api.ReadingFeed{Digest: "Nothing in Reading yet", Issues: []api.Issue{}})
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	keys, err := s.dbs.ContentKeysForTag(r.Context(), tag.ID, maxHiddenKeys)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	items, err := s.dbs.MessagesByContentKeys(r.Context(), "", keys, store.MaxInboxLimit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	issues := make([]api.Issue, 0, len(items))
	for _, m := range items {
		issues = append(issues, issueView(m))
	}
	digest := "Nothing in Reading yet"
	if len(issues) > 0 {
		digest = fmt.Sprintf("%d kept out of your inbox", len(issues))
	}
	writeJSON(w, http.StatusOK, api.ReadingFeed{Digest: digest, Issues: issues})
}

// issueView projects a Reading message onto the feed's row.
func issueView(m store.MessageSummary) api.Issue {
	blurb := m.Snippet
	return api.Issue{
		Id:       m.ID,
		Sender:   displayName(m.From),
		Initials: initials(m.From.Name, m.From.Address),
		Read:     !m.Unread,
		Title:    m.Subject,
		Blurb:    &blurb,
	}
}
