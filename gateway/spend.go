package gateway

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// SpendCaps is where the stats panel learns the caps. The gate implements it, so
// the screen states the caps the gate is applying and not a copy of the config.
type SpendCaps interface {
	AccountCapUSD(ctx context.Context, accountID string) float64
	GlobalCapUSD(ctx context.Context) float64
}

// WithSpend gives the spend endpoints the gate's caps. Without it they report the
// built-in defaults.
func (s *Server) WithSpend(caps SpendCaps) *Server {
	s.spendCaps = caps
	return s
}

// Bounds of the call log (STANDARDS 4a): a page is at most maxCallPage rows and an
// export at most exportRowLimit, stated in the response when it falls short.
const (
	defaultCallPage = 25
	maxCallPage     = 100
	exportPage      = 500
	maxFilterLen    = 64
)

// exportRowLimit is 200,000 rows, about 36 MiB of CSV; a variable only so a test
// can lower it.
var exportRowLimit = 200_000

type defaultCaps struct{}

func (defaultCaps) AccountCapUSD(context.Context, string) float64 { return llm.DefaultAccountCapUSD }
func (defaultCaps) GlobalCapUSD(context.Context) float64          { return llm.DefaultGlobalCapUSD }

func (s *Server) caps() SpendCaps {
	if s.spendCaps != nil {
		return s.spendCaps
	}
	return defaultCaps{}
}

// parseFrom reads the optional window start. A bad value is a 400, never "all
// time", so a typo cannot quietly widen a report.
func parseFrom(r *http.Request) (time.Time, bool) {
	raw := r.URL.Query().Get("from")
	if raw == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	return t, err == nil
}

var callOutcomes = map[string]bool{"ok": true, "error": true, "rejected": true, "refused": true}

// callFilter reads the log's filters from the query. Every value is checked:
// the outcome against the closed set, the others for length (they are matched
// as parameters, never spliced into SQL).
func callFilter(r *http.Request) (store.CallFilter, bool) {
	q := r.URL.Query()
	from, ok := parseFrom(r)
	f := store.CallFilter{Outcome: q.Get("outcome"), Feature: q.Get("feature"), AccountID: q.Get("account_id"), From: from}
	if !ok || (f.Outcome != "" && !callOutcomes[f.Outcome]) ||
		len(f.Feature) > maxFilterLen || len(f.AccountID) > maxFilterLen {
		return f, false
	}
	return f, true
}

func badFilter(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "bad_request", "That filter is not valid")
}

func (s *Server) handleSpend(w http.ResponseWriter, r *http.Request) {
	from, ok := parseFrom(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "That window is not valid")
		return
	}
	ctx := r.Context()
	report, err := s.dbs.SpendReport(ctx, from)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	period := store.Period(s.now())
	month, err := s.dbs.GlobalSpend(ctx, period)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := api.SpendSummary{
		TotalUsd:  report.Total.USD,
		Calls:     report.Total.Calls,
		MonthUsd:  month.USD,
		CapUsd:    s.caps().GlobalCapUSD(ctx),
		ByFeature: spendRows(report.ByFeature),
		ByModel:   spendRows(report.ByModel),
		ByAccount: make([]api.SpendAccountRow, 0, len(report.ByAccount)),
		Blocked:   make([]api.BlockedRow, 0, len(report.Blocked)),
	}
	for _, g := range report.ByAccount {
		spent, err := s.dbs.AccountSpend(ctx, g.Key, period)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		out.ByAccount = append(out.ByAccount, api.SpendAccountRow{
			Key: g.Key, Calls: g.Calls, Usd: g.USD, MonthUsd: spent.USD, CapUsd: s.caps().AccountCapUSD(ctx, g.Key),
		})
	}
	for _, g := range report.Blocked {
		out.Blocked = append(out.Blocked, api.BlockedRow{Reason: g.Key, Calls: g.Calls})
	}
	writeJSON(w, http.StatusOK, out)
}

func spendRows(groups []store.SpendGroup) []api.SpendRow {
	out := make([]api.SpendRow, 0, len(groups))
	for _, g := range groups {
		out = append(out, api.SpendRow{Key: g.Key, Calls: g.Calls, Usd: g.USD})
	}
	return out
}

func callRecord(c store.CallRow) api.CallRecord {
	rec := api.CallRecord{
		Id: strconv.FormatInt(c.ID, 10), At: c.At.UTC(), CallId: c.CallID, Feature: c.Feature,
		AccountId: c.AccountID, Provider: c.Provider, Endpoint: c.Endpoint, Model: c.Model,
		Outcome: api.CallRecordOutcome(c.Outcome), CostUsd: c.CostUSD, CostEstimated: c.CostEstimated,
		InputTokens: c.InputTokens, OutputTokens: c.OutputTokens, LatencyMs: c.LatencyMS,
	}
	if c.Reason != "" {
		reason := c.Reason
		rec.Reason = &reason
	}
	return rec
}

func (s *Server) handleListCalls(w http.ResponseWriter, r *http.Request) {
	f, ok := callFilter(r)
	if !ok {
		badFilter(w)
		return
	}
	q := r.URL.Query()
	limit := defaultCallPage
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			badFilter(w)
			return
		}
		limit = min(n, maxCallPage)
	}
	var before int64
	if raw := q.Get("cursor"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			badFilter(w)
			return
		}
		before = n
	}
	// One extra row says whether another page exists without a second query.
	rows, err := s.dbs.ListAPICalls(r.Context(), f, before, limit+1)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := api.CallPage{Items: make([]api.CallRecord, 0, min(len(rows), limit))}
	for i, row := range rows {
		if i == limit {
			next := strconv.FormatInt(rows[limit-1].ID, 10)
			page.NextCursor = &next
			break
		}
		page.Items = append(page.Items, callRecord(row))
	}
	writeJSON(w, http.StatusOK, page)
}

var csvHeader = []string{
	"time", "call_id", "account_id", "feature", "provider", "endpoint", "model", "outcome", "reason",
	"cost_usd", "cost_estimated", "input_tokens", "output_tokens", "latency_ms",
}

// inert makes a cell that a spreadsheet would read as a formula harmless. A model
// name or account id comes from outside, so it is not ours to trust.
func inert(s string) string {
	if s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@' || s[0] == '\t' || s[0] == '\r') {
		return "'" + s
	}
	return s
}

// handleExportCalls streams the log as CSV or JSON, a page at a time, so memory
// stays flat however long the ledger is. The size is stated up front; a ledger
// longer than the limit is cut and says so.
func (s *Server) handleExportCalls(w http.ResponseWriter, r *http.Request) {
	f, ok := callFilter(r)
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}
	if !ok || (format != "csv" && format != "json") {
		badFilter(w)
		return
	}
	ctx := r.Context()
	total, err := s.dbs.CountAPICalls(ctx, f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	want := min(total, exportRowLimit)
	h := w.Header()
	h.Set("X-Ivy-Rows", strconv.Itoa(total))
	if total > want {
		h.Set("X-Ivy-Truncated", "true")
	}
	h.Set("Content-Disposition", `attachment; filename="ivy-calls.`+format+`"`)

	var emit func(store.CallRow) error
	var finish func() error
	switch format {
	case "csv":
		h.Set("Content-Type", "text/csv; charset=utf-8")
		cw := csv.NewWriter(w)
		_ = cw.Write(csvHeader)
		emit = func(c store.CallRow) error {
			return cw.Write([]string{
				c.At.UTC().Format(time.RFC3339), inert(c.CallID), inert(c.AccountID), inert(c.Feature), inert(c.Provider),
				inert(c.Endpoint), inert(c.Model), inert(c.Outcome), inert(c.Reason),
				strconv.FormatFloat(c.CostUSD, 'f', -1, 64), strconv.FormatBool(c.CostEstimated),
				strconv.Itoa(c.InputTokens), strconv.Itoa(c.OutputTokens), strconv.Itoa(c.LatencyMS),
			})
		}
		finish = func() error { cw.Flush(); return cw.Error() }
	default:
		h.Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("["))
		first := true
		emit = func(c store.CallRow) error {
			b, err := json.Marshal(callRecord(c))
			if err != nil {
				return err
			}
			if !first {
				b = append([]byte{','}, b...)
			}
			first = false
			_, err = w.Write(b)
			return err
		}
		finish = func() error { _, err := w.Write([]byte("]")); return err }
	}

	var before int64
	for sent := 0; sent < want; {
		rows, err := s.dbs.ListAPICalls(ctx, f, before, min(exportPage, want-sent))
		if err != nil || ctx.Err() != nil {
			abortExport(ctx, err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if err := emit(row); err != nil {
				abortExport(ctx, err)
			}
		}
		sent += len(rows)
		before = rows[len(rows)-1].ID
	}
	if err := finish(); err != nil {
		abortExport(ctx, err)
	}
}

// abortExport ends a download that cannot be finished by resetting the
// connection. A file that stops partway but looks complete would be worse than a
// failed download, because the operator would trust the totals in it.
func abortExport(ctx context.Context, err error) {
	if err != nil {
		slog.ErrorContext(ctx, "gateway: call export failed partway", "error", err)
	}
	panic(http.ErrAbortHandler)
}
