package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxOutboxBodyBytes bounds an action request: a message id, an action word and
// an optional folder id, so a hostile body cannot grow the parse.
const maxOutboxBodyBytes = 8 << 10

// handleEnqueueOutbox is the one write path to IMAP. It resolves the reader's
// friendly action to a postcondition, commits the op row (durable before the
// caller is told "accepted"), and lets the outbox worker do the IMAP command.
// Nothing here talks to the server, so a failure past this point rolls back
// through the op's own state, never a DB-only change.
func (s *Server) handleEnqueueOutbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body api.OutboxAction
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOutboxBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That action is not valid")
		return
	}
	msg, err := s.actionTarget(ctx, body.MessageId)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	if errors.Is(err, errNotSynced) {
		writeError(w, http.StatusConflict, "not_synced",
			"Ivy has not seen the message in its new folder yet; try again in a moment")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	kind, expect, code, err := s.outboxAction(ctx, msg, body)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if code != "" {
		writeError(w, http.StatusConflict, code, outboxActionMessage(code))
		return
	}

	stored, _, err := s.dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: s.newID(), AccountID: msg.AccountID, Kind: kind,
		ContentKey: msg.ContentKey, SourceFolderID: msg.FolderID, Expect: expect,
		CreatedAt: s.now(),
	})
	switch {
	case errors.Is(err, store.ErrOutboxFull):
		writeError(w, http.StatusConflict, "outbox_full",
			"There are too many unsent actions; wait for them to finish")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.hintOutbox(msg.AccountID)
	writeJSON(w, http.StatusAccepted, s.outboxItem(ctx, stored))
}

// errNotSynced reports an action on a message the server has moved but sync has
// not yet mirrored in its new folder, so there is no row to act on.
var errNotSynced = errors.New("moved message not mirrored yet")

// actionTarget finds the live row an action acts on. A reader's Undo arrives
// holding the id of the row a finished move hid, so a row hidden as moved is
// followed to the copy in the folder that move delivered it to (never to a
// same-Message-ID copy elsewhere, which the content key alone would also match).
func (s *Server) actionTarget(ctx context.Context, id string) (store.Message, error) {
	msg, err := s.dbs.GetMessage(ctx, id)
	if !errors.Is(err, store.ErrNotFound) {
		return msg, err
	}
	hidden, err := s.dbs.GetMessageIncludingHidden(ctx, id)
	if err != nil || hidden.DisabledReason != store.DisabledMoved {
		return store.Message{}, store.ErrNotFound
	}
	dest, err := s.dbs.SettledMoveDestination(ctx, hidden.AccountID, hidden.ContentKey, hidden.FolderID)
	if err != nil {
		return store.Message{}, err
	}
	rowID, _, err := s.dbs.MessageRowRef(ctx, hidden.AccountID, hidden.ContentKey, dest)
	if errors.Is(err, store.ErrNotFound) {
		return store.Message{}, errNotSynced
	}
	if err != nil {
		return store.Message{}, err
	}
	msg, err = s.dbs.GetMessage(ctx, rowID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Message{}, errNotSynced
	}
	return msg, err
}

// outboxAction maps a reader action to an op kind and its postcondition. It
// resolves move destinations by folder role, so "archive" is the account's
// Archive folder and never a guessed name. A non-empty code is a 409 reason.
func (s *Server) outboxAction(ctx context.Context, msg store.Message, body api.OutboxAction) (kind string, expect store.OutboxExpect, code string, err error) {
	switch body.Action {
	case api.OutboxActionArchive:
		return s.moveToRole(ctx, msg, store.RoleArchive, "no_archive_folder")
	case api.OutboxActionTrash:
		return s.moveToRole(ctx, msg, store.RoleTrash, "no_trash_folder")
	case api.OutboxActionSpam:
		return s.moveToRole(ctx, msg, store.RoleJunk, "no_junk_folder")
	case api.OutboxActionNotJunk:
		return s.moveToRole(ctx, msg, store.RoleInbox, "no_inbox_folder")
	case api.OutboxActionFlag:
		return store.OutboxFlags, store.OutboxExpect{FlagsAdd: []string{`\Flagged`}}, "", nil
	case api.OutboxActionUnflag:
		return store.OutboxFlags, store.OutboxExpect{FlagsClear: []string{`\Flagged`}}, "", nil
	case api.OutboxActionSeen:
		return store.OutboxFlags, store.OutboxExpect{FlagsAdd: []string{`\Seen`}}, "", nil
	case api.OutboxActionUnseen:
		return store.OutboxFlags, store.OutboxExpect{FlagsClear: []string{`\Seen`}}, "", nil
	case api.OutboxActionMove:
		if body.DestinationFolderId == nil || *body.DestinationFolderId == "" {
			return "", store.OutboxExpect{}, "bad_destination", nil
		}
		dest, err := s.dbs.GetFolderByID(ctx, *body.DestinationFolderId)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return "", store.OutboxExpect{}, "bad_destination", nil
		case err != nil:
			return "", store.OutboxExpect{}, "", err
		case dest.AccountID != msg.AccountID, !dest.GoneAt.IsZero():
			return "", store.OutboxExpect{}, "bad_destination", nil
		case dest.ID == msg.FolderID:
			return "", store.OutboxExpect{}, "same_folder", nil
		}
		return store.OutboxMove, store.OutboxExpect{DestFolderID: dest.ID}, "", nil
	case api.OutboxActionExpunge:
		folder, err := s.dbs.GetFolderByID(ctx, msg.FolderID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return "", store.OutboxExpect{}, "not_trash", nil
		case err != nil:
			return "", store.OutboxExpect{}, "", err
		case folder.Role != store.RoleTrash:
			return "", store.OutboxExpect{}, "not_trash", nil
		}
		return store.OutboxExpunge, store.OutboxExpect{}, "", nil
	default:
		return "", store.OutboxExpect{}, "unknown_action", nil
	}
}

// moveToRole resolves a role folder and builds the move's postcondition.
func (s *Server) moveToRole(ctx context.Context, msg store.Message, role, code string) (string, store.OutboxExpect, string, error) {
	folder, err := s.dbs.FolderByRole(ctx, msg.AccountID, role)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "", store.OutboxExpect{}, code, nil
	case err != nil:
		return "", store.OutboxExpect{}, "", err
	case folder.ID == msg.FolderID:
		return "", store.OutboxExpect{}, "same_folder", nil
	}
	return store.OutboxMove, store.OutboxExpect{DestFolderID: folder.ID}, "", nil
}

// outboxActionMessage is the human copy behind a 409 action code.
func outboxActionMessage(code string) string {
	switch code {
	case "no_archive_folder":
		return "This account has no Archive folder"
	case "no_trash_folder":
		return "This account has no Trash folder"
	case "no_junk_folder":
		return "This account has no Junk folder"
	case "no_inbox_folder":
		return "This account has no Inbox"
	case "bad_destination":
		return "That folder is not available"
	case "same_folder":
		return "The message is already in that folder"
	case "not_trash":
		return "Only the Trash folder may be emptied"
	default:
		return "That action is not available"
	}
}

// handleListOutbox serves the live ops (the overlay) and recent terminal ones
// (the history and retry surface). account_id is optional: empty means every
// account, for the combined view.
func (s *Server) handleListOutbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := r.URL.Query().Get("account_id")
	active, err := s.dbs.OutboxByAccount(ctx, accountID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	recent, err := s.dbs.OutboxHistory(ctx, accountID, 50)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := api.OutboxList{Active: make([]api.OutboxItem, 0, len(active)), Recent: make([]api.OutboxItem, 0, len(recent))}
	for _, op := range active {
		out.Active = append(out.Active, s.outboxItem(ctx, op))
	}
	for _, op := range recent {
		out.Recent = append(out.Recent, s.outboxItem(ctx, op))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRetryOutbox puts a failed op back in the queue immediately.
func (s *Server) handleRetryOutbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	op, err := s.dbs.GetOutbox(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "action")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	case op.State != store.OutboxFailed:
		writeError(w, http.StatusConflict, "not_failed", "That action has not failed")
		return
	}
	if err := s.dbs.RetryOutbox(ctx, id, s.now()); err != nil {
		s.serverError(w, r, err)
		return
	}
	updated, err := s.dbs.GetOutbox(ctx, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.hintOutbox(op.AccountID)
	writeJSON(w, http.StatusOK, s.outboxItem(ctx, updated))
}

// handleDismissOutbox removes a terminal op from the history. A live op is
// refused; it is never silently dropped.
func (s *Server) handleDismissOutbox(w http.ResponseWriter, r *http.Request) {
	err := s.dbs.DeleteOutbox(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "action")
		return
	case errors.Is(err, store.ErrOutboxLive):
		writeError(w, http.StatusConflict, "not_terminal", "That action is still running")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// outboxItem renders one op for the client, resolving the mirror row id from the
// (content key, folder) the op stores.
func (s *Server) outboxItem(ctx context.Context, op store.OutboxOp) api.OutboxItem {
	item := api.OutboxItem{
		Id: op.ID, AccountId: op.AccountID, Kind: api.OutboxItemKind(op.Kind),
		State: api.OutboxItemState(op.State), Attempts: op.Attempts,
		CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt,
	}
	if op.Seq != 0 {
		seq := int(op.Seq)
		item.Seq = &seq
	}
	if op.SourceFolderID != "" {
		item.SourceFolderId = &op.SourceFolderID
	}
	if op.Expect.DestFolderID != "" {
		item.DestinationFolderId = &op.Expect.DestFolderID
	}
	if len(op.Expect.FlagsAdd) > 0 {
		add := op.Expect.FlagsAdd
		item.FlagsAdd = &add
	}
	if len(op.Expect.FlagsClear) > 0 {
		clear := op.Expect.FlagsClear
		item.FlagsClear = &clear
	}
	if op.LastErrorCode != "" {
		item.LastErrorCode = &op.LastErrorCode
	}
	if op.LastErrorDetail != "" {
		item.LastErrorDetail = &op.LastErrorDetail
	}
	if !op.CompletedAt.IsZero() {
		done := op.CompletedAt
		item.CompletedAt = &done
	}
	if id, _, err := s.dbs.MessageRowRef(ctx, op.AccountID, op.ContentKey, op.SourceFolderID); err == nil {
		item.MessageId = id
	}
	return item
}

// hintOutbox tells open browsers an outbox op changed, so they refetch the
// overlay. A server without a hub (a test, or a build that never wired events)
// is a no-op.
func (s *Server) hintOutbox(accountID string) {
	if s.events == nil {
		return
	}
	s.events.Publish(events.Event{Type: events.OutboxState, AccountID: accountID})
}
