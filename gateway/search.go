package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/search"
	"github.com/AutumnsGrove/Ivy/store"
)

const (
	defaultSearchLimit = 50
	maxSearchLimit     = 200
	// maxSearchQueryBytes bounds the query, which is also sent verbatim to the
	// embeddings provider.
	maxSearchQueryBytes = 2048
)

// semanticTimeout bounds the meaning half of a search (the query embedding and
// the vector scan), so a slow provider degrades to keyword hits promptly
// instead of holding the request open. A var so a test does not wait for it.
var semanticTimeout = 5 * time.Second

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
	if len(q) > maxSearchQueryBytes {
		writeError(w, http.StatusBadRequest, "bad_request", "That search is too long")
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

	vec := s.semanticHits(r.Context(), q, accounts, limit)
	semantic := map[string]bool{}
	for _, h := range vec {
		semantic[refKey(h.AccountID, h.ContentKey)] = true
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

// semanticHits embeds the query once and scans the stored vectors for it. The
// screen's "All accounts" names no account, so each account is offered to the
// gate in turn until one has a provider that will take it: at most one paid
// call. Anything that stops it, a policy refusal on every account or an outage,
// leaves the caller with keyword hits alone.
func (s *Server) semanticHits(ctx context.Context, q string, accounts []string, limit int) []search.Hit {
	if s.queryEmbed == nil || s.searchService == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, semanticTimeout)
	defer cancel()
	candidates := accounts
	if len(candidates) == 0 {
		all, err := s.dbs.ListAccounts(ctx)
		if err != nil {
			slog.WarnContext(ctx, "search: listing accounts for the query embedding failed", "error", err)
			return nil
		}
		for _, a := range all {
			candidates = append(candidates, a.ID)
		}
	}
	for _, id := range candidates {
		v, model, err := s.queryEmbed.EmbedQuery(ctx, id, q)
		switch {
		case err == nil && v.Dims > 0:
			hits, verr := s.searchService.VectorSearch(ctx, v, accounts, model, limit)
			if verr != nil {
				slog.WarnContext(ctx, "search: vector scan failed, keyword only", "error", verr)
				return nil
			}
			return hits
		case errors.Is(err, llm.ErrNoProvider), errors.Is(err, llm.ErrNotEnabled), errors.Is(err, llm.ErrCapReached), errors.Is(err, llm.ErrFeatureOff):
			continue // this account cannot embed; another may
		default:
			if err != nil {
				slog.WarnContext(ctx, "search: query embedding failed, keyword only", "account", id, "error", err)
			}
			return nil
		}
	}
	return nil
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
