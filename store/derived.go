package store

import (
	"context"
	"fmt"
)

// Derived is everything computed from a message's raw bytes: the parse-derived
// text columns, the sanitised HTML and the attachment rows. Version names the
// pipeline that produced it.
type Derived struct {
	Version        int
	BodyText       string
	Snippet        string
	HasAttachments bool
	ParseErrors    []string
	BodyStatus     string
	BodyHTML       string
	Attachments    []Attachment
}

// SetMessageDerived replaces a message's derived data and stamps its version,
// all in one transaction. A crash or an error part-way leaves the previous data
// and the previous version, so a row never claims a version whose data is only
// partly there (N14 in papercuts.md): a half-written message stays behind and
// the re-derive pass picks it up again. It is the only writer of these columns
// after the first insert.
func (d *DBs) SetMessageDerived(ctx context.Context, id string, v Derived) error {
	parseErrors, err := marshalStrings(v.ParseErrors)
	if err != nil {
		return err
	}
	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set derived data of message %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE messages SET body_text = ?, snippet = ?, has_attachments = ?, parse_errors = ?,
			body_status = ?, body_html_sanitized = ?, derived_version = ?
		WHERE id = ?`,
		v.BodyText, v.Snippet, v.HasAttachments, parseErrors, v.BodyStatus, v.BodyHTML, v.Version, id)
	if err != nil {
		return fmt.Errorf("set derived data of message %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set derived data of message %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := replaceAttachmentsTx(ctx, tx, id, v.Attachments); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set derived data of message %s: %w", id, err)
	}
	return nil
}

// MarkDeriveFailed records that a re-derive pass gave up on a message at
// version because its raw bytes are gone. It is not a derivation: the row stays
// behind (its derived_version is untouched), but MessageIDsBehind skips it until
// the version moves past this one.
func (d *DBs) MarkDeriveFailed(ctx context.Context, id string, version int) error {
	res, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE messages SET derive_failed_version = ? WHERE id = ?`, version, id)
	if err != nil {
		return fmt.Errorf("mark message %s derive-failed: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark message %s derive-failed: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MessageIDsBehind returns up to limit ids of visible, downloaded messages in an
// account whose derived data is older than version, newest first, leaving out
// those a pass already gave up on at this version. An empty account id means
// every account. Only ids are returned, so a pass holds one message in memory
// at a time.
func (d *DBs) MessageIDsBehind(ctx context.Context, accountID string, version, limit int) ([]string, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT id FROM messages
		WHERE derived_version < ? AND derive_failed_version < ? AND disabled_at IS NULL
		  AND (account_id = ? OR ? = '')
		  AND (length(raw_blob) > 0 OR raw_path IS NOT NULL)
		ORDER BY date DESC, id DESC
		LIMIT ?`, version, version, accountID, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("messages behind version %d: %w", version, err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("messages behind version %d: %w", version, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("messages behind version %d: %w", version, err)
	}
	return ids, nil
}
