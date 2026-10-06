package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// Identity is one address an account may send as, with the display name and
// signature the compose builder uses. It is locally owned state (CLAUDE.md rule
// 5): it lives in the backed-up state database, so it survives a mirror rebuild.
// The account's own address is always an identity; the gateway merges a
// synthetic primary row when no stored row matches it.
type Identity struct {
	ID          string
	AccountID   string
	Address     string
	DisplayName string
	Signature   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// IdentityInput is a create or edit. ID is the injected row id used only when
// the address is new; Address is the key within the account, so editing the
// primary identity is the same call as adding an alias.
type IdentityInput struct {
	ID          string
	AccountID   string
	Address     string
	DisplayName string
	Signature   string
	Now         time.Time
}

// Identity bounds (STANDARDS.md section 4a). Over any is a defined refusal
// rather than a silently truncated row.
const (
	// MaxIdentitiesPerAccount bounds how many addresses one account may send as.
	MaxIdentitiesPerAccount = 50
	// MaxIdentityAddressBytes bounds an outgoing addr-spec.
	MaxIdentityAddressBytes = 320
	// MaxSignatureBytes bounds one signature; a signature is a few lines, not a
	// second message.
	MaxSignatureBytes = 8 << 10
)

// ErrIdentityAddress reports an addr-spec that is not a bare ASCII address. A
// CR, LF or NUL is rejected, never stripped (CHUNK4-BRIEF invariant 8), and a
// non-ASCII address is refused because Purelymail has no SMTPUTF8.
var ErrIdentityAddress = errors.New("identity address is not valid")

// ErrIdentitySignature reports a signature over MaxSignatureBytes.
var ErrIdentitySignature = errors.New("signature is too long")

// ErrIdentityLimit reports an account at its identity ceiling. Handlers map it
// to a 409; an edit of an existing identity is still allowed at the limit.
var ErrIdentityLimit = errors.New("too many identities")

// ListIdentities returns every stored identity for an account, ordered by
// address so a list is stable. It does not include the synthetic primary; the
// gateway merges that because only it knows the account's address.
func (d *DBs) ListIdentities(ctx context.Context, accountID string) ([]Identity, error) {
	rows, err := d.State.Read.QueryContext(ctx, identitySelect+` WHERE account_id=? ORDER BY address`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Identity
	for rows.Next() {
		in, err := scanIdentity(rows)
		if err != nil {
			return nil, fmt.Errorf("list identities: %w", err)
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	return out, nil
}

// GetIdentity returns one identity by address, matching case-insensitively
// because a mail domain is case-insensitive in practice, or ErrNotFound.
func (d *DBs) GetIdentity(ctx context.Context, accountID, address string) (Identity, error) {
	row := d.State.Read.QueryRowContext(ctx,
		identitySelect+` WHERE account_id=? AND address=? COLLATE NOCASE`, accountID, address)
	in, err := scanIdentity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNotFound
	}
	if err != nil {
		return Identity{}, fmt.Errorf("get identity: %w", err)
	}
	return in, nil
}

// GetIdentityByID returns one identity by its row id, or ErrNotFound.
func (d *DBs) GetIdentityByID(ctx context.Context, id string) (Identity, error) {
	row := d.State.Read.QueryRowContext(ctx, identitySelect+` WHERE id=?`, id)
	in, err := scanIdentity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNotFound
	}
	if err != nil {
		return Identity{}, fmt.Errorf("get identity %s: %w", id, err)
	}
	return in, nil
}

// UpsertIdentity creates or edits the identity at (account, address) in one
// transaction. An edit keeps the row's id and created_at; a new row uses in.ID.
// The lookup is case-insensitive, so re-adding an address in another case edits
// the same identity rather than minting a duplicate.
func (d *DBs) UpsertIdentity(ctx context.Context, in IdentityInput) (Identity, error) {
	if !validIdentityAddress(in.Address) {
		return Identity{}, ErrIdentityAddress
	}
	if len(in.Signature) > MaxSignatureBytes {
		return Identity{}, ErrIdentitySignature
	}

	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return Identity{}, fmt.Errorf("upsert identity: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		existingID string
		createdAt  string
	)
	err = tx.QueryRowContext(ctx,
		`SELECT id, created_at FROM identities WHERE account_id=? AND address=? COLLATE NOCASE`,
		in.AccountID, in.Address).Scan(&existingID, &createdAt)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx,
			`UPDATE identities SET address=?, display_name=?, signature=?, updated_at=? WHERE id=?`,
			in.Address, in.DisplayName, in.Signature, formatTime(in.Now), existingID); err != nil {
			return Identity{}, fmt.Errorf("update identity: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Identity{}, fmt.Errorf("upsert identity: %w", err)
		}
		return d.GetIdentityByID(ctx, existingID)
	case !errors.Is(err, sql.ErrNoRows):
		return Identity{}, fmt.Errorf("upsert identity: %w", err)
	}

	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM identities WHERE account_id=?`, in.AccountID).Scan(&count); err != nil {
		return Identity{}, fmt.Errorf("upsert identity: %w", err)
	}
	if count >= MaxIdentitiesPerAccount {
		return Identity{}, ErrIdentityLimit
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO identities (id, account_id, address, display_name, signature, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.AccountID, in.Address, in.DisplayName, in.Signature,
		formatTime(in.Now), formatTime(in.Now)); err != nil {
		return Identity{}, fmt.Errorf("insert identity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Identity{}, fmt.Errorf("upsert identity: %w", err)
	}
	return d.GetIdentityByID(ctx, in.ID)
}

// DeleteIdentity removes one stored identity by row id, or ErrNotFound. It never
// touches the account's own address: the gateway refuses that before calling it,
// because only the gateway knows which address is primary.
func (d *DBs) DeleteIdentity(ctx context.Context, id string) error {
	res, err := d.State.Write.ExecContext(ctx, `DELETE FROM identities WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete identity %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete identity %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const identitySelect = `
	SELECT id, account_id, address, display_name, signature, created_at, updated_at
	FROM identities`

func scanIdentity(s scanner) (Identity, error) {
	var (
		in                   Identity
		createdAt, updatedAt string
	)
	if err := s.Scan(&in.ID, &in.AccountID, &in.Address, &in.DisplayName, &in.Signature, &createdAt, &updatedAt); err != nil {
		return Identity{}, err
	}
	created, err := parseTime(createdAt)
	if err != nil {
		return Identity{}, fmt.Errorf("created_at: %w", err)
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return Identity{}, fmt.Errorf("updated_at: %w", err)
	}
	in.CreatedAt, in.UpdatedAt = created, updated
	return in, nil
}

// validIdentityAddress reports a bare ASCII addr-spec: the rule the outbound
// builder applies, tightened to what may be stored. It is intentionally
// conservative; compose.Build validates precisely at send time, so a row that
// slips past is still refused at the wire.
func validIdentityAddress(addr string) bool {
	if addr == "" || len(addr) > MaxIdentityAddressBytes {
		return false
	}
	if strings.ContainsAny(addr, "\r\n\x00 \t") {
		return false
	}
	for i := range len(addr) {
		if addr[i] >= 0x80 {
			return false
		}
	}
	parsed, err := mail.ParseAddress(addr)
	return err == nil && parsed.Address == addr && parsed.Name == ""
}
