package devstack

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/rules"
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
	{ID: store.ReadingTagID, Slug: store.ReadingSlug, Name: store.ReadingTagName, Color: "lilac"},
	{ID: "t-receipts", Slug: "receipts", Name: "receipts", Color: "sky"},
	{ID: "t-legal", Slug: "legal", Name: "legal", Color: "coral"},
	{ID: "t-contact-form", Slug: "contact-form", Name: "contact form", Color: "rose"},
	{ID: "t-grove", Slug: "grove", Name: "grove", Color: "teal"},
	{ID: "t-ideas", Slug: "ideas", Name: "ideas", Color: "lilac"},
}

// seedRuleTime is fixed, so the rule hits, outbox ops and snoozes the dev seed
// produces are identical in both modes (the agreement test compares them).
var seedRuleTime = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// devRules are the header-only rules the dev mailbox ships with. Their subjects
// are the generated corpus's, so the example matches real demo mail; the fuzzy
// "looks like a receipt" conditions arrive with Jev in chunk 5.
var devRules = []struct {
	id string
	in store.RuleInput
}{
	{"r-newsletter", store.RuleInput{
		Conditions: []store.RuleCondition{{Field: store.RuleFieldSubject, Value: "Garden Weekly"}},
		Actions:    []store.RuleAction{{Type: store.RuleActionReading}},
		Enabled:    true,
	}},
	{"r-receipts", store.RuleInput{
		Conditions: []store.RuleCondition{{Field: store.RuleFieldSubject, Value: "Receipt for order"}},
		Actions:    []store.RuleAction{{Type: store.RuleActionTag, TagID: "t-receipts"}},
		Enabled:    true,
	}},
	{"r-contact", store.RuleInput{
		Conditions: []store.RuleCondition{{Field: store.RuleFieldSubject, Value: "contact form"}},
		Actions:    []store.RuleAction{{Type: store.RuleActionTag, TagID: "t-contact-form"}},
		Enabled:    true,
	}},
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
	if err := seedRules(ctx, dbs, accounts); err != nil {
		return err
	}
	return dbs.SetSetting(ctx, "", seededKey, "1")
}

// seedRules writes the dev rules and applies them to the mail already mirrored,
// which is what makes the dev mailbox show rule matches. Both modes run the same
// deterministic applier, so their hits and outbox rows agree.
func seedRules(ctx context.Context, dbs *store.DBs, accounts []config.Account) error {
	ids := 0
	applier := rules.NewApplier(dbs,
		func() time.Time { return seedRuleTime },
		func() string { ids++; return fmt.Sprintf("seed-op-%03d", ids) })
	accountIDs := make([]string, len(accounts))
	for i, a := range accounts {
		accountIDs[i] = a.ID
	}
	for _, r := range devRules {
		if _, err := dbs.CreateRule(ctx, r.id, r.in, seedRuleTime); err != nil {
			return fmt.Errorf("devstack: seed rule %s: %w", r.id, err)
		}
	}
	for _, r := range devRules {
		if _, err := applier.ApplyToExisting(ctx, r.id, accountIDs); err != nil {
			return fmt.Errorf("devstack: apply rule %s: %w", r.id, err)
		}
	}
	return nil
}
