package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/compose"
	"github.com/AutumnsGrove/Ivy/mime"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxDraftBodyBytes bounds a draft save: the same body cap as a send plus room
// for the headers and JSON envelope.
const maxDraftBodyBytes = compose.MaxBodyBytes + 64<<10

// maxDraftIDBytes bounds a client-supplied draft or version id.
const maxDraftIDBytes = 128

// maxDraftResumeBytes bounds the raw bytes a server-only draft is parsed from,
// so resuming a draft cannot grow the request past a defined size.
const maxDraftResumeBytes = 2 << 20

// handleListDrafts merges the local draft heads with the account's mirrored
// Drafts folder. The mirror is server truth (a draft made in Apple Mail shows),
// and the local head gives an autosave immediate feedback. Newest first.
func (s *Server) handleListDrafts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := r.URL.Query().Get("account_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	locals, err := s.dbs.LiveDraftHeads(ctx, accountID, limit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := api.DraftList{Drafts: []api.DraftSummary{}}
	seen := map[string]bool{}
	for _, d := range locals {
		out.Drafts = append(out.Drafts, draftSummaryLocal(d))
		seen[d.ContentKey] = true
	}
	if accountID != "" {
		server, err := s.dbs.DraftsInFolder(ctx, accountID, limit)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		for _, row := range server {
			if seen[row.ContentKey] {
				continue
			}
			out.Drafts = append(out.Drafts, draftSummaryServer(row))
		}
	}
	sort.SliceStable(out.Drafts, func(i, j int) bool {
		return out.Drafts[i].UpdatedAt.After(out.Drafts[j].UpdatedAt)
	})
	if len(out.Drafts) > store.MaxDraftListLimit {
		out.Drafts = out.Drafts[:store.MaxDraftListLimit]
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSaveDraft commits one immutable version and the outbox op that files it.
// `draftId` plus `baseVersion` give optimistic concurrency: a stale save is a
// conflict carrying the newer content, so a losing tab can be told.
func (s *Server) handleSaveDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDraftBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That draft is too large to save")
		return
	}
	var req api.DraftRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That draft is not valid")
		return
	}
	if req.AccountId == "" || req.From == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "A draft needs an account and a From address")
		return
	}
	if len(stringOr(req.Id, "")) > maxDraftIDBytes || len(stringOr(req.DraftId, "")) > maxDraftIDBytes {
		writeError(w, http.StatusBadRequest, "bad_request", "That draft id is too long")
		return
	}
	acct, err := s.dbs.GetAccount(ctx, req.AccountId)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "account")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	// Send-as is 4e: a draft may only be written as the account's own address or
	// one of its configured identities.
	fromRef, ok, err := s.identityForFrom(ctx, acct, req.From)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_from", "That address is not this account's")
		return
	}
	fromName := stringOr(req.FromName, "")
	if fromName == "" {
		fromName = fromRef.DisplayName
	}
	atts, err := s.resolveComposeAttachments(ctx, acct.ID, composeAttachments(req.Attachments))
	if err != nil {
		var ae *attachmentError
		if errors.As(err, &ae) {
			writeError(w, http.StatusBadRequest, "invalid_message", ae.Error())
			return
		}
		s.serverError(w, r, err)
		return
	}
	draftsFolder, err := s.dbs.FolderByRole(ctx, acct.ID, store.RoleDrafts)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusConflict, "no_drafts_folder", "This mailbox has no Drafts folder")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	now := s.now().UTC()
	msgID := s.newMessageID(req.From)
	// A draft keeps Bcc so it round-trips through the server copy; the wire copy
	// built at send time is the one that strips it.
	raw, _, err := compose.Build(compose.Message{
		From:        compose.Address{Name: fromName, Address: req.From},
		To:          composeAddresses(req.To),
		Cc:          composeAddresses(stringsOr(req.Cc)),
		Bcc:         composeAddresses(stringsOr(req.Bcc)),
		ReplyTo:     composeAddresses(stringsOr(req.ReplyTo)),
		Subject:     stringOr(req.Subject, ""),
		Text:        req.Text,
		Markdown:    boolOr(req.Markdown, false),
		InReplyTo:   stringOr(req.InReplyTo, ""),
		References:  stringsOr(req.References),
		MessageID:   msgID,
		Date:        now,
		KeepBcc:     true,
		Attachments: atts,
	})
	if err != nil {
		var ve *compose.ValidationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, "invalid_message", "Ivy cannot save that draft: "+ve.Reason)
			return
		}
		s.serverError(w, r, err)
		return
	}

	draftID := stringOr(req.DraftId, "")
	if draftID == "" {
		draftID = s.newID()
	}
	versionID := stringOr(req.Id, "")
	if versionID == "" {
		versionID = s.newID()
	}
	base := 0
	if req.BaseVersion != nil {
		base = *req.BaseVersion
	}
	stored, err := s.dbs.SaveDraft(ctx, store.SaveDraftInput{
		ID: versionID, DraftID: draftID, AccountID: acct.ID,
		DestFolderID: draftsFolder.ID, MessageID: msgID,
		Subject: stringOr(req.Subject, ""), To: req.To,
		Compose: body, Body: raw, BaseVersion: base, OpID: s.newID(), Now: now,
	})
	switch {
	case errors.Is(err, store.ErrDraftConflict):
		// stored is the current head; the loser is given the newer content.
		res, rerr := s.draftResume(ctx, stored)
		if rerr != nil {
			s.serverError(w, r, rerr)
			return
		}
		writeJSON(w, http.StatusConflict, res)
		return
	case errors.Is(err, store.ErrOutboxFull):
		writeError(w, http.StatusConflict, "outbox_full", "There is too much waiting to save this draft; try again shortly")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.hintSend(acct.ID)
	writeJSON(w, http.StatusOK, draftSummaryLocal(stored))
}

// handleGetDraft resumes a draft. A local draft returns the exact compose
// request; a draft created in another client is parsed back from the mirror.
func (s *Server) handleGetDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	accountID := r.URL.Query().Get("account_id")
	if accountID != "" {
		head, err := s.dbs.DraftHead(ctx, accountID, id)
		switch {
		case err == nil && head.State != store.DraftDiscarded && head.State != store.DraftSent:
			res, err := s.draftResume(ctx, head)
			if err != nil {
				s.serverError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, res)
			return
		case err != nil && !errors.Is(err, store.ErrNotFound):
			s.serverError(w, r, err)
			return
		}
	}
	msg, err := s.dbs.GetMessage(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "draft")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	res, err := s.draftResumeServer(ctx, msg)
	switch {
	case errors.Is(err, errDraftTooLarge):
		writeError(w, http.StatusConflict, "draft_too_large", "That draft is too large to open here")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleDeleteDraft removes a local draft (marking the head and queueing an
// expunge) or, for a server-only draft, queues the expunge directly.
func (s *Server) handleDeleteDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	accountID := r.URL.Query().Get("account_id")
	if accountID != "" {
		_, err := s.dbs.DiscardDraft(ctx, accountID, id, s.newID(), s.now().UTC())
		switch {
		case err == nil:
			s.hintSend(accountID)
			w.WriteHeader(http.StatusNoContent)
			return
		case !errors.Is(err, store.ErrNotFound):
			s.serverError(w, r, err)
			return
		}
	}
	msg, err := s.dbs.GetMessage(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "draft")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	_, _, err = s.dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: s.newID(), AccountID: msg.AccountID, Kind: store.OutboxDraft,
		ContentKey: msg.ContentKey, SourceFolderID: msg.FolderID,
		Expect: store.OutboxExpect{
			DestFolderID: msg.FolderID,
			Supersedes:   []string{msg.MessageID},
			Remove:       true,
		},
		CreatedAt: s.now().UTC(),
	})
	switch {
	case errors.Is(err, store.ErrOutboxFull):
		writeError(w, http.StatusConflict, "outbox_full", "There is too much waiting; try again shortly")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.hintSend(msg.AccountID)
	w.WriteHeader(http.StatusNoContent)
}

func draftSummaryLocal(d store.Draft) api.DraftSummary {
	id, draftID := d.DraftID, d.DraftID
	sum := api.DraftSummary{
		Id: id, DraftId: &draftID, AccountId: d.AccountID, Version: d.Version,
		Subject: d.Subject, To: append([]string{}, d.To...),
		UpdatedAt: d.UpdatedAt, Source: api.DraftSourceLocal,
	}
	if d.MessageID != "" {
		mid := d.MessageID
		sum.MessageId = &mid
	}
	return sum
}

func draftSummaryServer(row store.DraftServerRow) api.DraftSummary {
	sum := api.DraftSummary{
		Id: row.ID, AccountId: row.AccountID, Version: 0,
		Subject: row.Subject, To: addressStrings(row.To),
		UpdatedAt: row.Date, Source: api.DraftSourceServer,
	}
	if row.MessageID != "" {
		mid := row.MessageID
		sum.MessageId = &mid
	}
	return sum
}

// draftResume rebuilds a resume payload from a local version. The stored compose
// request is returned verbatim, so nothing the operator typed is re-derived;
// attachments are re-staged from the stored body so the screen gets fresh ids.
func (s *Server) draftResume(ctx context.Context, d store.Draft) (api.DraftResume, error) {
	id, draftID := d.DraftID, d.DraftID
	res := api.DraftResume{
		Id: id, DraftId: &draftID, AccountId: d.AccountID, Version: d.Version,
		Source: api.DraftSourceLocal, To: append([]string{}, d.To...),
	}
	if d.MessageID != "" {
		mid := d.MessageID
		res.MessageId = &mid
	}
	var req api.DraftRequest
	if json.Unmarshal(d.Compose, &req) == nil {
		if req.From != "" {
			from := req.From
			res.From = &from
		}
		res.FromName = req.FromName
		res.Subject = req.Subject
		res.Cc = req.Cc
		res.Bcc = req.Bcc
		res.ReplyTo = req.ReplyTo
		res.Markdown = req.Markdown
		res.InReplyTo = req.InReplyTo
		res.References = req.References
		res.Text = req.Text
	}
	atts, err := s.materializeAttachments(ctx, d.AccountID, d.Body)
	if err != nil {
		return api.DraftResume{}, err
	}
	if len(atts) > 0 {
		res.Attachments = &atts
	}
	return res, nil
}

// draftResumeServer parses a mirrored draft back into compose fields. Headers
// come from the mirror row; the text comes from the stored raw message, parsed
// without the inbound sanitiser (compose is the operator's own content).
func (s *Server) draftResumeServer(ctx context.Context, msg store.Message) (api.DraftResume, error) {
	res := api.DraftResume{
		Id: msg.ID, AccountId: msg.AccountID, Version: 0,
		Source: api.DraftSourceServer, To: addressStrings(msg.To),
	}
	if msg.MessageID != "" {
		mid := msg.MessageID
		res.MessageId = &mid
	}
	if msg.Subject != "" {
		subject := msg.Subject
		res.Subject = &subject
	}
	if cc := addressStrings(msg.CC); len(cc) > 0 {
		res.Cc = &cc
	}
	if rt := addressStrings(msg.ReplyTo); len(rt) > 0 {
		res.ReplyTo = &rt
	}
	if msg.InReplyTo != "" {
		irt := msg.InReplyTo
		res.InReplyTo = &irt
	}
	if refs := strings.Fields(msg.References); len(refs) > 0 {
		res.References = &refs
	}
	if msg.Size > maxDraftResumeBytes {
		return api.DraftResume{}, errDraftTooLarge
	}
	raw := msg.RawBlob
	if len(raw) == 0 {
		reader, err := s.dbs.MessageRawReader(ctx, msg.ID)
		if err != nil {
			return api.DraftResume{}, err
		}
		defer func() { _ = reader.Close() }()
		if raw, err = io.ReadAll(io.LimitReader(reader, maxDraftResumeBytes)); err != nil {
			return api.DraftResume{}, err
		}
	}
	res.Text = mime.Parse(raw).Text
	atts, err := s.materializeAttachments(ctx, msg.AccountID, raw)
	if err != nil {
		return api.DraftResume{}, err
	}
	if len(atts) > 0 {
		res.Attachments = &atts
	}
	return res, nil
}

var errDraftTooLarge = errors.New("draft is too large to resume")

func addressStrings(list []store.Address) []string {
	if len(list) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		if a.Address != "" {
			out = append(out, a.Address)
		}
	}
	return out
}
