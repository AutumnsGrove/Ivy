package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SetSetting writes one setting. An empty accountID is the global scope, which
// the settings table's composite key keeps distinct from every account's.
func (d *DBs) SetSetting(ctx context.Context, accountID, key, value string) error {
	_, err := d.State.Write.ExecContext(ctx, `
		INSERT INTO settings (account_id, key, value) VALUES (?, ?, ?)
		ON CONFLICT(account_id, key) DO UPDATE SET value=excluded.value`,
		accountID, key, value)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// GetSetting reads one setting and reports whether it was set, so a default is
// the caller's to choose.
func (d *DBs) GetSetting(ctx context.Context, accountID, key string) (string, bool, error) {
	var value string
	err := d.State.Read.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE account_id=? AND key=?`, accountID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get setting %s: %w", key, err)
	}
	return value, true, nil
}
