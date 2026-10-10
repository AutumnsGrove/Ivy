package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SetVettingOverride records that the operator lets helpers read a message the safety
// checks withheld. Setting it again is the same fact, so a double tap is harmless.
func (d *DBs) SetVettingOverride(ctx context.Context, accountID, contentKey string, at time.Time) error {
	if accountID == "" || contentKey == "" {
		return errors.New("set vetting override: an override needs an account and a content key")
	}
	_, err := d.State.Write.ExecContext(ctx, `
		INSERT INTO vetting_overrides (account_id, content_key, created_at) VALUES (?, ?, ?)
		ON CONFLICT(account_id, content_key) DO NOTHING`,
		accountID, contentKey, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set vetting override: %w", err)
	}
	// Steps that skipped this message for being withheld should look again now, not
	// when their pause expires. The skips live in the mirror, so this is a second
	// write; it is idempotent and an interrupted one only costs the wait.
	_, err = d.Mirror.Write.ExecContext(ctx,
		`DELETE FROM decision_misses WHERE account_id=? AND content_key=? AND reason=?`,
		accountID, contentKey, MissWithheld)
	if err != nil {
		return fmt.Errorf("set vetting override: %w", err)
	}
	return nil
}

// ClearVettingOverride puts a message back under the safety checks. Clearing one that
// was never set is not an error.
func (d *DBs) ClearVettingOverride(ctx context.Context, accountID, contentKey string) error {
	_, err := d.State.Write.ExecContext(ctx,
		`DELETE FROM vetting_overrides WHERE account_id=? AND content_key=?`, accountID, contentKey)
	if err != nil {
		return fmt.Errorf("clear vetting override: %w", err)
	}
	return nil
}

// VettingOverridden reports whether the operator has let helpers read this message.
func (d *DBs) VettingOverridden(ctx context.Context, accountID, contentKey string) (bool, error) {
	var one int
	err := d.State.Read.QueryRowContext(ctx,
		`SELECT 1 FROM vetting_overrides WHERE account_id=? AND content_key=?`, accountID, contentKey).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read vetting override: %w", err)
	}
	return true, nil
}
