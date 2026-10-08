package llm

import (
	"context"
	"fmt"
	"math"

	"github.com/AutumnsGrove/Ivy/store"
)

// Bounds of a bulk estimate (STANDARDS 4a). Above them the request is a caller
// bug, refused as ErrBadRequest, because nothing is being spent.
const (
	// MaxEstimateItems is ten million messages, far past any mailbox, so the
	// arithmetic cannot overflow however a caller asks.
	MaxEstimateItems = 10_000_000
	// MaxQuestionsPerCall is the operator's limit of 100 questions per Jev call
	// (brief section 2).
	MaxQuestionsPerCall = 100
	// bytesPerQuestion sizes a question the way the brief does: about 110 tokens
	// of instruction and criteria, at four bytes a token.
	bytesPerQuestion = 440
)

// callTokens is the one worst-case token formula. The entry points reserve with it
// before a call and Estimate sums it before a bulk job, so the figure the operator
// approves and the figure the gate reserves cannot drift apart.
//
// size is every input byte of the call (for Jev, the state plus the questions),
// questions is how many Jev questions it carries, and maxOut bounds a generating
// model's answer.
func callTokens(endpoint string, size, questions, maxOut int) (in, out int) {
	in = estimateTokensForBytes(size)
	switch endpoint {
	case EndpointSystemOne:
		out = decideOutputTokensPerQuestion * questions
	case EndpointChat:
		out = maxOut
	case EndpointVision:
		in += imageTokens
		out = maxOut
	}
	return in, out
}

func worstCase(slug, endpoint string, size, questions, maxOut int) float64 {
	in, out := callTokens(endpoint, size, questions, maxOut)
	return estimateCost(slug, endpoint, in, out)
}

// EstimateRequest describes a bulk job (a backfill, a dry run, a re-read after an
// edit) before it is started. Like every gate request it carries no price and no
// cap; the gate reads both.
type EstimateRequest struct {
	Feature   string
	AccountID string
	// Model is a provider slug. Empty means the model the feature would run on now,
	// which an embedding feature does not have, so it must be named there or the
	// estimate uses the dear fallback price.
	Model string
	// Items is how many calls the job makes; BytesPerItem is the input size of each
	// (a Jev state, a prompt), not counting the questions.
	Items        int
	BytesPerItem int
	// Questions is the Jev questions in each call.
	Questions int
	// MaxOutputTokens bounds a generating model's answer; zero means the default.
	MaxOutputTokens int
}

// Estimate is what a bulk job would cost at most, and whether the caps leave room.
type Estimate struct {
	// Model is the registry id priced, or the slug when it is not in the catalog.
	Model string
	Items int
	// USD is the worst case, rounded up to the next cent so a job never shows as
	// free and the figure never understates.
	USD float64
	// AccountHeadroomUSD and GlobalHeadroomUSD are what is left under each cap this
	// month after recorded spend and calls already in flight.
	AccountHeadroomUSD float64
	GlobalHeadroomUSD  float64
	// Fits: the job is under both headrooms. The cap still applies as the job
	// runs; this only tells the operator before they click.
	Fits bool
}

// Estimate prices a bulk job. It never calls a provider and writes nothing.
func (g *Gate) Estimate(ctx context.Context, req EstimateRequest) (Estimate, error) {
	spec, known := features[req.Feature]
	if why := checkEstimate(req, spec, known); why != "" {
		return Estimate{}, fmt.Errorf("%w: %s", ErrBadRequest, why)
	}

	slug, label := req.Model, req.Model
	if slug == "" {
		m, err := g.ModelFor(ctx, req.Feature)
		if err == nil {
			slug, label = m.Slug, m.ID
		}
	} else if m, ok := modelBySlug(slug); ok {
		label = m.ID
	}
	maxOut := req.MaxOutputTokens
	if maxOut == 0 {
		maxOut = defaultCompleteOutputTokens
	}
	size := req.BytesPerItem + req.Questions*bytesPerQuestion
	each := worstCase(slug, spec.endpoint, size, req.Questions, maxOut)
	usd := math.Ceil(each*float64(req.Items)*100) / 100

	accountRoom, globalRoom, err := g.headroom(ctx, req.AccountID)
	if err != nil {
		return Estimate{}, err
	}
	return Estimate{
		Model: label, Items: req.Items, USD: usd,
		AccountHeadroomUSD: accountRoom, GlobalHeadroomUSD: globalRoom,
		Fits: usd <= accountRoom && usd <= globalRoom,
	}, nil
}

func checkEstimate(req EstimateRequest, spec feature, known bool) string {
	limit := MaxCompleteBytes
	switch spec.endpoint {
	case EndpointEmbeddings:
		limit = MaxInputBytes
	case EndpointSystemOne:
		limit = MaxDecideBytes
	}
	switch {
	case req.AccountID == "":
		return "no account"
	case !known:
		return fmt.Sprintf("unknown feature %q", req.Feature)
	case req.Items < 1 || req.Items > MaxEstimateItems:
		return fmt.Sprintf("%d items outside 1..%d", req.Items, MaxEstimateItems)
	case req.BytesPerItem < 0 || req.BytesPerItem > limit:
		return fmt.Sprintf("%d bytes per item outside 0..%d", req.BytesPerItem, limit)
	case req.Questions < 0 || req.Questions > MaxQuestionsPerCall:
		return fmt.Sprintf("%d questions outside 0..%d", req.Questions, MaxQuestionsPerCall)
	case req.MaxOutputTokens < 0 || req.MaxOutputTokens > MaxCompleteOutputTokens:
		return fmt.Sprintf("max output tokens %d outside 0..%d", req.MaxOutputTokens, MaxCompleteOutputTokens)
	}
	return ""
}

// headroom is what the account's cap and the global cap each have left this month.
// It reads under the gate's lock, as reserve does, so a call in flight is counted.
func (g *Gate) headroom(ctx context.Context, accountID string) (account, global float64, err error) {
	period := store.Period(g.now())
	g.mu.Lock()
	defer g.mu.Unlock()
	spent, err := g.store.AccountSpend(ctx, accountID, period)
	if err != nil {
		return 0, 0, err
	}
	all, err := g.store.GlobalSpend(ctx, period)
	if err != nil {
		return 0, 0, err
	}
	account = max(0, g.capFor(ctx, accountID)-spent.USD-g.reserved[accountID])
	global = max(0, g.globalCap(ctx)-all.USD-g.reservedAll)
	return account, global, nil
}
