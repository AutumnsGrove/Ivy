package store

import (
	"context"
	"fmt"
	"time"
)

// ThreadMessage is the header projection the threading pass (chunk 2e) needs to
// rebuild one account's conversations. It carries no body, so threading a large
// mirror never loads a message.
type ThreadMessage struct {
	ID         string
	ContentKey string
	MessageID  string
	References string
	InReplyTo  string
	Subject    string
	Date       time.Time
}

// Thread is one stored conversation. MessageIDs is populated only when writing
// (ReplaceThreads); reads leave it empty and report MessageCount instead.
type Thread struct {
	ID            string
	AccountID     string
	RootMessageID string
	SubjectNorm   string
	LastDate      time.Time
	MessageCount  int
	MessageIDs    []string
}

// MessagesForThreading returns every visible message's threading headers for one
// account, oldest first. Disabled messages are excluded: they are hidden from
// every view and must not anchor a thread.
func (d *DBs) MessagesForThreading(ctx context.Context, accountID string) ([]ThreadMessage, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT id, content_key, COALESCE(message_id_hdr, ''), COALESCE(refs, ''),
		       COALESCE(in_reply_to, ''), COALESCE(subject, ''), COALESCE(date, '')
		FROM messages
		WHERE account_id = ? AND disabled_at IS NULL
		ORDER BY date, id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("messages for threading %s: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []ThreadMessage
	for rows.Next() {
		var (
			m    ThreadMessage
			date string
		)
		if err := rows.Scan(&m.ID, &m.ContentKey, &m.MessageID, &m.References, &m.InReplyTo, &m.Subject, &date); err != nil {
			return nil, fmt.Errorf("messages for threading %s: %w", accountID, err)
		}
		if date != "" {
			t, err := parseTime(date)
			if err != nil {
				return nil, fmt.Errorf("messages for threading %s: %w", accountID, err)
			}
			m.Date = t
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("messages for threading %s: %w", accountID, err)
	}
	return out, nil
}

// ReplaceThreads swaps in one account's conversations in a single transaction:
// the previous thread rows are dropped, every message's thread_id is cleared and
// the new assignment is written. Threads are derived from the mirror, so
// dropping a stale row loses nothing that a rebuild cannot recreate.
//
// It is one transaction so a reader never sees a half-rethreaded account, and
// one writer so it queues behind sync instead of racing it.
func (d *DBs) ReplaceThreads(ctx context.Context, accountID string, threads []Thread) error {
	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace threads for %s: %w", accountID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("replace threads for %s: %w", accountID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET thread_id = NULL WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("replace threads for %s: %w", accountID, err)
	}
	for _, th := range threads {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO threads (id, account_id, root_message_id, subject_norm, last_date, message_count)
			VALUES (?, ?, ?, ?, ?, ?)`,
			th.ID, accountID, nullableString(th.RootMessageID), nullableString(th.SubjectNorm),
			nullableTime(th.LastDate), len(th.MessageIDs)); err != nil {
			return fmt.Errorf("replace threads for %s: %w", accountID, err)
		}
		for _, id := range th.MessageIDs {
			if _, err := tx.ExecContext(ctx,
				`UPDATE messages SET thread_id = ? WHERE id = ? AND account_id = ?`, th.ID, id, accountID); err != nil {
				return fmt.Errorf("replace threads for %s: %w", accountID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace threads for %s: %w", accountID, err)
	}
	return nil
}

// ListThreads returns one account's conversations, newest last date first.
func (d *DBs) ListThreads(ctx context.Context, accountID string) ([]Thread, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT id, account_id, COALESCE(root_message_id, ''), COALESCE(subject_norm, ''),
		       COALESCE(last_date, ''), message_count
		FROM threads
		WHERE account_id = ?
		ORDER BY last_date DESC, id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list threads for %s: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Thread
	for rows.Next() {
		var (
			t        Thread
			lastDate string
		)
		if err := rows.Scan(&t.ID, &t.AccountID, &t.RootMessageID, &t.SubjectNorm, &lastDate, &t.MessageCount); err != nil {
			return nil, fmt.Errorf("list threads for %s: %w", accountID, err)
		}
		if lastDate != "" {
			parsed, err := parseTime(lastDate)
			if err != nil {
				return nil, fmt.Errorf("list threads for %s: %w", accountID, err)
			}
			t.LastDate = parsed
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list threads for %s: %w", accountID, err)
	}
	return out, nil
}
