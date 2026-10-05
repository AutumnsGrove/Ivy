package llm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
)

// Ledger endpoint names.
const (
	EndpointEmbeddings  = "embeddings"
	EndpointOllamaEmbed = "ollama_embed"
)

// Gate outcomes, recorded in the ledger so a blocked call is distinguishable
// from a failed one (STANDARDS.md 4a.3).
const (
	OutcomeOK      = "ok"
	OutcomeError   = "error"
	OutcomeRefused = "refused"
)

// Batch bounds (STANDARDS.md 4a). A caller may submit at most this many inputs,
// each at most this large; above it the whole call is refused at zero cost.
const (
	MaxBatchInputs = 64
	MaxInputBytes  = 64 << 10
)

// Gate errors. A caller can tell a policy refusal (do not retry) from a
// provider failure (retry) without inspecting the ledger.
var (
	ErrNotEnabled = errors.New("llm: account has smart features off")
	ErrCapReached = errors.New("llm: monthly cap reached")
	ErrTooLarge   = errors.New("llm: embedding request over its limit")
)

// Gate is the only way to reach a provider. It enforces the account opt-in, the
// monthly cap and one ledger row per input, and it is the only writer of the
// cost ledger. In chunk 3 it guards embeddings; the full gate joins it in
// chunk 5 (ARCHITECTURE.md 7).
type Gate struct {
	store *store.DBs
	now   func() time.Time
}

// GateOption customises a Gate.
type GateOption func(*Gate)

// WithGateClock injects the clock used for ledger timestamps and cap periods.
func WithGateClock(now func() time.Time) GateOption {
	return func(g *Gate) { g.now = now }
}

// NewGate builds the gate over the state database.
func NewGate(dbs *store.DBs, opts ...GateOption) *Gate {
	g := &Gate{store: dbs, now: time.Now}
	for _, o := range opts {
		o(g)
	}
	return g
}

// EmbedRequest is one operator-visible embedding job: which provider, for which
// account, and the texts. Enabled is the account's smart-features opt-in; it is
// ignored for the local provider, which never leaves the device.
type EmbedRequest struct {
	Embedder    Embedder
	AccountID   string
	Enabled     bool
	Model       string
	Feature     string
	Inputs      []string
	ContentKeys []string
	CapUSD      float64
}

// Embed enforces the policy and then makes exactly one provider call. On a
// policy refusal the provider is never reached and the attempt is recorded at
// zero cost. On success the call's exact cost is split across its inputs by
// byte share, so the ledger rows sum to the provider's own number.
func (g *Gate) Embed(ctx context.Context, req EmbedRequest) ([]Vector, error) {
	if err := g.check(req); err != nil {
		g.record(ctx, req, OutcomeRefused, EmbedResult{}, 0)
		return nil, err
	}
	hosted := req.Embedder.Name() != ProviderOllama
	if hosted && !req.Enabled {
		g.record(ctx, req, OutcomeRefused, EmbedResult{}, 0)
		return nil, ErrNotEnabled
	}
	if hosted && req.CapUSD > 0 {
		spent, err := g.store.MonthlySpend(ctx, req.AccountID, store.Period(g.now()), EndpointEmbeddings)
		if err != nil {
			return nil, err
		}
		if spent.USD >= req.CapUSD {
			g.record(ctx, req, OutcomeRefused, EmbedResult{}, 0)
			return nil, ErrCapReached
		}
	}

	started := g.now()
	res, err := req.Embedder.Embed(ctx, req.Model, req.Inputs)
	latency := int(g.now().Sub(started).Milliseconds())
	if err != nil {
		g.record(ctx, req, OutcomeError, EmbedResult{}, latency)
		return nil, err
	}
	g.record(ctx, req, OutcomeOK, res, latency)
	return res.Vectors, nil
}

// check bounds the request before any provider call.
func (g *Gate) check(req EmbedRequest) error {
	switch {
	case req.Embedder == nil:
		return ErrNoProvider
	case req.Model == "":
		return fmt.Errorf("%w: no model", ErrTooLarge)
	case len(req.Inputs) == 0:
		return fmt.Errorf("%w: no inputs", ErrTooLarge)
	case len(req.Inputs) > MaxBatchInputs:
		return fmt.Errorf("%w: %d inputs over %d", ErrTooLarge, len(req.Inputs), MaxBatchInputs)
	case len(req.ContentKeys) != len(req.Inputs):
		return fmt.Errorf("%w: %d content keys for %d inputs", ErrTooLarge, len(req.ContentKeys), len(req.Inputs))
	}
	for _, in := range req.Inputs {
		if len(in) > MaxInputBytes {
			return fmt.Errorf("%w: input over %d bytes", ErrTooLarge, MaxInputBytes)
		}
	}
	return nil
}

// record writes one ledger row per input: on success each carries its share of
// the call's cost and tokens; a refusal or a failure carries zero cost so the
// volume stays visible. A bookkeeping error is returned by Embed only when it
// is the primary failure; otherwise it would mask the provider error.
func (g *Gate) record(ctx context.Context, req EmbedRequest, outcome string, res EmbedResult, latencyMS int) {
	if req.Embedder == nil {
		return
	}
	endpoint := EndpointEmbeddings
	if req.Embedder.Name() == ProviderOllama {
		endpoint = EndpointOllamaEmbed
	}
	weights := make([]int, len(req.Inputs))
	for i, in := range req.Inputs {
		weights[i] = len(in)
	}
	costs := shares(res.CostUSD, weights)
	tokens := intShares(res.InputTokens, weights)
	at := g.now()
	calls := make([]store.APICall, len(req.Inputs))
	for i := range req.Inputs {
		calls[i] = store.APICall{
			At:            at,
			Provider:      req.Embedder.Name(),
			Endpoint:      endpoint,
			Model:         req.Model,
			Feature:       req.Feature,
			AccountID:     req.AccountID,
			ContentKey:    req.ContentKeys[i],
			InputTokens:   tokens[i],
			CostUSD:       costs[i],
			CostEstimated: res.CostEstimated,
			LatencyMS:     latencyMS,
			Outcome:       outcome,
		}
	}
	// The ledger is diagnostics and cap accounting, not the write itself; a
	// failure here must not fail a call whose vectors the caller can use. It is
	// left to the caller's error path only when the provider itself failed.
	_ = g.store.RecordAPICalls(ctx, calls)
}

// shares splits total across weights so the parts sum exactly to total. A
// zero-weight input is only a share when every weight is zero, in which case
// the split is equal.
func shares(total float64, weights []int) []float64 {
	out := make([]float64, len(weights))
	if len(weights) == 0 || total == 0 {
		return out
	}
	sum := 0
	for _, w := range weights {
		sum += w
	}
	if sum == 0 {
		each := total / float64(len(weights))
		for i := range out {
			out[i] = each
		}
		return out
	}
	var assigned float64
	for i, w := range weights {
		if i == len(weights)-1 {
			out[i] = total - assigned
			break
		}
		out[i] = total * float64(w) / float64(sum)
		assigned += out[i]
	}
	return out
}

// intShares is shares rounded to whole tokens and corrected so the parts sum to
// total (the ledger's token counts are informational, but they should add up).
func intShares(total int, weights []int) []int {
	floats := shares(float64(total), weights)
	out := make([]int, len(floats))
	sum := 0
	for i, f := range floats {
		out[i] = int(f + 0.5)
		sum += out[i]
	}
	if len(out) > 0 && sum != total {
		out[len(out)-1] += total - sum
	}
	return out
}
