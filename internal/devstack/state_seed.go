package devstack

import (
	"context"
	"fmt"
	"strings"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/store"
)

// seededKey marks a state.db that already holds the dev seed. Populate runs on
// every `up`, and without the marker a restart would overwrite whatever the
// operator renamed or tagged in the UI since.
const seededKey = "dev.state_seeded"

// profiles are the names and icons the dev accounts get, in config order.
var profiles = []struct{ name, icon string }{
	{"Personal", "🌿"},
	{"Studio", "🪴"},
	{"Projects", "🌙"},
}

// devTags are the operator's labels, with the colour names the frontend uses.
var devTags = []store.Tag{
	{ID: "t-receipts", Slug: "receipts", Name: "receipts", Color: "sky"},
	{ID: "t-legal", Slug: "legal", Name: "legal", Color: "coral"},
	{ID: "t-contact-form", Slug: "contact-form", Name: "contact form", Color: "rose"},
	{ID: "t-grove", Slug: "grove", Name: "grove", Color: "teal"},
	{ID: "t-ideas", Slug: "ideas", Name: "ideas", Color: "lilac"},
}

// tagFor places a seeded message by its subject: the demo corpus is generated,
// so its subjects are known. A message with no match stays untagged, and the
// "ideas" tag stays empty, as a fresh tag would.
func tagFor(subject string) string {
	s := strings.ToLower(subject)
	switch {
	case strings.HasPrefix(s, "receipt for order"):
		return "t-receipts"
	case strings.HasPrefix(s, "invoice"):
		return "t-legal"
	case strings.Contains(s, "contact form"):
		return "t-contact-form"
	case strings.Contains(s, "weekend plans"):
		return "t-grove"
	}
	return ""
}

// seedState writes the locally owned dev state: account names and icons, tags
// and their members. It runs after the mirror in either mode, since none of it
// comes from IMAP, and only once per state.db. Rules, snoozes, the ledger and Jev
// decisions join it when the chunk that creates their tables lands.
func seedState(ctx context.Context, dbs *store.DBs, accounts []config.Account) error {
	if _, done, err := dbs.GetSetting(ctx, "", seededKey); err != nil {
		return err
	} else if done {
		return nil
	}
	for i, a := range accounts {
		name, icon := a.ID, ""
		if i < len(profiles) {
			name, icon = profiles[i].name, profiles[i].icon
		}
		if err := dbs.SetAccountProfile(ctx, a.ID, name, icon); err != nil {
			return fmt.Errorf("devstack: seed profile %s: %w", a.ID, err)
		}
	}
	for _, t := range devTags {
		if err := dbs.UpsertTag(ctx, t); err != nil {
			return fmt.Errorf("devstack: seed tag %s: %w", t.ID, err)
		}
	}
	for _, a := range accounts {
		msgs, err := dbs.MessagesForThreading(ctx, a.ID)
		if err != nil {
			return fmt.Errorf("devstack: seed tags for %s: %w", a.ID, err)
		}
		for _, m := range msgs {
			if tag := tagFor(m.Subject); tag != "" {
				if err := dbs.TagMessage(ctx, a.ID, m.ContentKey, tag, "manual"); err != nil {
					return fmt.Errorf("devstack: seed tag for %s: %w", a.ID, err)
				}
			}
		}
	}
	return dbs.SetSetting(ctx, "", seededKey, "1")
}
