package store

import (
	"context"
	"errors"
	"hash/crc32"
	"strings"
	"testing"
	"time"
)

// seedRuleMessage writes a visible message with the headers a rule matches on.
func seedRuleMessage(t *testing.T, dbs *DBs, accountID, folderID, id, from, subject string, hasAttachment bool, at time.Time) {
	t.Helper()
	seedAccount(t, dbs, accountID)
	seedFolder(t, dbs, accountID, folderID)
	err := dbs.UpsertMessage(context.Background(), Message{
		ID: id, AccountID: accountID, FolderID: folderID, UID: crc32.ChecksumIEEE([]byte(id)),
		ContentKey: "ck:" + id, Date: at,
		From: Address{Address: from}, Subject: subject, HasAttachments: hasAttachment,
	})
	if err != nil {
		t.Fatalf("seedRuleMessage(%s): %v", id, err)
	}
}

func TestRuleCRUDAndMatchCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	in := RuleInput{
		AccountID:  "",
		Conditions: []RuleCondition{{Field: RuleFieldFrom, Value: "cloudflare.com"}},
		Actions:    []RuleAction{{Type: RuleActionTag, TagID: "t1"}},
		Enabled:    true,
	}
	rule, err := dbs.CreateRule(ctx, "r1", in, at)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if rule.ID != "r1" || !rule.Enabled || len(rule.Conditions) != 1 || len(rule.Actions) != 1 {
		t.Fatalf("rule = %+v", rule)
	}

	got, err := dbs.GetRule(ctx, "r1")
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got.Conditions[0].Value != "cloudflare.com" {
		t.Errorf("round-trip condition = %+v", got.Conditions)
	}

	if err := dbs.SetRuleEnabled(ctx, "r1", false, at); err != nil {
		t.Fatalf("SetRuleEnabled: %v", err)
	}
	if got, _ := dbs.GetRule(ctx, "r1"); got.Enabled {
		t.Error("rule still enabled after disabling")
	}

	// A hit is recorded once per (rule, account, content key), however often the
	// pass re-evaluates it.
	if err := dbs.RecordRuleHits(ctx, "r1", []RuleHit{
		{AccountID: "acct-1", ContentKey: "ck:a"},
		{AccountID: "acct-1", ContentKey: "ck:b"},
	}, at); err != nil {
		t.Fatalf("RecordRuleHits: %v", err)
	}
	if err := dbs.RecordRuleHits(ctx, "r1", []RuleHit{
		{AccountID: "acct-1", ContentKey: "ck:a"},
	}, at); err != nil {
		t.Fatalf("second RecordRuleHits: %v", err)
	}
	if got, _ := dbs.GetRule(ctx, "r1"); got.Matches != 2 {
		t.Errorf("matches = %d, want 2", got.Matches)
	}

	list, err := dbs.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(list) != 1 || list[0].Matches != 2 {
		t.Errorf("list = %+v", list)
	}

	if err := dbs.DeleteRule(ctx, "r1"); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	if _, err := dbs.GetRule(ctx, "r1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRule after delete = %v, want ErrNotFound", err)
	}
	if n := countRows(t, dbs, `SELECT count(*) FROM rule_hits`); n != 0 {
		t.Errorf("%d hits after deleting the rule, want 0", n)
	}
}

func TestRuleValidationRejectsBadVocabulary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	cases := map[string]RuleInput{
		"unknown condition field": {
			Conditions: []RuleCondition{{Field: "body", Value: "x"}},
			Actions:    []RuleAction{{Type: RuleActionReading}},
		},
		"empty condition value": {
			Conditions: []RuleCondition{{Field: RuleFieldFrom, Value: "  "}},
			Actions:    []RuleAction{{Type: RuleActionReading}},
		},
		"unknown action": {
			Conditions: []RuleCondition{{Field: RuleFieldFrom, Value: "x"}},
			Actions:    []RuleAction{{Type: "move"}},
		},
		"tag without a tag": {
			Conditions: []RuleCondition{{Field: RuleFieldFrom, Value: "x"}},
			Actions:    []RuleAction{{Type: RuleActionTag}},
		},
		"snooze without a preset": {
			Conditions: []RuleCondition{{Field: RuleFieldFrom, Value: "x"}},
			Actions:    []RuleAction{{Type: RuleActionSnooze}},
		},
		"no conditions": {Actions: []RuleAction{{Type: RuleActionReading}}},
		"no actions":    {Conditions: []RuleCondition{{Field: RuleFieldFrom, Value: "x"}}},
	}
	for name, in := range cases {
		if _, err := dbs.CreateRule(ctx, "r-"+name, in, at); err == nil {
			t.Errorf("%s: CreateRule accepted it", name)
		}
	}

	tooMany := RuleInput{Actions: []RuleAction{{Type: RuleActionReading}}}
	for range MaxRuleConditions + 1 {
		tooMany.Conditions = append(tooMany.Conditions, RuleCondition{Field: RuleFieldFrom, Value: "x"})
	}
	if _, err := dbs.CreateRule(ctx, "r-many", tooMany, at); err == nil {
		t.Error("CreateRule accepted too many conditions")
	}

	long := RuleInput{
		Conditions: []RuleCondition{{Field: RuleFieldFrom, Value: strings.Repeat("x", MaxRuleValueLen+1)}},
		Actions:    []RuleAction{{Type: RuleActionReading}},
	}
	if _, err := dbs.CreateRule(ctx, "r-long", long, at); err == nil {
		t.Error("CreateRule accepted an oversized condition value")
	}
}

func TestMessagesForRulesSkipsEvaluatedHiddenAndDuplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	base := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	seedRuleMessage(t, dbs, "acct-1", "inbox-1", "m1", "a@x.test", "fresh", false, base.Add(time.Hour))
	seedRuleMessage(t, dbs, "acct-1", "inbox-1", "m2", "b@x.test", "already evaluated", false, base)
	seedRuleMessage(t, dbs, "acct-1", "inbox-1", "m3", "c@x.test", "hidden", false, base)
	if err := dbs.DisableMessage(ctx, "m3", DisabledRemoved, base, ""); err != nil {
		t.Fatalf("DisableMessage: %v", err)
	}
	// The same content key in a second folder must yield one candidate, not two.
	seedFolder(t, dbs, "acct-1", "archive-1")
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "m1-copy", AccountID: "acct-1", FolderID: "archive-1", UID: 99,
		ContentKey: "ck:m1", Date: base, From: Address{Address: "a@x.test"}, Subject: "fresh",
	}); err != nil {
		t.Fatalf("seed copy: %v", err)
	}
	if err := dbs.MarkRuleEvaluated(ctx, "acct-1", []string{"ck:m2"}); err != nil {
		t.Fatalf("MarkRuleEvaluated: %v", err)
	}

	msgs, err := dbs.RuleMessages(ctx, "acct-1", 10, true)
	if err != nil {
		t.Fatalf("RuleMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ContentKey != "ck:m1" {
		t.Fatalf("candidates = %+v, want only ck:m1", msgs)
	}
	if msgs[0].From != "a@x.test" || msgs[0].Subject != "fresh" || msgs[0].FolderID == "" {
		t.Errorf("candidate headers = %+v", msgs[0])
	}

	// A dry run sees every visible message, evaluated or not.
	all, err := dbs.RuleMessages(ctx, "acct-1", 10, false)
	if err != nil {
		t.Fatalf("RuleMessages dry run: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("dry-run candidates = %d, want 2 (hidden excluded, duplicates folded)", len(all))
	}
}
