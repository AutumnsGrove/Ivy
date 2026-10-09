package llm

import (
	"context"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
)

func TestCanRunIsTheGatesAnswerWithoutMakingACall(t *testing.T) {
	ctx := context.Background()
	dbs, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	save := func(smart bool) {
		t.Helper()
		if err := dbs.SaveAccountConfig(ctx, store.AccountConfig{
			ID: "a", Address: "a@example.test", Username: "a",
			IMAPHost: "i", IMAPPort: 993, SMTPHost: "s", SMTPPort: 465,
			LLMEnabled: smart, EmbedProvider: "openrouter", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}
	}

	noProvider := NewGate(dbs)
	withProvider := NewGate(dbs, WithProviders(ProviderConfig{OpenRouterBase: "http://127.0.0.1:1", APIKey: "k"}))

	save(false)
	if withProvider.CanRun(ctx, "a", "needs_me") {
		t.Fatal("an account that has not opted in can run a feature")
	}
	save(true)
	if noProvider.CanRun(ctx, "a", "needs_me") {
		t.Fatal("a gate with no provider can run a Jev feature")
	}
	if withProvider.CanRun(ctx, "a", "classify") {
		t.Fatal("classify ships dark and ran without its switch")
	}
	if err := withProvider.SetFeatureEnabled(ctx, "a", "classify", true); err != nil {
		t.Fatal(err)
	}
	if !withProvider.CanRun(ctx, "a", "classify") {
		t.Fatal("an opted-in account with the switch on and a provider cannot run")
	}
	if withProvider.CanRun(ctx, "a", "no_such_feature") {
		t.Fatal("an unknown feature can run")
	}
	if withProvider.CanRun(ctx, "unknown-account", "classify") {
		t.Fatal("an account found nowhere can run")
	}

	// It is a read: no ledger row, whatever the answer.
	rows, err := dbs.RecentAPICalls(ctx, "a", 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("CanRun wrote to the ledger: %v, %v", rows, err)
	}
}
