package store

import (
	"context"
	"math"
	"testing"
	"time"
)

var spendEpoch = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func seedLedger(t *testing.T, dbs *DBs, calls ...APICall) {
	t.Helper()
	for i, c := range calls {
		if c.At.IsZero() {
			c.At = spendEpoch.Add(time.Duration(i) * time.Minute)
		}
		if c.Outcome == "" {
			c.Outcome = "ok"
		}
		if err := dbs.RecordAPICall(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
}

// The totals count calls, not rows: one embedding call writes a row per input, all
// sharing a call id, and the stats panel must not report each input as a call.
func TestSpendReportCountsCallsNotRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedLedger(t, dbs,
		APICall{Endpoint: "embeddings", Feature: "search", Model: "m1", AccountID: "a", CostUSD: 0.25, CallID: "c1"},
		APICall{Endpoint: "embeddings", Feature: "search", Model: "m1", AccountID: "a", CostUSD: 0.75, CallID: "c1"},
		APICall{Endpoint: "systemone", Feature: "needs_me", Model: "jev-latest", AccountID: "b", CostUSD: 1, CallID: "c2"},
		// A refusal is not spend and not a sent call; it is counted as blocked, by reason.
		APICall{Endpoint: "chat", Feature: "summary", Model: "m2", AccountID: "a", Outcome: "refused", Reason: "cap_account", CallID: "c3"},
		APICall{Endpoint: "chat", Feature: "summary", Model: "m2", AccountID: "b", Outcome: "refused", Reason: "cap_account", CallID: "c4"},
		APICall{Endpoint: "chat", Feature: "summary", Model: "m2", AccountID: "b", Outcome: "refused", Reason: "withheld", CallID: "c5"},
		// A call with no id (an old row) still counts once.
		APICall{Endpoint: "chat", Feature: "ask", Model: "m2", AccountID: "a", Outcome: "error"},
	)
	r, err := dbs.SpendReport(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total.Calls != 3 || math.Abs(r.Total.USD-2) > 1e-12 {
		t.Errorf("total = %+v, want 3 calls (c1, c2, the id-less error) and $2", r.Total)
	}
	byKey := func(gs []SpendGroup) map[string]SpendGroup {
		m := map[string]SpendGroup{}
		for _, g := range gs {
			m[g.Key] = g
		}
		return m
	}
	feat := byKey(r.ByFeature)
	if feat["search"].Calls != 1 || feat["search"].USD != 1 || feat["needs_me"].USD != 1 || feat["ask"].Calls != 1 {
		t.Errorf("by feature = %+v", r.ByFeature)
	}
	if _, ok := feat["summary"]; ok {
		t.Errorf("by feature includes a feature that only had refusals: %+v", r.ByFeature)
	}
	if acct := byKey(r.ByAccount); acct["a"].Calls != 2 || acct["b"].Calls != 1 || acct["a"].USD != 1 {
		t.Errorf("by account = %+v", r.ByAccount)
	}
	if model := byKey(r.ByModel); model["m1"].USD != 1 || model["jev-latest"].Calls != 1 {
		t.Errorf("by model = %+v", r.ByModel)
	}
	blocked := byKey(r.Blocked)
	if blocked["cap_account"].Calls != 2 || blocked["withheld"].Calls != 1 || len(blocked) != 2 {
		t.Errorf("blocked = %+v, want cap_account 2 and withheld 1", r.Blocked)
	}
	// Largest spend first, so the screen's order needs no further sorting.
	if len(r.ByFeature) < 2 || r.ByFeature[0].USD < r.ByFeature[1].USD {
		t.Errorf("by feature not ordered by spend: %+v", r.ByFeature)
	}
}

func TestSpendReportOnlyCountsTheWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedLedger(t, dbs,
		APICall{At: spendEpoch.AddDate(0, 0, -10), Endpoint: "chat", Feature: "ask", AccountID: "a", CostUSD: 5, CallID: "old"},
		APICall{At: spendEpoch, Endpoint: "chat", Feature: "ask", AccountID: "a", CostUSD: 1, CallID: "new"},
	)
	week, err := dbs.SpendReport(ctx, spendEpoch.AddDate(0, 0, -7))
	if err != nil || week.Total.USD != 1 || week.Total.Calls != 1 {
		t.Fatalf("7 days = %+v, %v; want only the recent call", week.Total, err)
	}
	all, _ := dbs.SpendReport(ctx, time.Time{})
	if all.Total.USD != 6 || all.Total.Calls != 2 {
		t.Fatalf("all time = %+v, want both", all.Total)
	}
	empty, err := dbs.SpendReport(ctx, spendEpoch.AddDate(1, 0, 0))
	if err != nil || empty.Total != (SpendGroup{}) || len(empty.ByFeature) != 0 {
		t.Fatalf("future window = %+v, %v; want nothing", empty, err)
	}
}

// Paging is keyset on the row id, so a row written while the operator is reading
// the log neither repeats nor skips a row on the next page.
func TestListAPICallsPagesWithoutRepeatsOrGaps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	for i := range 25 {
		seedLedger(t, dbs, APICall{At: spendEpoch.Add(time.Duration(i) * time.Minute), Endpoint: "chat", Feature: "ask", AccountID: "a", CallID: "c"})
	}
	first, err := dbs.ListAPICalls(ctx, CallFilter{}, 0, 10)
	if err != nil || len(first) != 10 {
		t.Fatalf("first page = %d, %v", len(first), err)
	}
	// A new call arrives between page loads.
	seedLedger(t, dbs, APICall{At: spendEpoch.Add(time.Hour), Endpoint: "chat", Feature: "ask", AccountID: "a", CallID: "late"})

	seen := map[int64]bool{}
	for _, r := range first {
		seen[r.ID] = true
	}
	cursor := first[len(first)-1].ID
	for {
		page, err := dbs.ListAPICalls(ctx, CallFilter{}, cursor, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, r := range page {
			if seen[r.ID] {
				t.Fatalf("row %d appeared twice", r.ID)
			}
			seen[r.ID] = true
		}
		cursor = page[len(page)-1].ID
	}
	if len(seen) != 25 {
		t.Errorf("paged %d rows, want the 25 that existed at the first page", len(seen))
	}
	if first[0].ID < first[1].ID {
		t.Error("not newest first")
	}
}

func TestListAPICallsFilters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedLedger(t, dbs,
		APICall{Endpoint: "chat", Feature: "ask", AccountID: "a", Outcome: "ok"},
		APICall{Endpoint: "chat", Feature: "ask", AccountID: "b", Outcome: "error"},
		APICall{Endpoint: "systemone", Feature: "needs_me", AccountID: "a", Outcome: "refused", Reason: "not_enabled"},
		APICall{At: spendEpoch.AddDate(0, 0, -30), Endpoint: "chat", Feature: "ask", AccountID: "a", Outcome: "ok"},
	)
	cases := map[string]struct {
		f    CallFilter
		want int
	}{
		"outcome": {CallFilter{Outcome: "error"}, 1},
		"feature": {CallFilter{Feature: "ask"}, 3},
		"account": {CallFilter{AccountID: "a"}, 3},
		"window":  {CallFilter{From: spendEpoch.AddDate(0, 0, -1)}, 3},
		"all":     {CallFilter{}, 4},
		"none":    {CallFilter{Outcome: "rejected"}, 0},
	}
	for name, c := range cases {
		rows, err := dbs.ListAPICalls(ctx, c.f, 0, 100)
		if err != nil || len(rows) != c.want {
			t.Errorf("%s: %d rows, %v; want %d", name, len(rows), err, c.want)
		}
		if n, err := dbs.CountAPICalls(ctx, c.f); err != nil || n != c.want {
			t.Errorf("%s: count %d, %v; want %d", name, n, err, c.want)
		}
	}
	rows, _ := dbs.ListAPICalls(ctx, CallFilter{Outcome: "refused"}, 0, 10)
	if len(rows) != 1 || rows[0].Reason != "not_enabled" {
		t.Errorf("refused row = %+v, want its reason kept", rows)
	}
}

func TestListAPICallsBoundsTheLimit(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	if rows, err := dbs.ListAPICalls(context.Background(), CallFilter{}, 0, 0); err != nil || rows != nil {
		t.Errorf("limit 0 = %v, %v; want nothing", rows, err)
	}
}
