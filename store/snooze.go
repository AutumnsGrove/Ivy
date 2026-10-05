package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MaxSnoozes bounds how many messages one account may hold snoozed, so the
// inbox's exclusion list stays a small JSON array (STANDARDS.md 4a).
const MaxSnoozes = 2000

// ErrSnoozeLimit reports a snooze created past MaxSnoozes.
var ErrSnoozeLimit = errors.New("too many snoozes")

// Snooze is one locally hidden message: it stays on the server and in the
// mirror, and the inbox view hides it until Until (round 10).
type Snooze struct {
	AccountID  string
	ContentKey string
	Until      time.Time
	CreatedAt  time.Time
}

// SnoozeMessage sets (or extends) a message's hide-until. It is local state, so
// it never touches IMAP.
func (d *DBs) SnoozeMessage(ctx context.Context, accountID, contentKey string, until, now time.Time) error {
	if until.Before(now) {
		return d.ClearSnooze(ctx, accountID, contentKey)
	}
	var count int
	if err := d.State.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM snoozes WHERE account_id = ?`, accountID).Scan(&count); err != nil {
		return fmt.Errorf("snooze message: count: %w", err)
	}
	if count >= MaxSnoozes {
		// Extending an existing snooze is still allowed at the limit.
		var exists int
		_ = d.State.Read.QueryRowContext(ctx,
			`SELECT count(*) FROM snoozes WHERE account_id = ? AND content_key = ?`, accountID, contentKey).Scan(&exists)
		if exists == 0 {
			return ErrSnoozeLimit
		}
	}
	_, err := d.State.Write.ExecContext(ctx,
		`INSERT INTO snoozes (account_id, content_key, until, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (account_id, content_key) DO UPDATE SET until = excluded.until`,
		accountID, contentKey, formatTime(until), formatTime(now))
	if err != nil {
		return fmt.Errorf("snooze message: %w", err)
	}
	return nil
}

// ClearSnooze wakes a message now.
func (d *DBs) ClearSnooze(ctx context.Context, accountID, contentKey string) error {
	_, err := d.State.Write.ExecContext(ctx,
		`DELETE FROM snoozes WHERE account_id = ? AND content_key = ?`, accountID, contentKey)
	if err != nil {
		return fmt.Errorf("clear snooze: %w", err)
	}
	return nil
}

// ActiveSnoozeKeys returns the content keys still hidden at now, optionally for
// one account. The gateway passes them to the inbox query so a snoozed message
// is excluded without a cross-database join.
func (d *DBs) ActiveSnoozeKeys(ctx context.Context, accountID string, now time.Time) ([]string, error) {
	rows, err := d.State.Read.QueryContext(ctx,
		`SELECT content_key FROM snoozes
		 WHERE until > ? AND (? = '' OR account_id = ?)
		 ORDER BY until LIMIT ?`,
		formatTime(now), accountID, accountID, MaxSnoozes)
	if err != nil {
		return nil, fmt.Errorf("active snooze keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("active snooze keys: %w", err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("active snooze keys: %w", err)
	}
	return out, nil
}

// ListSnoozed returns one account's snoozes still hidden at now, soonest to
// wake first.
func (d *DBs) ListSnoozed(ctx context.Context, accountID string, now time.Time) ([]Snooze, error) {
	rows, err := d.State.Read.QueryContext(ctx,
		`SELECT account_id, content_key, until, created_at FROM snoozes
		 WHERE until > ? AND (? = '' OR account_id = ?)
		 ORDER BY until`,
		formatTime(now), accountID, accountID)
	if err != nil {
		return nil, fmt.Errorf("list snoozed: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Snooze
	for rows.Next() {
		var (
			s              Snooze
			until, created string
		)
		if err := rows.Scan(&s.AccountID, &s.ContentKey, &until, &created); err != nil {
			return nil, fmt.Errorf("list snoozed: %w", err)
		}
		if s.Until, err = parseTime(until); err != nil {
			return nil, fmt.Errorf("list snoozed: %w", err)
		}
		if s.CreatedAt, err = parseTime(created); err != nil {
			return nil, fmt.Errorf("list snoozed: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list snoozed: %w", err)
	}
	return out, nil
}

// SnoozeFor reports the wake time of one message, if it is snoozed and still
// hidden at now.
func (d *DBs) SnoozeFor(ctx context.Context, accountID, contentKey string, now time.Time) (time.Time, bool, error) {
	var until string
	err := d.State.Read.QueryRowContext(ctx,
		`SELECT until FROM snoozes WHERE account_id = ? AND content_key = ?`, accountID, contentKey).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("snooze for: %w", err)
	}
	t, err := parseTime(until)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("snooze for: %w", err)
	}
	if !t.After(now) {
		return time.Time{}, false, nil
	}
	return t, true, nil
}
