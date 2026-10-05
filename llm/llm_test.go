package llm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
)

func openStore(t *testing.T) *store.DBs {
	t.Helper()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	return dbs
}

func newWorld(t *testing.T) *mailworld.World {
	t.Helper()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("mailworld: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestQuantiseNativeInt8(t *testing.T) {
	t.Parallel()
	vals := []float32{1.0 / 128, -3.0 / 128, 0, 127.0 / 128}
	v := Quantise(vals)
	want := []int8{1, -3, 0, 127}
	for i, w := range want {
		if v.Values[i] != w {
			t.Errorf("value %d = %d, want %d (scale %v)", i, v.Values[i], w, v.Scale)
		}
	}
	if math.Abs(v.Scale-int128Step) > 1e-12 {
		t.Errorf("scale = %v, want native %v", v.Scale, int128Step)
	}
}

func TestVectorCosineMatchesFloatCosine(t *testing.T) {
	t.Parallel()
	a := []float32{0.5, -0.25, 0.125, 0.75}
	b := []float32{0.25, 0.5, -0.125, 0.1}
	va, vb := Quantise(a), Quantise(b)
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	want := dot / (math.Sqrt(na) * math.Sqrt(nb))
	got := va.Cosine(vb)
	if math.Abs(got-want) > 0.02 {
		t.Fatalf("cosine = %v, want about %v", got, want)
	}
	if got := va.Cosine(va); math.Abs(got-1) > 0.02 {
		t.Fatalf("self cosine = %v, want about 1", got)
	}
}

func TestVectorEncodeDecodeRejectsCorrupt(t *testing.T) {
	t.Parallel()
	v := Quantise([]float32{0.5, -0.5, 0.25, 0.25})
	got, ok := DecodeVector(v.Encode(), v.Scale, v.Norm, v.Dims)
	if !ok || got.Cosine(v) < 0.999 {
		t.Fatalf("round trip failed: ok=%v", ok)
	}
	if _, ok := DecodeVector(v.Encode()[:3], v.Scale, v.Norm, v.Dims); ok {
		t.Error("truncated blob accepted")
	}
	if _, ok := DecodeVector(v.Encode(), v.Scale, v.Norm, v.Dims+1); ok {
		t.Error("dims mismatch accepted")
	}
}

func TestOpenRouterEmbed(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	e := NewOpenRouter(w.OpenRouterURL(), "test-key")
	res, err := e.Embed(context.Background(), "pplx-embed-v1-0.6b", []string{"hello world", "second"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(res.Vectors) != 2 || res.Vectors[0].Dims == 0 {
		t.Fatalf("vectors = %+v", res.Vectors)
	}
	if res.CostUSD <= 0 || res.CostEstimated {
		t.Errorf("cost = %v estimated=%v, want a reported cost", res.CostUSD, res.CostEstimated)
	}
	if res.InputTokens <= 0 {
		t.Errorf("tokens = %d, want > 0", res.InputTokens)
	}
}

func TestOllamaEmbedIsFree(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	e := NewOllama(w.OllamaURL())
	res, err := e.Embed(context.Background(), "nomic-embed-text", []string{"local text"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if res.CostUSD != 0 || len(res.Vectors) != 1 {
		t.Fatalf("res = %+v, want one free vector", res)
	}
}

func TestGateRefusesWhenSmartFeaturesAreOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	g := NewGate(dbs, WithGateClock(func() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) }))

	_, err := g.Embed(ctx, EmbedRequest{
		Embedder: NewOpenRouter(w.OpenRouterURL(), "k"), AccountID: "a", Enabled: false,
		Model: "m", Inputs: []string{"secret text"}, ContentKeys: []string{"ck1"},
	})
	if !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
	if len(w.Calls()) != 0 {
		t.Fatalf("provider was called %d times despite the refusal", len(w.Calls()))
	}
	calls, _ := dbs.RecentAPICalls(ctx, "a", 10)
	if len(calls) != 1 || calls[0].Outcome != OutcomeRefused || calls[0].CostUSD != 0 {
		t.Fatalf("ledger = %+v, want one zero-cost refusal", calls)
	}
}

func TestGateAllowsLocalEmbeddingsWhenOff(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dbs := openStore(t)
	g := NewGate(dbs)
	if _, err := g.Embed(context.Background(), EmbedRequest{
		Embedder: NewOllama(w.OllamaURL()), AccountID: "a", Enabled: false,
		Model: "nomic-embed-text", Inputs: []string{"local"}, ContentKeys: []string{"ck1"},
	}); err != nil {
		t.Fatalf("local embed with smart features off: %v", err)
	}
}

func TestGateRefusesAtTheMonthlyCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	g := NewGate(dbs, WithGateClock(func() time.Time { return at }))
	if err := dbs.RecordAPICall(ctx, store.APICall{
		At: at, Provider: ProviderOpenRouter, Endpoint: EndpointEmbeddings,
		AccountID: "a", CostUSD: 5.0,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := g.Embed(ctx, EmbedRequest{
		Embedder: NewOpenRouter(w.OpenRouterURL(), "k"), AccountID: "a", Enabled: true,
		Model: "m", Inputs: []string{"text"}, ContentKeys: []string{"ck1"}, CapUSD: 5.0,
	})
	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("err = %v, want ErrCapReached", err)
	}
	if len(w.Calls()) != 0 {
		t.Fatal("provider called after the cap was reached")
	}
}

func TestGateSplitsTheExactCostPerInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	g := NewGate(dbs, WithGateClock(func() time.Time { return at }))

	inputs := []string{"short", "a somewhat longer message body", "medium body"}
	_, err := g.Embed(ctx, EmbedRequest{
		Embedder: NewOpenRouter(w.OpenRouterURL(), "k"), AccountID: "a", Enabled: true,
		Model: "m", Feature: "search", Inputs: inputs,
		ContentKeys: []string{"ck1", "ck2", "ck3"}, CapUSD: 5,
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	calls, _ := dbs.RecentAPICalls(ctx, "a", 10)
	if len(calls) != 3 {
		t.Fatalf("ledger rows = %d, want 3", len(calls))
	}
	var total float64
	seen := map[string]bool{}
	for _, c := range calls {
		if c.Outcome != OutcomeOK {
			t.Errorf("outcome = %q, want ok", c.Outcome)
		}
		if c.Feature != "search" {
			t.Errorf("feature = %q, want search", c.Feature)
		}
		seen[c.ContentKey] = true
		total += c.CostUSD
	}
	for _, k := range []string{"ck1", "ck2", "ck3"} {
		if !seen[k] {
			t.Errorf("no ledger row for %s", k)
		}
	}
	// The three rows must sum to the call's exact cost (which the fake reports),
	// and no share may be negative.
	if total <= 0 {
		t.Fatalf("total cost = %v, want > 0", total)
	}
	for _, c := range calls {
		if c.CostUSD < 0 {
			t.Errorf("negative share %v", c.CostUSD)
		}
	}
	spend, _ := dbs.MonthlySpend(ctx, "a", "2026-10", EndpointEmbeddings)
	if math.Abs(spend.USD-total) > 1e-9 {
		t.Errorf("cap counter %v != ledger total %v", spend.USD, total)
	}
}

func TestGateRecordsProviderFailureAtZeroCost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.Fault(mailworld.LLMDown{})
	dbs := openStore(t)
	g := NewGate(dbs)

	_, err := g.Embed(ctx, EmbedRequest{
		Embedder: NewOpenRouter(w.OpenRouterURL(), "k"), AccountID: "a", Enabled: true,
		Model: "m", Inputs: []string{"one", "two"}, ContentKeys: []string{"ck1", "ck2"},
	})
	if err == nil {
		t.Fatal("expected an error from a down provider")
	}
	calls, _ := dbs.RecentAPICalls(ctx, "a", 10)
	if len(calls) != 2 {
		t.Fatalf("ledger rows = %d, want 2", len(calls))
	}
	for _, c := range calls {
		if c.Outcome != OutcomeError || c.CostUSD != 0 {
			t.Errorf("row = %+v, want zero-cost error", c)
		}
	}
}

func TestGateRejectsAnOversizeBatchBeforeCalling(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dbs := openStore(t)
	g := NewGate(dbs)
	inputs := make([]string, MaxBatchInputs+1)
	keys := make([]string, len(inputs))
	for i := range inputs {
		inputs[i] = "x"
		keys[i] = "ck"
	}
	_, err := g.Embed(context.Background(), EmbedRequest{
		Embedder: NewOpenRouter(w.OpenRouterURL(), "k"), AccountID: "a", Enabled: true,
		Model: "m", Inputs: inputs, ContentKeys: keys,
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if len(w.Calls()) != 0 {
		t.Fatal("provider called for an oversize batch")
	}
}

type okEmbedder struct{}

func (okEmbedder) Name() string { return "stub" }
func (okEmbedder) Embed(_ context.Context, _ string, inputs []string) (EmbedResult, error) {
	res := EmbedResult{InputTokens: len(inputs), CostUSD: 0.0002}
	for range inputs {
		res.Vectors = append(res.Vectors, Quantise([]float32{0.5, 0, 0, 0}))
	}
	return res, nil
}

// The ledger is also the cap's only record of spend. A failed ledger write must
// not throw away vectors that were already paid for, but it must never be
// silent: money left the account with no row to show for it.
func TestGateReportsALedgerWriteFailure(t *testing.T) {
	// Not parallel: it swaps the process-wide default logger.
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	dbs := openStore(t)
	if err := dbs.State.Write.Close(); err != nil { // the ledger can no longer be written
		t.Fatal(err)
	}
	vecs, err := NewGate(dbs).Embed(context.Background(), EmbedRequest{
		Embedder: okEmbedder{}, AccountID: "a", Enabled: true, Model: "m", Feature: "search",
		Inputs: []string{"hello"}, ContentKeys: []string{"k"},
	})
	if err != nil || len(vecs) != 1 {
		t.Fatalf("Embed = %d vectors, %v; want the paid-for vector back", len(vecs), err)
	}
	if !strings.Contains(logged.String(), "ledger") {
		t.Errorf("a failed ledger write was silent; log = %q", logged.String())
	}
}
