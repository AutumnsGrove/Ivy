package store

import (
	"context"
	"fmt"
	"strings"
)

// The "placed for you" tags that a message's category puts on it (5d decision 6).
// Like Needs you they are reserved, so the tag itself cannot be deleted (the next
// message would only recreate it), and local only, so they are never written to the
// server as keywords. A message can lose one: that is the operator saying no.
const (
	categoryTagPrefix  = "t-cat-"
	categorySlugPrefix = "cat-"
)

// placedCategory is one category that earns a tag. Personal mail is left out so no
// automated behaviour ever touches it, and "other" says nothing worth a tag.
var placedCategories = []struct{ Key, Name, Color string }{
	{"newsletter", "Newsletter", "sky"},
	{"receipt", "Receipt", "gold"},
	{"notification", "Notification", "slate"},
	{"marketing", "Marketing", "sand"},
	{"contact_form", "Contact form", "teal"},
	{"security_report", "Security", "berry"},
	{"legal_notice", "Legal", "orchid"},
}

// PlacedCategories lists the categories that place a tag, in display order.
func PlacedCategories() []string {
	out := make([]string, len(placedCategories))
	for i, c := range placedCategories {
		out[i] = c.Key
	}
	return out
}

func categoryTag(category string) (Tag, bool) {
	for _, c := range placedCategories {
		if c.Key == category {
			return Tag{ID: categoryTagPrefix + c.Key, Slug: categorySlugPrefix + c.Key, Name: c.Name, Color: c.Color}, true
		}
	}
	return Tag{}, false
}

func isCategoryTag(id string) bool {
	key, found := strings.CutPrefix(id, categoryTagPrefix)
	if !found {
		return false
	}
	_, ok := categoryTag(key)
	return ok
}

// PlaceCategoryTag puts the category's tag on a message, creating the tag if needed.
// Placing twice is a no-op. A category with no tag is ErrNotFound and changes nothing.
func (d *DBs) PlaceCategoryTag(ctx context.Context, accountID, contentKey, category string) error {
	tag, ok := categoryTag(category)
	if !ok {
		return fmt.Errorf("place category tag %q: %w", category, ErrNotFound)
	}
	if _, err := d.State.Write.ExecContext(ctx,
		`INSERT OR IGNORE INTO tags (id, slug, name, color) VALUES (?, ?, ?, ?)`,
		tag.ID, tag.Slug, tag.Name, tag.Color); err != nil {
		return fmt.Errorf("ensure category tag: %w", err)
	}
	return d.TagMessage(ctx, accountID, contentKey, tag.ID, TagSourcePlaced)
}
