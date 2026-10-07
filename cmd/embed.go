package cmd

import (
	"context"
	"errors"
	"log/slog"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/gateway"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/search"
	"github.com/AutumnsGrove/Ivy/store"
)

// Embedding is the search side of the one gate: what the gateway needs to embed a
// query, and the background worker that embeds mail once. `ivy run` and the dev
// harness both build it here, so the dev stack and the e2e suite exercise the
// code that ships.
type Embedding struct {
	// Query embeds a search query through the gate. Never nil: an account with no
	// provider answers llm.ErrNoProvider, which search reads as keyword-only.
	Query gateway.QueryEmbedder
	// Worker embeds each message once. Nil when no account has a usable provider.
	Worker *search.EmbedWorker
}

// NewEmbedding builds the embedding stack from the operator's config. apiKey is
// the hosted provider's key (the environment's OPENROUTER_API_KEY in production);
// an account that needs it and has none is left out with a warning.
func NewEmbedding(cfg *config.Config, dbs *store.DBs, apiKey string) *Embedding {
	gate := llm.NewGate(dbs)
	embedders, models := buildEmbedders(cfg, apiKey)
	emb := &Embedding{Query: newQueryEmbedder(cfg, gate, embedders, models)}
	if accounts := embedAccounts(cfg, embedders, models); len(accounts) > 0 {
		emb.Worker = search.NewEmbedWorker(dbs, gate, accounts, search.WorkerOptions{})
	}
	return emb
}

// buildEmbedders picks the provider per account from the operator's config. An
// account with no provider, or a hosted one with no API key, is left out, so
// search for it stays keyword-only and never reaches a paid endpoint.
func buildEmbedders(cfg *config.Config, apiKey string) (map[string]llm.Embedder, map[string]string) {
	embedders := map[string]llm.Embedder{}
	models := map[string]string{}
	for _, a := range cfg.Accounts {
		switch a.EmbedProvider {
		case "openrouter":
			if apiKey == "" {
				slog.Warn("embeddings are off for this account: embed_provider is openrouter but OPENROUTER_API_KEY is not set",
					"account", a.ID)
				continue
			}
			embedders[a.ID] = llm.NewOpenRouter(cfg.LLM.OpenRouterBase, apiKey)
			models[a.ID] = cfg.LLM.EmbedModel
		case "ollama":
			if cfg.LLM.OllamaURL == "" {
				slog.Warn("embeddings are off for this account: embed_provider is ollama but llm.ollama_url is not set",
					"account", a.ID)
				continue
			}
			embedders[a.ID] = llm.NewOllama(cfg.LLM.OllamaURL)
			models[a.ID] = cfg.LLM.OllamaEmbedModel
		}
	}
	return embedders, models
}

// embedAccounts builds the worker's per-account policy from config.
func embedAccounts(cfg *config.Config, embedders map[string]llm.Embedder, models map[string]string) []search.AccountConfig {
	var out []search.AccountConfig
	for _, a := range cfg.Accounts {
		emb, ok := embedders[a.ID]
		if !ok {
			continue
		}
		out = append(out, search.AccountConfig{
			ID: a.ID, Embedder: emb, Model: models[a.ID],
			Enabled: a.LLMEnabled, CapUSD: cfg.LLM.MonthlyCapUSD,
		})
	}
	return out
}

// queryEmbedder implements gateway.QueryEmbedder through the one gate, so the
// query embedding in search is governed by the same opt-in and cap as the
// message embeddings.
type queryEmbedder struct {
	gate      *llm.Gate
	embedders map[string]llm.Embedder
	models    map[string]string
	accounts  map[string]config.Account
	cap       float64
}

func newQueryEmbedder(cfg *config.Config, gate *llm.Gate, embedders map[string]llm.Embedder, models map[string]string) *queryEmbedder {
	byID := make(map[string]config.Account, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		byID[a.ID] = a
	}
	return &queryEmbedder{gate: gate, embedders: embedders, models: models, accounts: byID, cap: cfg.LLM.MonthlyCapUSD}
}

// EmbedQuery turns a search query into a vector for one account. A refusal or
// an outage is returned, and the HTTP handler falls back to keyword search.
func (q *queryEmbedder) EmbedQuery(ctx context.Context, accountID, query string) (llm.Vector, string, error) {
	emb, ok := q.embedders[accountID]
	if !ok {
		return llm.Vector{}, "", llm.ErrNoProvider
	}
	model := q.models[accountID]
	vecs, err := q.gate.Embed(ctx, llm.EmbedRequest{
		Embedder: emb, AccountID: accountID, Enabled: q.accounts[accountID].LLMEnabled,
		Model: model, Feature: "search", Inputs: []string{query},
		ContentKeys: []string{accountID + ":query"}, CapUSD: q.cap,
	})
	if err != nil {
		return llm.Vector{}, "", err
	}
	if len(vecs) == 0 {
		return llm.Vector{}, "", errors.New("embedder returned no vector")
	}
	return vecs[0], model, nil
}

// smartOf is each configured account's smart-features choice, for the API to
// report without reading it back from the mirror.
func smartOf(accounts []config.Account) map[string]bool {
	out := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		out[a.ID] = a.LLMEnabled
	}
	return out
}
