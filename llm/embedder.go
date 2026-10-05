package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Provider names, as they appear in the ledger and config.
const (
	ProviderOpenRouter = "openrouter"
	ProviderOllama     = "ollama"
)

// EmbedResult is one provider call's answer: a vector per input, in order, the
// tokens it reported and its exact cost (CostEstimated when it reported none).
type EmbedResult struct {
	Vectors       []Vector
	InputTokens   int
	CostUSD       float64
	CostEstimated bool
}

// Embedder is one provider behind the gate. It is deliberately small and
// unexported in its implementations: the only way to reach one is the gate.
type Embedder interface {
	// Name is the provider name for the ledger.
	Name() string
	// Embed returns one vector per input, in the same order.
	Embed(ctx context.Context, model string, inputs []string) (EmbedResult, error)
}

// responseLimit bounds a provider's HTTP body, so a hostile or broken endpoint
// cannot make Ivy allocate without limit (STANDARDS.md 4a).
const responseLimit = 64 << 20

// httpClient is the shared provider client. The gate supplies a context with a
// deadline, so the timeout here is a backstop.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}

// openRouter is the hosted default (ARCHITECTURE.md 6). It is an
// OpenRouter-compatible chat/embeddings endpoint.
type openRouter struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewOpenRouter builds the hosted embedder. baseURL includes /api/v1.
func NewOpenRouter(baseURL, apiKey string) Embedder {
	return &openRouter{baseURL: trimSlash(baseURL), apiKey: apiKey, client: newHTTPClient()}
}

func (o *openRouter) Name() string { return ProviderOpenRouter }

func (o *openRouter) Embed(ctx context.Context, model string, inputs []string) (EmbedResult, error) {
	reqBody, err := json.Marshal(map[string]any{"model": model, "input": inputs})
	if err != nil {
		return EmbedResult{}, fmt.Errorf("embeddings request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return EmbedResult{}, fmt.Errorf("embeddings request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return EmbedResult{}, fmt.Errorf("embeddings call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit))
	if err != nil {
		return EmbedResult{}, fmt.Errorf("embeddings response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return EmbedResult{}, fmt.Errorf("embeddings provider status %d: %s", resp.StatusCode, truncateForError(body))
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int      `json:"prompt_tokens"`
			TotalTokens  int      `json:"total_tokens"`
			Cost         *float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return EmbedResult{}, fmt.Errorf("embeddings response: %w", err)
	}
	if len(out.Data) != len(inputs) {
		return EmbedResult{}, fmt.Errorf("embeddings provider returned %d vectors for %d inputs", len(out.Data), len(inputs))
	}
	res := EmbedResult{InputTokens: out.Usage.PromptTokens}
	if res.InputTokens == 0 {
		res.InputTokens = out.Usage.TotalTokens
	}
	if out.Usage.Cost != nil {
		res.CostUSD = *out.Usage.Cost
	} else {
		// No reported cost would be ledgered as $0 and the cap could never trip.
		res.CostEstimated = true
		if res.InputTokens == 0 {
			res.InputTokens = estimateTokens(inputs)
		}
		res.CostUSD = estimateCost(model, res.InputTokens)
	}
	for _, d := range out.Data {
		res.Vectors = append(res.Vectors, Quantise(d.Embedding))
	}
	return res, nil
}

// ollama is the local option (ARCHITECTURE.md 6). It costs nothing and is
// allowed even for an account with smart features off, because nothing leaves
// the device.
type ollama struct {
	baseURL string
	client  *http.Client
}

// NewOllama builds the local embedder. baseURL is the server root, without a
// path (for example http://127.0.0.1:11434).
func NewOllama(baseURL string) Embedder {
	return &ollama{baseURL: trimSlash(baseURL), client: newHTTPClient()}
}

func (o *ollama) Name() string { return ProviderOllama }

func (o *ollama) Embed(ctx context.Context, model string, inputs []string) (EmbedResult, error) {
	reqBody, err := json.Marshal(map[string]any{"model": model, "input": inputs})
	if err != nil {
		return EmbedResult{}, fmt.Errorf("ollama request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(reqBody))
	if err != nil {
		return EmbedResult{}, fmt.Errorf("ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return EmbedResult{}, fmt.Errorf("ollama call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit))
	if err != nil {
		return EmbedResult{}, fmt.Errorf("ollama response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return EmbedResult{}, fmt.Errorf("ollama status %d: %s", resp.StatusCode, truncateForError(body))
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
		PromptEval int         `json:"prompt_eval_count"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return EmbedResult{}, fmt.Errorf("ollama response: %w", err)
	}
	if len(out.Embeddings) != len(inputs) {
		return EmbedResult{}, fmt.Errorf("ollama returned %d vectors for %d inputs", len(out.Embeddings), len(inputs))
	}
	res := EmbedResult{InputTokens: out.PromptEval}
	for _, e := range out.Embeddings {
		res.Vectors = append(res.Vectors, Quantise(e))
	}
	return res, nil
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// truncateForError keeps a provider's error body out of logs beyond a useful
// size (and never includes mail text, which a provider could echo back).
func truncateForError(b []byte) string {
	const limit = 512
	if len(b) > limit {
		return string(b[:limit])
	}
	return string(b)
}

// ErrNoProvider is returned by a gate call that has no embedder configured.
var ErrNoProvider = errors.New("llm: no embedding provider configured")

// promptUSDPerToken is each hosted embedding model's listed prompt price (from
// OpenRouter's /api/v1/embeddings/models, 2026-10-05). It is only the fallback
// for a response that reports no cost, so the monthly cap still sees the spend.
var promptUSDPerToken = map[string]float64{
	"perplexity/pplx-embed-v1-0.6b": 0.000000004,
	"perplexity/pplx-embed-v1-4b":   0.00000003,
}

// unpricedUSDPerToken is assumed for a model with no listed price: the dearest
// price above. A cap that trips early is an annoyance; one that never trips is
// a bill.
const unpricedUSDPerToken = 0.00000003

// estimateCost prices tokens at the model's listed rate.
func estimateCost(model string, tokens int) float64 {
	price, ok := promptUSDPerToken[model]
	if !ok {
		price = unpricedUSDPerToken
	}
	return float64(tokens) * price
}

// estimateTokens sizes inputs the way chunking does (4 bytes a token), for a
// provider that reports no usage.
func estimateTokens(inputs []string) int {
	bytes := 0
	for _, in := range inputs {
		bytes += len(in)
	}
	return (bytes + 3) / 4
}
