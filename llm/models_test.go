package llm

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestTheRegistryIsWellFormed(t *testing.T) {
	t.Parallel()
	ids, slugs := map[string]bool{}, map[string]bool{}
	for _, m := range Models() {
		if m.ID == "" || m.Name == "" || m.Slug == "" {
			t.Errorf("model %+v needs an id, a display name and a provider slug", m)
		}
		if ids[m.ID] || slugs[m.Slug] {
			t.Errorf("model %q repeats an id or a slug", m.ID)
		}
		ids[m.ID], slugs[m.Slug] = true, true
		switch m.Kind {
		case EndpointChat, EndpointVision:
			if m.OutPerM <= 0 || m.InPerM <= 0 || m.MaxTokens <= 0 {
				t.Errorf("model %q is a generating model and needs both prices and a token bound", m.ID)
			}
		case EndpointSystemOne, EndpointEmbeddings:
			if m.InPerM <= 0 {
				t.Errorf("model %q needs an input price", m.ID)
			}
		default:
			t.Errorf("model %q has kind %q, want a gate endpoint", m.ID, m.Kind)
		}
	}
	def, ok := LookupModel(DefaultChatModelID)
	if !ok {
		t.Fatalf("the built-in default chat model %q is not in the registry", DefaultChatModelID)
	}
	// Vision falls back to the default when a stored choice cannot see.
	if def.Kind != EndpointChat || !def.Multimodal {
		t.Errorf("the default %q must be a chat model that reads images", def.ID)
	}
}

// A price in the registry is the price the gate reserves and the fallback cost the
// ledger records, so there is one table and not two.
func TestPricesComeFromTheRegistry(t *testing.T) {
	t.Parallel()
	for _, m := range Models() {
		p := priceFor(m.Slug, m.Kind)
		if math.Abs(p.in-m.InPerM/1e6) > 1e-15 || math.Abs(p.out-m.OutPerM/1e6) > 1e-15 {
			t.Errorf("%s: priceFor = %+v, registry says %v/%v per M", m.ID, p, m.InPerM, m.OutPerM)
		}
	}
}

func TestTheModelAFeatureUses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	g := NewGate(dbs)

	got, err := g.ModelFor(ctx, "summary")
	if err != nil || got.ID != DefaultChatModelID {
		t.Fatalf("a chat feature with nothing chosen = %q, %v; want the built-in default %q", got.ID, err, DefaultChatModelID)
	}
	if got, err = g.ModelFor(ctx, "needs_me"); err != nil || got.Kind != EndpointSystemOne {
		t.Fatalf("a Jev feature = %+v, %v; want the helper decision model", got, err)
	}

	other := otherChatModel(t)
	if err := dbs.SetSetting(ctx, "", SettingChatModel, other.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = g.ModelFor(ctx, "summary"); got.ID != other.ID {
		t.Errorf("the chosen default = %q, want %q", got.ID, other.ID)
	}
	if err := dbs.SetSetting(ctx, "", FeatureModelKey("digest"), DefaultChatModelID); err != nil {
		t.Fatal(err)
	}
	if got, _ = g.ModelFor(ctx, "digest"); got.ID != DefaultChatModelID {
		t.Errorf("a per-feature override = %q, want %q to beat the default", got.ID, DefaultChatModelID)
	}
	if got, _ = g.ModelFor(ctx, "summary"); got.ID != other.ID {
		t.Errorf("an override on digest changed summary to %q", got.ID)
	}
}

// A catalog that drops a model must not break the features that had chosen it, and a
// setting must not be able to point a feature at a model of the wrong kind.
func TestAStoredChoiceThatNoLongerFitsFallsBackToTheDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	g := NewGate(dbs)

	for _, bad := range []string{"retired-model", JevModelID, EmbedSmallModelID, ""} {
		if err := dbs.SetSetting(ctx, "", FeatureModelKey("summary"), bad); err != nil {
			t.Fatal(err)
		}
		got, err := g.ModelFor(ctx, "summary")
		if err != nil || got.ID != DefaultChatModelID {
			t.Errorf("override %q gave %q, %v; want the default", bad, got.ID, err)
		}
	}
}

func TestVisionNeedsAModelThatCanSee(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	g := NewGate(dbs)

	blind := blindChatModel(t)
	if err := dbs.SetSetting(ctx, "", SettingChatModel, blind.ID); err != nil {
		t.Fatal(err)
	}
	got, err := g.ModelFor(ctx, "vision")
	if err != nil || !got.Multimodal {
		t.Fatalf("vision with a text-only default = %+v, %v; want a model that reads images", got, err)
	}
	if err := dbs.SetSetting(ctx, "", FeatureModelKey("vision"), blind.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = g.ModelFor(ctx, "vision"); !got.Multimodal {
		t.Errorf("a text-only override reached vision: %q", got.ID)
	}
}

func TestOnlyFeaturesThatUseARegistryModelHaveOne(t *testing.T) {
	t.Parallel()
	g := NewGate(openStore(t))
	for _, name := range []string{"search", "embed", "no-such-feature"} {
		if _, err := g.ModelFor(context.Background(), name); !errors.Is(err, ErrNoModel) {
			t.Errorf("ModelFor(%q) = %v, want ErrNoModel", name, err)
		}
	}
}

// otherChatModel is any generating model other than the default.
func otherChatModel(t *testing.T) Model {
	t.Helper()
	for _, m := range Models() {
		if m.Kind == EndpointChat && m.ID != DefaultChatModelID {
			return m
		}
	}
	t.Fatal("the registry needs a second chat model for the picker to mean anything")
	return Model{}
}

// blindChatModel is a chat model that cannot read images.
func blindChatModel(t *testing.T) Model {
	t.Helper()
	for _, m := range Models() {
		if m.Kind == EndpointChat && !m.Multimodal {
			return m
		}
	}
	t.Fatal("the registry needs a text-only chat model")
	return Model{}
}

func TestABulkEstimateIsRoundedUpToTheCent(t *testing.T) {
	t.Parallel()
	g := NewGate(openStore(t))
	jev, _ := LookupModel(JevModelID)

	// One message is a fraction of a cent, and shows as one cent, never as zero.
	one, err := g.Estimate(context.Background(), EstimateRequest{
		Feature: "needs_me", AccountID: "a", Items: 1, BytesPerItem: 4000, Questions: 3,
	})
	if err != nil || one.USD != 0.01 {
		t.Fatalf("one message = %+v, %v; want $0.01", one, err)
	}

	// 100,000 messages: the exact worst case, rounded up and never below it.
	big, err := g.Estimate(context.Background(), EstimateRequest{
		Feature: "needs_me", AccountID: "a", Items: 100_000, BytesPerItem: 4000, Questions: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	tokens := float64(estimateTokensForBytes(4000 + 3*bytesPerQuestion))
	exact := 100_000 * (tokens*jev.InPerM/1e6 + float64(3*decideOutputTokensPerQuestion)*jev.OutPerM/1e6)
	if big.USD < exact || big.USD-exact >= 0.01+1e-9 {
		t.Errorf("estimate = %v, want %v rounded up to the next cent", big.USD, exact)
	}
	if cents := big.USD * 100; math.Abs(cents-math.Round(cents)) > 1e-6 {
		t.Errorf("estimate %v is not a whole number of cents", big.USD)
	}
	if big.Model != jev.ID {
		t.Errorf("model = %q, want the helper decision model %q", big.Model, jev.ID)
	}
}

// The estimate uses the model the feature would really run, so changing the chosen
// model changes the number the operator is asked to approve.
func TestAnEstimateFollowsTheChosenModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	g := NewGate(dbs)
	req := EstimateRequest{Feature: "summary", AccountID: "a", Items: 5000, BytesPerItem: 8000, MaxOutputTokens: 300}

	before, err := g.Estimate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	other := otherChatModel(t)
	if err := dbs.SetSetting(ctx, "", SettingChatModel, other.ID); err != nil {
		t.Fatal(err)
	}
	after, err := g.Estimate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if after.Model != other.ID || before.Model != DefaultChatModelID {
		t.Fatalf("models = %q then %q, want %q then %q", before.Model, after.Model, DefaultChatModelID, other.ID)
	}
	def, _ := LookupModel(DefaultChatModelID)
	if def.InPerM != other.InPerM || def.OutPerM != other.OutPerM {
		if before.USD == after.USD {
			t.Errorf("two models with different prices gave the same estimate %v", before.USD)
		}
	}
}

func TestAnEstimateSaysWhetherTheCapsLeaveRoom(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	g := NewGate(dbs)
	req := EstimateRequest{Feature: "needs_me", AccountID: "a", Items: 100_000, BytesPerItem: 4000, Questions: 3}

	if err := dbs.SetSetting(ctx, "a", SettingAccountCapUSD, "1.00"); err != nil {
		t.Fatal(err)
	}
	est, err := g.Estimate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if est.Fits || est.AccountHeadroomUSD != 1.00 {
		t.Errorf("a job over the account's cap: %+v, want Fits=false and $1.00 of room", est)
	}

	if err := dbs.SetSetting(ctx, "a", SettingAccountCapUSD, "100.00"); err != nil {
		t.Fatal(err)
	}
	if err := dbs.SetSetting(ctx, "", SettingGlobalCapUSD, "2.00"); err != nil {
		t.Fatal(err)
	}
	if est, _ = g.Estimate(ctx, req); est.Fits || est.GlobalHeadroomUSD != 2.00 {
		t.Errorf("a job over the global cap: %+v, want Fits=false and $2.00 of room", est)
	}

	if err := dbs.SetSetting(ctx, "", SettingGlobalCapUSD, "100.00"); err != nil {
		t.Fatal(err)
	}
	if est, _ = g.Estimate(ctx, req); !est.Fits {
		t.Errorf("a job that fits both caps: %+v, want Fits=true", est)
	}

	// A cap of zero is no spend at all, so nothing fits, however small.
	if err := dbs.SetSetting(ctx, "a", SettingAccountCapUSD, "0"); err != nil {
		t.Fatal(err)
	}
	small := EstimateRequest{Feature: "needs_me", AccountID: "a", Items: 1, BytesPerItem: 100, Questions: 1}
	if est, _ = g.Estimate(ctx, small); est.Fits {
		t.Errorf("a zero cap let a job fit: %+v", est)
	}
}

func TestABadEstimateRequestIsRefused(t *testing.T) {
	t.Parallel()
	g := NewGate(openStore(t))
	ok := EstimateRequest{Feature: "needs_me", AccountID: "a", Items: 10, BytesPerItem: 100, Questions: 1}
	cases := map[string]func(*EstimateRequest){
		"no account":      func(r *EstimateRequest) { r.AccountID = "" },
		"unknown feature": func(r *EstimateRequest) { r.Feature = "mystery" },
		"no items":        func(r *EstimateRequest) { r.Items = 0 },
		"negative bytes":  func(r *EstimateRequest) { r.BytesPerItem = -1 },
		"too many items":  func(r *EstimateRequest) { r.Items = MaxEstimateItems + 1 },
		"too many bytes":  func(r *EstimateRequest) { r.BytesPerItem = MaxDecideBytes + 1 },
	}
	for name, mutate := range cases {
		req := ok
		mutate(&req)
		if _, err := g.Estimate(context.Background(), req); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v, want ErrBadRequest", name, err)
		}
	}
}
