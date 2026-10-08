package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
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

// optIn stores the account's smart-features switch, the way the app does. The gate
// reads it from there; a request cannot carry it.
func optIn(t *testing.T, dbs *store.DBs, id string, on bool) {
	t.Helper()
	ctx := context.Background()
	if err := dbs.SaveAccountConfig(ctx, store.AccountConfig{
		ID: id, Address: id + "@example.test", Username: id,
		IMAPHost: "imap.example.test", IMAPPort: 993, SMTPHost: "smtp.example.test", SMTPPort: 465,
		LLMEnabled: on, EmbedProvider: "openrouter", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("save account config: %v", err)
	}
}

// gateFor builds a gate whose providers are the fake world, the way `ivy run`
// points it at the real ones.
func gateFor(t *testing.T, dbs *store.DBs, w *mailworld.World, opts ...GateOption) *Gate {
	t.Helper()
	base := []GateOption{WithProviders(ProviderConfig{
		OpenRouterBase: w.OpenRouterURL(), APIKey: "k", OllamaURL: w.OllamaURL(),
	})}
	return NewGate(dbs, append(base, opts...)...)
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
	e := newOpenRouter(w.OpenRouterURL(), "test-key")
	vecs, u, err := e.embed(context.Background(), "pplx-embed-v1-0.6b", []string{"hello world", "second"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 2 || vecs[0].Dims == 0 {
		t.Fatalf("vectors = %+v", vecs)
	}
	if u.CostUSD <= 0 || u.CostEstimated {
		t.Errorf("cost = %v estimated=%v, want a reported cost", u.CostUSD, u.CostEstimated)
	}
	if u.InputTokens <= 0 {
		t.Errorf("tokens = %d, want > 0", u.InputTokens)
	}
}

func TestOllamaEmbedIsFree(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	vecs, u, err := newOllama(w.OllamaURL()).embed(context.Background(), "nomic-embed-text", []string{"local text"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if u.CostUSD != 0 || len(vecs) != 1 {
		t.Fatalf("usage = %+v, %d vectors, want one free vector", u, len(vecs))
	}
}

func TestGateRefusesWhenSmartFeaturesAreOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", false)
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) }))

	_, err := g.Embed(ctx, EmbedRequest{
		Provider: ProviderOpenRouter, AccountID: "a", Feature: "search",
		Model: "m", Inputs: []string{"secret text"}, ContentKeys: []string{"ck1"},
	})
	if !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
	if len(w.Calls()) != 0 {
		t.Fatalf("provider was called %d times despite the refusal", len(w.Calls()))
	}
	calls, _ := dbs.RecentAPICalls(ctx, "a", 10)
	if len(calls) != 1 || calls[0].Outcome != OutcomeRefused || calls[0].CostUSD != 0 || calls[0].Reason != ReasonNotEnabled {
		t.Fatalf("ledger = %+v, want one zero-cost refusal with reason not_enabled", calls)
	}
}

func TestGateAllowsLocalEmbeddingsWhenOff(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", false)
	g := gateFor(t, dbs, w)
	if _, err := g.Embed(context.Background(), EmbedRequest{
		Provider: ProviderOllama, AccountID: "a", Feature: "search",
		Model: "nomic-embed-text", Inputs: []string{"local"}, ContentKeys: []string{"ck1"},
	}); err != nil {
		t.Fatalf("local embed with smart features off: %v", err)
	}
	calls, _ := dbs.RecentAPICalls(context.Background(), "a", 10)
	if len(calls) != 1 || calls[0].Endpoint != EndpointOllamaEmbed || calls[0].CostUSD != 0 {
		t.Fatalf("ledger = %+v, want one zero-cost ollama_embed row", calls)
	}
}

func TestGateRefusesAtTheMonthlyCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return at }), WithDefaultCaps(5, 100))
	if err := dbs.RecordAPICall(ctx, store.APICall{
		At: at, Provider: ProviderOpenRouter, Endpoint: EndpointEmbeddings,
		AccountID: "a", CostUSD: 5.0,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := g.Embed(ctx, EmbedRequest{
		Provider: ProviderOpenRouter, AccountID: "a", Feature: "search",
		Model: "m", Inputs: []string{"text"}, ContentKeys: []string{"ck1"},
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
	optIn(t, dbs, "a", true)
	at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return at }))

	inputs := []string{"short", "a somewhat longer message body", "medium body"}
	_, err := g.Embed(ctx, EmbedRequest{
		Provider: ProviderOpenRouter, AccountID: "a",
		Model: "m", Feature: "search", Inputs: inputs,
		ContentKeys: []string{"ck1", "ck2", "ck3"},
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
	optIn(t, dbs, "a", true)
	g := gateFor(t, dbs, w)

	_, err := g.Embed(ctx, EmbedRequest{
		Provider: ProviderOpenRouter, AccountID: "a", Feature: "search",
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
	optIn(t, dbs, "a", true)
	g := gateFor(t, dbs, w)
	inputs := make([]string, MaxBatchInputs+1)
	keys := make([]string, len(inputs))
	for i := range inputs {
		inputs[i] = "x"
		keys[i] = "ck"
	}
	_, err := g.Embed(context.Background(), EmbedRequest{
		Provider: ProviderOpenRouter, AccountID: "a", Feature: "search",
		Model: "m", Inputs: inputs, ContentKeys: keys,
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if len(w.Calls()) != 0 {
		t.Fatal("provider called for an oversize batch")
	}
}

// okEmbedder stands in for a provider inside the package, so a test can control
// exactly what comes back without a server.
type okEmbedder struct{}

func (okEmbedder) embed(_ context.Context, _ string, inputs []string) ([]Vector, usage, error) {
	vecs := make([]Vector, len(inputs))
	for i := range vecs {
		vecs[i] = Quantise([]float32{0.5, 0, 0, 0})
	}
	return vecs, usage{InputTokens: len(inputs), CostUSD: 0.0002}, nil
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
	optIn(t, dbs, "a", true)
	g := NewGate(dbs)
	g.embedders[ProviderOpenRouter] = okEmbedder{}
	if err := dbs.State.Write.Close(); err != nil { // the ledger can no longer be written
		t.Fatal(err)
	}
	vecs, err := g.Embed(context.Background(), EmbedRequest{
		Provider: ProviderOpenRouter, AccountID: "a", Model: "m", Feature: "search",
		Inputs: []string{"hello"}, ContentKeys: []string{"k"},
	})
	if err != nil || len(vecs) != 1 {
		t.Fatalf("Embed = %d vectors, %v; want the paid-for vector back", len(vecs), err)
	}
	if !strings.Contains(logged.String(), "ledger") {
		t.Errorf("a failed ledger write was silent; log = %q", logged.String())
	}
}

// embeddingsServer answers /embeddings with one vector per input and the given
// usage object, so a test can control exactly what the provider reports.
func embeddingsServer(t *testing.T, usage string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var data []string
		for i := range req.Input {
			data = append(data, fmt.Sprintf(`{"index":%d,"embedding":[0.5,0,0,0]}`, i))
		}
		_, _ = fmt.Fprintf(w, `{"data":[%s],"usage":%s}`, strings.Join(data, ","), usage)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// With no usage.cost the call used to be ledgered at $0, so the monthly cap
// never saw the spend and could never trip. The cost is now estimated from the
// tokens and the model's listed price, and flagged as an estimate.
func TestOpenRouterEstimatesTheCostWhenNoneIsReported(t *testing.T) {
	t.Parallel()
	srv := embeddingsServer(t, `{"prompt_tokens":1000000,"total_tokens":1000000}`)

	_, u, err := newOpenRouter(srv.URL, "k").embed(context.Background(), "perplexity/pplx-embed-v1-0.6b", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !u.CostEstimated {
		t.Error("CostEstimated = false, want true when the provider reported no cost")
	}
	if math.Abs(u.CostUSD-0.004) > 1e-9 {
		t.Errorf("cost = %v for 1M tokens, want $0.004 (the model's listed price)", u.CostUSD)
	}
}

// A model with no listed price is estimated high, not at zero: a cap that trips
// early is an annoyance, a cap that never trips is a bill.
func TestAnUnpricedModelIsEstimatedConservatively(t *testing.T) {
	t.Parallel()
	srv := embeddingsServer(t, `{"prompt_tokens":1000000}`)
	_, u, err := newOpenRouter(srv.URL, "k").embed(context.Background(), "someone/new-model", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if u.CostUSD < 0.03 {
		t.Errorf("cost = %v for 1M tokens of an unpriced model, want at least the dearest listed price ($0.03)", u.CostUSD)
	}
}

// With no usage at all, the tokens are estimated from the input size the same
// way chunking sizes them (4 bytes a token), so the cost is still not zero.
func TestTokensAreEstimatedWhenTheProviderReportsNone(t *testing.T) {
	t.Parallel()
	srv := embeddingsServer(t, `{}`)
	_, u, err := newOpenRouter(srv.URL, "k").embed(context.Background(), "perplexity/pplx-embed-v1-4b", []string{strings.Repeat("x", 4000)})
	if err != nil {
		t.Fatal(err)
	}
	if u.InputTokens != 1000 || u.CostUSD <= 0 || !u.CostEstimated {
		t.Errorf("tokens=%d cost=%v estimated=%v, want 1000 tokens and a non-zero estimate", u.InputTokens, u.CostUSD, u.CostEstimated)
	}
}

// A provider that refuses one particular document (400, 413, 422) is saying
// something about the document; an outage, a rate limit or a bad key is saying
// something about the provider. The ledger tells them apart so only the first
// kind can ever count against a document.
func TestGateRecordsADocumentRefusalDistinctlyFromAnOutage(t *testing.T) {
	t.Parallel()
	for status, want := range map[int]string{
		http.StatusBadRequest:            OutcomeRejected,
		http.StatusRequestEntityTooLarge: OutcomeRejected,
		http.StatusUnprocessableEntity:   OutcomeRejected,
		http.StatusUnauthorized:          OutcomeError,
		http.StatusForbidden:             OutcomeError,
		http.StatusTooManyRequests:       OutcomeError,
		http.StatusServiceUnavailable:    OutcomeError,
	} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			t.Cleanup(srv.Close)
			dbs := openStore(t)
			optIn(t, dbs, "a", true)
			g := NewGate(dbs, WithProviders(ProviderConfig{OpenRouterBase: srv.URL, APIKey: "k"}))
			_, err := g.Embed(context.Background(), EmbedRequest{
				Provider: ProviderOpenRouter, AccountID: "a", Feature: "search",
				Model: "m", Inputs: []string{"one"}, ContentKeys: []string{"ck1"},
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			calls, _ := dbs.RecentAPICalls(context.Background(), "a", 10)
			if len(calls) != 1 || calls[0].Outcome != want {
				t.Fatalf("status %d: ledger = %+v, want outcome %q", status, calls, want)
			}
		})
	}
}
