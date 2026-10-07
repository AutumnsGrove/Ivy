package gateway

import (
	"context"
	"strings"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// partyBuilder turns stored addresses into the people on a message, with the
// same identity rules as the People page: the operator's merges are followed and
// the operator's own addresses have no page.
type partyBuilder struct {
	links map[string]string
	own   map[string]bool
}

func (s *Server) partyBuilder(ctx context.Context) (partyBuilder, error) {
	links, err := s.dbs.PersonLinks(ctx)
	if err != nil {
		return partyBuilder{}, err
	}
	accounts, err := s.dbs.ListAccounts(ctx)
	if err != nil {
		return partyBuilder{}, err
	}
	own := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		own[strings.ToLower(a.Address)] = true
	}
	return partyBuilder{links: links, own: own}, nil
}

func (b partyBuilder) one(a store.Address) api.MessageParty {
	addr := strings.ToLower(strings.TrimSpace(a.Address))
	p := api.MessageParty{Name: store.CleanName(a.Name), Address: addr}
	if addr != "" && !b.own[addr] {
		id := resolvePerson(addr, b.links)
		p.PersonId = &id
	}
	return p
}

// list is never nil, so the JSON is [] rather than null.
func (b partyBuilder) list(in []store.Address) []api.MessageParty {
	out := make([]api.MessageParty, 0, len(in))
	for _, a := range in {
		out = append(out, b.one(a))
	}
	return out
}

// authView summarises the verdicts a trusted server attached. With none it says
// so; "unknown" must never read as an all-clear.
func authView(a store.AuthResults) api.MessageAuth {
	out := api.MessageAuth{State: api.None}
	verdicts := []string{a.SPF, a.DKIM, a.DMARC}
	var seen, passed, failed int
	for _, v := range verdicts {
		if v == "" {
			continue
		}
		seen++
		switch strings.ToLower(v) {
		case "pass":
			passed++
		case "fail", "softfail", "permerror", "temperror":
			failed++
		}
	}
	if seen == 0 {
		return out
	}
	switch {
	case failed > 0:
		out.State = api.Fail
	case passed == seen:
		out.State = api.Pass
	default:
		out.State = api.Mixed
	}
	if a.AuthservID != "" {
		out.AuthservId = &a.AuthservID
	}
	out.Spf, out.Dkim, out.Dmarc = nonEmpty(a.SPF), nonEmpty(a.DKIM), nonEmpty(a.DMARC)
	return out
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
