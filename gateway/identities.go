package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/compose"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxIdentityBodyBytes bounds an identity request: an address, a display name
// and a signature, with room for the JSON envelope.
const maxIdentityBodyBytes = 16 << 10

// maxIdentityNameRunes bounds a display name. The builder RFC 2047 encodes it,
// so bytes are not the concern; the operator's name is short.
const maxIdentityNameRunes = 120

// handleListIdentities serves the account's sendable addresses: the stored rows
// merged with a synthetic primary row for the account's own address.
func (s *Server) handleListIdentities(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.accountOrNotFound(w, r)
	if !ok {
		return
	}
	views, _, _, err := s.accountIdentities(r.Context(), acct)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if views == nil {
		views = []api.Identity{}
	}
	writeJSON(w, http.StatusOK, api.IdentityList{Identities: views})
}

// handleSaveIdentity creates or edits the identity at an address. Editing the
// primary identity is the same call as adding an alias, because the store keys
// on (account, address); nothing can change the account's own address.
func (s *Server) handleSaveIdentity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	acct, ok := s.accountOrNotFound(w, r)
	if !ok {
		return
	}
	var body api.IdentityInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIdentityBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That identity is not valid")
		return
	}
	name := strings.TrimSpace(stringOr(body.Name, ""))
	if utf8.RuneCountInString(name) > maxIdentityNameRunes {
		writeError(w, http.StatusBadRequest, "bad_request", "That name is too long")
		return
	}
	// The builder refuses these at send time; refusing here keeps a bad name from
	// being saved and then breaking every send as this address.
	if hasBadHeaderBytes(name) {
		writeError(w, http.StatusBadRequest, "bad_request", "That name is not valid")
		return
	}
	stored, err := s.dbs.UpsertIdentity(ctx, store.IdentityInput{
		ID: s.newID(), AccountID: acct.ID, Address: strings.TrimSpace(body.Address),
		DisplayName: name, Signature: stringOr(body.Signature, ""), Now: s.now().UTC(),
	})
	switch {
	case errors.Is(err, store.ErrIdentityAddress):
		writeError(w, http.StatusBadRequest, "bad_request", "That is not a plain email address Ivy can send as")
		return
	case errors.Is(err, store.ErrIdentitySignature):
		writeError(w, http.StatusBadRequest, "bad_request", "That signature is too long")
		return
	case errors.Is(err, store.ErrIdentityLimit):
		writeError(w, http.StatusConflict, "too_many_identities", "There are too many sending addresses; remove one first")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, identityView(acct, stored))
}

// handleDeleteIdentity removes a stored alias. The account's own address is
// primary and is refused here, because only this layer knows which address that
// is.
func (s *Server) handleDeleteIdentity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	acct, ok := s.accountOrNotFound(w, r)
	if !ok {
		return
	}
	in, err := s.dbs.GetIdentityByID(ctx, r.PathValue("identityId"))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	if errors.Is(err, store.ErrNotFound) || in.AccountID != acct.ID {
		s.notFound(w, r, "identity")
		return
	}
	if strings.EqualFold(in.Address, acct.Address) {
		writeError(w, http.StatusConflict, "primary_identity", "The account's own address cannot be removed")
		return
	}
	if err := s.dbs.DeleteIdentity(ctx, in.ID); errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "identity")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleReplyPrefill computes a reply or reply-all for one message. The compose
// screen may change any of it before sending.
func (s *Server) handleReplyPrefill(w http.ResponseWriter, r *http.Request) {
	p, accountID, err := s.messagePrefill(r, r.URL.Query().Get("all") == "true", false)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "message")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prefillView(p, accountID))
}

// handleForwardPrefill computes a forward for one message.
func (s *Server) handleForwardPrefill(w http.ResponseWriter, r *http.Request) {
	p, accountID, err := s.messagePrefill(r, false, true)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "message")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prefillView(p, accountID))
}

// messagePrefill reads one message and its account's identities, then runs the
// pure reply or forward logic. It also reports the message's account, so the
// compose screen loads that account's identities rather than the default's.
func (s *Server) messagePrefill(r *http.Request, all, forward bool) (compose.Prefill, string, error) {
	ctx := r.Context()
	m, err := s.dbs.GetMessage(ctx, r.PathValue("id"))
	if err != nil {
		return compose.Prefill{}, "", err
	}
	acct, err := s.dbs.GetAccount(ctx, m.AccountID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// A message whose account is gone still gets a default identity, so a
		// reply offers the account's address rather than nothing.
		acct = store.Account{ID: m.AccountID}
	case err != nil:
		return compose.Prefill{}, "", err
	}
	_, refs, def, err := s.accountIdentities(ctx, acct)
	if err != nil {
		return compose.Prefill{}, "", err
	}
	in := incomingFrom(m)
	if forward {
		return compose.Forward(in, refs, def), m.AccountID, nil
	}
	return compose.Reply(in, refs, def, all), m.AccountID, nil
}

// accountOrNotFound loads the account in the path or writes a 404.
func (s *Server) accountOrNotFound(w http.ResponseWriter, r *http.Request) (store.Account, bool) {
	acct, err := s.dbs.GetAccount(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "account")
		return store.Account{}, false
	case err != nil:
		s.serverError(w, r, err)
		return store.Account{}, false
	}
	return acct, true
}

// accountIdentities merges the account's stored identities with a synthetic
// primary for its own address. It returns the contract views, the refs the reply
// logic consumes, and the default identity.
func (s *Server) accountIdentities(ctx context.Context, acct store.Account) ([]api.Identity, []compose.IdentityRef, compose.IdentityRef, error) {
	stored, err := s.dbs.ListIdentities(ctx, acct.ID)
	if err != nil {
		return nil, nil, compose.IdentityRef{}, err
	}
	var (
		views      []api.Identity
		refs       []compose.IdentityRef
		primary    = compose.IdentityRef{Address: acct.Address, DisplayName: acct.DisplayName}
		hasPrimary bool
	)
	for _, in := range stored {
		ref := compose.IdentityRef{Address: in.Address, DisplayName: in.DisplayName, Signature: in.Signature}
		views = append(views, identityView(acct, in))
		refs = append(refs, ref)
		if strings.EqualFold(in.Address, acct.Address) {
			hasPrimary = true
			primary = ref
		}
	}
	if !hasPrimary {
		views = append([]api.Identity{{
			AccountId: acct.ID, Address: acct.Address, Name: acct.DisplayName, Primary: true,
		}}, views...)
		refs = append([]compose.IdentityRef{primary}, refs...)
	}
	return views, refs, primary, nil
}

// identityView marks the account's own address as primary so the settings editor
// knows not to offer its removal.
func identityView(acct store.Account, in store.Identity) api.Identity {
	return api.Identity{
		Id: in.ID, AccountId: in.AccountID, Address: in.Address,
		Name: in.DisplayName, Signature: in.Signature,
		Primary: strings.EqualFold(in.Address, acct.Address),
	}
}

// identityForFrom reports whether an account may send as an address, and the
// identity to use (display name and signature). The account's own address is
// always sendable, even before a stored row exists for it (4e).
func (s *Server) identityForFrom(ctx context.Context, acct store.Account, from string) (compose.IdentityRef, bool, error) {
	if strings.EqualFold(from, acct.Address) {
		ref, err := s.primaryIdentity(ctx, acct)
		return ref, err == nil, err
	}
	in, err := s.dbs.GetIdentity(ctx, acct.ID, from)
	if errors.Is(err, store.ErrNotFound) {
		return compose.IdentityRef{}, false, nil
	}
	if err != nil {
		return compose.IdentityRef{}, false, err
	}
	return compose.IdentityRef{Address: in.Address, DisplayName: in.DisplayName, Signature: in.Signature}, true, nil
}

// primaryIdentity is the account's own identity, using a stored row's display
// name and signature when one exists.
func (s *Server) primaryIdentity(ctx context.Context, acct store.Account) (compose.IdentityRef, error) {
	in, err := s.dbs.GetIdentity(ctx, acct.ID, acct.Address)
	if errors.Is(err, store.ErrNotFound) {
		return compose.IdentityRef{Address: acct.Address, DisplayName: acct.DisplayName}, nil
	}
	if err != nil {
		return compose.IdentityRef{}, err
	}
	return compose.IdentityRef{Address: in.Address, DisplayName: in.DisplayName, Signature: in.Signature}, nil
}

// incomingFrom projects a stored message onto the pure reply logic's input. The
// stored References header is a space-joined string; it is split back into ids.
func incomingFrom(m store.Message) compose.Incoming {
	return compose.Incoming{
		From:        storeComposeAddresses([]store.Address{m.From}),
		To:          storeComposeAddresses(m.To),
		Cc:          storeComposeAddresses(m.CC),
		ReplyTo:     storeComposeAddresses(m.ReplyTo),
		DeliveredTo: storeComposeAddresses(m.DeliveredTo),
		Subject:     m.Subject,
		Date:        m.Date,
		MessageID:   m.MessageID,
		References:  strings.Fields(m.References),
		Text:        m.BodyText,
	}
}

func storeComposeAddresses(list []store.Address) []compose.Address {
	if len(list) == 0 {
		return nil
	}
	out := make([]compose.Address, 0, len(list))
	for _, a := range list {
		if a.Address == "" {
			continue
		}
		out = append(out, compose.Address{Name: a.Name, Address: a.Address})
	}
	return out
}

func prefillView(p compose.Prefill, accountID string) api.ComposePrefill {
	v := api.ComposePrefill{
		AccountId:  accountID,
		From:       p.From.Address,
		To:         prefillAddresses(p.To),
		Cc:         prefillAddresses(p.Cc),
		Subject:    p.Subject,
		Text:       p.Text,
		References: nonNilStrings(p.References),
	}
	if p.From.Name != "" {
		v.FromName = strp(p.From.Name)
	}
	if p.InReplyTo != "" {
		v.InReplyTo = strp(p.InReplyTo)
	}
	if p.ReplyTarget != "" {
		v.ReplyTarget = strp(p.ReplyTarget)
	}
	if p.MissingIdentity != "" {
		v.MissingIdentity = strp(p.MissingIdentity)
	}
	return v
}

func prefillAddresses(list []compose.Address) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.Address
	}
	return out
}

func nonNilStrings(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func strp(s string) *string { return &s }
