package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// Attachment is one file or inline part of a message. StoragePath is the part
// path mime.CopyPart serves from the message's raw bytes; ContentHash is the
// durable identity across messages and mirror rebuilds, so extraction and
// embeddings are keyed on the content, never on the row.
type Attachment struct {
	ID          string
	MessageID   string
	Filename    string
	MIMEType    string
	Size        int64
	ContentHash string
	CID         string
	StoragePath string
}

// ReplaceMessageAttachments swaps in one message's attachment rows in a single
// transaction, so a re-sync replaces rather than duplicates them. A nil list
// clears the message's rows. Like the body and thread columns, the table has
// exactly one writer; the read path never derives a row.
func (d *DBs) ReplaceMessageAttachments(ctx context.Context, messageID string, atts []Attachment) error {
	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace attachments for %s: %w", messageID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := replaceAttachmentsTx(ctx, tx, messageID, atts); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace attachments for %s: %w", messageID, err)
	}
	return nil
}

// replaceAttachmentsTx is the one place attachment rows are written, inside the
// caller's transaction, so SetMessageDerived can make them part of a larger
// all-or-nothing write.
func replaceAttachmentsTx(ctx context.Context, tx *sql.Tx, messageID string, atts []Attachment) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM attachments WHERE message_id = ?`, messageID); err != nil {
		return fmt.Errorf("replace attachments for %s: %w", messageID, err)
	}
	for _, a := range atts {
		id := a.ID
		if id == "" {
			id = attachmentID(messageID, a.StoragePath)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO attachments (id, message_id, filename, mime, size, content_hash, cid, storage_path)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, messageID, a.Filename, a.MIMEType, a.Size,
			nullableString(a.ContentHash), nullableString(a.CID), nullableString(a.StoragePath)); err != nil {
			return fmt.Errorf("replace attachments for %s: %w", messageID, err)
		}
	}
	return nil
}

// ListAttachments returns one message's attachments in document order. It is a
// table read: nothing here touches the raw message.
func (d *DBs) ListAttachments(ctx context.Context, messageID string) ([]Attachment, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, attachmentSelect+` WHERE message_id = ? ORDER BY rowid`, messageID)
	if err != nil {
		return nil, fmt.Errorf("list attachments for %s: %w", messageID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, fmt.Errorf("list attachments for %s: %w", messageID, err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list attachments for %s: %w", messageID, err)
	}
	return out, nil
}

// GetAttachmentByPath returns the attachment at a part path, or ErrNotFound.
func (d *DBs) GetAttachmentByPath(ctx context.Context, messageID, path string) (Attachment, error) {
	row := d.Mirror.Read.QueryRowContext(ctx,
		attachmentSelect+` WHERE message_id = ? AND storage_path = ?`, messageID, path)
	return oneAttachment(row, messageID)
}

// GetAttachmentByCID returns the inline attachment with a content id, or
// ErrNotFound. A message has at most one part per content id.
func (d *DBs) GetAttachmentByCID(ctx context.Context, messageID, cid string) (Attachment, error) {
	row := d.Mirror.Read.QueryRowContext(ctx,
		attachmentSelect+` WHERE message_id = ? AND cid = ? LIMIT 1`, messageID, cid)
	return oneAttachment(row, messageID)
}

func oneAttachment(row *sql.Row, messageID string) (Attachment, error) {
	a, err := scanAttachment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("get attachment for %s: %w", messageID, err)
	}
	return a, nil
}

const attachmentSelect = `
	SELECT id, message_id, COALESCE(filename, ''), COALESCE(mime, ''), size,
	       COALESCE(content_hash, ''), COALESCE(cid, ''), COALESCE(storage_path, '')
	FROM attachments`

func scanAttachment(s scanner) (Attachment, error) {
	var a Attachment
	err := s.Scan(&a.ID, &a.MessageID, &a.Filename, &a.MIMEType, &a.Size,
		&a.ContentHash, &a.CID, &a.StoragePath)
	return a, err
}

// attachmentID keys a row by its message and part path. It is stable across a
// re-sync, and distinct for the same path in two different messages.
func attachmentID(messageID, path string) string {
	sum := sha256.Sum256([]byte(messageID + "\x00" + path))
	return hex.EncodeToString(sum[:16])
}

// MaxAttachmentRefs bounds how many messages one attachment hash resolves to, so
// a file attached to a great many messages (a logo in every signature) cannot
// turn one extraction or one search hit into an unbounded reindex or result.
const MaxAttachmentRefs = 1000

// ContentRef names one message by the durable identity the derived tables use.
type ContentRef struct {
	AccountID  string
	ContentKey string
}

// ContentRefsForAttachment returns the visible messages that carry an
// attachment, newest first, one per content key. Extracted text and embeddings
// are keyed by the attachment's content hash, shared by every message with that
// file, so anything found by hash has to fan back out to all of them. An empty
// accountID means every account.
func (d *DBs) ContentRefsForAttachment(ctx context.Context, accountID, hash string) ([]ContentRef, error) {
	if hash == "" {
		return nil, nil
	}
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT m.account_id, m.content_key
		FROM attachments a
		JOIN messages m ON m.id = a.message_id
		WHERE a.content_hash = ? AND m.disabled_at IS NULL
		  AND (? = '' OR m.account_id = ?)
		GROUP BY m.account_id, m.content_key
		ORDER BY max(m.date) DESC
		LIMIT ?`, hash, accountID, accountID, MaxAttachmentRefs)
	if err != nil {
		return nil, fmt.Errorf("content refs for attachment: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ContentRef
	for rows.Next() {
		var r ContentRef
		if err := rows.Scan(&r.AccountID, &r.ContentKey); err != nil {
			return nil, fmt.Errorf("content refs for attachment: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("content refs for attachment: %w", err)
	}
	return out, nil
}

// MailAttachment is one attachment already in the mirror, offered for "From
// your mail". Path is the part path the reader's attachment endpoints use.
type MailAttachment struct {
	MessageID string
	Path      string
	Name      string
	MIMEType  string
	Size      int64
	Inline    bool
}

// ListRecentMailAttachments returns an account's visible attachments, newest
// message first, optionally filtered by a name substring. limit is a hard
// bound; disabled mail is never offered.
func (d *DBs) ListRecentMailAttachments(ctx context.Context, accountID, query string, limit int) ([]MailAttachment, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT a.message_id, COALESCE(a.storage_path, ''), COALESCE(a.filename, ''),
		       COALESCE(a.mime, ''), a.size, COALESCE(a.cid, '')
		FROM attachments a
		JOIN messages m ON m.id = a.message_id
		WHERE m.account_id = ? AND m.disabled_at IS NULL AND a.storage_path IS NOT NULL
		  AND (? = '' OR a.filename LIKE ?)
		ORDER BY COALESCE(m.date, '') DESC, a.rowid
		LIMIT ?`, accountID, query, "%"+query+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("recent mail attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []MailAttachment
	for rows.Next() {
		var a MailAttachment
		var cid string
		if err := rows.Scan(&a.MessageID, &a.Path, &a.Name, &a.MIMEType, &a.Size, &cid); err != nil {
			return nil, fmt.Errorf("recent mail attachments: %w", err)
		}
		a.Inline = cid != ""
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent mail attachments: %w", err)
	}
	return out, nil
}
