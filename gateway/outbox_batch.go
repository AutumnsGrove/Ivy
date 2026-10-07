package gateway

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

const (
	// maxBatchMessages bounds one selection. It is above a loaded page, and below
	// the per-account queue cap, so a batch that fits the cap is the common case
	// and one that cannot is refused whole.
	maxBatchMessages = 200
	// maxBatchBodyBytes holds 200 ids with room to spare, so a hostile body
	// cannot grow the parse.
	maxBatchBodyBytes = 32 << 10
)

// batchActions are the actions a selection can take. Expunge is deliberately
// absent: erasing is Empty Trash's own confirmed path.
var batchActions = map[api.OutboxBatchAction]api.OutboxActionAction{
	api.OutboxBatchActionArchive: api.OutboxActionArchive,
	api.OutboxBatchActionTrash:   api.OutboxActionTrash,
	api.OutboxBatchActionSpam:    api.OutboxActionSpam,
	api.OutboxBatchActionNotJunk: api.OutboxActionNotJunk,
	api.OutboxBatchActionFlag:    api.OutboxActionFlag,
	api.OutboxBatchActionUnflag:  api.OutboxActionUnflag,
	api.OutboxBatchActionSeen:    api.OutboxActionSeen,
	api.OutboxBatchActionUnseen:  api.OutboxActionUnseen,
	api.OutboxBatchActionMove:    api.OutboxActionMove,
	api.OutboxBatchActionTag:     api.OutboxActionTag,
	api.OutboxBatchActionUntag:   api.OutboxActionUntag,
}

// handleEnqueueOutboxBatch applies one action to a selection. Every message is
// resolved exactly as a single action would be (same target, same postcondition),
// the ops are committed in one transaction, and a message that cannot take the
// action is reported with its reason instead of failing the rest. It is still one
// op per message through the outbox, never a second write path.
func (s *Server) handleEnqueueOutboxBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body api.OutboxBatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBatchBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That selection is not valid")
		return
	}
	action, ok := batchActions[body.Action]
	switch {
	case !ok:
		writeError(w, http.StatusBadRequest, "unknown_action", "That action is not available for a selection")
		return
	case len(body.MessageIds) == 0:
		writeError(w, http.StatusBadRequest, "bad_request", "Nothing is selected")
		return
	case len(body.MessageIds) > maxBatchMessages:
		writeError(w, http.StatusBadRequest, "batch_too_large", "Select fewer messages at once")
		return
	}

	var (
		ops     []store.OutboxOp
		primary []int // the index in ops of each message's own op, in order
		skipped = []api.OutboxSkipped{}
		seen    = make(map[string]bool, len(body.MessageIds))
		accts   = map[string]bool{}
	)
	skip := func(id, code, message string) {
		skipped = append(skipped, api.OutboxSkipped{MessageId: id, Code: code, Message: message})
	}
	for _, id := range body.MessageIds {
		if seen[id] {
			continue
		}
		seen[id] = true

		msg, err := s.actionTarget(ctx, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			skip(id, "not_found", "That message is gone")
			continue
		case errors.Is(err, errNotSynced):
			skip(id, "not_synced", "Ivy has not seen that message in its new folder yet")
			continue
		case err != nil:
			s.serverError(w, r, err)
			return
		}
		single := api.OutboxAction{
			MessageId: id, Action: action, DestinationFolderId: body.DestinationFolderId, TagId: body.TagId,
		}
		kind, expect, code, err := s.outboxAction(ctx, msg, single)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if code != "" {
			skip(id, code, outboxActionMessage(code))
			continue
		}
		primary = append(primary, len(ops))
		ops = append(ops, store.OutboxOp{
			ID: s.newID(), AccountID: msg.AccountID, Kind: kind,
			ContentKey: msg.ContentKey, SourceFolderID: msg.FolderID, Expect: expect,
			CreatedAt: s.now(),
		})
		accts[msg.AccountID] = true

		// Untag clears the keyword from every live copy, as the single action does;
		// the extra ops join the same transaction so the batch stays whole.
		if action == api.OutboxActionUntag && len(expect.FlagsClear) == 1 {
			keyword := expect.FlagsClear[0]
			rows, err := s.dbs.KeywordRows(ctx, keyword, msg.AccountID, msg.ContentKey, store.MaxQueuedOps)
			if err != nil {
				s.serverError(w, r, err)
				return
			}
			for _, row := range rows {
				if row.FolderID == msg.FolderID {
					continue
				}
				ops = append(ops, store.OutboxOp{
					ID: s.newID(), AccountID: row.AccountID, Kind: store.OutboxFlags,
					ContentKey: row.ContentKey, SourceFolderID: row.FolderID,
					Expect: store.OutboxExpect{FlagsClear: []string{keyword}}, CreatedAt: s.now(),
				})
			}
		}
	}

	items := []api.OutboxItem{}
	if len(ops) > 0 {
		stored, err := s.dbs.EnqueueOutboxBatch(ctx, ops)
		switch {
		case errors.Is(err, store.ErrOutboxFull):
			writeError(w, http.StatusConflict, "outbox_full",
				"There are too many unsent actions; wait for them to finish")
			return
		case err != nil:
			s.serverError(w, r, err)
			return
		}
		for _, i := range primary {
			items = append(items, s.outboxItem(ctx, stored[i]))
		}
		for acct := range accts {
			s.hintOutbox(acct)
		}
	}
	writeJSON(w, http.StatusAccepted, api.OutboxBatchResult{Ops: items, Skipped: skipped})
}
