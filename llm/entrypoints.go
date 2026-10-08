package llm

import (
	"context"
	"fmt"
)

// The four typed entry points. Each builds an admission from its request, takes
// the one path (Gate.run), and returns the provider's answer. None carries an
// opt-in flag, a cap, a price or a client: the gate resolves all of those.

// EmbedRequest is one embedding job: which provider kind, for which account, and
// the texts. A local provider (ProviderOllama) never leaves the device, so it is
// exempt from the opt-in and the cap and is still ledgered at zero cost.
type EmbedRequest struct {
	Provider    string
	AccountID   string
	Model       string
	Feature     string
	Inputs      []string
	ContentKeys []string
}

// Embed makes exactly one provider call, after the policy. Each input gets a
// ledger row carrying its byte share of the call's exact cost.
func (g *Gate) Embed(ctx context.Context, req EmbedRequest) ([]Vector, error) {
	a := admission{
		feature: req.Feature, accounts: []string{req.AccountID}, keys: req.ContentKeys,
		provider: req.Provider, model: req.Model,
	}
	bytes := 0
	a.weights = make([]int, len(req.Inputs))
	for i, in := range req.Inputs {
		a.weights[i] = len(in)
		bytes += len(in)
	}
	a.oversize = checkEmbed(req)
	a.estimate = estimateCost(req.Model, EndpointEmbeddings, estimateTokensForBytes(bytes), 0)

	var vecs []Vector
	err := g.run(ctx, a, func(ctx context.Context) (usage, error) {
		var u usage
		var err error
		vecs, u, err = g.embedders[req.Provider].embed(ctx, req.Model, req.Inputs)
		return u, err
	})
	if err != nil {
		return nil, err
	}
	return vecs, nil
}

func checkEmbed(req EmbedRequest) string {
	switch {
	case req.Model == "":
		return "no model"
	case len(req.Inputs) == 0:
		return "no inputs"
	case len(req.Inputs) > MaxBatchInputs:
		return fmt.Sprintf("%d inputs over %d", len(req.Inputs), MaxBatchInputs)
	case len(req.ContentKeys) != len(req.Inputs):
		return fmt.Sprintf("%d content keys for %d inputs", len(req.ContentKeys), len(req.Inputs))
	}
	for _, in := range req.Inputs {
		if len(in) > MaxInputBytes {
			return fmt.Sprintf("input over %d bytes", MaxInputBytes)
		}
	}
	return ""
}

// DecideRequest asks Jev (/systemone) a set of questions about one clipped piece
// of state. The caller clips; the gate refuses an overrun as too_large.
type DecideRequest struct {
	Feature     string
	AccountIDs  []string
	ContentKeys []string
	Model       string
	State       string
	Questions   map[string]Question
}

// DecideResult is Jev's answers and what the call cost.
type DecideResult struct {
	Answers map[string]Answer
	CostUSD float64
}

// Decide makes one /systemone call.
func (g *Gate) Decide(ctx context.Context, req DecideRequest) (DecideResult, error) {
	size := len(req.State)
	for id, q := range req.Questions {
		size += len(id) + len(q.Type) + len(q.Instructions)
		for k, v := range q.Criteria {
			size += len(k) + len(v)
		}
	}
	a := admission{
		feature: req.Feature, accounts: req.AccountIDs, keys: req.ContentKeys,
		provider: ProviderOpenRouter, model: req.Model,
		oversize: checkCall(req.Model, size, MaxDecideBytes, len(req.ContentKeys)),
		estimate: estimateCost(req.Model, EndpointSystemOne,
			estimateTokensForBytes(size), decideOutputTokensPerQuestion*len(req.Questions)),
	}
	var res DecideResult
	err := g.run(ctx, a, func(ctx context.Context) (usage, error) {
		answers, u, err := g.decider.decide(ctx, req.Model, req.State, req.Questions)
		res = DecideResult{Answers: answers, CostUSD: u.CostUSD}
		return u, err
	})
	return res, err
}

// CompleteRequest is one chat completion. The output is untrusted text: callers
// validate and render it as plain text (brief invariant 5).
type CompleteRequest struct {
	Feature     string
	AccountIDs  []string
	ContentKeys []string
	Model       string
	System      string
	Prompt      string
	// MaxTokens bounds the answer; zero means the default.
	MaxTokens int
}

// CompleteResult is the model's text and what the call cost.
type CompleteResult struct {
	Text    string
	CostUSD float64
}

// Complete makes one chat call.
func (g *Gate) Complete(ctx context.Context, req CompleteRequest) (CompleteResult, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultCompleteOutputTokens
	}
	size := len(req.System) + len(req.Prompt)
	a := admission{
		feature: req.Feature, accounts: req.AccountIDs, keys: req.ContentKeys,
		provider: ProviderOpenRouter, model: req.Model,
		oversize: checkCall(req.Model, size, MaxCompleteBytes, len(req.ContentKeys)),
		estimate: estimateCost(req.Model, EndpointChat, estimateTokensForBytes(size), maxTokens),
	}
	if a.oversize == "" && (maxTokens < 0 || maxTokens > MaxCompleteOutputTokens) {
		a.oversize = fmt.Sprintf("max tokens %d outside 1..%d", maxTokens, MaxCompleteOutputTokens)
	}
	messages := []chatMessage{{Role: "user", Content: req.Prompt}}
	if req.System != "" {
		messages = append([]chatMessage{{Role: "system", Content: req.System}}, messages...)
	}
	var res CompleteResult
	err := g.run(ctx, a, func(ctx context.Context) (usage, error) {
		text, u, err := g.completer.complete(ctx, req.Model, messages, maxTokens)
		res = CompleteResult{Text: text, CostUSD: u.CostUSD}
		return u, err
	})
	return res, err
}

// SeeRequest asks a vision model about one image. The image arrives already
// prepared (downscaled, EXIF stripped) by the caller.
type SeeRequest struct {
	Feature     string
	AccountIDs  []string
	ContentKeys []string
	Model       string
	Prompt      string
	MediaType   string
	Image       []byte
	MaxTokens   int
}

// SeeResult is the model's text and what the call cost.
type SeeResult struct {
	Text    string
	CostUSD float64
}

// See makes one vision call.
func (g *Gate) See(ctx context.Context, req SeeRequest) (SeeResult, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultCompleteOutputTokens
	}
	a := admission{
		feature: req.Feature, accounts: req.AccountIDs, keys: req.ContentKeys,
		provider: ProviderOpenRouter, model: req.Model,
		oversize: checkCall(req.Model, len(req.Prompt), MaxCompleteBytes, len(req.ContentKeys)),
		estimate: estimateCost(req.Model, EndpointVision,
			estimateTokensForBytes(len(req.Prompt))+imageTokens, maxTokens),
	}
	switch {
	case a.oversize != "":
	case len(req.Image) == 0 || req.MediaType == "":
		a.oversize = "no image"
	case len(req.Image) > MaxImageBytes:
		a.oversize = fmt.Sprintf("image over %d bytes", MaxImageBytes)
	case maxTokens < 0 || maxTokens > MaxCompleteOutputTokens:
		a.oversize = fmt.Sprintf("max tokens %d outside 1..%d", maxTokens, MaxCompleteOutputTokens)
	}
	messages := []chatMessage{{Role: "user", Content: []map[string]any{
		{"type": "text", "text": req.Prompt},
		imagePart(req.MediaType, req.Image),
	}}}
	var res SeeResult
	err := g.run(ctx, a, func(ctx context.Context) (usage, error) {
		text, u, err := g.completer.complete(ctx, req.Model, messages, maxTokens)
		res = SeeResult{Text: text, CostUSD: u.CostUSD}
		return u, err
	})
	return res, err
}

// checkCall is the size bound shared by the non-embedding entry points: a model
// is named, the payload is under its limit and the ledger rows are bounded.
func checkCall(model string, size, limit, keys int) string {
	switch {
	case model == "":
		return "no model"
	case size > limit:
		return fmt.Sprintf("%d bytes over %d", size, limit)
	case keys > MaxLedgerKeys:
		return fmt.Sprintf("%d content keys over %d", keys, MaxLedgerKeys)
	}
	return ""
}
