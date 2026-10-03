package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// The operator's own account profile (display name, icon, photo) lives in
// state.db, not on the mirror's accounts row: the mirror is rebuilt from IMAP
// and never backed up, so anything the operator typed must not live there
// (CLAUDE.md non-negotiable 5). The account id is the config id both databases
// share.

// SetAccountProfile writes the display name and icon. The photo is left alone.
// The account must exist in the mirror, so a typo cannot create a ghost row.
func (d *DBs) SetAccountProfile(ctx context.Context, id, displayName, icon string) error {
	if err := d.requireAccount(ctx, id); err != nil {
		return err
	}
	if _, err := d.State.Write.ExecContext(ctx, `
		INSERT INTO account_profiles (account_id, display_name, icon) VALUES (?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET
			display_name = excluded.display_name, icon = excluded.icon`,
		id, displayName, icon); err != nil {
		return fmt.Errorf("set account profile %s: %w", id, err)
	}
	return nil
}

// SetAccountPhoto writes or clears the account's photo; a nil photo removes it.
// The name and icon are left alone.
func (d *DBs) SetAccountPhoto(ctx context.Context, id string, photo []byte) error {
	if err := d.requireAccount(ctx, id); err != nil {
		return err
	}
	if _, err := d.State.Write.ExecContext(ctx, `
		INSERT INTO account_profiles (account_id, photo_blob) VALUES (?, ?)
		ON CONFLICT(account_id) DO UPDATE SET photo_blob = excluded.photo_blob`,
		id, photo); err != nil {
		return fmt.Errorf("set account photo %s: %w", id, err)
	}
	return nil
}

// GetAccountPhoto returns the stored photo bytes, or ErrNotFound when there is
// no photo. It is the only read that touches the blob, so listing accounts
// never loads image data into memory.
func (d *DBs) GetAccountPhoto(ctx context.Context, id string) ([]byte, error) {
	var photo []byte
	err := d.State.Read.QueryRowContext(ctx,
		`SELECT photo_blob FROM account_profiles WHERE account_id = ?`, id).Scan(&photo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get account photo %s: %w", id, err)
	}
	if len(photo) == 0 {
		return nil, ErrNotFound
	}
	return photo, nil
}

func (d *DBs) requireAccount(ctx context.Context, id string) error {
	var one int
	err := d.Mirror.Read.QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("account %s: %w", id, err)
	}
	return nil
}

// overlayProfiles lays each account's profile over its mirror row, in place.
// Profiles are few (one per account), so it reads them all rather than
// building an IN list.
func (d *DBs) overlayProfiles(ctx context.Context, accounts []Account) error {
	if len(accounts) == 0 {
		return nil
	}
	rows, err := d.State.Read.QueryContext(ctx, `
		SELECT account_id, display_name, icon,
		       (photo_blob IS NOT NULL AND length(photo_blob) > 0)
		FROM account_profiles`)
	if err != nil {
		return fmt.Errorf("account profiles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byID := make(map[string]*Account, len(accounts))
	for i := range accounts {
		byID[accounts[i].ID] = &accounts[i]
	}
	for rows.Next() {
		var (
			id, name, icon string
			hasPhoto       bool
		)
		if err := rows.Scan(&id, &name, &icon, &hasPhoto); err != nil {
			return fmt.Errorf("account profiles: %w", err)
		}
		if a, ok := byID[id]; ok {
			a.DisplayName, a.Icon, a.HasPhoto = name, icon, hasPhoto
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("account profiles: %w", err)
	}
	return nil
}

// moveLegacyProfiles copies a profile that older builds kept on the mirror row
// into state.db, then empties the mirror columns. It runs on every Open and is
// idempotent: a profile already in state wins (INSERT OR IGNORE), so a copy
// interrupted between the two steps, or a stale mirror value, never overwrites
// what the operator chose.
func (d *DBs) moveLegacyProfiles(ctx context.Context) error {
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT id, display_name, icon, photo_blob FROM accounts
		WHERE display_name != '' OR icon != '' OR photo_blob IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("legacy profiles: %w", err)
	}
	type legacy struct {
		id, name, icon string
		photo          []byte
	}
	var found []legacy
	for rows.Next() {
		var l legacy
		if err := rows.Scan(&l.id, &l.name, &l.icon, &l.photo); err != nil {
			_ = rows.Close()
			return fmt.Errorf("legacy profiles: %w", err)
		}
		found = append(found, l)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("legacy profiles: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("legacy profiles: %w", err)
	}
	for _, l := range found {
		if _, err := d.State.Write.ExecContext(ctx, `
			INSERT OR IGNORE INTO account_profiles (account_id, display_name, icon, photo_blob)
			VALUES (?, ?, ?, ?)`, l.id, l.name, l.icon, l.photo); err != nil {
			return fmt.Errorf("legacy profile %s: %w", l.id, err)
		}
	}
	if len(found) == 0 {
		return nil
	}
	if _, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE accounts SET display_name = '', icon = '', photo_blob = NULL`); err != nil {
		return fmt.Errorf("clear legacy profiles: %w", err)
	}
	return nil
}
