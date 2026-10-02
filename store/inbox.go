package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const (
	defaultInboxLimit = 50
	maxInboxLimit     = 200
)

// MessageSummary is the list-view projection of a message: enough for the
// inbox row without loading a body.
type MessageSummary struct {
	ID        string
	AccountID string
	From      Address
	Subject   string
	Snippet   string
	Date      time.Time
	Unread    bool
	Needs     bool
}

// InboxQuery selects a page of inbox summaries. An empty AccountID means the
// combined view across every account.
type InboxQuery struct {
	AccountID string
	Cursor    string
	Limit     int
}

// InboxPage is one page plus the counts for the whole view (not just the page).
type InboxPage struct {
	Items       []MessageSummary
	NextCursor  string
	UnreadCount int
	NeedCount   int
}

// ListInbox returns a page of inbox summaries, newest first, with keyset
// pagination on (date, id). Counts cover the whole view, not the page.
func (d *DBs) ListInbox(ctx context.Context, q InboxQuery) (InboxPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultInboxLimit
	}
	if limit > maxInboxLimit {
		limit = maxInboxLimit
	}

	page := InboxPage{}
	if err := d.scanInboxCounts(ctx, q.AccountID, &page); err != nil {
		return InboxPage{}, err
	}

	cursorDate, cursorID, err := decodeCursor(q.Cursor)
	if err != nil {
		return InboxPage{}, err
	}

	rows, err := d.Mirror.Read.QueryContext(ctx, inboxSelect,
		q.AccountID, q.AccountID, cursorDate, cursorDate, cursorID, limit)
	if err != nil {
		return InboxPage{}, fmt.Errorf("list inbox: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		summary, err := scanInboxSummary(rows)
		if err != nil {
			return InboxPage{}, fmt.Errorf("list inbox: %w", err)
		}
		page.Items = append(page.Items, summary)
	}
	if err := rows.Err(); err != nil {
		return InboxPage{}, fmt.Errorf("list inbox: %w", err)
	}
	if len(page.Items) == limit {
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeCursor(formatTime(last.Date), last.ID)
	}
	return page, nil
}

func (d *DBs) scanInboxCounts(ctx context.Context, accountID string, page *InboxPage) error {
	var seen, need int64
	err := d.Mirror.Read.QueryRowContext(ctx, inboxCountsSelect, accountID, accountID).
		Scan(&seen, &need)
	if err != nil {
		return fmt.Errorf("inbox counts: %w", err)
	}
	page.UnreadCount = int(seen)
	page.NeedCount = int(need)
	return nil
}

// The needs_me left join cannot multiply rows: its (account_id, content_key)
// primary key is unique, so it only ever adds a flag. The join uses the
// folder_id index but SQLite still sorts the page in a temp b-tree; that is a
// benchmark item for the live endpoint in 2f, not a correctness problem.
const inboxSelect = `
	SELECT m.id, m.account_id, COALESCE(m.from_json, ''), COALESCE(m.subject, ''),
	       COALESCE(m.snippet, ''), COALESCE(m.date, ''), m.seen,
	       CASE WHEN n.account_id IS NULL THEN 0 ELSE 1 END
	FROM messages m
	JOIN folders f ON f.id = m.folder_id
	LEFT JOIN needs_me n ON n.account_id = m.account_id
		AND n.content_key = m.content_key AND n.verdict = 'needs'
	WHERE f.role = 'inbox'
	  AND m.disabled_at IS NULL
	  AND (? = '' OR m.account_id = ?)
	  AND (? = '' OR (m.date, m.id) < (?, ?))
	ORDER BY m.date DESC, m.id DESC
	LIMIT ?`

const inboxCountsSelect = `
	SELECT
		COALESCE(sum(CASE WHEN m.seen = 0 THEN 1 ELSE 0 END), 0),
		COALESCE(sum(CASE WHEN n.account_id IS NOT NULL THEN 1 ELSE 0 END), 0)
	FROM messages m
	JOIN folders f ON f.id = m.folder_id
	LEFT JOIN needs_me n ON n.account_id = m.account_id
		AND n.content_key = m.content_key AND n.verdict = 'needs'
	WHERE f.role = 'inbox'
	  AND m.disabled_at IS NULL
	  AND (? = '' OR m.account_id = ?)`

func scanInboxSummary(s scanner) (MessageSummary, error) {
	var (
		summary  MessageSummary
		fromJSON string
		date     string
		unread   bool
		needs    bool
	)
	err := s.Scan(&summary.ID, &summary.AccountID, &fromJSON, &summary.Subject,
		&summary.Snippet, &date, &unread, &needs)
	if err != nil {
		return MessageSummary{}, err
	}
	summary.Unread = !unread
	summary.Needs = needs
	if err := decodeJSON(fromJSON, &summary.From); err != nil {
		return MessageSummary{}, fmt.Errorf("from: %w", err)
	}
	if date != "" {
		t, err := parseTime(date)
		if err != nil {
			return MessageSummary{}, fmt.Errorf("date: %w", err)
		}
		summary.Date = t
	}
	return summary, nil
}

type inboxCursor struct {
	Date string `json:"d"`
	ID   string `json:"i"`
}

func encodeCursor(date, id string) string {
	b, err := json.Marshal(inboxCursor{Date: date, ID: id})
	if err != nil {
		// Marshalling two strings cannot fail.
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(cursor string) (string, string, error) {
	if cursor == "" {
		return "", "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", fmt.Errorf("decode cursor: %w", err)
	}
	var c inboxCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return "", "", fmt.Errorf("decode cursor: %w", err)
	}
	return c.Date, c.ID, nil
}
