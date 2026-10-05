package store

import (
	"context"
	"testing"
	"time"
)

func TestRecordAPICallRollsUpTheMonth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	for _, c := range []APICall{
		{At: at, Provider: "openrouter", Endpoint: "embeddings", AccountID: "a", CostUSD: 0.25, InputTokens: 10},
		{At: at.Add(time.Hour), Provider: "openrouter", Endpoint: "embeddings", AccountID: "a", CostUSD: 0.5, InputTokens: 20},
		{At: at, Provider: "ollama", Endpoint: "ollama_embed", AccountID: "a", CostUSD: 0, InputTokens: 5},
		{At: at, Provider: "openrouter", Endpoint: "embeddings", AccountID: "b", CostUSD: 5, InputTokens: 1},
	} {
		if err := dbs.RecordAPICall(ctx, c); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	got, err := dbs.MonthlySpend(ctx, "a", "2026-10", "embeddings")
	if err != nil {
		t.Fatal(err)
	}
	if got.Calls != 2 || got.USD != 0.75 {
		t.Fatalf("account a embeddings = %+v, want 2 calls / $0.75", got)
	}
	if got, _ := dbs.MonthlySpend(ctx, "a", "2026-11", "embeddings"); got != (Spend{}) {
		t.Fatalf("next month = %+v, want zero", got)
	}
	if got, _ := dbs.MonthlySpend(ctx, "b", "2026-10", "embeddings"); got.USD != 5 {
		t.Fatalf("account b = %+v, want $5", got)
	}
	// A local provider is ledgered at zero cost so its volume is still visible.
	if got, _ := dbs.MonthlySpend(ctx, "a", "2026-10", "ollama_embed"); got.Calls != 1 || got.USD != 0 {
		t.Fatalf("ollama = %+v, want 1 call / $0", got)
	}
}

func TestRecentAPICallsNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	for i, account := range []string{"a", "b", "a"} {
		if err := dbs.RecordAPICall(ctx, APICall{
			At: base.Add(time.Duration(i) * time.Minute), Endpoint: "embeddings",
			AccountID: account, CostUSD: 0.1, CallID: account, CostEstimated: i == 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := dbs.RecentAPICalls(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].AccountID != "a" {
		t.Fatalf("all = %+v, want newest first", all)
	}
	if !all[1].CostEstimated {
		t.Errorf("middle call should be marked cost-estimated: %+v", all[1])
	}
	onlyA, _ := dbs.RecentAPICalls(ctx, "a", 10)
	if len(onlyA) != 2 {
		t.Fatalf("account a = %d rows, want 2", len(onlyA))
	}
}

func TestPeriodIsUTC(t *testing.T) {
	t.Parallel()
	// 00:30 on the 1st in +02:00 is still the previous month in UTC.
	loc := time.FixedZone("plus2", 2*3600)
	if got := Period(time.Date(2026, 11, 1, 0, 30, 0, 0, loc)); got != "2026-10" {
		t.Fatalf("Period = %q, want 2026-10", got)
	}
}
