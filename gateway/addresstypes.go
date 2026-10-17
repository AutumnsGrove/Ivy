package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxAddressTypeBodyBytes bounds a request: an address, a kind and a flag.
const maxAddressTypeBodyBytes = 4 << 10

// handleListAddressTypes serves the account's marked addresses; every other address is
// normal and is not listed.
func (s *Server) handleListAddressTypes(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.accountOrNotFound(w, r)
	if !ok {
		return
	}
	stored, err := s.dbs.ListAddressTypes(r.Context(), acct.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := api.AddressTypeList{Types: make([]api.AddressType, 0, len(stored))}
	for _, t := range stored {
		out.Types = append(out.Types, addressTypeView(t.Address, t.Kind, t.LLMOn))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSaveAddressType marks an address, changes its mark or (kind normal) clears it.
// A security address starts with models off, and the flag means nothing for any other
// kind, so it is stored off there: only a security address can ever let a model read.
func (s *Server) handleSaveAddressType(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.accountOrNotFound(w, r)
	if !ok {
		return
	}
	var body api.AddressTypeInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAddressTypeBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That address setting is not valid")
		return
	}
	kind := store.MailType(body.Kind)
	llmOn := kind == store.MailSecurity && body.Llm != nil && *body.Llm
	address := strings.TrimSpace(body.Address)
	err := s.dbs.SetAddressType(r.Context(), store.AddressTypeInput{
		AccountID: acct.ID, Address: address, Kind: kind, LLMOn: llmOn, Now: s.now().UTC(),
	})
	switch {
	case errors.Is(err, store.ErrIdentityAddress):
		writeError(w, http.StatusBadRequest, "bad_request", "That is not a plain email address")
		return
	case errors.Is(err, store.ErrBadMailType):
		writeError(w, http.StatusBadRequest, "bad_request", "That is not a kind of address Ivy knows")
		return
	case errors.Is(err, store.ErrAddressTypeLimit):
		writeError(w, http.StatusConflict, "too_many_addresses", "Too many addresses are marked; clear one first")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, addressTypeView(address, kind, llmOn))
}

func addressTypeView(address string, kind store.MailType, llmOn bool) api.AddressType {
	return api.AddressType{Address: address, Kind: api.MailTypeKind(kind), Llm: llmOn}
}
