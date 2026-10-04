package store

import (
	"context"
	"fmt"
)

// Tag is one of the operator's own labels. Tags and their membership are locally
// owned state, so they live in state.db and are keyed by content key, which
// survives a move between folders (CLAUDE.md non-negotiable 5).
type Tag struct {
	ID    string
	Slug  string
	Name  string
	Color string
}

// UpsertTag writes a tag keyed by id, so re-seeding or renaming never duplicates it.
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
