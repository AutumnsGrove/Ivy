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
	// MaxInboxLimit is the largest page any list view serves, exported so a
	// local view (snoozed, tag filter) pages the same way.
	MaxInboxLimit = maxInboxLimit
)

// MessageSummary is the list-view projection of a message: enough for the
// inbox row without loading a body.
type MessageSummary struct {
	ID         string
	AccountID  string
	ContentKey string
	From       Address
	Subject    string
	Snippet    string
	Date       time.Time
	Unread     bool
	Flagged    bool
	Needs      bool
}

// InboxQuery selects a page of inbox summaries. An empty AccountID means the
// combined view across every account. Role picks the folder view (inbox,
// archive, trash or junk); empty means the inbox.
type InboxQuery struct {
	AccountID string
	Role      string
	Cursor    string
	Limit     int
	// Hide removes locally hidden mail from the view: snoozed messages and mail
	// in Reading. Those memberships live in state.db, so the caller loads them
	// and passes them here rather than joining the databases. A content key is
	// the hash of the Message-ID, so the same post delivered to two accounts
	// shares one; hiding is by (account, key) so one account's copy never hides
	// another's.
	Hide []ContentRef
}

// listableRole reports whether a role has a list view. The role is bound as a
// parameter, but an unknown one would still read as an empty folder.
func listableRole(role string) bool {
	switch role {
	case RoleInbox, RoleArchive, RoleTrash, RoleJunk:
		return true
	}
	return false
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

	role := q.Role
	if role == "" {
		role = RoleInbox
	}
	if !listableRole(role) {
		return InboxPage{}, fmt.Errorf("%w: %q", ErrBadRole, role)
	}

	page := InboxPage{}
	if err := d.scanInboxCounts(ctx, role, q.AccountID, q.Hide, &page); err != nil {
		return InboxPage{}, err
	}

	cursorDate, cursorID, err := decodeCursor(q.Cursor)
	if err != nil {
		return InboxPage{}, fmt.Errorf("%w: %w", ErrBadCursor, err)
	}

	rows, err := d.Mirror.Read.QueryContext(ctx, inboxSelect,
		role, q.AccountID, q.AccountID, jsonRefs(q.Hide), cursorID, cursorDate, cursorID, limit)
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
	page.NextCursor = nextCursor(page.Items, limit)
	return page, nil
}

func (d *DBs) scanInboxCounts(ctx context.Context, role, accountID string, hide []ContentRef, page *InboxPage) error {
	var seen, need int64
	err := d.Mirror.Read.QueryRowContext(ctx, inboxCountsSelect, role, accountID, accountID, jsonRefs(hide)).
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
	       COALESCE(m.snippet, ''), COALESCE(m.date, ''), m.seen, m.flagged,
	       CASE WHEN n.account_id IS NULL THEN 0 ELSE 1 END, m.content_key
	FROM messages m
	JOIN folders f ON f.id = m.folder_id
	LEFT JOIN needs_me n ON n.account_id = m.account_id
		AND n.content_key = m.content_key AND n.verdict = 'needs'
	WHERE f.role = ?
	  AND m.disabled_at IS NULL
	  AND (? = '' OR m.account_id = ?)
	  AND (m.account_id || char(31) || m.content_key) NOT IN (SELECT value FROM json_each(?))
	  AND (? = '' OR (COALESCE(m.date, ''), m.id) < (?, ?))
	ORDER BY COALESCE(m.date, '') DESC, m.id DESC
	LIMIT ?`

const inboxCountsSelect = `
	SELECT
		COALESCE(sum(CASE WHEN m.seen = 0 THEN 1 ELSE 0 END), 0),
		COALESCE(sum(CASE WHEN n.account_id IS NOT NULL THEN 1 ELSE 0 END), 0)
	FROM messages m
	JOIN folders f ON f.id = m.folder_id
	LEFT JOIN needs_me n ON n.account_id = m.account_id
		AND n.content_key = m.content_key AND n.verdict = 'needs'
	WHERE f.role = ?
	  AND m.disabled_at IS NULL
	  AND (? = '' OR m.account_id = ?)
	  AND (m.account_id || char(31) || m.content_key) NOT IN (SELECT value FROM json_each(?))`

func scanInboxSummary(s scanner) (MessageSummary, error) {
	var (
		summary  MessageSummary
		fromJSON string
		date     string
		unread   bool
		flagged  bool
		needs    bool
	)
	err := s.Scan(&summary.ID, &summary.AccountID, &fromJSON, &summary.Subject,
		&summary.Snippet, &date, &unread, &flagged, &needs, &summary.ContentKey)
	if err != nil {
		return MessageSummary{}, err
	}
	summary.Unread = !unread
	summary.Flagged = flagged
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

// jsonRefs renders (account, content key) pairs as a JSON array of
// "account US key" strings (US is char(31), which neither part contains) for a
// json_each membership filter. A nil list is the empty array, which matches
// nothing.
func jsonRefs(refs []ContentRef) string {
	if len(refs) == 0 {
		return "[]"
	}
	flat := make([]string, len(refs))
	for i, r := range refs {
		flat[i] = r.AccountID + "\x1f" + r.ContentKey
	}
	b, err := json.Marshal(flat)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// MessagesByContentRefs returns one summary per (account, content key), newest
// first, for local views whose membership lives in state.db (snoozed, Reading,
// a tag), with keyset paging on (date, id) like the inbox. A ref that is hidden
// or unknown is simply absent. The page carries no counts.
func (d *DBs) MessagesByContentRefs(ctx context.Context, accountID string, refs []ContentRef, cursor string, limit int) (InboxPage, error) {
	// Validate the cursor even for an empty view: a bad one is a bad request
	// whatever the view holds.
	cursorDate, cursorID, err := decodeCursor(cursor)
	if err != nil {
		return InboxPage{}, fmt.Errorf("%w: %w", ErrBadCursor, err)
	}
	if len(refs) == 0 {
		return InboxPage{}, nil
	}
	if limit <= 0 {
		limit = defaultInboxLimit
	}
	if limit > maxInboxLimit {
		limit = maxInboxLimit
	}
	rows, err := d.Mirror.Read.QueryContext(ctx, messagesByRefsSelect,
		accountID, accountID, jsonRefs(refs), cursorID, cursorDate, cursorID, limit)
	if err != nil {
		return InboxPage{}, fmt.Errorf("messages by content refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var page InboxPage
	for rows.Next() {
		summary, err := scanInboxSummary(rows)
		if err != nil {
			return InboxPage{}, fmt.Errorf("messages by content refs: %w", err)
		}
		page.Items = append(page.Items, summary)
	}
	if err := rows.Err(); err != nil {
		return InboxPage{}, fmt.Errorf("messages by content refs: %w", err)
	}
	page.NextCursor = nextCursor(page.Items, limit)
	return page, nil
}

const messagesByRefsSelect = `
	SELECT m.id, m.account_id, COALESCE(m.from_json, ''), COALESCE(m.subject, ''),
	       COALESCE(m.snippet, ''), COALESCE(m.date, ''), m.seen, m.flagged,
	       CASE WHEN n.account_id IS NULL THEN 0 ELSE 1 END, m.content_key
	FROM messages m
	LEFT JOIN needs_me n ON n.account_id = m.account_id
		AND n.content_key = m.content_key AND n.verdict = 'needs'
	WHERE m.disabled_at IS NULL
	  AND (? = '' OR m.account_id = ?)
	  AND (m.account_id || char(31) || m.content_key) IN (SELECT value FROM json_each(?))
	GROUP BY m.account_id, m.content_key
	HAVING m.date IS MAX(m.date)
	   AND (? = '' OR (COALESCE(m.date, ''), m.id) < (?, ?))
	ORDER BY COALESCE(m.date, '') DESC, m.id DESC
	LIMIT ?`

// nextCursor is the cursor after a full page, or "" when the page was short and
// so was the last. The date is "" for an undated message, which sorts last.
func nextCursor(items []MessageSummary, limit int) string {
	if len(items) < limit {
		return ""
	}
	last := items[len(items)-1]
	date := ""
	if !last.Date.IsZero() {
		date = formatTime(last.Date)
	}
	return encodeCursor(date, last.ID)
}
