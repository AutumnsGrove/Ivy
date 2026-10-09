package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/jev"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// An account that asks for embeddings but lacks what they need (the API key, the
// Ollama address) was dropped without a word, so the operator saw a search that
// quietly never used meaning and no hint why.
func TestBuildEmbeddersSaysWhyAnAccountIsLeftOut(t *testing.T) {
	// Not parallel: it swaps the default logger.
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg := &config.Config{
		Accounts: []config.Account{
			{ID: "hosted", EmbedProvider: "openrouter", LLMEnabled: true},
			{ID: "local", EmbedProvider: "ollama"},
		},
	}
	embedders, _ := buildEmbedders(cfg, "")
	if len(embedders) != 0 {
		t.Fatalf("embedders = %v, want none without a key or an Ollama address", embedders)
	}
	out := logged.String()
	for _, want := range []string{"hosted", "OPENROUTER_API_KEY", "local", "ollama_url"} {
		if !strings.Contains(out, want) {
			t.Errorf("log does not mention %q: %s", want, out)
		}
	}
}

// The embedding stack is built in one place so `ivy run` and the dev harness run
// the same code: the dev stack used to build its gateway without it, so dev and
// the e2e suite never exercised the search that ships.
func TestNewEmbeddingEmbedsAndAnswersQueriesAgainstTheFakeProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, err := mailworld.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	dbs, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	if err := dbs.UpsertAccount(ctx, store.Account{ID: "a1", Address: "a1@example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertFolder(ctx, store.Folder{ID: "f1", AccountID: "a1", Name: "INBOX", Role: store.RoleInbox}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertMessage(ctx, store.Message{
		ID: "m1", AccountID: "a1", FolderID: "f1", UID: 1, ContentKey: "ck1",
		Subject: "Invoice", BodyText: "the domain invoice", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		LLM:      config.LLM{OpenRouterBase: w.OpenRouterURL(), EmbedModel: "m", MonthlyCapUSD: 5},
		Accounts: []config.Account{{ID: "a1", EmbedProvider: "openrouter", LLMEnabled: true}},
	}

	emb := NewEmbedding(cfg, dbs, "k")
	if emb.Worker == nil {
		t.Fatal("no worker for an account with a provider")
	}
	if n, err := emb.Worker.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want the one message embedded", n, err)
	}
	v, model, err := emb.Query.EmbedQuery(ctx, "a1", "domain")
	if err != nil || v.Dims == 0 || model != "m" {
		t.Fatalf("EmbedQuery = dims %d model %q, %v", v.Dims, model, err)
	}
}

func TestNewEmbeddingHasNoWorkerWhenNoAccountEmbeds(t *testing.T) {
	t.Parallel()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	emb := NewEmbedding(&config.Config{Accounts: []config.Account{{ID: "a1"}}}, dbs, "")
	if emb.Worker != nil {
		t.Error("a worker for accounts that have no embedding provider")
	}
	if _, _, err := emb.Query.EmbedQuery(context.Background(), "a1", "q"); !errors.Is(err, llm.ErrNoProvider) {
		t.Errorf("EmbedQuery = %v, want ErrNoProvider so search stays keyword-only", err)
	}
	// A nil worker must not become a non-nil interface, or Mirror health would call
	// a method on nil instead of reporting meaning search as off.
	if emb.Backlog() != nil {
		t.Error("Backlog is a non-nil interface with no worker behind it")
	}
}

// An account connected from the app follows the switch in state.db while Ivy
// runs, for the worker and for search alike; the config only said what it was at
// startup. Turning it off must stop remote calls at once.
func TestEmbeddingFollowsTheAppSwitchWhileRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, err := mailworld.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	dbs, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	if err := dbs.SaveAccountConfig(ctx, store.AccountConfig{
		ID: "a1", Address: "a1@example.test", Username: "a1",
		IMAPHost: "imap.example.test", IMAPPort: 993, SMTPHost: "smtp.example.test", SMTPPort: 465,
		LLMEnabled: true, EmbedProvider: "openrouter", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		LLM:      config.LLM{OpenRouterBase: w.OpenRouterURL(), EmbedModel: "m", MonthlyCapUSD: 5},
		Accounts: []config.Account{{ID: "a1", EmbedProvider: "openrouter", LLMEnabled: true}},
	}
	emb := NewEmbedding(cfg, dbs, "k", FromApp("a1"))

	if _, _, err := emb.Query.EmbedQuery(ctx, "a1", "domain"); err != nil {
		t.Fatalf("EmbedQuery with smart on = %v", err)
	}
	calls := len(w.Calls())

	if err := dbs.SetAccountSmart(ctx, "a1", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := emb.Query.EmbedQuery(ctx, "a1", "domain"); !errors.Is(err, llm.ErrNotEnabled) {
		t.Fatalf("EmbedQuery after turning smart off = %v, want ErrNotEnabled", err)
	}
	if got := len(w.Calls()); got != calls {
		t.Errorf("the provider was called %d more times after smart was turned off", got-calls)
	}
}

func devQuestion() jev.Question {
	return jev.Question{
		ID: "is_personal", Instructions: "Is this written by a person to the owner? Newsletters and receipts do not count.",
		Criteria:    map[string]string{"no": "automated or bulk", "yes": "a person wrote it"},
		QuietOption: "no", Threshold: 0.8,
	}
}

func jevCalls(w *mailworld.World) int {
	n := 0
	for _, c := range w.Calls() {
		if c.Endpoint == "systemone" {
			n++
		}
	}
	return n
}

// The classifier shares the one gate with search. Shipped with no questions it makes
// no request at all, even with an account opted in and the key present; given a
// question it classifies only mail that arrives after the first pass.
func TestNewEmbeddingBuildsTheJevLayerOnTheSameGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, err := mailworld.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	dbs, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	if err := dbs.UpsertAccount(ctx, store.Account{ID: "a1", Address: "a1@example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertFolder(ctx, store.Folder{ID: "f1", AccountID: "a1", Name: "INBOX", Role: store.RoleInbox}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		LLM:      config.LLM{OpenRouterBase: w.OpenRouterURL(), EmbedModel: "m", MonthlyCapUSD: 5},
		Accounts: []config.Account{{ID: "a1", LLMEnabled: true}},
	}

	shipped := NewEmbedding(cfg, dbs, "k")
	if shipped.Classifier == nil || shipped.Odds == nil {
		t.Fatal("the Jev layer is missing from the shared builder")
	}
	if err := shipped.Caps.SetFeatureEnabled(ctx, "a1", jev.CallFeature, true); err != nil {
		t.Fatal(err)
	}
	if n, err := shipped.Classifier.RunOnce(ctx); err != nil || n != 0 || jevCalls(w) != 0 {
		t.Fatalf("with no questions: processed=%d err=%v calls=%d", n, err, jevCalls(w))
	}

	emb := NewEmbedding(cfg, dbs, "k", WithQuestions(devQuestion()))
	if n, _ := emb.Classifier.RunOnce(ctx); n != 0 { // first pass only sets the watermark
		t.Fatalf("the first pass read %d messages", n)
	}
	if err := dbs.UpsertMessage(ctx, store.Message{
		ID: "m1", AccountID: "a1", FolderID: "f1", UID: 1, ContentKey: "ck1", Subject: "Lunch?",
		BodyText: "Are you free on Friday?", Date: time.Now().Add(time.Minute), InternalDate: time.Now().Add(time.Minute),
		From: store.Address{Name: "Ada", Address: "ada@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := emb.Classifier.RunOnce(ctx); err != nil || n != 1 || jevCalls(w) != 1 {
		t.Fatalf("processed=%d err=%v calls=%d; want the new message read once", n, err, jevCalls(w))
	}
	odds, err := emb.Odds.Odds(ctx, "a1", "ck1")
	if err != nil || len(odds.Answers) != 1 || odds.Answers[0].QuestionID != "is_personal" {
		t.Fatalf("odds = %+v, %v", odds, err)
	}
}

func TestNewEmbeddingClassifierIsInertWithoutAKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	_ = dbs.UpsertAccount(ctx, store.Account{ID: "a1", Address: "a1@example.test"})
	emb := NewEmbedding(&config.Config{Accounts: []config.Account{{ID: "a1", LLMEnabled: true}}}, dbs, "", WithQuestions(devQuestion()))
	if n, err := emb.Classifier.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("without a key: processed=%d err=%v", n, err)
	}
}
