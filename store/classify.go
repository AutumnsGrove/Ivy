package store

import (
	"context"
	"fmt"
	"time"
)

// classifiable is the shared filter for the Jev worker's queue: visible mail in the
// Inbox or Junk that arrived at or after the watermark and has not been considered.
// Archive, Sent, Drafts and Trash are never classified automatically (5b decision 1).
// Arrival is the server's internal date, falling back to the Date header.
const classifiable = `
	FROM messages m JOIN folders f ON f.id = m.folder_id
	WHERE m.account_id = ? AND f.role IN ('inbox', 'junk') AND m.disabled_at IS NULL
	  AND COALESCE(NULLIF(m.internaldate, ''), m.date) >= ?
	  AND NOT EXISTS (SELECT 1 FROM classified c
	                  WHERE c.account_id = m.account_id AND c.content_key = m.content_key)`

// UnclassifiedMessages returns up to limit messages for the Jev worker, newest first,
// one per content key (a message filed in two folders is asked about once, and its
// Inbox copy is preferred so the Inbox questions apply). since is the account's
// arrival watermark: nothing older is ever eligible.
func (d *DBs) UnclassifiedMessages(ctx context.Context, accountID string, since time.Time, limit int) ([]Candidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	// MIN picks the row SQLite reports the bare columns from, so id and role are the
	// Inbox copy's when there is one.
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT m.id, f.role, MIN(CASE f.role WHEN 'inbox' THEN 0 ELSE 1 END),
		       COALESCE(NULLIF(m.internaldate, ''), m.date) AS arrived `+classifiable+`
		GROUP BY m.content_key
		ORDER BY arrived DESC, m.content_key
		LIMIT ?`, accountID, formatTime(since), limit)
	if err != nil {
		return nil, fmt.Errorf("list unclassified messages: %w", err)
	}
	var found []Candidate
	for rows.Next() {
		var id, role, arrived string
		var pref int
		if err := rows.Scan(&id, &role, &pref, &arrived); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("list unclassified messages: %w", err)
		}
		found = append(found, Candidate{Message: Message{ID: id}, Role: role})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("list unclassified messages: %w", err)
	}
	_ = rows.Close()

	for i := range found {
		m, err := d.GetMessage(ctx, found[i].Message.ID)
		if err != nil {
			return nil, fmt.Errorf("list unclassified messages: %w", err)
		}
		found[i].Message = m
	}
	return found, nil
}

// Candidate is a message waiting for the Jev worker, with the role of the folder it
// sits in (inbox or junk), which decides which questions apply.
type Candidate struct {
	Message Message
	Role    string
}

// CountUnclassified is how many distinct messages are waiting for the account since
// the watermark: the queue depth, and the item count of a backfill estimate.
func (d *DBs) CountUnclassified(ctx context.Context, accountID string, since time.Time) (int, error) {
	var n int
	err := d.Mirror.Read.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT m.content_key) `+classifiable, accountID, formatTime(since)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unclassified messages: %w", err)
	}
	return n, nil
}

// MeanBodyBytes is the average size of the waiting messages' text, for pricing a
// backfill. Zero when nothing is waiting.
func (d *DBs) MeanBodyBytes(ctx context.Context, accountID string, since time.Time) (int, error) {
	var mean float64
	err := d.Mirror.Read.QueryRowContext(ctx,
		`SELECT COALESCE(AVG(LENGTH(m.body_text) + LENGTH(m.subject)), 0) `+classifiable,
		accountID, formatTime(since)).Scan(&mean)
	if err != nil {
		return 0, fmt.Errorf("mean body bytes: %w", err)
	}
	return int(mean), nil
}

// MarkClassified records that the worker has considered these content keys. It is
// idempotent.
func (d *DBs) MarkClassified(ctx context.Context, accountID string, contentKeys []string, at time.Time) error {
	if len(contentKeys) == 0 {
		return nil
	}
	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mark classified: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, k := range contentKeys {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO classified (account_id, content_key, at) VALUES (?, ?, ?)`,
			accountID, k, formatTime(at)); err != nil {
			return fmt.Errorf("mark classified: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mark classified: %w", err)
	}
	return nil
}
