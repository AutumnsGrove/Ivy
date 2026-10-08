package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// AccountConfig is the connection description of an account connected from the
// app. It is the non-secret half: the password is a file under data/secrets
// (internal/secrets) and is never stored here, because state.db is backed up.
type AccountConfig struct {
	ID            string
	Address       string
	Username      string
	IMAPHost      string
	IMAPPort      int
	SMTPHost      string
	SMTPPort      int
	LLMEnabled    bool
	EmbedProvider string
	CreatedAt     time.Time
}

// SetAccountSmart turns an app-connected account's smart features on or off.
// Turning it on names the hosted provider if none is set, as connecting with
// smart on does; turning it off keeps the provider so it can be turned back on.
// An account with no stored config (declared in ivy.yaml) is ErrNotFound: the
// file is the operator's, and the app does not rewrite it.
func (d *DBs) SetAccountSmart(ctx context.Context, id string, on bool) error {
	res, err := d.State.Write.ExecContext(ctx, `
		UPDATE account_configs
		SET llm_enabled = ?,
		    embed_provider = CASE WHEN ? AND embed_provider = '' THEN 'openrouter' ELSE embed_provider END
		WHERE id = ?`, on, on, id)
	if err != nil {
		return fmt.Errorf("set smart for %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("set smart for %s: %w", id, err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveAccountConfig inserts or updates an account's connection details,
// preserving the original created_at on update.
func (d *DBs) SaveAccountConfig(ctx context.Context, a AccountConfig) error {
	switch {
	case a.ID == "", a.Address == "", a.Username == "":
		return errors.New("save account config: id, address and username are required")
	case a.IMAPHost == "", a.SMTPHost == "":
		return errors.New("save account config: imap and smtp hosts are required")
	case a.IMAPPort < 1 || a.IMAPPort > 65535, a.SMTPPort < 1 || a.SMTPPort > 65535:
		return errors.New("save account config: ports must be 1-65535")
	}
	if _, err := d.State.Write.ExecContext(ctx, `
		INSERT INTO account_configs (
			id, address, username, imap_host, imap_port, smtp_host, smtp_port,
			llm_enabled, embed_provider, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			address=excluded.address, username=excluded.username,
			imap_host=excluded.imap_host, imap_port=excluded.imap_port,
			smtp_host=excluded.smtp_host, smtp_port=excluded.smtp_port,
			llm_enabled=excluded.llm_enabled, embed_provider=excluded.embed_provider`,
		a.ID, a.Address, a.Username, a.IMAPHost, a.IMAPPort, a.SMTPHost, a.SMTPPort,
		a.LLMEnabled, a.EmbedProvider, formatTime(a.CreatedAt)); err != nil {
		return fmt.Errorf("save account config %s: %w", a.ID, err)
	}
	return nil
}

// AccountConfigs returns every account connected from the app, oldest first.
func (d *DBs) AccountConfigs(ctx context.Context) ([]AccountConfig, error) {
	rows, err := d.State.Read.QueryContext(ctx, `
		SELECT id, address, username, imap_host, imap_port, smtp_host, smtp_port,
		       llm_enabled, embed_provider, created_at
		FROM account_configs ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list account configs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AccountConfig
	for rows.Next() {
		var a AccountConfig
		var created string
		if err := rows.Scan(&a.ID, &a.Address, &a.Username, &a.IMAPHost, &a.IMAPPort,
			&a.SMTPHost, &a.SMTPPort, &a.LLMEnabled, &a.EmbedProvider, &created); err != nil {
			return nil, fmt.Errorf("scan account config: %w", err)
		}
		if a.CreatedAt, err = parseTime(created); err != nil {
			return nil, fmt.Errorf("account config %s created_at: %w", a.ID, err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list account configs: %w", err)
	}
	return out, nil
}
