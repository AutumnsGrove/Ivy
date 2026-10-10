package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// UrgencyBar says which cached urgency answers count as "urgent" for ordering: the
// current wording of the urgency question, from the current Jev model, choosing the
// high option with both its probability and the answer's confidence at the bar. The
// caller takes all of it from the live registry, so retuning changes the order with
// no migration. A zero value ranks nothing as urgent.
type UrgencyBar struct {
	QuestionID string
	Hash       string
	Model      string
	High       string
	Threshold  float64
}

// NeedsAttentionQuery selects a page of the Needs attention view.
type NeedsAttentionQuery struct {
	// AccountID limits the view to one account; empty means every account.
	AccountID string
	Cursor    string
	Limit     int
	// Hide removes locally hidden mail (snoozed, in Reading), as the inbox does.
	Hide    []ContentRef
	Urgency UrgencyBar
}

// needsAttentionSelect lists the Inbox messages with an open "needs" verdict. rank is
// 0 for a clearly urgent one and 1 for the rest, so the order is urgent first and
// then newest first inside each group. The keyset condition spells that mixed order
// out: a later page is a higher rank, or the same rank and an older (date, id).
const needsAttentionSelect = `
	SELECT id, account_id, from_json, subject, snippet, date, seen, flagged, needs, reason, content_key, rank FROM (
		SELECT m.id AS id, m.account_id AS account_id, COALESCE(m.from_json, '') AS from_json,
		       COALESCE(m.subject, '') AS subject, COALESCE(m.snippet, '') AS snippet,
		       COALESCE(m.date, '') AS date, m.seen AS seen, m.flagged AS flagged, 1 AS needs,
		       COALESCE(n.reason, '') AS reason, m.content_key AS content_key,
		       CASE WHEN u.account_id IS NULL THEN 1 ELSE 0 END AS rank
		FROM needs_me n
		CROSS JOIN messages m ON m.account_id = n.account_id AND m.content_key = n.content_key
		CROSS JOIN folders f ON f.id = m.folder_id
		LEFT JOIN decisions u ON u.account_id = m.account_id AND u.content_key = m.content_key
			AND u.question_id = ? AND u.instruction_hash = ? AND u.model = ? AND u.choice = ?
			AND u.confidence >= ?
			AND json_extract(u.probabilities, '$."' || u.choice || '"') >= ?
		WHERE n.verdict = 'needs' AND n.state <> 'dismissed'
		  AND f.role = 'inbox' AND m.disabled_at IS NULL
		  AND (? = '' OR m.account_id = ?)
		  AND (m.account_id || char(31) || m.content_key) NOT IN (SELECT value FROM json_each(?))
		GROUP BY m.account_id, m.content_key
		HAVING m.date IS MAX(m.date)
	)
	WHERE (? = '' OR rank > ? OR (rank = ? AND (date < ? OR (date = ? AND id < ?))))
	ORDER BY rank, date DESC, id DESC
	LIMIT ?`

const needsAttentionCount = `
	SELECT COUNT(DISTINCT m.account_id || char(31) || m.content_key)
	FROM needs_me n
	CROSS JOIN messages m ON m.account_id = n.account_id AND m.content_key = n.content_key
	CROSS JOIN folders f ON f.id = m.folder_id
	WHERE n.verdict = 'needs' AND n.state <> 'dismissed'
	  AND f.role = 'inbox' AND m.disabled_at IS NULL
	  AND (? = '' OR m.account_id = ?)
	  AND (m.account_id || char(31) || m.content_key) NOT IN (SELECT value FROM json_each(?))`

type needsCursor struct {
	Rank int    `json:"r"`
	Date string `json:"d"`
	ID   string `json:"i"`
}

func encodeNeedsCursor(c needsCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeNeedsCursor(s string) (needsCursor, error) {
	var c needsCursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	if c.Rank < 0 || c.Rank > 1 {
		return c, fmt.Errorf("rank %d", c.Rank)
	}
	return c, nil
}

// rankScanner lets scanInboxSummary read the ten columns it knows and then the rank.
type rankScanner struct {
	scanner
	rank *int
}

func (r rankScanner) Scan(dest ...any) error { return r.scanner.Scan(append(dest, r.rank)...) }

// NeedsAttention returns a page of the Needs attention view: Inbox mail with an open
// verdict, urgent first and newest first inside each group, with keyset paging.
// NeedCount is the whole view's size, not the page's.
func (d *DBs) NeedsAttention(ctx context.Context, q NeedsAttentionQuery) (InboxPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultInboxLimit
	}
	if limit > maxInboxLimit {
		limit = maxInboxLimit
	}
	var cur needsCursor
	if q.Cursor != "" {
		var err error
		if cur, err = decodeNeedsCursor(q.Cursor); err != nil {
			return InboxPage{}, fmt.Errorf("%w: %w", ErrBadCursor, err)
		}
	}
	hide := jsonRefs(q.Hide)

	page := InboxPage{}
	if err := d.Mirror.Read.QueryRowContext(ctx, needsAttentionCount, q.AccountID, q.AccountID, hide).Scan(&page.NeedCount); err != nil {
		return InboxPage{}, fmt.Errorf("count needs attention: %w", err)
	}
	// With no urgency question configured nothing can match the bar.
	u := q.Urgency
	rows, err := d.Mirror.Read.QueryContext(ctx, needsAttentionSelect,
		u.QuestionID, u.Hash, u.Model, u.High, u.Threshold, u.Threshold,
		q.AccountID, q.AccountID, hide,
		q.Cursor, cur.Rank, cur.Rank, cur.Date, cur.Date, cur.ID, limit)
	if err != nil {
		return InboxPage{}, fmt.Errorf("list needs attention: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ranks []int
	for rows.Next() {
		var rank int
		summary, err := scanInboxSummary(rankScanner{scanner: rows, rank: &rank})
		if err != nil {
			return InboxPage{}, fmt.Errorf("list needs attention: %w", err)
		}
		page.Items = append(page.Items, summary)
		ranks = append(ranks, rank)
	}
	if err := rows.Err(); err != nil {
		return InboxPage{}, fmt.Errorf("list needs attention: %w", err)
	}
	if len(page.Items) == limit {
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeNeedsCursor(needsCursor{
			Rank: ranks[len(ranks)-1], Date: dateKey(last), ID: last.ID,
		})
	}
	return page, nil
}

// dateKey is the date string the SQL compares: RFC 3339 as stored, or empty for an
// undated message.
func dateKey(m MessageSummary) string {
	if m.Date.IsZero() {
		return ""
	}
	return formatTime(m.Date)
}
