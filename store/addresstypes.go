package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MailType is what one of the operator's own addresses receives. Normal is the absence
// of a setting, so it is never stored.
type MailType string

const (
	MailNormal MailType = "normal"
	// MailContactForm marks an address a website form writes to: replies go to the
	// visitor, not the form's sender.
	MailContactForm MailType = "contact_form"
	// MailSecurity marks an address that receives security and abuse reports. No model
	// reads mail sent to it until the operator turns that address on.
	MailSecurity MailType = "security"
)

// MaxAddressTypesPerAccount bounds how many addresses one account may mark.
const MaxAddressTypesPerAccount = 100

// ErrBadMailType reports a kind that is none of the three.
var ErrBadMailType = errors.New("unknown mail type")

// ErrAddressTypeLimit reports an account at its ceiling; editing a marked address is
// still allowed there.
var ErrAddressTypeLimit = errors.New("too many marked addresses")

// AddressType is one marked address.
type AddressType struct {
	AccountID string
	Address   string
	Kind      MailType
	// LLMOn lets models read this address's mail. It only matters for MailSecurity,
	// which ships off; the other kinds are never blocked.
	LLMOn     bool
	UpdatedAt time.Time
}

// AddressTypeInput sets one address's type. Kind normal forgets it.
type AddressTypeInput struct {
	AccountID string
	Address   string
	Kind      MailType
	LLMOn     bool
	Now       time.Time
}

// SetAddressType records, changes or (for normal) removes an address's type in one
// transaction.
func (d *DBs) SetAddressType(ctx context.Context, in AddressTypeInput) error {
	if !validIdentityAddress(in.Address) {
		return ErrIdentityAddress
	}
	switch in.Kind {
	case MailNormal, MailContactForm, MailSecurity:
	default:
		return ErrBadMailType
	}
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set address type: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if in.Kind == MailNormal {
		if _, err := tx.ExecContext(ctx, `DELETE FROM address_types WHERE account_id=? AND address=?`, in.AccountID, in.Address); err != nil {
			return fmt.Errorf("set address type: %w", err)
		}
		return tx.Commit()
	}
	var exists int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM address_types WHERE account_id=? AND address=?`, in.AccountID, in.Address).Scan(&exists); err != nil {
		return fmt.Errorf("set address type: %w", err)
	}
	if exists == 0 {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM address_types WHERE account_id=?`, in.AccountID).Scan(&n); err != nil {
			return fmt.Errorf("set address type: %w", err)
		}
		if n >= MaxAddressTypesPerAccount {
			return ErrAddressTypeLimit
		}
	}
	llm := 0
	if in.LLMOn {
		llm = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO address_types (account_id, address, kind, llm_on, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(account_id, address) DO UPDATE SET kind=excluded.kind, llm_on=excluded.llm_on, updated_at=excluded.updated_at`,
		in.AccountID, in.Address, string(in.Kind), llm, formatTime(in.Now)); err != nil {
		return fmt.Errorf("set address type: %w", err)
	}
	return tx.Commit()
}

// ListAddressTypes returns an account's marked addresses, ordered by address.
func (d *DBs) ListAddressTypes(ctx context.Context, accountID string) ([]AddressType, error) {
	rows, err := d.State.Read.QueryContext(ctx,
		`SELECT address, kind, llm_on, updated_at FROM address_types WHERE account_id=? ORDER BY address`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list address types: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AddressType
	for rows.Next() {
		var (
			t       AddressType
			kind    string
			llm     int
			updated string
		)
		if err := rows.Scan(&t.Address, &kind, &llm, &updated); err != nil {
			return nil, fmt.Errorf("list address types: %w", err)
		}
		at, err := parseTime(updated)
		if err != nil {
			return nil, fmt.Errorf("list address types: %w", err)
		}
		t.AccountID, t.Kind, t.LLMOn, t.UpdatedAt = accountID, MailType(kind), llm != 0, at
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list address types: %w", err)
	}
	return out, nil
}

// ModelsBlockedFor reports whether no model may read this message: it names, as a
// recipient, Cc or Delivered-To, a security or abuse address the operator has not
// turned on. It is the one question every model path asks, so it fails closed: if the
// guarded set is non-empty and the message's recipients cannot be read (the mirror has
// no such message, or the read fails), the answer is yes.
func (d *DBs) ModelsBlockedFor(ctx context.Context, accountID, contentKey string) (bool, error) {
	guarded, err := d.guardedAddresses(ctx, accountID)
	if err != nil {
		return true, err
	}
	if len(guarded) == 0 {
		return false, nil
	}
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT COALESCE(to_json, ''), COALESCE(cc_json, ''), COALESCE(delivered_to_json, '')
		FROM messages WHERE account_id = ? AND content_key = ?`, accountID, contentKey)
	if err != nil {
		return true, fmt.Errorf("recipients of %s: %w", contentKey, err)
	}
	defer func() { _ = rows.Close() }()
	seen := false
	for rows.Next() {
		seen = true
		var to, cc, delivered string
		if err := rows.Scan(&to, &cc, &delivered); err != nil {
			return true, fmt.Errorf("recipients of %s: %w", contentKey, err)
		}
		for _, raw := range []string{to, cc, delivered} {
			var list []Address
			if err := decodeJSON(raw, &list); err != nil {
				return true, fmt.Errorf("recipients of %s: %w", contentKey, err)
			}
			for _, a := range list {
				if guarded[strings.ToLower(strings.TrimSpace(a.Address))] {
					return true, nil
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return true, fmt.Errorf("recipients of %s: %w", contentKey, err)
	}
	return !seen, nil
}

// guardedAddresses is the lowercased set of security addresses still off.
func (d *DBs) guardedAddresses(ctx context.Context, accountID string) (map[string]bool, error) {
	rows, err := d.State.Read.QueryContext(ctx,
		`SELECT address FROM address_types WHERE account_id=? AND kind=? AND llm_on=0`, accountID, string(MailSecurity))
	if err != nil {
		return nil, fmt.Errorf("guarded addresses: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, fmt.Errorf("guarded addresses: %w", err)
		}
		out[strings.ToLower(a)] = true
	}
	return out, rows.Err()
}
