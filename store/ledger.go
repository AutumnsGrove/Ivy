package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// APICall is one ledger row: a remote model call and, for a batch, one row per
// item in the batch with the call's exact cost allocated by token share
// (ARCHITECTURE.md 3). CostEstimated is set when the provider reported no cost
// and it had to be computed from token counts.
type APICall struct {
	At            time.Time
	Provider      string
	Endpoint      string
	Model         string
	Feature       string
	AccountID     string
	ContentKey    string
	InputTokens   int
	OutputTokens  int
	CostUSD       float64
	CostEstimated bool
	LatencyMS     int
	Outcome       string
	CallID        string
}

// Spend is a month-to-date roll-up for one account and endpoint.
type Spend struct {
	USD   float64
	Calls int
}

// Period names the monthly bucket a call belongs to, in UTC.
func Period(t time.Time) string {
	return t.UTC().Format("2006-01")
}

// RecordAPICall appends one ledger row. It is a convenience over RecordAPICalls
// for the single-call case.
func (d *DBs) RecordAPICall(ctx context.Context, c APICall) error {
	return d.RecordAPICalls(ctx, []APICall{c})
}

// RecordAPICalls appends ledger rows and adds their cost to the monthly
// counters in one transaction, so the stats panel and the cap can never
// disagree about what was spent. A batch (one embedding call's per-message
// rows) lands as a unit. It is the only writer of either table.
func (d *DBs) RecordAPICalls(ctx context.Context, calls []APICall) error {
	if len(calls) == 0 {
		return nil
	}
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record api calls: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	insertCall, err := tx.PrepareContext(ctx, `
		INSERT INTO api_calls (
			at, provider, endpoint, model, feature, account_id, content_key,
			input_tokens, output_tokens, cost_usd, cost_estimated, latency_ms, outcome, call_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("record api calls: %w", err)
	}
	defer func() { _ = insertCall.Close() }()
	upsertCap, err := tx.PrepareContext(ctx, `
		INSERT INTO api_caps (account_id, period, endpoint, spent_usd, calls)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(account_id, period, endpoint) DO UPDATE SET
			spent_usd = spent_usd + excluded.spent_usd,
			calls     = calls + 1`)
	if err != nil {
		return fmt.Errorf("record api calls: %w", err)
	}
	defer func() { _ = upsertCap.Close() }()

	for _, c := range calls {
		estimated := 0
		if c.CostEstimated {
			estimated = 1
		}
		if _, err := insertCall.ExecContext(ctx,
			c.At.UTC().Format(time.RFC3339), c.Provider, c.Endpoint, c.Model, c.Feature,
			c.AccountID, c.ContentKey, c.InputTokens, c.OutputTokens, c.CostUSD, estimated,
			c.LatencyMS, c.Outcome, c.CallID); err != nil {
			return fmt.Errorf("record api calls: %w", err)
		}
		if _, err := upsertCap.ExecContext(ctx,
			c.AccountID, Period(c.At), c.Endpoint, c.CostUSD); err != nil {
			return fmt.Errorf("record api calls: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("record api calls: %w", err)
	}
	return nil
}

// MonthlySpend returns what one account has spent on one endpoint in one
// period. A period with no calls is the zero value, so "spent nothing" and "no
// such row" are the same answer.
func (d *DBs) MonthlySpend(ctx context.Context, accountID, period, endpoint string) (Spend, error) {
	var s Spend
	err := d.State.Read.QueryRowContext(ctx, `
		SELECT spent_usd, calls FROM api_caps
		WHERE account_id = ? AND period = ? AND endpoint = ?`,
		accountID, period, endpoint).Scan(&s.USD, &s.Calls)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Spend{}, fmt.Errorf("monthly spend: %w", err)
	}
	return s, nil
}

// RecentAPICalls returns the newest ledger rows, optionally for one account
// (an empty account means all). The caller pages the list, so limit is capped
// by the caller and never unbounded here.
func (d *DBs) RecentAPICalls(ctx context.Context, accountID string, limit int) ([]APICall, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := d.State.Read.QueryContext(ctx, `
		SELECT at, provider, endpoint, model, feature, account_id, content_key,
		       input_tokens, output_tokens, cost_usd, cost_estimated, latency_ms, outcome, call_id
		FROM api_calls
		WHERE account_id = ? OR ? = ''
		ORDER BY id DESC
		LIMIT ?`, accountID, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("recent api calls: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []APICall
	for rows.Next() {
		var (
			c         APICall
			at        string
			estimated int
		)
		if err := rows.Scan(&at, &c.Provider, &c.Endpoint, &c.Model, &c.Feature,
			&c.AccountID, &c.ContentKey, &c.InputTokens, &c.OutputTokens,
			&c.CostUSD, &estimated, &c.LatencyMS, &c.Outcome, &c.CallID); err != nil {
			return nil, fmt.Errorf("recent api calls: %w", err)
		}
		t, err := parseTime(at)
		if err != nil {
			return nil, fmt.Errorf("recent api calls: %w", err)
		}
		c.At = t
		c.CostEstimated = estimated != 0
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent api calls: %w", err)
	}
	return out, nil
}
