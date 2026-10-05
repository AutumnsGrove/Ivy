package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"hash/crc32"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxPersonConversations bounds a person page's conversation list.
const maxPersonConversations = 20

// person is one merged view: several addresses (the operator may have merged
// them) seen across one or more accounts.
type person struct {
	id        string
	addresses []string
	name      string
	accounts  map[string]bool
	count     int
	first     time.Time
	last      time.Time
	latest    string
	writesTo  string
}

// peoplePageSize is how many people one page of the list carries.
const peoplePageSize = 100

// peopleOffset reads a People cursor: the offset of the first person of the page,
// base64 so the client treats it as opaque. The list is the in-memory grouping
// of every address, in a deterministic order, so an offset is exact; a rebuild
// between two pages can shift a row, never lose the whole page.
func peopleOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0, errors.New("bad people cursor")
	}
	return n, nil
}

func peopleCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// handleListPeople groups the derived correspondent rows into people and serves
// them 100 at a time, most correspondence first. The list carries no
// conversations or tags: those are a per-person page's work.
func (s *Server) handleListPeople(w http.ResponseWriter, r *http.Request) {
	offset, err := peopleOffset(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That page link is not valid")
		return
	}
	rows, links, own, err := s.personRows(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	groups := groupPeople(rows, links, own)
	next := ""
	if offset >= len(groups) {
		groups = nil
	} else if end := offset + peoplePageSize; end < len(groups) {
		groups, next = groups[offset:end], peopleCursor(end)
	} else {
		groups = groups[offset:]
	}
	out := make([]api.Person, 0, len(groups))
	for _, p := range groups {
		out = append(out, api.Person{
			Id:            p.id,
			Name:          p.name,
			Initials:      initials(p.name, p.id),
			Email:         p.id,
			Slot:          personSlot(p.id),
			Latest:        p.latest,
			When:          p.last.UTC(),
			WritesTo:      p.writesTo,
			Since:         p.first.UTC(),
			Count:         p.count,
			Addresses:     p.addresses,
			Tags:          []string{},
			Conversations: []api.Conversation{},
		})
	}
	page := api.PeoplePage{Items: out}
	if next != "" {
		page.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, page)
}

// handleGetPerson serves one person with their conversations and tags.
func (s *Server) handleGetPerson(w http.ResponseWriter, r *http.Request) {
	rows, links, own, err := s.personRows(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	groups := groupPeople(rows, links, own)
	want := strings.ToLower(r.PathValue("id"))
	var found *person
	for i := range groups {
		if groups[i].id == want {
			found = &groups[i]
			break
		}
	}
	if found == nil {
		s.notFound(w, r, "person")
		return
	}
	conversations, err := s.dbs.ConversationsForAddresses(r.Context(), found.addresses, maxPersonConversations)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	views := make([]api.Conversation, 0, len(conversations))
	keys := make([]string, 0, len(conversations))
	for _, c := range conversations {
		unread := c.Unread
		views = append(views, api.Conversation{
			Id:      c.ID,
			Subject: c.Subject,
			Preview: c.Snippet,
			When:    c.Date.UTC(),
			Unread:  &unread,
		})
		keys = append(keys, c.ContentKey)
	}
	tags, err := s.personTagNames(r.Context(), keys)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.Person{
		Id:            found.id,
		Name:          found.name,
		Initials:      initials(found.name, found.id),
		Email:         found.id,
		Slot:          personSlot(found.id),
		Latest:        found.latest,
		When:          found.last.UTC(),
		WritesTo:      found.writesTo,
		Since:         found.first.UTC(),
		Count:         found.count,
		Addresses:     found.addresses,
		Tags:          tags,
		Conversations: views,
	})
}

// handleLinkPersonAddress merges one address into a person.
func (s *Server) handleLinkPersonAddress(w http.ResponseWriter, r *http.Request) {
	var body api.PersonAddress
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTagBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That is not a valid request")
		return
	}
	if err := s.dbs.LinkPerson(r.Context(), body.Address, r.PathValue("id"), s.now()); err != nil {
		if errors.Is(err, store.ErrBadPerson) {
			writeError(w, http.StatusBadRequest, "bad_request", "That address cannot be merged into itself")
			return
		}
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUnlinkPersonAddress splits an address back out of a person.
func (s *Server) handleUnlinkPersonAddress(w http.ResponseWriter, r *http.Request) {
	if err := s.dbs.UnlinkPerson(r.Context(), r.PathValue("address")); errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "address")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) personRows(ctx context.Context) ([]store.PersonRow, map[string]string, map[string]bool, error) {
	rows, err := s.dbs.ListPeople(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	links, err := s.dbs.PersonLinks(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	accounts, err := s.dbs.ListAccounts(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	own := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		own[strings.ToLower(a.Address)] = true
	}
	return rows, links, own, nil
}

// personTagNames returns the distinct tag names carried by a person's
// conversation messages.
func (s *Server) personTagNames(ctx context.Context, keys []string) ([]string, error) {
	if len(keys) == 0 {
		return []string{}, nil
	}
	byKey, err := s.dbs.TagsForMessages(ctx, "", keys)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, tags := range byKey {
		for _, t := range tags {
			if !seen[t.Name] {
				seen[t.Name] = true
				out = append(out, t.Name)
			}
		}
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// resolvePerson follows merge links to the canonical address, bounded so a
// hostile link cycle cannot loop.
func resolvePerson(address string, links map[string]string) string {
	seen := map[string]bool{}
	for range 8 {
		next, ok := links[address]
		if !ok || next == address || seen[next] {
			return address
		}
		seen[address] = true
		address = next
	}
	return address
}

// groupPeople folds the per-address rows into people, applying the operator's
// merges and dropping the operator's own addresses. A person's id is the
// canonical address.
func groupPeople(rows []store.PersonRow, links map[string]string, own map[string]bool) []person {
	byID := map[string]*person{}
	for _, row := range rows {
		if own[row.Address] {
			continue
		}
		id := resolvePerson(row.Address, links)
		p := byID[id]
		if p == nil {
			p = &person{id: id, accounts: map[string]bool{}, first: row.FirstSeen}
			byID[id] = p
		}
		p.addresses = append(p.addresses, row.Address)
		p.accounts[row.AccountID] = true
		p.count += row.MessageCount
		if p.name == "" && row.Name != "" {
			p.name = row.Name
		}
		if p.writesTo == "" || row.LastSeen.After(p.last) {
			p.writesTo = row.AccountID
		}
		if p.first.IsZero() || row.FirstSeen.Before(p.first) {
			p.first = row.FirstSeen
		}
		if row.LastSeen.After(p.last) {
			p.last = row.LastSeen
			p.latest = row.LastSubject
			if row.Name != "" {
				p.name = row.Name
			}
		}
	}
	out := make([]person, 0, len(byID))
	for _, p := range byID {
		if p.name == "" {
			p.name = p.id
		}
		sort.Strings(p.addresses)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		if !out[i].last.Equal(out[j].last) {
			return out[i].last.After(out[j].last)
		}
		return out[i].id < out[j].id // map order is random; a page boundary needs a fixed one
	})
	return out
}

// personSlot picks one of the five avatar colours from the address, so a person
// keeps the same colour between visits.
func personSlot(address string) api.AccountSlot {
	return api.AccountSlot(int(crc32.ChecksumIEEE([]byte(address))%5) + 1)
}
