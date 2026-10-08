package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Provider kinds, as they appear in the ledger, config and requests. A request
// names a kind; it never carries a client.
const (
	ProviderOpenRouter = "openrouter"
	ProviderOllama     = "ollama"
)

// usage is what a provider call consumed and cost. CostEstimated is set when the
// provider reported no cost and it was priced from the tokens, so the cap still
// sees the spend.
type usage struct {
	InputTokens   int
	OutputTokens  int
	CostUSD       float64
	CostEstimated bool
}

// The clients below are the only code that reaches a provider. They are unexported
// and built by the gate, so the only way to make a paid call is through it.
type (
	embedder interface {
		embed(ctx context.Context, model string, inputs []string) ([]Vector, usage, error)
	}
	decider interface {
		decide(ctx context.Context, model, state string, questions map[string]Question) (map[string]Answer, usage, error)
	}
	completer interface {
		complete(ctx context.Context, model string, messages []chatMessage, maxTokens int) (string, usage, error)
	}
)

// StatusError is a provider's non-200 answer. The gate reads the status to tell a
// refusal of one input from an outage.
type StatusError struct {
	Provider string
	Status   int
	Body     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s status %d: %s", e.Provider, e.Status, e.Body)
}

// responseLimit bounds a provider's HTTP body, so a hostile or broken endpoint
// cannot make Ivy allocate without limit (STANDARDS.md 4a).
const responseLimit = 64 << 20

// The gate supplies a context with a deadline, so the client timeout is only a
// backstop.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 150 * time.Second}
}

// httpProvider is a JSON-over-HTTP endpoint, with an optional bearer key.
type httpProvider struct {
	name    string
	baseURL string
	apiKey  string
	client  *http.Client
}

// post sends in as JSON to path and decodes the answer into out. A non-200 is a
// *StatusError carrying a truncated body.
func (p *httpProvider) post(ctx context.Context, path string, in, out any) error {
	reqBody, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("%s request: %w", p.name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("%s request: %w", p.name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s call: %w", p.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit))
	if err != nil {
		return fmt.Errorf("%s response: %w", p.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return &StatusError{Provider: p.name, Status: resp.StatusCode, Body: truncateForError(body)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s response: %w", p.name, err)
	}
	return nil
}

// openRouter is the hosted provider: embeddings, Jev (/systemone), chat and
// vision, all behind one key.
type openRouter struct{ httpProvider }

func newOpenRouter(baseURL, apiKey string) *openRouter {
	return &openRouter{httpProvider{name: ProviderOpenRouter, baseURL: trimSlash(baseURL), apiKey: apiKey, client: newHTTPClient()}}
}

func (o *openRouter) embed(ctx context.Context, model string, inputs []string) ([]Vector, usage, error) {
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
	if err := o.post(ctx, "/embeddings", map[string]any{"model": model, "input": inputs}, &out); err != nil {
		return nil, usage{}, err
	}
	if len(out.Data) != len(inputs) {
		return nil, usage{}, fmt.Errorf("embeddings provider returned %d vectors for %d inputs", len(out.Data), len(inputs))
	}
	u := usage{InputTokens: out.Usage.PromptTokens}
	if u.InputTokens == 0 {
		u.InputTokens = out.Usage.TotalTokens
	}
	if out.Usage.Cost != nil {
		u.CostUSD = *out.Usage.Cost
	} else {
		// No reported cost would be ledgered as $0 and the cap could never trip.
		u.CostEstimated = true
		if u.InputTokens == 0 {
			u.InputTokens = estimateTokens(inputs)
		}
		u.CostUSD = estimateCost(model, EndpointEmbeddings, u.InputTokens, 0)
	}
	vecs := make([]Vector, len(out.Data))
	for i, d := range out.Data {
		vecs[i] = Quantise(d.Embedding)
	}
	return vecs, u, nil
}

// Question is one thing Jev is asked about a message (JEV.md 2).
type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

// Answer is Jev's reply to one question: a choice, the probability of each option
// and its confidence. It is data, never authority (brief invariant 5).
type Answer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

func (o *openRouter) decide(ctx context.Context, model, state string, questions map[string]Question) (map[string]Answer, usage, error) {
	var out struct {
		Answers map[string]Answer `json:"answers"`
		Usage   struct {
			InputTokens  int      `json:"input_tokens"`
			OutputTokens int      `json:"output_tokens"`
			Cost         *float64 `json:"cost"`
		} `json:"usage"`
	}
	in := map[string]any{"model": model, "state": state, "questions": questions}
	if err := o.post(ctx, "/systemone", in, &out); err != nil {
		return nil, usage{}, err
	}
	u := usage{InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens}
	if out.Usage.Cost != nil {
		u.CostUSD = *out.Usage.Cost
	} else {
		u.CostEstimated = true
		u.CostUSD = estimateCost(model, EndpointSystemOne, u.InputTokens, u.OutputTokens)
	}
	return out.Answers, u, nil
}

// chatMessage is one message of a chat call. Content is a string or, for an image,
// a list of parts.
type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func (o *openRouter) complete(ctx context.Context, model string, messages []chatMessage, maxTokens int) (string, usage, error) {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int      `json:"prompt_tokens"`
			CompletionTokens int      `json:"completion_tokens"`
			OutputTokens     int      `json:"output_tokens"`
			Cost             *float64 `json:"cost"`
		} `json:"usage"`
	}
	in := map[string]any{"model": model, "messages": messages, "max_tokens": maxTokens}
	if err := o.post(ctx, "/chat/completions", in, &out); err != nil {
		return "", usage{}, err
	}
	if len(out.Choices) == 0 {
		return "", usage{}, errors.New("chat provider returned no choice")
	}
	u := usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens}
	if u.OutputTokens == 0 {
		u.OutputTokens = out.Usage.OutputTokens
	}
	text := out.Choices[0].Message.Content
	if out.Usage.Cost != nil {
		u.CostUSD = *out.Usage.Cost
	} else {
		u.CostEstimated = true
		endpoint := EndpointChat
		if hasImage(messages) {
			endpoint = EndpointVision
		}
		u.CostUSD = estimateCost(model, endpoint, u.InputTokens, u.OutputTokens)
	}
	return text, u, nil
}

// imagePart is a chat content part carrying an image as a data URL.
func imagePart(mediaType string, data []byte) map[string]any {
	return map[string]any{
		"type":      "image_url",
		"image_url": map[string]string{"url": "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)},
	}
}

func hasImage(messages []chatMessage) bool {
	for _, m := range messages {
		if parts, ok := m.Content.([]map[string]any); ok {
			for _, p := range parts {
				if p["type"] == "image_url" {
					return true
				}
			}
		}
	}
	return false
}

// ollamaEmbed is the local option (ARCHITECTURE.md 6). It costs nothing and is
// allowed even for an account with smart features off, because nothing leaves the
// device.
type ollamaEmbed struct{ httpProvider }

func newOllama(baseURL string) *ollamaEmbed {
	return &ollamaEmbed{httpProvider{name: ProviderOllama, baseURL: trimSlash(baseURL), client: newHTTPClient()}}
}

func (o *ollamaEmbed) embed(ctx context.Context, model string, inputs []string) ([]Vector, usage, error) {
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
		PromptEval int         `json:"prompt_eval_count"`
	}
	if err := o.post(ctx, "/api/embed", map[string]any{"model": model, "input": inputs}, &out); err != nil {
		return nil, usage{}, err
	}
	if len(out.Embeddings) != len(inputs) {
		return nil, usage{}, fmt.Errorf("ollama returned %d vectors for %d inputs", len(out.Embeddings), len(inputs))
	}
	vecs := make([]Vector, len(out.Embeddings))
	for i, e := range out.Embeddings {
		vecs[i] = Quantise(e)
	}
	return vecs, usage{InputTokens: out.PromptEval}, nil
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

// estimateTokens sizes inputs the way chunking does (4 bytes a token), for a
// provider that reports no usage.
func estimateTokens(inputs []string) int {
	n := 0
	for _, in := range inputs {
		n += len(in)
	}
	return estimateTokensForBytes(n)
}
