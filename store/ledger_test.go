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

// A refused document is counted by calls, not rows: one failed call for a
// document with several chunks writes a row per chunk, all sharing the call's id
// (and, at the ledger's one-second resolution, often its timestamp too).
func TestCountRejectedCallsCountsCallsNotRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	rec := func(callID, key, outcome string, rows int) {
		t.Helper()
		var calls []APICall
		for range rows {
			calls = append(calls, APICall{At: at, Provider: "p", Endpoint: "embeddings", AccountID: "a", ContentKey: key, Outcome: outcome, CallID: callID})
		}
		if err := dbs.RecordAPICalls(ctx, calls); err != nil {
			t.Fatal(err)
		}
	}
	rec("c1", "ck1", "rejected", 3) // one call, three chunks
	rec("c2", "ck1", "rejected", 3) // a second call, in the same second
	rec("c3", "ck1", "error", 3)    // an outage: not counted
	rec("c4", "ck2", "rejected", 1) // another document

	n, err := dbs.CountRejectedCalls(ctx, "a", "ck1")
	if err != nil || n != 2 {
		t.Fatalf("CountRejectedCalls = %d, %v; want 2", n, err)
	}
}

// The cap is one budget for everything an account spends, not one per endpoint,
// so Jev, chat and vision cannot each be given a separate allowance.
func TestSpendSumsEveryEndpointForTheCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, c := range []APICall{
		{At: at, Endpoint: "embeddings", AccountID: "a", CostUSD: 0.25},
		{At: at, Endpoint: "systemone", AccountID: "a", CostUSD: 0.5},
		{At: at, Endpoint: "chat", AccountID: "b", CostUSD: 1},
		{At: at.AddDate(0, 1, 0), Endpoint: "chat", AccountID: "a", CostUSD: 9},
	} {
		if err := dbs.RecordAPICall(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := dbs.AccountSpend(ctx, "a", "2026-10"); err != nil || got.USD != 0.75 || got.Calls != 2 {
		t.Fatalf("account a = %+v, %v; want $0.75 over 2 calls across endpoints", got, err)
	}
	if got, err := dbs.GlobalSpend(ctx, "2026-10"); err != nil || got.USD != 1.75 || got.Calls != 3 {
		t.Fatalf("global = %+v, %v; want $1.75 over 3 calls, October only", got, err)
	}
}

// A refusal records why, so the stats panel can show what the gates blocked by
// cause rather than as one number.
func TestRecordAPICallKeepsTheRefusalReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if err := dbs.RecordAPICall(ctx, APICall{
		At: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), Endpoint: "chat", AccountID: "a",
		Outcome: "refused", Reason: "cap_account", CallID: "c1",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := dbs.RecentAPICalls(ctx, "a", 10)
	if err != nil || len(rows) != 1 || rows[0].Reason != "cap_account" {
		t.Fatalf("rows = %+v, %v; want the refusal reason kept", rows, err)
	}
}

// ProbeLedger answers "could a ledger row be written right now?" without leaving
// one behind: the gate's breaker uses it to learn that a broken ledger healed.
func TestProbeLedgerLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if err := dbs.ProbeLedger(ctx); err != nil {
		t.Fatalf("probe on a healthy ledger = %v", err)
	}
	if rows, _ := dbs.RecentAPICalls(ctx, "", 10); len(rows) != 0 {
		t.Errorf("probe left %d rows", len(rows))
	}
	if got, _ := dbs.GlobalSpend(ctx, Period(time.Now())); got.Calls != 0 {
		t.Errorf("probe left a counter: %+v", got)
	}

	if _, err := dbs.State.Write.ExecContext(ctx,
		`CREATE TRIGGER block_ledger BEFORE INSERT ON api_calls BEGIN SELECT RAISE(ABORT, 'ledger broken'); END`); err != nil {
		t.Fatal(err)
	}
	if err := dbs.ProbeLedger(ctx); err == nil {
		t.Fatal("probe succeeded although a ledger insert is blocked")
	}
}
