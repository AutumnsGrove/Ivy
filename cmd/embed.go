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
func NewEmbedding(cfg *config.Config, dbs *store.DBs, apiKey string, opts ...EmbeddingOption) *Embedding {
	var o embeddingOptions
	for _, opt := range opts {
		opt(&o)
	}
	gate := llm.NewGate(dbs,
		llm.WithProviders(llm.ProviderConfig{
			OpenRouterBase: cfg.LLM.OpenRouterBase, APIKey: apiKey, OllamaURL: cfg.LLM.OllamaURL,
		}),
		llm.WithAccountPolicy(llm.NewAccountPolicy(dbs, o.configuredSettings(cfg))),
		llm.WithDefaultCaps(cfg.LLM.MonthlyCapUSD, llm.DefaultGlobalCapUSD),
	)
	providers, models := buildEmbedders(cfg, apiKey)
	emb := &Embedding{Query: newQueryEmbedder(gate, providers, models)}
	if accounts := embedAccounts(cfg, providers, models); len(accounts) > 0 {
		emb.Worker = search.NewEmbedWorker(dbs, gate, accounts, search.WorkerOptions{})
	}
	return emb
}

type embeddingOptions struct{ fromApp map[string]bool }

// EmbeddingOption adjusts NewEmbedding.
type EmbeddingOption func(*embeddingOptions)

// FromApp names the accounts connected from the app. Their smart-features switch
// lives in state.db and is read on every call, so turning it off in Settings stops
// remote calls at once. Any other account keeps what ivy.yaml said at startup.
func FromApp(ids ...string) EmbeddingOption {
	return func(o *embeddingOptions) {
		o.fromApp = make(map[string]bool, len(ids))
		for _, id := range ids {
			o.fromApp[id] = true
		}
	}
}

// configuredSettings is the startup choice of every account ivy.yaml declares,
// except those the app manages, which the gate reads live from state.db.
func (o embeddingOptions) configuredSettings(cfg *config.Config) map[string]llm.AccountSettings {
	out := make(map[string]llm.AccountSettings, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		if !o.fromApp[a.ID] {
			out[a.ID] = llm.AccountSettings{Smart: a.LLMEnabled}
		}
	}
	return out
}

// buildEmbedders picks the provider kind per account from the operator's config. An
// account with no provider, or a hosted one with no API key, is left out, so
// search for it stays keyword-only and never reaches a paid endpoint.
func buildEmbedders(cfg *config.Config, apiKey string) (providers, models map[string]string) {
	providers = map[string]string{}
	models = map[string]string{}
	for _, a := range cfg.Accounts {
		switch a.EmbedProvider {
		case "openrouter":
			if apiKey == "" {
				slog.Warn("embeddings are off for this account: embed_provider is openrouter but OPENROUTER_API_KEY is not set",
					"account", a.ID)
				continue
			}
			providers[a.ID] = llm.ProviderOpenRouter
			models[a.ID] = cfg.LLM.EmbedModel
		case "ollama":
			if cfg.LLM.OllamaURL == "" {
				slog.Warn("embeddings are off for this account: embed_provider is ollama but llm.ollama_url is not set",
					"account", a.ID)
				continue
			}
			providers[a.ID] = llm.ProviderOllama
			models[a.ID] = cfg.LLM.OllamaEmbedModel
		}
	}
	return providers, models
}

// embedAccounts builds the worker's per-account provider choice from config.
func embedAccounts(cfg *config.Config, providers, models map[string]string) []search.AccountConfig {
	var out []search.AccountConfig
	for _, a := range cfg.Accounts {
		kind, ok := providers[a.ID]
		if !ok {
			continue
		}
		out = append(out, search.AccountConfig{ID: a.ID, Provider: kind, Model: models[a.ID]})
	}
	return out
}

// queryEmbedder implements gateway.QueryEmbedder through the one gate, so the
// query embedding in search is governed by the same opt-in and cap as the
// message embeddings.
type queryEmbedder struct {
	gate      *llm.Gate
	providers map[string]string
	models    map[string]string
}

func newQueryEmbedder(gate *llm.Gate, providers, models map[string]string) *queryEmbedder {
	return &queryEmbedder{gate: gate, providers: providers, models: models}
}

// EmbedQuery turns a search query into a vector for one account. A refusal or
// an outage is returned, and the HTTP handler falls back to keyword search.
func (q *queryEmbedder) EmbedQuery(ctx context.Context, accountID, query string) (llm.Vector, string, error) {
	kind, ok := q.providers[accountID]
	if !ok {
		return llm.Vector{}, "", llm.ErrNoProvider
	}
	model := q.models[accountID]
	vecs, err := q.gate.Embed(ctx, llm.EmbedRequest{
		Provider: kind, AccountID: accountID,
		Model: model, Feature: "search", Inputs: []string{query},
		ContentKeys: []string{accountID + ":query"},
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
