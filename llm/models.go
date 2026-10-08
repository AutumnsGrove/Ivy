package llm

import (
	"context"
	"errors"
	"log/slog"
)

// Model is one entry of the code-defined catalog, the Polaris pattern: the id is
// stable across version bumps (a stored choice keeps resolving when the provider's
// slug changes), and adding a model is a code change, not configuration. Provider
// order, temperature and reasoning join the entry with the first caller that sends
// them (5c); until then they would be fields nothing reads.
type Model struct {
	// ID is what settings store. Never reuse one for a different model.
	ID string
	// Name is the display name for the picker.
	Name string
	// Slug is the provider's model id, what goes on the wire.
	Slug string
	// Kind is the gate endpoint the model serves: chat, vision, systemone or
	// embeddings. A multimodal chat model is chat with Multimodal set.
	Kind string
	// Multimodal: the model reads images, so it may serve vision.
	Multimodal bool
	// MaxTokens bounds one answer.
	MaxTokens int
	// InPerM and OutPerM are listed dollars per million tokens. They are the
	// fallback cost for a response that reports none and the worst case the gate
	// reserves, so a flat table must not understate: a model whose price jumps with
	// prompt length (Polaris's haiku) stays out until the format can say so.
	InPerM, OutPerM float64
}

// Built-in model ids.
const (
	// JevModelID is the helper decision model: Jev behind /systemone. The interface
	// calls it that; the name Jev stays in code and docs.
	JevModelID         = "jev"
	DefaultChatModelID = "deepseek"
	EmbedSmallModelID  = "embed-small"
	EmbedLargeModelID  = "embed-large"
)

// registry is the whole catalog. Prices are the listed rates on the date beside
// each; re-survey before trusting them past a quarter (they only size caps and
// estimates, since a response that reports its cost is ledgered at that cost).
var registry = []Model{
	// docs/JEV.md: $0.042/M input tokens, free output.
	{ID: JevModelID, Name: "Helper decision model", Slug: "jev-latest", Kind: EndpointSystemOne, InPerM: 0.042},

	// OpenRouter /api/v1/embeddings/models, 2026-10-05.
	{ID: EmbedSmallModelID, Name: "Embeddings, small", Slug: "perplexity/pplx-embed-v1-0.6b", Kind: EndpointEmbeddings, InPerM: 0.004},
	{ID: EmbedLargeModelID, Name: "Embeddings, large", Slug: "perplexity/pplx-embed-v1-4b", Kind: EndpointEmbeddings, InPerM: 0.03},

	// The chat models are from Polaris's registry (its live endpoint surveys,
	// 2026-09-22 to 2026-09-30). Gate G2 confirms the defaults before the first live
	// chat call (5b).
	{
		ID: DefaultChatModelID, Name: "DeepSeek V4.1 Flash", Slug: "deepseek/deepseek-v4.1-flash",
		Kind: EndpointChat, Multimodal: true, MaxTokens: 32000, InPerM: 0.14, OutPerM: 0.42,
	},
	{
		ID: "mimo", Name: "MiMo v2.6 Flash", Slug: "xiaomi/mimo-v2.6-flash",
		Kind: EndpointChat, Multimodal: true, MaxTokens: 32000, InPerM: 0.14, OutPerM: 0.28,
	},
	// Text-only, the cheapest: for features that never see an image.
	{
		ID: "mercury", Name: "Mercury 2.5", Slug: "inception/mercury-2.5",
		Kind: EndpointChat, MaxTokens: 32000, InPerM: 0.04, OutPerM: 0.15,
	},
}

// Models lists the catalog for the settings picker. The slice is a copy.
func Models() []Model { return append([]Model(nil), registry...) }

// LookupModel finds a model by its stable id.
func LookupModel(id string) (Model, bool) {
	for _, m := range registry {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

func modelBySlug(slug string) (Model, bool) {
	for _, m := range registry {
		if m.Slug == slug {
			return m, true
		}
	}
	return Model{}, false
}

// price is a model's listed dollars per token.
type price struct{ in, out float64 }

// Fallback prices for a model with no listed price: dear on purpose. A cap that
// trips early is an annoyance; one that never trips is a bill.
var unpriced = map[string]price{
	EndpointEmbeddings: {in: 0.00000003},
	EndpointSystemOne:  {in: 0.000001, out: 0.000001},
	EndpointChat:       {in: 0.000015, out: 0.000075},
	EndpointVision:     {in: 0.000015, out: 0.000075},
}

// priceFor is the listed price of a model (by provider slug), or the fallback for
// its endpoint.
func priceFor(slug, endpoint string) price {
	if m, ok := modelBySlug(slug); ok {
		return price{in: m.InPerM / 1e6, out: m.OutPerM / 1e6}
	}
	if p, ok := unpriced[endpoint]; ok {
		return p
	}
	return unpriced[EndpointChat]
}

// estimateCost prices a call's tokens at the model's listed rate.
func estimateCost(slug, endpoint string, inTokens, outTokens int) float64 {
	p := priceFor(slug, endpoint)
	return float64(inTokens)*p.in + float64(outTokens)*p.out
}

// estimateTokensForBytes sizes text the way chunking does, at 4 bytes a token,
// rounded up so the estimate errs high.
func estimateTokensForBytes(n int) int { return (n + 3) / 4 }

// Settings that choose models. Both are global: a model is a property of the
// install and its key, not of one mailbox.
const (
	// SettingChatModel is the default chat model, a registry id.
	SettingChatModel = "llm.chat_model"
	settingModelPfx  = "llm.model."
)

// FeatureModelKey is the setting that overrides the chat model for one feature.
func FeatureModelKey(feature string) string { return settingModelPfx + feature }

// ErrNoModel means a feature does not run on a registry model: it is unknown, or it
// is an embedding, whose model is the account's own setting.
var ErrNoModel = errors.New("llm: the feature has no registry model")

// ModelFor is the model a feature will run on right now: the Jev model for the
// Jev features; for the generating ones the feature's override, else the chosen
// default, else the built-in. A stored id the catalog no longer holds, or one of
// the wrong kind for the feature, is skipped with a warning and never an error,
// so a catalog update cannot break a feature that had chosen the retired model.
func (g *Gate) ModelFor(ctx context.Context, name string) (Model, error) {
	spec, ok := features[name]
	if !ok {
		return Model{}, ErrNoModel
	}
	switch spec.endpoint {
	case EndpointSystemOne:
		m, _ := LookupModel(JevModelID)
		return m, nil
	case EndpointChat, EndpointVision:
	default:
		return Model{}, ErrNoModel
	}
	for _, key := range []string{FeatureModelKey(name), SettingChatModel} {
		id, ok, err := g.store.GetSetting(ctx, "", key)
		if err != nil || !ok || id == "" {
			continue
		}
		if m, usable := LookupModel(id); usable && fitsFeature(m, spec) {
			return m, nil
		}
		slog.WarnContext(ctx, "llm: a stored model choice cannot serve this feature; using the default",
			"setting", key, "model", id, "feature", name)
	}
	m, _ := LookupModel(DefaultChatModelID)
	return m, nil
}

// fitsFeature: a generating model, able to read images when the feature sends one.
func fitsFeature(m Model, spec feature) bool {
	if m.Kind != EndpointChat && m.Kind != EndpointVision {
		return false
	}
	return spec.endpoint != EndpointVision || m.Multimodal
}
