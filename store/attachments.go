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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace attachments for %s: %w", messageID, err)
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
