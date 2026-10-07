package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/compose"
	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxSendBodyBytes bounds a compose request: the body cap plus room for the
// headers and the JSON envelope, so a hostile request cannot grow the parse.
const maxSendBodyBytes = compose.MaxBodyBytes + 64<<10

// maxSendIDBytes bounds the client's idempotency id, which becomes the row's
// primary key and a URL segment.
const maxSendIDBytes = 128

// handleSendMessage queues an outgoing message. The Send button is the explicit
// confirmation (CLAUDE.md rule 6); this handler never submits anything itself.
// It builds the wire and Sent copies, commits the queue row (durable before the
// caller is told "accepted"), and lets the send worker do the SMTP work after
// the undo window. A client-supplied id makes a retried request idempotent.
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSendBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That message is too large to send")
		return
	}
	var req api.SendRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That message is not valid")
		return
	}
	if req.AccountId == "" || req.From == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "A message needs an account and a From address")
		return
	}
	if len(stringOr(req.Id, "")) > maxSendIDBytes {
		writeError(w, http.StatusBadRequest, "bad_request", "That message id is too long")
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

	// A client id is the request's idempotency key: a repeated tap returns the
	// row already queued instead of building and queueing a second one.
	if id := stringOr(req.Id, ""); id != "" {
		existing, err := s.dbs.GetSend(ctx, id)
		switch {
		case err == nil:
			if existing.AccountID != acct.ID {
				s.notFound(w, r, "send")
				return
			}
			writeJSON(w, http.StatusAccepted, s.sendStatus(existing))
			return
		case !errors.Is(err, store.ErrNotFound):
			s.serverError(w, r, err)
			return
		}
	}

	// Send-as is 4e: the From must be the account's own address or one of its
	// configured identities, so nothing can be sent as an arbitrary address.
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

	now := s.now().UTC()
	msgID := s.newMessageID(req.From)
	msg := compose.Message{
		From:       compose.Address{Name: fromName, Address: req.From},
		To:         composeAddresses(req.To),
		Cc:         composeAddresses(stringsOr(req.Cc)),
		Bcc:        composeAddresses(stringsOr(req.Bcc)),
		ReplyTo:    composeAddresses(stringsOr(req.ReplyTo)),
		Subject:    req.Subject,
		Text:       req.Text,
		Markdown:   boolOr(req.Markdown, false),
		InReplyTo:  stringOr(req.InReplyTo, ""),
		References: stringsOr(req.References),
		MessageID:  msgID,
		Date:       now,
	}
	raw, env, err := compose.Build(msg)
	if err != nil {
		var ve *compose.ValidationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, "invalid_message", "Ivy cannot send that message: "+ve.Reason)
			return
		}
		s.serverError(w, r, err)
		return
	}
	// The Sent copy is the same message with Bcc kept; the wire copy has none.
	msg.KeepBcc = true
	sentRaw, _, err := compose.Build(msg)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	delay, err := s.dbs.UndoSendDelay(ctx, acct.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var deadline time.Time
	if delay > 0 {
		deadline = now.Add(time.Duration(delay) * time.Second)
	}
	id := stringOr(req.Id, "")
	if id == "" {
		id = s.newID()
	}
	stored, _, err := s.dbs.EnqueueSend(ctx, store.SendMessage{
		ID: id, AccountID: acct.ID, MessageID: msgID, ContentKey: store.ContentKey(msgID, nil),
		EnvelopeFrom: env.From, Recipients: env.To, WireBody: raw, SentBody: sentRaw,
		Draft: body, UndoDeadline: deadline, CreatedAt: now, UpdatedAt: now,
		// The draft version this send came from, so the worker removes exactly that
		// server copy after the 250 and a failed or undone send keeps it.
		DraftMessageID: stringOr(req.DraftMessageId, ""),
	})
	switch {
	case errors.Is(err, store.ErrSendFull):
		writeError(w, http.StatusConflict, "send_full", "There are too many unsent messages; wait for them to finish")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.hintSend(acct.ID)
	writeJSON(w, http.StatusAccepted, s.sendStatus(stored))
}

// handleUndoSend cancels a queued message before its server-side deadline and
// hands the draft back. At or after the deadline, or once the message left the
// queue, there is no undo.
func (s *Server) handleUndoSend(w http.ResponseWriter, r *http.Request) {
	cancelled, err := s.dbs.CancelSend(r.Context(), r.PathValue("id"), s.now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "send")
		return
	case errors.Is(err, store.ErrSendTooLate):
		writeError(w, http.StatusConflict, "too_late", "This message can no longer be undone")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.hintSend(cancelled.AccountID)
	writeJSON(w, http.StatusOK, s.sendStatus(cancelled))
}

// handleListSends serves the live sends (the undo overlay) and recent terminal
// ones (the Not-sent and history surface).
func (s *Server) handleListSends(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := r.URL.Query().Get("account_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.dbs.SendsByAccount(ctx, accountID, limit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := api.SendList{Active: []api.SendStatus{}, Recent: []api.SendStatus{}}
	for _, m := range rows {
		st := s.sendStatus(m)
		switch m.State {
		case store.SendQueued, store.SendSubmitting, store.SendSubmitted:
			out.Active = append(out.Active, st)
		default:
			out.Recent = append(out.Recent, st)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetSend serves one send in any state, so a screen can watch it settle.
func (s *Server) handleGetSend(w http.ResponseWriter, r *http.Request) {
	m, err := s.dbs.GetSend(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "send")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sendStatus(m))
}

// sendStatus maps a stored row to the contract. Subject and draft come from the
// stored request, so nothing the operator typed is re-derived from the MIME.
func (s *Server) sendStatus(m store.SendMessage) api.SendStatus {
	st := api.SendStatus{
		Id:        m.ID,
		AccountId: m.AccountID,
		State:     api.SendStatusState(m.State),
		To:        append([]string{}, m.Recipients...),
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
	if m.MessageID != "" {
		st.MessageId = &m.MessageID
	}
	if m.EnvelopeFrom != "" {
		st.From = &m.EnvelopeFrom
	}
	if m.Attempts != 0 {
		st.Attempts = &m.Attempts
	}
	if m.LastErrorCode != "" {
		st.LastErrorCode = &m.LastErrorCode
	}
	if m.LastErrorDetail != "" {
		st.LastErrorDetail = &m.LastErrorDetail
	}
	if !m.UndoDeadline.IsZero() {
		st.UndoDeadline = &m.UndoDeadline
	}
	if !m.CompletedAt.IsZero() {
		st.CompletedAt = &m.CompletedAt
	}
	if len(m.Draft) > 0 {
		draft := string(m.Draft)
		st.Draft = &draft
		var req api.SendRequest
		if json.Unmarshal(m.Draft, &req) == nil && req.Subject != "" {
			st.Subject = &req.Subject
		}
	}
	return st
}

// hintSend tells open browsers an outgoing message changed, so the compose
// screen can update its Undo and Not-sent states. A server without a hub is a
// no-op.
func (s *Server) hintSend(accountID string) {
	if s.events == nil {
		return
	}
	s.events.Publish(events.Event{Type: events.SendState, AccountID: accountID})
}

// newMessageID mints the injected Message-ID for one send. The domain comes
// from the From address so the id looks like a real one; it is generated once
// here and reused by every retry and the Sent copy.
func (s *Server) newMessageID(from string) string {
	domain := "ivy.local"
	if _, d, ok := strings.Cut(from, "@"); ok && d != "" {
		domain = d
	}
	return "<" + s.newID() + "@" + domain + ">"
}

func composeAddresses(list []string) []compose.Address {
	if len(list) == 0 {
		return nil
	}
	out := make([]compose.Address, len(list))
	for i, a := range list {
		out[i] = compose.Address{Address: a}
	}
	return out
}

func stringOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

func boolOr(p *bool, fallback bool) bool {
	if p == nil {
		return fallback
	}
	return *p
}

func stringsOr(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}
