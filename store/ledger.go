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
	// Reason is why the gate refused the call; empty for any call that reached
	// the provider.
	Reason string
	CallID string
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
			input_tokens, output_tokens, cost_usd, cost_estimated, latency_ms, outcome, reason, call_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
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
			c.LatencyMS, c.Outcome, c.Reason, c.CallID); err != nil {
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

// AccountSpend is what one account has spent in a period across every endpoint.
// The cap is one budget per account, so Jev, chat and vision cannot each be given
// an allowance of their own.
func (d *DBs) AccountSpend(ctx context.Context, accountID, period string) (Spend, error) {
	var s Spend
	err := d.State.Read.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(spent_usd), 0), COALESCE(SUM(calls), 0) FROM api_caps
		WHERE account_id = ? AND period = ?`, accountID, period).Scan(&s.USD, &s.Calls)
	if err != nil {
		return Spend{}, fmt.Errorf("account spend: %w", err)
	}
	return s, nil
}

// GlobalSpend is what every account together has spent in a period, for the one
// global cap.
func (d *DBs) GlobalSpend(ctx context.Context, period string) (Spend, error) {
	var s Spend
	err := d.State.Read.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(spent_usd), 0), COALESCE(SUM(calls), 0) FROM api_caps
		WHERE period = ?`, period).Scan(&s.USD, &s.Calls)
	if err != nil {
		return Spend{}, fmt.Errorf("global spend: %w", err)
	}
	return s, nil
}

// ProbeLedger reports whether a ledger row and its counter could be written
// right now. It runs the real inserts and rolls them back, so it exercises the
// same constraints and triggers a real write would without leaving a row behind.
// The gate's breaker uses it to learn that a broken ledger has healed.
func (d *DBs) ProbeLedger(ctx context.Context) error {
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("probe ledger: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO api_calls (at, outcome) VALUES (?, 'probe')`,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("probe ledger: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO api_caps (account_id, period, endpoint, spent_usd, calls)
		VALUES ('', '', 'probe', 0, 1)
		ON CONFLICT(account_id, period, endpoint) DO UPDATE SET calls = calls + 1`); err != nil {
		return fmt.Errorf("probe ledger: %w", err)
	}
	return nil
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
		       input_tokens, output_tokens, cost_usd, cost_estimated, latency_ms, outcome, reason, call_id
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
			&c.CostUSD, &estimated, &c.LatencyMS, &c.Outcome, &c.Reason, &c.CallID); err != nil {
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

// OutcomeRejected is the ledger outcome of a call the provider refused because of
// the document it was given (the gate decides which statuses mean that). It lives
// here because the count below depends on its spelling.
const OutcomeRejected = "rejected"

// CountRejectedCalls returns how many separate calls the provider has refused for
// one document. It counts call ids, not rows: a call for a document of several
// chunks writes a row per chunk, all sharing the call's id.
func (d *DBs) CountRejectedCalls(ctx context.Context, accountID, contentKey string) (int, error) {
	var n int
	err := d.State.Read.QueryRowContext(ctx, `
		SELECT count(DISTINCT call_id) FROM api_calls
		WHERE account_id = ? AND content_key = ? AND outcome = ? AND call_id <> ''`,
		accountID, contentKey, OutcomeRejected).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count rejected calls: %w", err)
	}
	return n, nil
}
