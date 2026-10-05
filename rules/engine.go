// Package rules evaluates the operator's rules. A rule is data (conditions and
// actions), and the only engine here is case-insensitive substring matching
// against headers, so running a rule never costs a model call (round 20). Fuzzy
// "looks like" conditions arrive with Jev in chunk 5.
package rules

import (
	"strings"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
)

// Match reports whether every condition holds for one message. An empty rule or
// an unknown field never matches, so a malformed stored rule cannot tag a whole
// mailbox.
func Match(conditions []store.RuleCondition, msg store.RuleMessage) bool {
	if len(conditions) == 0 {
		return false
	}
	for _, c := range conditions {
		if !matchOne(c, msg) {
			return false
		}
	}
	return true
}

func matchOne(c store.RuleCondition, msg store.RuleMessage) bool {
	switch c.Field {
	case store.RuleFieldFrom:
		return containsFold(msg.From, c.Value) || containsFold(msg.FromName, c.Value)
	case store.RuleFieldSubject:
		return containsFold(msg.Subject, c.Value)
	case store.RuleFieldAccount:
		return msg.AccountID == c.Value
	case store.RuleFieldHasAttachment:
		switch c.Value {
		case "true":
			return msg.HasAttachment
		case "false":
			return !msg.HasAttachment
		}
	}
	return false
}

func containsFold(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// SnoozeUntil resolves a snooze preset to the instant a message comes back, in
// the same location as now (the operator's local time). An unknown preset
// returns now, which wakes the message at once rather than never.
func SnoozeUntil(preset string, now time.Time) time.Time {
	switch preset {
	case store.SnoozeLaterToday:
		evening := time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 0, 0, now.Location())
		if evening.After(now) {
			return evening
		}
		return nextDay(now, 8)
	case store.SnoozeTomorrow:
		return nextDay(now, 8)
	case store.SnoozeWeekend:
		return nextWeekday(time.Saturday, 8, now)
	case store.SnoozeNextWeek:
		return nextWeekday(time.Monday, 8, now)
	}
	return now
}

// nextDay returns tomorrow at the given hour, in now's location.
func nextDay(now time.Time, hour int) time.Time {
	d := now.AddDate(0, 0, 1)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, now.Location())
}

// nextWeekday returns the next occurrence of wd at the given hour strictly
// after now, so a preset never schedules a time in the past.
func nextWeekday(wd time.Weekday, hour int, now time.Time) time.Time {
	at := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	at = at.AddDate(0, 0, (int(wd)-int(now.Weekday())+7)%7)
	if !at.After(now) {
		at = at.AddDate(0, 0, 7)
	}
	return at
}
