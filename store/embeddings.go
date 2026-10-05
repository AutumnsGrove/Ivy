package store

import (
	"context"
	"fmt"
)

// Embedding is one stored vector: the int8 payload plus the scale and norm
// needed to compute cosine (llm.Vector). Ref is a message content key for a
// body chunk or an attachment content hash for an attachment chunk.
type Embedding struct {
	AccountID string
	Ref       string
	Kind      string
	ChunkIx   int
	Model     string
	Dims      int
	Scale     float64
	Norm      float64
	Vector    []byte
}

// UpsertEmbeddings writes a batch of vectors in one transaction, replacing any
// row with the same identity. A re-run after a crash therefore cannot duplicate
// a chunk.
func (d *DBs) UpsertEmbeddings(ctx context.Context, items []Embedding) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("upsert embeddings: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO embeddings (account_id, ref, kind, chunk_ix, model, dims, scale, norm, vector)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, ref, kind, chunk_ix, model, dims) DO UPDATE SET
			scale=excluded.scale, norm=excluded.norm, vector=excluded.vector,
			created_at=datetime('now')`)
	if err != nil {
		return fmt.Errorf("upsert embeddings: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, e := range items {
		if _, err := stmt.ExecContext(ctx, e.AccountID, e.Ref, e.Kind, e.ChunkIx,
			e.Model, e.Dims, e.Scale, e.Norm, e.Vector); err != nil {
			return fmt.Errorf("upsert embeddings: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("upsert embeddings: %w", err)
	}
	return nil
}

// PendingBodyRef is one content key that still needs a body embedding.
type PendingBodyRef struct {
	AccountID  string
	ContentKey string
	Subject    string
	Body       string
}

// PendingBodyRefs returns content keys with derived text that have no body
// embedding for this model yet, newest first. It is keyed by content key, so a
// message in two folders is embedded once (N8).
func (d *DBs) PendingBodyRefs(ctx context.Context, accountID, model string, limit int) ([]PendingBodyRef, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT m.account_id, m.content_key, COALESCE(m.subject, ''), COALESCE(m.body_text, '')
		FROM messages m
		WHERE m.disabled_at IS NULL
		  AND COALESCE(m.body_text, '') <> ''
		  AND (m.account_id = ? OR ? = '')
		  AND NOT EXISTS (
			SELECT 1 FROM embeddings e
			WHERE e.account_id = m.account_id AND e.ref = m.content_key
			  AND e.kind = ? AND e.model = ?)
		GROUP BY m.account_id, m.content_key
		ORDER BY max(m.date) DESC
		LIMIT ?`, accountID, accountID, ExtractKindBody, model, limit)
	if err != nil {
		return nil, fmt.Errorf("pending body refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PendingBodyRef
	for rows.Next() {
		var p PendingBodyRef
		if err := rows.Scan(&p.AccountID, &p.ContentKey, &p.Subject, &p.Body); err != nil {
			return nil, fmt.Errorf("pending body refs: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pending body refs: %w", err)
	}
	return out, nil
}

// PendingAttachmentRef is one attachment content hash that still needs an
// embedding for an account.
type PendingAttachmentRef struct {
	AccountID string
	Hash      string
	Text      string
}

// PendingAttachmentRefs returns attachments with extracted text that have no
// embedding for this model yet, newest first. One row per (account, hash),
// because the same file shared by many messages is embedded once.
func (d *DBs) PendingAttachmentRefs(ctx context.Context, accountID, model string, limit int) ([]PendingAttachmentRef, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT DISTINCT m.account_id, a.content_hash, et.text, m.date
		FROM attachments a
		JOIN messages m ON m.id = a.message_id
		JOIN extracted_text et ON et.ref = a.content_hash AND et.kind = ?
		WHERE et.status = 'ok' AND et.text <> ''
		  AND m.disabled_at IS NULL
		  AND (m.account_id = ? OR ? = '')
		  AND NOT EXISTS (
			SELECT 1 FROM embeddings e
			WHERE e.account_id = m.account_id AND e.ref = a.content_hash
			  AND e.kind = ? AND e.model = ?)
		GROUP BY m.account_id, a.content_hash
		ORDER BY max(m.date) DESC
		LIMIT ?`, ExtractKindAttachment, accountID, accountID, ExtractKindAttachment, model, limit)
	if err != nil {
		return nil, fmt.Errorf("pending attachment refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PendingAttachmentRef
	for rows.Next() {
		var (
			p    PendingAttachmentRef
			date string
		)
		if err := rows.Scan(&p.AccountID, &p.Hash, &p.Text, &date); err != nil {
			return nil, fmt.Errorf("pending attachment refs: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pending attachment refs: %w", err)
	}
	return out, nil
}

// EachEmbedding streams every vector for the given accounts and model, so a
// brute-force scan never loads the whole table into memory (PERFORMANCE.md 1).
// The callback sees one row at a time and must not retain the Vector slice.
func (d *DBs) EachEmbedding(ctx context.Context, accountIDs []string, model string, fn func(Embedding) error) error {
	args := []any{model}
	filter := ""
	if len(accountIDs) > 0 {
		filter = " AND account_id IN (" + placeholders(len(accountIDs)) + ")"
		for _, id := range accountIDs {
			args = append(args, id)
		}
	}
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT account_id, ref, kind, chunk_ix, model, dims, scale, norm, vector
		FROM embeddings
		WHERE model = ?`+filter+`
		ORDER BY account_id, ref, chunk_ix`, args...)
	if err != nil {
		return fmt.Errorf("each embedding: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e Embedding
		if err := rows.Scan(&e.AccountID, &e.Ref, &e.Kind, &e.ChunkIx, &e.Model,
			&e.Dims, &e.Scale, &e.Norm, &e.Vector); err != nil {
			return fmt.Errorf("each embedding: %w", err)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("each embedding: %w", err)
	}
	return nil
}

