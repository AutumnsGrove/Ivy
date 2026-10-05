package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Tag limits (STANDARDS.md 4a). A tag name is operator input and a slug becomes
// an IMAP keyword on the server, so both are bounded with a defined outcome.
const (
	// MaxTags is how many tags may exist; creating one more is ErrTagLimit.
	MaxTags = 200
	// MaxTagNameLen is the longest tag name in characters; longer is ErrTagName.
	MaxTagNameLen = 64
	// MaxTagSlugLen is the longest slug in bytes. It bounds the keyword Ivy
	// writes and, on read-back, the keyword it is willing to consider.
	MaxTagSlugLen = 48
)

// KeywordPrefix starts every IMAP keyword Ivy owns for a tag. Keywords are IMAP
// atoms and case-insensitive, so the slug after it is lower-case [a-z0-9-].
const KeywordPrefix = "$ivy-"

// ErrTagName reports a tag name that is empty or longer than MaxTagNameLen.
var ErrTagName = errors.New("bad tag name")

// ErrTagLimit reports a tag created past MaxTags.
var ErrTagLimit = errors.New("too many tags")

// Tag is one of the operator's own labels. Tags and their membership are locally
// owned state, so they live in state.db and are keyed by content key, which
// survives a move between folders (CLAUDE.md non-negotiable 5).
type Tag struct {
	ID    string
	Slug  string
	Name  string
	Color string
}

// TagSummary is a tag with the number of messages in it.
type TagSummary struct {
	Tag
	Count int
}

// TagMember is one message in a tag.
type TagMember struct {
	AccountID  string
	ContentKey string
}

// asciiFold maps the Latin letters a tag name is likely to carry to ASCII. It
// is a table, not x/text, because a slug only has to be a stable keyword, not a
// linguistically correct transliteration; anything it does not know separates
// words.
var asciiFold = func() map[rune]string {
	m := map[rune]string{}
	for to, from := range map[string]string{
		"a": "àáâãäåāăą", "c": "çćĉċč", "d": "ďđ", "e": "èéêëēĕėęě", "g": "ĝğġģ",
		"h": "ĥħ", "i": "ìíîïĩīĭįı", "j": "ĵ", "k": "ķ", "l": "ĺļľŀł", "n": "ñńņň",
		"o": "òóôõöøōŏő", "r": "ŕŗř", "s": "śŝşš", "t": "ţťŧ", "u": "ùúûüũūŭůűų",
		"w": "ŵ", "y": "ýÿŷ", "z": "źżž",
	} {
		for _, r := range from {
			m[r] = to
		}
	}
	m['ß'], m['æ'], m['œ'] = "ss", "ae", "oe"
	return m
}()

// TagSlug derives the ASCII slug a tag name gets as its keyword: lower-case
// letters and digits, with any run of other characters as one hyphen. It can be
// empty (a name with no Latin letters), which CreateTag answers with "tag".
func TagSlug(name string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(name) {
		var piece string
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			piece = string(r)
		default:
			piece = asciiFold[r]
		}
		if piece == "" {
			hyphen = b.Len() > 0
			continue
		}
		if hyphen {
			b.WriteByte('-')
			hyphen = false
		}
		b.WriteString(piece)
	}
	slug := b.String()
	if len(slug) > MaxTagSlugLen {
		slug = strings.TrimRight(slug[:MaxTagSlugLen], "-")
	}
	return slug
}

// TagKeyword is the IMAP keyword a tag's slug is written as.
func TagKeyword(slug string) string { return KeywordPrefix + slug }

// SlugFromKeyword reads a tag slug back out of an IMAP flag. Anything that is
// not exactly an Ivy keyword with a well-formed slug of at most MaxTagSlugLen is
// refused, because another client or hostile mail can put any text there.
func SlugFromKeyword(flag string) (string, bool) {
	if len(flag) <= len(KeywordPrefix) || !strings.EqualFold(flag[:len(KeywordPrefix)], KeywordPrefix) {
		return "", false
	}
	slug := strings.ToLower(flag[len(KeywordPrefix):])
	if len(slug) > MaxTagSlugLen {
		return "", false
	}
	for i := range len(slug) {
		c := slug[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return "", false
		}
	}
	return slug, true
}

func cleanTagName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxTagNameLen {
		return "", ErrTagName
	}
	return name, nil
}

// slugCandidate is the n-th choice for a base slug: the base, then base-2, base-3
// and so on. The suffix always fits: a long base is cut to make room for it.
func slugCandidate(base string, n int) string {
	if n == 1 {
		return base
	}
	suffix := "-" + strconv.Itoa(n)
	if len(base)+len(suffix) > MaxTagSlugLen {
		base = strings.TrimRight(base[:MaxTagSlugLen-len(suffix)], "-")
	}
	return base + suffix
}

// CreateTag adds a tag. Its slug is fixed here and never recomputed, so a rename
// cannot orphan the keyword on the server; a slug another tag already has gets a
// numeric suffix (round 51).
func (d *DBs) CreateTag(ctx context.Context, id, name, color string) (Tag, error) {
	name, err := cleanTagName(name)
	if err != nil {
		return Tag{}, err
	}
	base := TagSlug(name)
	if base == "" {
		base = "tag"
	}
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return Tag{}, fmt.Errorf("create tag: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tags`).Scan(&count); err != nil {
		return Tag{}, fmt.Errorf("create tag: count: %w", err)
	}
	if count >= MaxTags {
		return Tag{}, ErrTagLimit
	}
	// At most MaxTags rows exist, so a free candidate is found within MaxTags+1.
	for n := 1; n <= MaxTags+1; n++ {
		slug := slugCandidate(base, n)
		var taken int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tags WHERE slug = ?`, slug).Scan(&taken); err != nil {
			return Tag{}, fmt.Errorf("create tag: slug: %w", err)
		}
		if taken > 0 {
			continue
		}
		tag := Tag{ID: id, Slug: slug, Name: name, Color: color}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tags (id, slug, name, color) VALUES (?, ?, ?, ?)`, tag.ID, tag.Slug, tag.Name, tag.Color); err != nil {
			return Tag{}, fmt.Errorf("create tag %s: %w", id, err)
		}
		return tag, tx.Commit()
	}
	return Tag{}, fmt.Errorf("create tag %s: no free slug for %q", id, base)
}

// UpdateTag renames and recolours a tag. The slug stays, so the keyword already
// written to the server keeps meaning this tag.
func (d *DBs) UpdateTag(ctx context.Context, id, name, color string) (Tag, error) {
	name, err := cleanTagName(name)
	if err != nil {
		return Tag{}, err
	}
	res, err := d.State.Write.ExecContext(ctx, `UPDATE tags SET name = ?, color = ? WHERE id = ?`, name, color, id)
	if err != nil {
		return Tag{}, fmt.Errorf("update tag %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return Tag{}, fmt.Errorf("update tag %s: %w", id, err)
		}
		return Tag{}, ErrNotFound
	}
	return d.GetTag(ctx, id)
}

// GetTag returns a tag by id, or ErrNotFound.
func (d *DBs) GetTag(ctx context.Context, id string) (Tag, error) {
	return d.oneTag(ctx, `SELECT id, slug, name, color FROM tags WHERE id = ?`, id)
}

// TagBySlug returns a tag by slug, or ErrNotFound. A keyword is case-insensitive
// on the server, so the lookup is too.
func (d *DBs) TagBySlug(ctx context.Context, slug string) (Tag, error) {
	return d.oneTag(ctx, `SELECT id, slug, name, color FROM tags WHERE slug = ?`, strings.ToLower(slug))
}

func (d *DBs) oneTag(ctx context.Context, query string, arg string) (Tag, error) {
	var t Tag
	err := d.State.Read.QueryRowContext(ctx, query, arg).Scan(&t.ID, &t.Slug, &t.Name, &t.Color)
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, ErrNotFound
	}
	if err != nil {
		return Tag{}, fmt.Errorf("read tag: %w", err)
	}
	return t, nil
}

// ListTags returns every tag, by name, with its message count.
func (d *DBs) ListTags(ctx context.Context) ([]TagSummary, error) {
	rows, err := d.State.Read.QueryContext(ctx, `
		SELECT t.id, t.slug, t.name, t.color, count(m.tag_id)
		FROM tags t LEFT JOIN message_tags m ON m.tag_id = t.id
		GROUP BY t.id ORDER BY lower(t.name), t.id`)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TagSummary
	for rows.Next() {
		var s TagSummary
		if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.Color, &s.Count); err != nil {
			return nil, fmt.Errorf("list tags: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	return out, nil
}

// TagsForMessages returns the tags of each content key of one account, by name.
// A key with no tag is absent from the map. The caller bounds keys (a page).
func (d *DBs) TagsForMessages(ctx context.Context, accountID string, contentKeys []string) (map[string][]Tag, error) {
	out := map[string][]Tag{}
	if len(contentKeys) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(contentKeys)+1)
	args = append(args, accountID)
	for _, k := range contentKeys {
		args = append(args, k)
	}
	rows, err := d.State.Read.QueryContext(ctx, `
		SELECT m.content_key, t.id, t.slug, t.name, t.color
		FROM message_tags m JOIN tags t ON t.id = m.tag_id
		WHERE m.account_id = ? AND m.content_key IN (`+strings.TrimSuffix(strings.Repeat("?,", len(contentKeys)), ",")+`)
		ORDER BY lower(t.name), t.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("tags for messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var t Tag
		if err := rows.Scan(&key, &t.ID, &t.Slug, &t.Name, &t.Color); err != nil {
			return nil, fmt.Errorf("tags for messages: %w", err)
		}
		out[key] = append(out[key], t)
	}
	return out, rows.Err()
}

// TagMembers returns every message in a tag. Deleting a tag walks it to clear
// the keyword from the server.
func (d *DBs) TagMembers(ctx context.Context, tagID string) ([]TagMember, error) {
	rows, err := d.State.Read.QueryContext(ctx,
		`SELECT account_id, content_key FROM message_tags WHERE tag_id = ? ORDER BY account_id, content_key`, tagID)
	if err != nil {
		return nil, fmt.Errorf("tag members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TagMember
	for rows.Next() {
		var m TagMember
		if err := rows.Scan(&m.AccountID, &m.ContentKey); err != nil {
			return nil, fmt.Errorf("tag members: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpsertTag writes a tag keyed by id, so re-seeding never duplicates it. It
// takes the slug as given; the operator's path is CreateTag.
func (d *DBs) UpsertTag(ctx context.Context, t Tag) error {
	_, err := d.State.Write.ExecContext(ctx, `
		INSERT INTO tags (id, slug, name, color) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET slug=excluded.slug, name=excluded.name, color=excluded.color`,
		t.ID, t.Slug, t.Name, t.Color)
	if err != nil {
		return fmt.Errorf("upsert tag %s: %w", t.ID, err)
	}
	return nil
}

// TagMessage puts a message in a tag. Tagging twice is a no-op; the tag must
// exist. source records who tagged it (the operator, a rule, an IMAP keyword).
func (d *DBs) TagMessage(ctx context.Context, accountID, contentKey, tagID, source string) error {
	_, err := d.State.Write.ExecContext(ctx, `
		INSERT INTO message_tags (account_id, content_key, tag_id, source) VALUES (?, ?, ?, ?)
		ON CONFLICT(account_id, content_key, tag_id) DO NOTHING`,
		accountID, contentKey, tagID, source)
	if err != nil {
		return fmt.Errorf("tag message %s in %s: %w", contentKey, tagID, err)
	}
	return nil
}

// UntagMessage takes a message out of a tag. Untagging a message that is not in
// it is a no-op.
func (d *DBs) UntagMessage(ctx context.Context, accountID, contentKey, tagID string) error {
	_, err := d.State.Write.ExecContext(ctx,
		`DELETE FROM message_tags WHERE account_id = ? AND content_key = ? AND tag_id = ?`,
		accountID, contentKey, tagID)
	if err != nil {
		return fmt.Errorf("untag message %s from %s: %w", contentKey, tagID, err)
	}
	return nil
}

// DeleteTag removes a tag and its memberships together, or ErrNotFound. It only
// touches state.db: clearing the keyword from the server is the caller's job,
// through the outbox, before this runs.
func (d *DBs) DeleteTag(ctx context.Context, id string) error {
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_tags WHERE tag_id = ?`, id); err != nil {
		return fmt.Errorf("delete tag %s: memberships: %w", id, err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete tag %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete tag %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
