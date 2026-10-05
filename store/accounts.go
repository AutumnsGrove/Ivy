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
// update so a config reload never resets it. DisplayName, Icon and HasPhoto are
// the operator's profile, which lives in state.db (profiles.go) and is not
// written here.
func (d *DBs) UpsertAccount(ctx context.Context, a Account) error {
	_, err := d.Mirror.Write.ExecContext(ctx, `
		INSERT INTO accounts (
			id, address, color, sort_order, llm_enabled,
			vision_enabled, imap_host, imap_port, smtp_host,
			smtp_port, username, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			address=excluded.address, color=excluded.color, sort_order=excluded.sort_order,
			llm_enabled=excluded.llm_enabled, vision_enabled=excluded.vision_enabled,
			imap_host=excluded.imap_host,
			imap_port=excluded.imap_port, smtp_host=excluded.smtp_host,
			smtp_port=excluded.smtp_port, username=excluded.username`,
		a.ID, a.Address, a.Color, a.SortOrder, a.LLMEnabled,
		a.VisionEnabled, a.IMAPHost, a.IMAPPort, a.SMTPHost, a.SMTPPort,
		a.Username, formatTime(a.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert account %s: %w", a.ID, err)
	}
	return nil
}

// GetAccount returns one account or ErrNotFound, with the operator's profile
// from state.db laid over the mirror row.
func (d *DBs) GetAccount(ctx context.Context, id string) (Account, error) {
	row := d.Mirror.Read.QueryRowContext(ctx, accountSelect+` WHERE id = ?`, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("get account %s: %w", id, err)
	}
	accounts := []Account{a}
	if err := d.overlayProfiles(ctx, accounts); err != nil {
		return Account{}, err
	}
	return accounts[0], nil
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
	if err := d.overlayProfiles(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// The profile columns (display_name, icon, photo_blob) are deliberately not
// selected: since round 32 they belong to state.db.
const accountSelect = `
	SELECT id, address, color, sort_order, llm_enabled, vision_enabled,
	       imap_host, imap_port, smtp_host, smtp_port, username, created_at
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
		&a.ID, &a.Address, &a.Color, &a.SortOrder,
		&a.LLMEnabled, &a.VisionEnabled, &a.IMAPHost, &a.IMAPPort,
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

// parseOptionalTime reads a column that is NULL (read as "") for a message with
// no Date header; that is the zero time, not a parse error.
func parseOptionalTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return parseTime(s)
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(timeLayout, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}
