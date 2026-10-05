package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/search"
	"github.com/AutumnsGrove/Ivy/store"
)

const (
	defaultSearchLimit = 50
	maxSearchLimit     = 200
)

// QueryEmbedder embeds a search query for one account through the gate, so the
// single chokepoint still governs the only paid call search makes. A nil
// QueryEmbedder leaves search keyword-only.
type QueryEmbedder interface {
	EmbedQuery(ctx context.Context, accountID, query string) (llm.Vector, string, error)
}

// handleSearch serves the one search box: FTS5 keyword hits fused with vector
// hits when the selected account has a provider. A provider outage is not an
// error; search quietly falls back to keyword (ARCHITECTURE.md 6).
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Type something to search for")
		return
	}
	accounts := r.URL.Query()["account_id"]
	limit := clampSearchLimit(r.URL.Query().Get("limit"))

	ftsHits, err := s.dbs.SearchFTS(r.Context(), q, accounts, limit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	fts := make([]search.Hit, len(ftsHits))
	for i, h := range ftsHits {
		fts[i] = search.Hit{AccountID: h.AccountID, ContentKey: h.ContentKey}
	}

	var vec []search.Hit
	semantic := map[string]bool{}
	if s.queryEmbed != nil && s.searchService != nil && len(accounts) > 0 {
		v, model, err := s.queryEmbed.EmbedQuery(r.Context(), accounts[0], q)
		if err == nil && v.Dims > 0 {
			if vh, verr := s.searchService.VectorSearch(r.Context(), v, accounts, model, limit); verr == nil {
				vec = vh
				for _, h := range vh {
					semantic[refKey(h.AccountID, h.ContentKey)] = true
				}
			}
		}
	}

	fused := fts
	if s.searchService != nil {
		fused = s.searchService.Hybrid(fts, vec, limit)
	}

	keysByAccount := map[string][]string{}
	for _, h := range fused {
		keysByAccount[h.AccountID] = append(keysByAccount[h.AccountID], h.ContentKey)
	}
	namesByAccount := map[string]map[string]string{}
	for accountID, keys := range keysByAccount {
		byKey, err := s.tagNames(r.Context(), accountID, keys)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		namesByAccount[accountID] = byKey
	}

	out := api.SearchResults{Query: q, Hits: make([]api.SearchHit, 0, len(fused))}
	for _, h := range fused {
		m, err := s.dbs.SummaryForContent(r.Context(), h.AccountID, h.ContentKey)
		if errors.Is(err, store.ErrNotFound) {
			continue // hidden by the time the page was built
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		sem := semantic[refKey(h.AccountID, h.ContentKey)]
		view := api.SearchHit{
			Id:        m.ID,
			AccountId: m.AccountID,
			From:      displayName(m.From),
			Date:      m.Date.UTC(),
			Subject:   m.Subject,
			Preview:   m.Snippet,
			Semantic:  &sem,
		}
		if name, ok := namesByAccount[h.AccountID][h.ContentKey]; ok {
			view.Tag = &name
		}
		out.Hits = append(out.Hits, view)
	}
	out.Total = len(out.Hits)
	writeJSON(w, http.StatusOK, out)
}

func clampSearchLimit(s string) int {
	n := defaultSearchLimit
	if s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n = v
		}
	}
	if n > maxSearchLimit {
		n = maxSearchLimit
	}
	return n
}

func refKey(accountID, contentKey string) string { return accountID + "\x00" + contentKey }
