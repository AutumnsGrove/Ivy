package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PersonRow is one address's correspondence with one account, derived from the
// visible mail. People is a derived view, so it lives in mirror.db; the
// operator's merges of several addresses into one person are locally owned
// (person_links in state.db) and applied when reading.
type PersonRow struct {
	Address      string
	AccountID    string
	Name         string
	MessageCount int
	FirstSeen    time.Time
	LastSeen     time.Time
	LastSubject  string
}

// RebuildPeople recomputes one account's correspondents from its visible mail:
// every address that appears in a From, To or Cc header. It replaces the
// account's rows, so it is idempotent. The operator's own addresses are derived
// too and filtered out when read, so the result never depends on which accounts
// happened to be mirrored first.
func (d *DBs) RebuildPeople(ctx context.Context, accountID string) error {
	rows, err := d.Mirror.Read.QueryContext(ctx, peopleSourceQuery, accountID)
	if err != nil {
		return fmt.Errorf("rebuild people: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type agg struct {
		name    string
		count   int
		first   time.Time
		last    time.Time
		subject string
	}
	people := map[string]*agg{}
	for rows.Next() {
		var from, to, cc, date, subject string
		if err := rows.Scan(&from, &to, &cc, &date, &subject); err != nil {
			return fmt.Errorf("rebuild people: %w", err)
		}
		at, err := parseTime(date)
		if err != nil {
			return fmt.Errorf("rebuild people: %w", err)
		}
		addrs := decodeAddresses(from, to, cc)
		seen := map[string]bool{}
		for _, a := range addrs {
			addr := strings.ToLower(strings.TrimSpace(a.Address))
			if addr == "" || seen[addr] {
				continue
			}
			seen[addr] = true
			e := people[addr]
			if e == nil {
				e = &agg{first: at}
				people[addr] = e
			}
			if e.name == "" && a.Name != "" {
				e.name = a.Name
			}
			e.count++
			if e.first.IsZero() || at.Before(e.first) {
				e.first = at
			}
			if !at.Before(e.last) {
				e.last = at
				e.subject = subject
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rebuild people: %w", err)
	}

	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rebuild people: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM people WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("rebuild people: clear: %w", err)
	}
	for addr, e := range people {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO people (address, account_id, name, message_count, first_seen, last_seen, last_subject)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			addr, accountID, e.name, e.count, formatTime(e.first), formatTime(e.last), e.subject); err != nil {
			return fmt.Errorf("rebuild people: insert: %w", err)
		}
	}
	return tx.Commit()
}

// decodeAddresses reads the stored header JSON: one address for From, an array
// for To and Cc. Malformed JSON is skipped, never fatal.
func decodeAddresses(from, to, cc string) []Address {
	seen := map[string]bool{}
	var out []Address
	add := func(a Address) {
		key := strings.ToLower(strings.TrimSpace(a.Address))
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, a)
	}
	var f Address
	if from != "" && json.Unmarshal([]byte(from), &f) == nil {
		add(f)
	}
	for _, raw := range []string{to, cc} {
		if raw == "" {
			continue
		}
		var list []Address
		if json.Unmarshal([]byte(raw), &list) == nil {
			for _, a := range list {
				add(a)
			}
		}
	}
	return out
}

// ListPeople returns every derived correspondent row.
func (d *DBs) ListPeople(ctx context.Context) ([]PersonRow, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx,
		`SELECT address, account_id, name, message_count, first_seen, last_seen, last_subject
		 FROM people ORDER BY address, account_id`)
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PersonRow
	for rows.Next() {
		var (
			p                    PersonRow
			first, last, subject string
		)
		if err := rows.Scan(&p.Address, &p.AccountID, &p.Name, &p.MessageCount, &first, &last, &subject); err != nil {
			return nil, fmt.Errorf("list people: %w", err)
		}
		if p.FirstSeen, err = parseTime(first); err != nil {
			return nil, fmt.Errorf("list people: %w", err)
		}
		if p.LastSeen, err = parseTime(last); err != nil {
			return nil, fmt.Errorf("list people: %w", err)
		}
		p.LastSubject = subject
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	return out, nil
}

// PersonLinks returns the operator's merges: an address mapped to the canonical
// address of the person it belongs to.
func (d *DBs) PersonLinks(ctx context.Context) (map[string]string, error) {
	rows, err := d.State.Read.QueryContext(ctx, `SELECT address, person_id FROM person_links`)
	if err != nil {
		return nil, fmt.Errorf("person links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var addr, person string
		if err := rows.Scan(&addr, &person); err != nil {
			return nil, fmt.Errorf("person links: %w", err)
		}
		out[addr] = person
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("person links: %w", err)
	}
	return out, nil
}

// LinkPerson merges one address into a person, whose id is the canonical
// address. A merge is locally owned, so a mirror rebuild keeps it.
func (d *DBs) LinkPerson(ctx context.Context, address, personID string, now time.Time) error {
	address = strings.ToLower(strings.TrimSpace(address))
	personID = strings.ToLower(strings.TrimSpace(personID))
	if address == "" || personID == "" || address == personID {
		return ErrBadPerson
	}
	_, err := d.State.Write.ExecContext(ctx,
		`INSERT INTO person_links (address, person_id, created_at) VALUES (?, ?, ?)
		 ON CONFLICT (address) DO UPDATE SET person_id = excluded.person_id`,
		address, personID, formatTime(now))
	if err != nil {
		return fmt.Errorf("link person: %w", err)
	}
	return nil
}

// UnlinkPerson removes one address's merge, so it is its own person again.
func (d *DBs) UnlinkPerson(ctx context.Context, address string) error {
	address = strings.ToLower(strings.TrimSpace(address))
	res, err := d.State.Write.ExecContext(ctx, `DELETE FROM person_links WHERE address = ?`, address)
	if err != nil {
		return fmt.Errorf("unlink person: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PersonConversation is one thread's latest message involving a person.
type PersonConversation struct {
	ID         string
	ThreadID   string
	Subject    string
	Snippet    string
	Date       time.Time
	Unread     bool
	ContentKey string
}

// ConversationsForAddresses returns the most recent message per thread that
// involves any of the addresses, for a person page. It matches the stored
// header JSON by substring, bounded by limit.
func (d *DBs) ConversationsForAddresses(ctx context.Context, addresses []string, limit int) ([]PersonConversation, error) {
	if len(addresses) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var where []string
	var args []any
	for _, a := range addresses {
		like := "%\"" + strings.ToLower(a) + "\"%"
		where = append(where, `(lower(COALESCE(m.from_json,'')) LIKE ? OR lower(COALESCE(m.to_json,'')) LIKE ? OR lower(COALESCE(m.cc_json,'')) LIKE ?)`)
		args = append(args, like, like, like)
	}
	query := `SELECT m.id, COALESCE(m.thread_id,''), COALESCE(m.subject,''), COALESCE(m.snippet,''),
	                 COALESCE(m.date,''), m.seen, m.content_key
	          FROM messages m
	          WHERE m.disabled_at IS NULL AND (` + strings.Join(where, " OR ") + `)
	          GROUP BY COALESCE(NULLIF(m.thread_id, ''), m.id)
	          HAVING m.date = MAX(m.date)
	          ORDER BY m.date DESC
	          LIMIT ?`
	args = append(args, limit)
	rows, err := d.Mirror.Read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("conversations for person: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PersonConversation
	for rows.Next() {
		var (
			m    PersonConversation
			date string
			seen bool
		)
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.Subject, &m.Snippet, &date, &seen, &m.ContentKey); err != nil {
			return nil, fmt.Errorf("conversations for person: %w", err)
		}
		m.Unread = !seen
		if m.Date, err = parseTime(date); err != nil {
			return nil, fmt.Errorf("conversations for person: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conversations for person: %w", err)
	}
	return out, nil
}

// ErrBadPerson reports an empty or self-referential person merge.
var ErrBadPerson = errors.New("bad person link")

const peopleSourceQuery = `
	SELECT COALESCE(m.from_json, ''), COALESCE(m.to_json, '[]'), COALESCE(m.cc_json, '[]'),
	       COALESCE(m.date, ''), COALESCE(m.subject, '')
	FROM messages m
	WHERE m.account_id = ? AND m.disabled_at IS NULL
	GROUP BY m.content_key
	HAVING m.date = MAX(m.date)
	ORDER BY m.date, m.id`

