package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxTagBodyBytes bounds a tag request: a name of at most 64 characters and a
// colour word, so a hostile body cannot grow the parse.
const maxTagBodyBytes = 4 << 10

// defaultTagColor is the accent, so a tag created without a choice is never
// colourless.
const defaultTagColor = api.Lilac

func userTag(t store.TagSummary) api.UserTag {
	return api.UserTag{Id: t.ID, Slug: t.Slug, Name: t.Name, Color: api.TagColor(t.Color), Count: t.Count}
}

// handleListTags serves the operator's tags with their message counts. The
// placed tags (Jev, chunk 5) and the active rule count (3g) do not exist yet, so
// they are honestly empty and zero rather than invented.
func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.dbs.ListTags(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := api.TagsOverview{Mine: make([]api.UserTag, 0, len(tags)), Placed: []api.PlacedTag{}}
	for _, t := range tags {
		out.Mine = append(out.Mine, userTag(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateTag adds a tag. The slug that becomes its IMAP keyword is fixed
// here, so nothing later (a rename) can orphan keywords already on the server.
func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	var body api.TagCreate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTagBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That tag is not valid")
		return
	}
	color := defaultTagColor
	if body.Color != nil {
		color = *body.Color
	}
	if !color.Valid() {
		writeError(w, http.StatusBadRequest, "bad_request", "That colour is not available")
		return
	}
	tag, err := s.dbs.CreateTag(r.Context(), s.newID(), body.Name, string(color))
	switch {
	case errors.Is(err, store.ErrTagName):
		writeError(w, http.StatusBadRequest, "bad_request", "A tag needs a name of up to 64 characters")
		return
	case errors.Is(err, store.ErrTagLimit):
		writeError(w, http.StatusConflict, "too_many_tags", "There are too many tags; delete one first")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, userTag(store.TagSummary{Tag: tag}))
}

// handleUpdateTag renames or recolours a tag. Fields left out keep their value.
func (s *Server) handleUpdateTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body api.TagUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTagBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That change is not valid")
		return
	}
	if body.Color != nil && !body.Color.Valid() {
		writeError(w, http.StatusBadRequest, "bad_request", "That colour is not available")
		return
	}
	current, err := s.dbs.GetTag(ctx, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "tag")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	name, color := current.Name, current.Color
	if body.Name != nil {
		name = *body.Name
	}
	if body.Color != nil {
		color = string(*body.Color)
	}
	updated, err := s.dbs.UpdateTag(ctx, current.ID, name, color)
	switch {
	case errors.Is(err, store.ErrTagName):
		writeError(w, http.StatusBadRequest, "bad_request", "A tag needs a name of up to 64 characters")
		return
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "tag")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	count := 0
	if members, err := s.dbs.TagMembers(ctx, updated.ID); err == nil {
		count = len(members)
	}
	writeJSON(w, http.StatusOK, userTag(store.TagSummary{Tag: updated, Count: count}))
}

// handleDeleteTag clears the tag's keyword from every live copy on the server
// through the outbox, then drops the tag and its memberships. Nothing is
// deleted unless every clear fits in the outbox, so a refusal never leaves a
// half-deleted tag. A tag-add still waiting in the queue is not cancelled: if it
// lands after the delete the keyword is an unknown slug, which read-back ignores.
func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tag, err := s.dbs.GetTag(ctx, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "tag")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	keyword := store.TagKeyword(tag.Slug)
	rows, err := s.dbs.KeywordRows(ctx, keyword, "", "", store.MaxQueuedOps+1)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	need := map[string]int{}
	for _, row := range rows {
		need[row.AccountID]++
	}
	for accountID, n := range need {
		queued, err := s.dbs.OutboxByAccount(ctx, accountID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if len(queued)+n > store.MaxQueuedOps {
			writeError(w, http.StatusConflict, "outbox_full",
				"Too many messages carry this tag to clear at once; wait for the queue to drain")
			return
		}
	}
	for _, row := range rows {
		if err := s.enqueueKeywordClear(ctx, row, keyword); err != nil {
			if errors.Is(err, store.ErrOutboxFull) {
				writeError(w, http.StatusConflict, "outbox_full", "There are too many unsent actions; wait for them to finish")
				return
			}
			s.serverError(w, r, err)
			return
		}
	}
	if err := s.dbs.DeleteTag(ctx, tag.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	for accountID := range need {
		s.hintOutbox(accountID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enqueueKeywordClear(ctx context.Context, row store.KeywordRow, keyword string) error {
	_, _, err := s.dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: s.newID(), AccountID: row.AccountID, Kind: store.OutboxFlags,
		ContentKey: row.ContentKey, SourceFolderID: row.FolderID,
		Expect: store.OutboxExpect{FlagsClear: []string{keyword}}, CreatedAt: s.now(),
	})
	return err
}

// tagKeywordAction resolves a tag or untag action to the keyword flag op it is.
// A missing or unknown tag is a refusal the UI can word, not a queued failure.
func (s *Server) tagKeywordAction(ctx context.Context, body api.OutboxAction) (store.OutboxExpect, string, error) {
	if body.TagId == nil || *body.TagId == "" {
		return store.OutboxExpect{}, "unknown_tag", nil
	}
	tag, err := s.dbs.GetTag(ctx, *body.TagId)
	if errors.Is(err, store.ErrNotFound) {
		return store.OutboxExpect{}, "unknown_tag", nil
	}
	if err != nil {
		return store.OutboxExpect{}, "", err
	}
	keyword := []string{store.TagKeyword(tag.Slug)}
	if body.Action == api.OutboxActionTag {
		return store.OutboxExpect{FlagsAdd: keyword}, "", nil
	}
	return store.OutboxExpect{FlagsClear: keyword}, "", nil
}

// clearOtherCopies queues the keyword's removal on every other live copy of the
// message (the same mail kept in two folders shares a content key, N8), so an
// untag does not leave a copy that read-back would later find carrying it.
func (s *Server) clearOtherCopies(ctx context.Context, msg store.Message, expect store.OutboxExpect) error {
	if len(expect.FlagsClear) != 1 {
		return nil
	}
	keyword := expect.FlagsClear[0]
	rows, err := s.dbs.KeywordRows(ctx, keyword, msg.AccountID, msg.ContentKey, store.MaxQueuedOps)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.FolderID == msg.FolderID {
			continue
		}
		if err := s.enqueueKeywordClear(ctx, row, keyword); err != nil {
			return err
		}
	}
	return nil
}

// tagNames returns the first tag name (by name) of each content key of one
// account, for the list and the reader; the contract carries one.
func (s *Server) tagNames(ctx context.Context, accountID string, keys []string) (map[string]string, error) {
	byKey, err := s.dbs.TagsForMessages(ctx, accountID, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(byKey))
	for key, tags := range byKey {
		if len(tags) > 0 {
			out[key] = tags[0].Name
		}
	}
	return out, nil
}
