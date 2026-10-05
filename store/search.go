package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Extracted-text kinds. A body is keyed by the message content key; an
// attachment by its decoded-bytes content hash, so the same file shared by two
// messages is read once (ARCHITECTURE.md 3).
const (
	ExtractKindBody       = "body"
	ExtractKindAttachment = "attachment"
)

// ExtractedText is the plain text pulled out of one body or attachment. Status
// distinguishes "there was no text" (empty) from "I could not read this"
// (failed); see extract.Result (STANDARDS.md 4a.3).
type ExtractedText struct {
	Ref            string
	Kind           string
	Tier           int
	Status         string
	Text           string
	DerivedVersion int
}

// UpsertExtractedText records the text of one body or attachment. It is
// idempotent: re-running an extraction that succeeds again just rewrites it.
func (d *DBs) UpsertExtractedText(ctx context.Context, e ExtractedText) error {
	if e.Ref == "" {
		return errors.New("upsert extracted text: empty ref")
	}
	if e.Kind != ExtractKindBody && e.Kind != ExtractKindAttachment {
		return fmt.Errorf("upsert extracted text: unknown kind %q", e.Kind)
	}
	_, err := d.Mirror.Write.ExecContext(ctx, `
		INSERT INTO extracted_text (ref, kind, tier, status, text, derived_version, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(ref, kind) DO UPDATE SET
			tier=excluded.tier, status=excluded.status, text=excluded.text,
			derived_version=excluded.derived_version, updated_at=excluded.updated_at`,
		e.Ref, e.Kind, e.Tier, e.Status, e.Text, e.DerivedVersion)
	if err != nil {
		return fmt.Errorf("upsert extracted text %s/%s: %w", e.Kind, e.Ref, err)
	}
	return nil
}

// GetExtractedText returns one body's or attachment's extracted text, or
// ErrNotFound.
func (d *DBs) GetExtractedText(ctx context.Context, ref, kind string) (ExtractedText, error) {
	var e ExtractedText
	err := d.Mirror.Read.QueryRowContext(ctx, `
		SELECT ref, kind, tier, status, text, derived_version FROM extracted_text
		WHERE ref = ? AND kind = ?`, ref, kind).
		Scan(&e.Ref, &e.Kind, &e.Tier, &e.Status, &e.Text, &e.DerivedVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ExtractedText{}, ErrNotFound
	}
	if err != nil {
		return ExtractedText{}, fmt.Errorf("get extracted text %s/%s: %w", kind, ref, err)
	}
	return e, nil
}

// SearchDoc is the searchable text of one content key. One document covers a
// message wherever it lives: a copy in two folders and an identical
// Message-ID are one document (N8), so the index never returns the same mail
// twice.
type SearchDoc struct {
	AccountID  string
	ContentKey string
	Subject    string
	Body       string
	Attachment string
}

// IndexSearchDoc writes a content key's document: it allocates the
// search_docs row once and replaces the FTS row, so a re-index cannot leave a
// stale copy behind. An empty document is still stored; it simply never
// matches.
func (d *DBs) IndexSearchDoc(ctx context.Context, doc SearchDoc) error {
	if doc.AccountID == "" || doc.ContentKey == "" {
		return errors.New("index search doc: account and content key are required")
	}
	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("index search doc %s: %w", doc.ContentKey, err)
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM search_docs WHERE account_id = ? AND content_key = ?`,
		doc.AccountID, doc.ContentKey).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, ierr := tx.ExecContext(ctx,
			`INSERT INTO search_docs (account_id, content_key) VALUES (?, ?)`,
			doc.AccountID, doc.ContentKey)
		if ierr != nil {
			return fmt.Errorf("index search doc %s: %w", doc.ContentKey, ierr)
		}
		if id, ierr = res.LastInsertId(); ierr != nil {
			return fmt.Errorf("index search doc %s: %w", doc.ContentKey, ierr)
		}
	case err != nil:
		return fmt.Errorf("index search doc %s: %w", doc.ContentKey, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE rowid = ?`, id); err != nil {
		return fmt.Errorf("index search doc %s: clear: %w", doc.ContentKey, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO search_index (rowid, subject, body, attachment) VALUES (?, ?, ?, ?)`,
		id, doc.Subject, doc.Body, doc.Attachment); err != nil {
		return fmt.Errorf("index search doc %s: write: %w", doc.ContentKey, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index search doc %s: %w", doc.ContentKey, err)
	}
	return nil
}

// SearchHit is one full-text result. Rank is the raw FTS5 bm25 score, where a
// lower (more negative) number is a better match.
type SearchHit struct {
	AccountID  string
	ContentKey string
	Rank       float64
}

// SearchFTS runs one full-text query over visible mail. accountIDs restricts
// the search (empty means every account), and a content key is only returned
// while it has at least one live row, so a hidden message never appears even
// though its index entry is kept for a later restore (invariant 10).
func (d *DBs) SearchFTS(ctx context.Context, query string, accountIDs []string, limit int) ([]SearchHit, error) {
	match := FTSQuery(query)
	if match == "" || limit <= 0 {
		return nil, nil
	}
	args := []any{match}
	accountFilter := ""
	if len(accountIDs) > 0 {
		accountFilter = " AND d.account_id IN (" + placeholders(len(accountIDs)) + ")"
		for _, id := range accountIDs {
			args = append(args, id)
		}
	}
	args = append(args, limit)
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT d.account_id, d.content_key, bm25(search_index) AS rank
		FROM search_index
		JOIN search_docs d ON d.id = search_index.rowid
		WHERE search_index MATCH ?`+accountFilter+`
		  AND EXISTS (
			SELECT 1 FROM messages m
			WHERE m.account_id = d.account_id AND m.content_key = d.content_key
			  AND m.disabled_at IS NULL)
		ORDER BY rank
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", query, err)
	}
	defer func() { _ = rows.Close() }()

	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.AccountID, &h.ContentKey, &h.Rank); err != nil {
			return nil, fmt.Errorf("search %q: %w", query, err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search %q: %w", query, err)
	}
	return hits, nil
}

// placeholders returns "?, ?, ?" for n values.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// FTSQuery turns operator free text into a safe FTS5 MATCH expression. Every
// term is double-quoted, so an FTS5 operator or a stray quote in the input is a
// literal word and cannot change the query; a trailing '*' on the last unquoted
// term makes it a prefix match ("renew*"). A double-quoted span is one phrase.
// It returns "" when the input holds no term, which the caller reads as "no
// query" rather than "match nothing".
func FTSQuery(q string) string {
	var terms []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			terms = append(terms, cur.String())
			cur.Reset()
		}
	}
	for _, r := range q {
		switch {
		case r == '"':
			inQuote = !inQuote
		case unicode.IsSpace(r) && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	if len(terms) == 0 {
		return ""
	}
	var b strings.Builder
	for i, t := range terms {
		prefix := ""
		if i == len(terms)-1 && !inQuote && strings.HasSuffix(t, "*") && len(t) > 1 {
			t = strings.TrimSuffix(t, "*")
			prefix = "*"
		}
		if t == "" || t == "*" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(t, `"`, `""`))
		b.WriteByte('"')
		b.WriteString(prefix)
	}
	return b.String()
}
