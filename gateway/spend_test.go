package gateway

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

var spendNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func spendServer(t *testing.T) (string, *store.DBs) {
	t.Helper()
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		s.WithClock(func() time.Time { return spendNow })
		// The gate is where the caps live; the server asks it, never the config.
		s.WithSpend(llm.NewGate(s.dbs, llm.WithGateClock(func() time.Time { return spendNow }), llm.WithDefaultCaps(5, 10)))
	})
	return srv.URL + "/api/v1", dbs
}

func record(t *testing.T, dbs *store.DBs, calls ...store.APICall) {
	t.Helper()
	if err := dbs.RecordAPICalls(context.Background(), calls); err != nil {
		t.Fatal(err)
	}
}

// Every total on the stats panel equals the sum of the ledger rows it summarises
// (5a "Tests and exit"). The rows are read back through the export, the way the
// operator would, and compared with the roll-up, over a ledger with batches,
// refusals, failures, two accounts and a window.
func TestEveryTotalEqualsTheSumOfItsRows(t *testing.T) {
	t.Parallel()
	base, dbs := spendServer(t)
	rng := rand.New(rand.NewSource(7))
	features := []string{"search", "needs_me", "summary", "ask"}
	models := []string{"m1", "m2", "jev-latest"}
	reasons := []string{"cap_account", "withheld", "not_enabled"}
	for i := range 120 {
		at := spendNow.Add(-time.Duration(rng.Intn(20*24)) * time.Hour)
		account := []string{"a", "b"}[rng.Intn(2)]
		feature, model := features[rng.Intn(len(features))], models[rng.Intn(len(models))]
		callID := fmt.Sprintf("call-%d", i)
		rows := 1 + rng.Intn(3)
		outcome := []string{"ok", "ok", "ok", "error", "refused"}[rng.Intn(5)]
		var batch []store.APICall
		for range rows {
			c := store.APICall{
				At: at, Provider: "openrouter", Endpoint: "chat", Model: model, Feature: feature,
				AccountID: account, Outcome: outcome, CallID: callID,
			}
			switch outcome {
			case "ok":
				c.CostUSD = float64(rng.Intn(1000)) / 1e6
			case "refused":
				c.Reason = reasons[rng.Intn(len(reasons))]
			}
			batch = append(batch, c)
		}
		record(t, dbs, batch...)
	}

	from := spendNow.Add(-7 * 24 * time.Hour)
	for _, window := range []struct {
		name string
		from *time.Time
	}{{"all time", nil}, {"7 days", &from}} {
		q := url.Values{"format": {"json"}}
		sq := url.Values{}
		if window.from != nil {
			q.Set("from", window.from.Format(time.RFC3339))
			sq.Set("from", window.from.Format(time.RFC3339))
		}
		var summary api.SpendSummary
		if code := getJSON(t, base+"/spend?"+sq.Encode(), &summary); code != http.StatusOK {
			t.Fatalf("%s: summary status %d", window.name, code)
		}
		var rows []api.CallRecord
		if code := getJSON(t, base+"/spend/calls/export?"+q.Encode(), &rows); code != http.StatusOK {
			t.Fatalf("%s: export status %d", window.name, code)
		}

		var usd float64
		calls := map[string]bool{}
		byFeature := map[string]float64{}
		byAccount := map[string]float64{}
		byModel := map[string]float64{}
		blocked := map[string]map[string]bool{}
		for _, r := range rows {
			if r.Outcome == api.CallRecordOutcomeRefused {
				if blocked[*r.Reason] == nil {
					blocked[*r.Reason] = map[string]bool{}
				}
				blocked[*r.Reason][r.CallId] = true
				continue
			}
			usd += r.CostUsd
			calls[r.CallId] = true
			byFeature[r.Feature] += r.CostUsd
			byAccount[r.AccountId] += r.CostUsd
			byModel[r.Model] += r.CostUsd
		}
		if math.Abs(summary.TotalUsd-usd) > 1e-9 || summary.Calls != len(calls) {
			t.Errorf("%s: total = $%v over %d calls, rows sum to $%v over %d calls", window.name, summary.TotalUsd, summary.Calls, usd, len(calls))
		}
		check := func(kind string, got []api.SpendRow, want map[string]float64) {
			if len(got) != len(want) {
				t.Errorf("%s: %s has %d groups, rows have %d", window.name, kind, len(got), len(want))
			}
			var sum float64
			for _, g := range got {
				sum += g.Usd
				if math.Abs(g.Usd-want[g.Key]) > 1e-9 {
					t.Errorf("%s: %s %q = $%v, rows sum to $%v", window.name, kind, g.Key, g.Usd, want[g.Key])
				}
			}
			if math.Abs(sum-summary.TotalUsd) > 1e-9 {
				t.Errorf("%s: %s groups sum to $%v, total is $%v", window.name, kind, sum, summary.TotalUsd)
			}
		}
		check("feature", summary.ByFeature, byFeature)
		check("model", summary.ByModel, byModel)
		var accounts []api.SpendRow
		for _, a := range summary.ByAccount {
			accounts = append(accounts, api.SpendRow{Key: a.Key, Usd: a.Usd, Calls: a.Calls})
		}
		check("account", accounts, byAccount)
		for _, b := range summary.Blocked {
			if b.Calls != len(blocked[b.Reason]) {
				t.Errorf("%s: blocked %s = %d, rows have %d distinct calls", window.name, b.Reason, b.Calls, len(blocked[b.Reason]))
			}
		}
		if len(summary.Blocked) != len(blocked) {
			t.Errorf("%s: blocked has %d reasons, rows have %d", window.name, len(summary.Blocked), len(blocked))
		}
	}
}

// The month figures are what the gate caps against (the UTC calendar month and its
// own counters), whatever window the screen shows, and the caps come from the gate.
func TestSpendSummaryReportsTheMonthAgainstTheCaps(t *testing.T) {
	t.Parallel()
	base, dbs := spendServer(t)
	record(t, dbs,
		store.APICall{At: spendNow.AddDate(0, -1, 0), Endpoint: "chat", AccountID: "a", CostUSD: 7, Outcome: "ok", CallID: "last-month"},
		store.APICall{At: spendNow.Add(-time.Hour), Endpoint: "chat", Feature: "ask", AccountID: "a", CostUSD: 2, Outcome: "ok", CallID: "c1"},
		store.APICall{At: spendNow.Add(-time.Hour), Endpoint: "chat", Feature: "ask", AccountID: "b", CostUSD: 1, Outcome: "ok", CallID: "c2"},
	)
	if err := dbs.SetSetting(context.Background(), "a", llm.SettingAccountCapUSD, "3"); err != nil {
		t.Fatal(err)
	}
	var s api.SpendSummary
	if code := getJSON(t, base+"/spend?from="+url.QueryEscape(spendNow.Add(-2*time.Hour).Format(time.RFC3339)), &s); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if s.TotalUsd != 3 {
		t.Errorf("window total = %v, want only the last two hours ($3)", s.TotalUsd)
	}
	if s.MonthUsd != 3 || s.CapUsd != 10 {
		t.Errorf("month = $%v of $%v, want $3 of the $10 global cap (last month excluded)", s.MonthUsd, s.CapUsd)
	}
	for _, a := range s.ByAccount {
		switch a.Key {
		case "a":
			if a.MonthUsd != 2 || a.CapUsd != 3 {
				t.Errorf("a = %+v, want $2 of its own $3 cap", a)
			}
		case "b":
			if a.MonthUsd != 1 || a.CapUsd != 5 {
				t.Errorf("b = %+v, want $1 of the $5 default", a)
			}
		}
	}
}

func TestSpendSummaryOfAnEmptyLedgerIsZerosNotNulls(t *testing.T) {
	t.Parallel()
	base, _ := spendServer(t)
	resp, err := http.Get(base + "/spend")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"byFeature", "byAccount", "byModel", "blocked"} {
		if string(raw[k]) != "[]" {
			t.Errorf("%s = %s, want []", k, raw[k])
		}
	}
	if string(raw["totalUsd"]) != "0" || string(raw["capUsd"]) != "10" {
		t.Errorf("total %s cap %s, want 0 and the default $10", raw["totalUsd"], raw["capUsd"])
	}
}

func TestSpendRejectsABadWindow(t *testing.T) {
	t.Parallel()
	base, _ := spendServer(t)
	for _, from := range []string{"yesterday", "2026-10-15", "1"} {
		var e api.Error
		if code := getJSON(t, base+"/spend?from="+url.QueryEscape(from), &e); code != http.StatusBadRequest || e.Code != "bad_request" {
			t.Errorf("from=%q: status %d code %q, want 400 bad_request", from, code, e.Code)
		}
	}
}

func TestCallLogPagesFiltersAndKeepsReasons(t *testing.T) {
	t.Parallel()
	base, dbs := spendServer(t)
	for i := range 30 {
		record(t, dbs, store.APICall{
			At: spendNow.Add(-time.Duration(i) * time.Minute), Endpoint: "chat", Feature: "ask", Model: "m",
			AccountID: "a", Outcome: "ok", CostUSD: 0.5, CallID: fmt.Sprintf("c%d", i),
		})
	}
	record(t, dbs, store.APICall{At: spendNow, Endpoint: "chat", Feature: "summary", AccountID: "b", Outcome: "refused", Reason: "cap_global", CallID: "r1"})

	var first api.CallPage
	if code := getJSON(t, base+"/spend/calls?limit=10", &first); code != http.StatusOK || len(first.Items) != 10 || first.NextCursor == nil {
		t.Fatalf("first page: status %d, %d items, cursor %v", code, len(first.Items), first.NextCursor)
	}
	if first.Items[0].Outcome != api.CallRecordOutcomeRefused || *first.Items[0].Reason != "cap_global" {
		t.Errorf("newest = %+v, want the refusal with its reason", first.Items[0])
	}
	seen := map[string]bool{}
	for _, it := range first.Items {
		seen[it.Id] = true
	}
	page, pages := first, 1
	for page.NextCursor != nil {
		next := api.CallPage{}
		if code := getJSON(t, base+"/spend/calls?limit=10&cursor="+*page.NextCursor, &next); code != http.StatusOK {
			t.Fatalf("page %d: status %d", pages+1, code)
		}
		for _, it := range next.Items {
			if seen[it.Id] {
				t.Fatalf("row %s repeated", it.Id)
			}
			seen[it.Id] = true
		}
		page, pages = next, pages+1
	}
	if len(seen) != 31 || pages != 4 {
		t.Errorf("paged %d rows over %d pages, want 31 over 4", len(seen), pages)
	}

	var refused api.CallPage
	getJSON(t, base+"/spend/calls?outcome=refused", &refused)
	if len(refused.Items) != 1 || refused.NextCursor != nil {
		t.Errorf("refused filter = %+v, want the one row and no cursor", refused)
	}
	var byAccount api.CallPage
	getJSON(t, base+"/spend/calls?account_id=b&feature=summary", &byAccount)
	if len(byAccount.Items) != 1 {
		t.Errorf("account+feature filter = %d rows, want 1", len(byAccount.Items))
	}
	var def api.CallPage
	getJSON(t, base+"/spend/calls", &def)
	if len(def.Items) != 25 {
		t.Errorf("default page = %d rows, want 25", len(def.Items))
	}
	var big api.CallPage
	getJSON(t, base+"/spend/calls?limit=100000", &big)
	if len(big.Items) != 31 {
		t.Errorf("an oversized limit is clamped to 100, which here returns all 31; got %d", len(big.Items))
	}
}

func TestCallLogRejectsWhatItDoesNotUnderstand(t *testing.T) {
	t.Parallel()
	base, _ := spendServer(t)
	for _, q := range []string{
		"outcome=__proto__", "outcome=acted", "cursor=forged", "cursor=-1", "limit=abc", "limit=0", "from=nope",
		"feature=" + strings.Repeat("x", 200),
	} {
		var e api.Error
		if code := getJSON(t, base+"/spend/calls?"+q, &e); code != http.StatusBadRequest || e.Code != "bad_request" {
			t.Errorf("%s: status %d code %q, want 400 bad_request", q, code, e.Code)
		}
	}
}

func TestExportCSVNeutralisesFormulasAndStatesItsSize(t *testing.T) {
	t.Parallel()
	base, dbs := spendServer(t)
	record(t, dbs,
		store.APICall{
			At: spendNow, Provider: "openrouter", Endpoint: "chat", Feature: "ask", Model: `=HYPERLINK("http://evil")`,
			AccountID: "+a", Outcome: "ok", CostUSD: 0.000123, InputTokens: 10, OutputTokens: 5, LatencyMS: 40, CallID: "c1",
		},
		store.APICall{At: spendNow, Endpoint: "chat", Feature: "summary", Model: "a,b\"c", AccountID: "a", Outcome: "refused", Reason: "withheld", CallID: "c2"},
	)
	resp, err := http.Get(base + "/spend/calls/export")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "ivy-calls.csv") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if resp.Header.Get("X-Ivy-Rows") != "2" || resp.Header.Get("X-Ivy-Truncated") != "" {
		t.Errorf("rows %q truncated %q, want 2 and not truncated", resp.Header.Get("X-Ivy-Rows"), resp.Header.Get("X-Ivy-Truncated"))
	}
	records, err := csv.NewReader(resp.Body).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v", err)
	}
	if len(records) != 3 || records[0][0] != "time" {
		t.Fatalf("records = %v", records)
	}
	col := map[string]int{}
	for i, h := range records[0] {
		col[h] = i
	}
	// Newest first, but both share a time, so find the rows by call id.
	byCall := map[string][]string{records[1][col["call_id"]]: records[1], records[2][col["call_id"]]: records[2]}
	c1, c2 := byCall["c1"], byCall["c2"]
	if c1[col["model"]] != `'=HYPERLINK("http://evil")` || c1[col["account_id"]] != "'+a" {
		t.Errorf("formula cells = %q / %q, want a leading apostrophe", c1[col["model"]], c1[col["account_id"]])
	}
	if c1[col["cost_usd"]] != "0.000123" || c1[col["input_tokens"]] != "10" || c1[col["latency_ms"]] != "40" {
		t.Errorf("numbers = %q %q %q", c1[col["cost_usd"]], c1[col["input_tokens"]], c1[col["latency_ms"]])
	}
	if c2[col["model"]] != `a,b"c` || c2[col["reason"]] != "withheld" || c2[col["outcome"]] != "refused" {
		t.Errorf("quoting/reason = %q %q %q", c2[col["model"]], c2[col["reason"]], c2[col["outcome"]])
	}
}

func TestExportJSONHonoursFiltersAndATruncationLimit(t *testing.T) {
	// Not parallel: it lowers a package-level limit.
	prev := exportRowLimit
	exportRowLimit = 3
	t.Cleanup(func() { exportRowLimit = prev })

	base, dbs := spendServer(t)
	for i := range 5 {
		// Rows are written as calls finish, so a later insert is a later call.
		record(t, dbs, store.APICall{At: spendNow.Add(time.Duration(i) * time.Minute), Endpoint: "chat", Feature: "ask", AccountID: "a", Outcome: "ok", CallID: fmt.Sprintf("c%d", i)})
	}
	resp, err := http.Get(base + "/spend/calls/export?format=json&feature=ask")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rows []api.CallRecord
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if len(rows) != 3 || rows[0].CallId != "c4" {
		t.Errorf("rows = %d (first %s), want the 3 newest", len(rows), rows[0].CallId)
	}
	if resp.Header.Get("X-Ivy-Rows") != "5" || resp.Header.Get("X-Ivy-Truncated") != "true" {
		t.Errorf("rows %q truncated %q, want 5 and true", resp.Header.Get("X-Ivy-Rows"), resp.Header.Get("X-Ivy-Truncated"))
	}

	var empty []api.CallRecord
	if code := getJSON(t, base+"/spend/calls/export?format=json&feature=nothing", &empty); code != http.StatusOK || empty == nil || len(empty) != 0 {
		t.Errorf("empty export: status %d, %v; want 200 and []", code, empty)
	}
	var e api.Error
	if code := getJSON(t, base+"/spend/calls/export?format=xml", &e); code != http.StatusBadRequest {
		t.Errorf("format=xml: status %d, want 400", code)
	}
}
