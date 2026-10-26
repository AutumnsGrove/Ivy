package store

import (
	"context"
	"fmt"
	"time"
)

// WaitingCandidate is a Sent message that nothing has answered since.
type WaitingCandidate struct {
	MessageID  string
	ContentKey string
	// ThreadID is empty for a message that belongs to no thread.
	ThreadID string
	Date     time.Time
}

// waitingOnSelect finds Sent messages with no newer live message in their thread. "Live"
// is the same set the summary reads (liveThread): not hidden, not in Trash or Junk, so
// a reply the owner deleted does not end the wait. A copy of the message itself, filed
// in a second folder, shares its content key and is not an answer. Dates compare as
// the RFC 3339 strings stored, as the inbox keyset does; an undated message cannot be
// waited on because there is no way to say how long.
const waitingOnSelect = `
	SELECT m.id, m.content_key, COALESCE(m.thread_id, ''), m.date
	FROM messages m
	CROSS JOIN folders f ON f.id = m.folder_id
	WHERE f.role = 'sent' AND m.disabled_at IS NULL
	  AND (? = '' OR m.account_id = ?)
	  AND m.date IS NOT NULL AND m.date <= ?
	  AND NOT EXISTS (
		SELECT 1 FROM messages n
		JOIN folders nf ON nf.id = n.folder_id
		WHERE m.thread_id IS NOT NULL AND m.thread_id <> ''
		  AND n.account_id = m.account_id AND n.thread_id = m.thread_id
		  AND n.content_key <> m.content_key AND n.disabled_at IS NULL
		  AND nf.role NOT IN ('trash', 'junk')
		  AND (COALESCE(n.date, '') > m.date OR (COALESCE(n.date, '') = m.date AND n.id > m.id)))
	GROUP BY m.account_id, m.content_key
	ORDER BY m.date, m.id
	LIMIT ?`

// WaitingOnCandidates lists threads whose newest message the owner still has is their
// own, in Sent, dated at or before sentBefore: the ones that may be waiting on someone.
// Longest wait first. An empty accountID means every account. It decides nothing about
// whether the message expected an answer; that is a question for the caller.
func (d *DBs) WaitingOnCandidates(ctx context.Context, accountID string, sentBefore time.Time, limit int) ([]WaitingCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := d.Mirror.Read.QueryContext(ctx, waitingOnSelect, accountID, accountID, formatTime(sentBefore), limit)
	if err != nil {
		return nil, fmt.Errorf("list waiting on: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []WaitingCandidate
	for rows.Next() {
		var c WaitingCandidate
		var date string
		if err := rows.Scan(&c.MessageID, &c.ContentKey, &c.ThreadID, &date); err != nil {
			return nil, fmt.Errorf("list waiting on: %w", err)
		}
		if c.Date, err = parseTime(date); err != nil {
			return nil, fmt.Errorf("list waiting on: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list waiting on: %w", err)
	}
	return out, nil
}
