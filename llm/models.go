package llm

// price is a model's listed dollars per token.
type price struct{ in, out float64 }

// prices holds the listed price of each model Ivy knows. It has two jobs: the
// fallback cost for a response that reports none (so the cap still sees the
// spend), and the worst-case estimate the gate reserves before a call. 5a.3's
// code-defined model registry grows this table; the lookup stays here.
var prices = map[string]price{
	// OpenRouter /api/v1/embeddings/models, 2026-10-05.
	"perplexity/pplx-embed-v1-0.6b": {in: 0.000000004},
	"perplexity/pplx-embed-v1-4b":   {in: 0.00000003},
	// docs/JEV.md: $0.042/M input tokens, free output.
	"jev-latest": {in: 0.042 / 1_000_000},
}

// Fallback prices for a model with no listed price: dear on purpose. A cap that
// trips early is an annoyance; one that never trips is a bill.
var unpriced = map[string]price{
	EndpointEmbeddings: {in: 0.00000003},
	EndpointSystemOne:  {in: 0.000001, out: 0.000001},
	EndpointChat:       {in: 0.000015, out: 0.000075},
	EndpointVision:     {in: 0.000015, out: 0.000075},
}

// priceFor is the listed price of a model, or the fallback for its endpoint.
func priceFor(model, endpoint string) price {
	if p, ok := prices[model]; ok {
		return p
	}
	if p, ok := unpriced[endpoint]; ok {
		return p
	}
	return unpriced[EndpointChat]
}

// estimateCost prices a call's tokens at the model's listed rate.
func estimateCost(model, endpoint string, inTokens, outTokens int) float64 {
	p := priceFor(model, endpoint)
	return float64(inTokens)*p.in + float64(outTokens)*p.out
}

// estimateTokensForBytes sizes text the way chunking does, at 4 bytes a token,
// rounded up so the estimate errs high.
func estimateTokensForBytes(n int) int { return (n + 3) / 4 }
