package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The stats panel reads the ledger here. The gate is the only writer (ledger.go);
// these are read-only roll-ups and a paged log over the same rows, so a total can
// never disagree with the rows it summarises.

// callKey counts a call once however many rows it wrote: an embedding call writes
// a row per input, all sharing a call id. A row with no id (written before ids
// existed) counts as its own call.
const callKey = `COUNT(DISTINCT CASE WHEN call_id <> '' THEN call_id ELSE 'row-' || id END)`

// SpendGroup is the calls and dollars of one feature, account, model or refusal
// reason.
type SpendGroup struct {
	Key   string
	Calls int
	USD   float64
}

// SpendReport is every roll-up the stats panel shows for one window.
type SpendReport struct {
	// Total counts calls that reached a provider (including failed ones, which
	// cost nothing); a refused call is Blocked, not spend.
	Total     SpendGroup
	ByFeature []SpendGroup
	ByAccount []SpendGroup
	ByModel   []SpendGroup
	// Blocked is the calls the gates turned away, by reason.
	Blocked []SpendGroup
}

// SpendReport rolls the ledger up for calls at or after from; the zero time is all
// time. Groups come largest spend first, then by key, so a screen needs no sorting.
func (d *DBs) SpendReport(ctx context.Context, from time.Time) (SpendReport, error) {
	var r SpendReport
	window, args := windowClause(from)

	total, err := d.spendGroups(ctx, `'total'`, `outcome <> 'refused'`+window, args)
	if err != nil {
		return SpendReport{}, err
	}
	if len(total) == 1 {
		r.Total = total[0]
		r.Total.Key = ""
	}
	for _, g := range []struct {
		out *[]SpendGroup
		col string
	}{{&r.ByFeature, "feature"}, {&r.ByAccount, "account_id"}, {&r.ByModel, "model"}} {
		if *g.out, err = d.spendGroups(ctx, g.col, `outcome <> 'refused'`+window, args); err != nil {
			return SpendReport{}, err
		}
	}
	if r.Blocked, err = d.spendGroups(ctx, "reason", `outcome = 'refused'`+window, args); err != nil {
		return SpendReport{}, err
	}
	return r, nil
}

// spendGroups groups the matching rows by col. col and where are constants of this
// file; only the window's bound arrives as a parameter.
func (d *DBs) spendGroups(ctx context.Context, col, where string, args []any) ([]SpendGroup, error) {
	rows, err := d.State.Read.QueryContext(ctx, fmt.Sprintf(`
		SELECT %[1]s, %[2]s, COALESCE(SUM(cost_usd), 0) FROM api_calls
		WHERE %[3]s GROUP BY %[1]s ORDER BY SUM(cost_usd) DESC, %[1]s`, col, callKey, where), args...)
	if err != nil {
		return nil, fmt.Errorf("spend report: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SpendGroup
	for rows.Next() {
		var g SpendGroup
		if err := rows.Scan(&g.Key, &g.Calls, &g.USD); err != nil {
			return nil, fmt.Errorf("spend report: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("spend report: %w", err)
	}
	return out, nil
}

// CallFilter narrows the call log. A zero field matches everything.
type CallFilter struct {
	Outcome   string
	Feature   string
	AccountID string
	From      time.Time
}

// CallRow is a ledger row with its id, which is the paging cursor.
type CallRow struct {
	APICall
	ID int64
}

// MaxCallPage bounds a page of the log, whatever the caller asks.
const MaxCallPage = 500

func windowClause(from time.Time) (string, []any) {
	if from.IsZero() {
		return "", nil
	}
	return ` AND at >= ?`, []any{from.UTC().Format(time.RFC3339)}
}

func (f CallFilter) where() (string, []any) {
	clauses := []string{"1 = 1"}
	var args []any
	for _, c := range []struct{ col, val string }{
		{"outcome", f.Outcome}, {"feature", f.Feature}, {"account_id", f.AccountID},
	} {
		if c.val != "" {
			clauses = append(clauses, c.col+" = ?")
			args = append(args, c.val)
		}
	}
	window, wargs := windowClause(f.From)
	return strings.Join(clauses, " AND ") + window, append(args, wargs...)
}

// ListAPICalls returns up to limit rows, newest first. before is the id of the last
// row already seen (zero for the first page); paging by id means a row written
// meanwhile neither repeats nor shifts the next page.
func (d *DBs) ListAPICalls(ctx context.Context, f CallFilter, before int64, limit int) ([]CallRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	limit = min(limit, MaxCallPage)
	where, args := f.where()
	if before > 0 {
		where += " AND id < ?"
		args = append(args, before)
	}
	rows, err := d.State.Read.QueryContext(ctx, `
		SELECT id, at, provider, endpoint, model, feature, account_id, content_key,
		       input_tokens, output_tokens, cost_usd, cost_estimated, latency_ms, outcome, reason, call_id
		FROM api_calls WHERE `+where+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("list api calls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []CallRow
	for rows.Next() {
		var (
			c         CallRow
			at        string
			estimated int
		)
		if err := rows.Scan(&c.ID, &at, &c.Provider, &c.Endpoint, &c.Model, &c.Feature, &c.AccountID,
			&c.ContentKey, &c.InputTokens, &c.OutputTokens, &c.CostUSD, &estimated, &c.LatencyMS,
			&c.Outcome, &c.Reason, &c.CallID); err != nil {
			return nil, fmt.Errorf("list api calls: %w", err)
		}
		if c.At, err = parseTime(at); err != nil {
			return nil, fmt.Errorf("list api calls: %w", err)
		}
		c.CostEstimated = estimated != 0
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list api calls: %w", err)
	}
	return out, nil
}

// CountAPICalls is how many rows a filter matches, so an export can state its
// size (and whether it was cut short) before streaming.
func (d *DBs) CountAPICalls(ctx context.Context, f CallFilter) (int, error) {
	where, args := f.where()
	var n int
	if err := d.State.Read.QueryRowContext(ctx, `SELECT count(*) FROM api_calls WHERE `+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count api calls: %w", err)
	}
	return n, nil
}
