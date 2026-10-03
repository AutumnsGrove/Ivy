package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Account is one configured mailbox in the mirror. Host, port and username are
// non-secret connection details; the password lives only in the environment.
type Account struct {
	ID            string
	Address       string
	DisplayName   string
	Icon          string
	Color         string
	SortOrder     int
	LLMEnabled    bool
	VisionEnabled bool
	// HasPhoto reports whether the account has a stored photo; the bytes are
	// read only by GetAccountPhoto, so a list never loads image data.
	HasPhoto  bool
	IMAPHost  string
	IMAPPort  int
	SMTPHost  string
	SMTPPort  int
	Username  string
	CreatedAt time.Time
}

// UpsertAccount writes an account, preserving the original created_at on
// update so a config reload never resets it.
func (d *DBs) UpsertAccount(ctx context.Context, a Account) error {
	_, err := d.Mirror.Write.ExecContext(ctx, `
		INSERT INTO accounts (
			id, address, display_name, icon, color, sort_order, llm_enabled,
			vision_enabled, imap_host, imap_port, smtp_host,
			smtp_port, username, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			address=excluded.address, display_name=excluded.display_name,
			icon=excluded.icon, color=excluded.color, sort_order=excluded.sort_order,
			llm_enabled=excluded.llm_enabled, vision_enabled=excluded.vision_enabled,
			imap_host=excluded.imap_host,
			imap_port=excluded.imap_port, smtp_host=excluded.smtp_host,
			smtp_port=excluded.smtp_port, username=excluded.username`,
		a.ID, a.Address, a.DisplayName, a.Icon, a.Color, a.SortOrder, a.LLMEnabled,
		a.VisionEnabled, a.IMAPHost, a.IMAPPort, a.SMTPHost, a.SMTPPort,
		a.Username, formatTime(a.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert account %s: %w", a.ID, err)
	}
	return nil
}

// SetAccountProfile writes the user-owned display name and icon. It touches
// only those columns, so a rename can never disturb the connection fields the
// sync owns (ARCHITECTURE.md section 3).
func (d *DBs) SetAccountProfile(ctx context.Context, id, displayName, icon string) error {
	res, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE accounts SET display_name = ?, icon = ? WHERE id = ?`, displayName, icon, id)
	if err != nil {
		return fmt.Errorf("set account profile %s: %w", id, err)
	}
	return accountAffected(res, id)
}

// SetAccountPhoto writes or clears the account's photo; a nil photo removes it.
func (d *DBs) SetAccountPhoto(ctx context.Context, id string, photo []byte) error {
	res, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE accounts SET photo_blob = ? WHERE id = ?`, photo, id)
	if err != nil {
		return fmt.Errorf("set account photo %s: %w", id, err)
	}
	return accountAffected(res, id)
}

// GetAccountPhoto returns the stored photo bytes, or ErrNotFound when there is
// no photo. It is the only read that touches the blob, so listing accounts
// never loads image data into memory.
func (d *DBs) GetAccountPhoto(ctx context.Context, id string) ([]byte, error) {
	var photo []byte
	err := d.Mirror.Read.QueryRowContext(ctx,
		`SELECT photo_blob FROM accounts WHERE id = ?`, id).Scan(&photo)
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

func accountAffected(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("account %s rows affected: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetAccount returns one account or ErrNotFound.
func (d *DBs) GetAccount(ctx context.Context, id string) (Account, error) {
	row := d.Mirror.Read.QueryRowContext(ctx, accountSelect+` WHERE id = ?`, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("get account %s: %w", id, err)
	}
	return a, nil
}

// ListAccounts returns every account in display order.
func (d *DBs) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, accountSelect+` ORDER BY sort_order, address`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	return out, nil
}

const accountSelect = `
	SELECT id, address, display_name, icon, color, sort_order, llm_enabled,
	       vision_enabled, (photo_blob IS NOT NULL AND length(photo_blob) > 0),
	       imap_host, imap_port, smtp_host,
	       smtp_port, username, created_at
	FROM accounts`

type scanner interface {
	Scan(dest ...any) error
}

func scanAccount(s scanner) (Account, error) {
	var (
		a         Account
		createdAt string
	)
	if err := s.Scan(
		&a.ID, &a.Address, &a.DisplayName, &a.Icon, &a.Color, &a.SortOrder,
		&a.LLMEnabled, &a.VisionEnabled, &a.HasPhoto, &a.IMAPHost, &a.IMAPPort,
		&a.SMTPHost, &a.SMTPPort, &a.Username, &createdAt,
	); err != nil {
		return Account{}, err
	}
	t, err := parseTime(createdAt)
	if err != nil {
		return Account{}, fmt.Errorf("created_at: %w", err)
	}
	a.CreatedAt = t
	return a, nil
}

// timeLayout is fixed-width so stored timestamps sort lexicographically in the
// same order as chronologically; the mirror orders inbox pages on this column.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(timeLayout, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}
