package rules

import (
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
)

func msg() store.RuleMessage {
	return store.RuleMessage{
		ContentKey:    "ck:1",
		AccountID:     "acct-1",
		From:          "renewals@cloudflare.com",
		FromName:      "Cloudflare",
		Subject:       "Receipt for your renewal",
		HasAttachment: true,
	}
}

func TestMatchHeaderConditions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cond store.RuleCondition
		want bool
	}{
		{"from address substring", store.RuleCondition{Field: store.RuleFieldFrom, Value: "cloudflare.com"}, true},
		{"from display name", store.RuleCondition{Field: store.RuleFieldFrom, Value: "Cloudflare"}, true},
		{"from case-insensitive", store.RuleCondition{Field: store.RuleFieldFrom, Value: "CLOUDFLARE"}, true},
		{"from miss", store.RuleCondition{Field: store.RuleFieldFrom, Value: "example.org"}, false},
		{"subject substring", store.RuleCondition{Field: store.RuleFieldSubject, Value: "renewal"}, true},
		{"subject miss", store.RuleCondition{Field: store.RuleFieldSubject, Value: "invoice"}, false},
		{"account exact", store.RuleCondition{Field: store.RuleFieldAccount, Value: "acct-1"}, true},
		{"account miss", store.RuleCondition{Field: store.RuleFieldAccount, Value: "acct-2"}, false},
		{"attachment true", store.RuleCondition{Field: store.RuleFieldHasAttachment, Value: "true"}, true},
		{"attachment false", store.RuleCondition{Field: store.RuleFieldHasAttachment, Value: "false"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match([]store.RuleCondition{tc.cond}, msg()); got != tc.want {
				t.Errorf("Match(%+v) = %v, want %v", tc.cond, got, tc.want)
			}
		})
	}
}

func TestMatchRequiresEveryCondition(t *testing.T) {
	t.Parallel()
	both := []store.RuleCondition{
		{Field: store.RuleFieldFrom, Value: "cloudflare.com"},
		{Field: store.RuleFieldSubject, Value: "receipt"},
	}
	if !Match(both, msg()) {
		t.Error("both conditions hold but Match said no")
	}
	third := append(both, store.RuleCondition{Field: store.RuleFieldSubject, Value: "invoice"})
	if Match(third, msg()) {
		t.Error("one condition misses but Match said yes")
	}
}

func TestMatchEmptyOrUnknownNeverMatches(t *testing.T) {
	t.Parallel()
	if Match(nil, msg()) {
		t.Error("no conditions matched")
	}
	if Match([]store.RuleCondition{{Field: "body", Value: "receipt"}}, msg()) {
		t.Error("an unknown field matched")
	}
	// A value longer than any header cannot match, and must not panic.
	long := []store.RuleCondition{{Field: store.RuleFieldSubject, Value: string(make([]byte, 4096))}}
	if Match(long, msg()) {
		t.Error("an oversized value matched")
	}
}

func TestSnoozeUntilPresets(t *testing.T) {
	t.Parallel()
	// A Wednesday morning, so every preset lands in the same week.
	now := time.Date(2026, 10, 7, 9, 30, 0, 0, time.UTC)
	cases := map[string]time.Time{
		store.SnoozeLaterToday: time.Date(2026, 10, 7, 18, 0, 0, 0, time.UTC),
		store.SnoozeTomorrow:   time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC),
		store.SnoozeWeekend:    time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC),
		store.SnoozeNextWeek:   time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC),
	}
	for preset, want := range cases {
		if got := SnoozeUntil(preset, now); !got.Equal(want) {
			t.Errorf("SnoozeUntil(%s) = %s, want %s", preset, got, want)
		}
	}

	// "Later today" after the evening cutoff rolls to tomorrow morning.
	late := time.Date(2026, 10, 7, 19, 0, 0, 0, time.UTC)
	if got := SnoozeUntil(store.SnoozeLaterToday, late); !got.Equal(time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("late later_today = %s, want tomorrow 08:00", got)
	}
	// On a Saturday the weekend preset rolls a week, never into the past.
	sat := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if got := SnoozeUntil(store.SnoozeWeekend, sat); !got.Equal(time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("saturday weekend = %s, want next saturday 08:00", got)
	}
}
