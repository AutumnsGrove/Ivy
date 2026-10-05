package rules

import (
	"context"
	"fmt"
	"hash/crc32"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
)

func newTestDB(t *testing.T) *store.DBs {
	t.Helper()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	return dbs
}

func seedAccount(t *testing.T, dbs *store.DBs, id string) {
	t.Helper()
	if err := dbs.UpsertAccount(context.Background(), store.Account{
		ID: id, Address: id + "@example.test", CreatedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
}

func seedFolder(t *testing.T, dbs *store.DBs, accountID, folderID string) {
	t.Helper()
	if err := dbs.UpsertFolder(context.Background(), store.Folder{
		ID: folderID, AccountID: accountID, Name: folderID, Role: store.RoleInbox,
	}); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
}

func seedMessage(t *testing.T, dbs *store.DBs, accountID, folderID, id, contentKey, from, subject string) {
	t.Helper()
	if err := dbs.UpsertMessage(context.Background(), store.Message{
		ID: id, AccountID: accountID, FolderID: folderID, UID: crc32.ChecksumIEEE([]byte(id)),
		ContentKey: contentKey, Date: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
		From: store.Address{Address: from}, Subject: subject,
	}); err != nil {
		t.Fatalf("seed message: %v", err)
	}
}

func fixedClock() (func() time.Time, time.Time) {
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	return func() time.Time { return at }, at
}

func counterIDs() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("op-%d", n) }
}

func TestEvaluateAppliesTagReadingAndSnooze(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := newTestDB(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "inbox-1")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m1", "ck:1", "billing@cloudflare.com", "Receipt for your renewal")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m2", "ck:2", "news@wildflowers.test", "Weekly newsletter")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m3", "ck:3", "friend@example.org", "Lunch?")

	if err := dbs.UpsertTag(ctx, store.Tag{ID: "t1", Slug: "receipts", Name: "receipts", Color: "sky"}); err != nil {
		t.Fatal(err)
	}
	now, at := fixedClock()
	mk := func(id string, conds []store.RuleCondition, acts []store.RuleAction) {
		if _, err := dbs.CreateRule(ctx, id, store.RuleInput{Conditions: conds, Actions: acts, Enabled: true}, at); err != nil {
			t.Fatalf("CreateRule(%s): %v", id, err)
		}
	}
	mk("r1", []store.RuleCondition{{Field: store.RuleFieldFrom, Value: "cloudflare.com"}},
		[]store.RuleAction{{Type: store.RuleActionTag, TagID: "t1"}})
	mk("r2", []store.RuleCondition{{Field: store.RuleFieldSubject, Value: "newsletter"}},
		[]store.RuleAction{{Type: store.RuleActionReading}})
	mk("r3", []store.RuleCondition{{Field: store.RuleFieldFrom, Value: "invoices.test"}},
		[]store.RuleAction{{Type: store.RuleActionSnooze, Snooze: store.SnoozeTomorrow}})

	a := NewApplier(dbs, now, counterIDs())
	n, err := a.Evaluate(ctx, "acct-1", store.MaxRuleEvalBatch)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if n != 3 {
		t.Fatalf("evaluated %d, want 3", n)
	}

	// Two tag writes: the explicit tag and the Reading keyword.
	ops, err := dbs.OutboxByAccount(ctx, "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("%d outbox ops, want 2 (%+v)", len(ops), ops)
	}
	var sawTag, sawReading bool
	for _, op := range ops {
		if len(op.Expect.FlagsAdd) != 1 {
			t.Errorf("op %+v has %v flags", op.ID, op.Expect.FlagsAdd)
			continue
		}
		switch op.Expect.FlagsAdd[0] {
		case store.TagKeyword("receipts"):
			sawTag = true
		case store.TagKeyword(store.ReadingSlug):
			sawReading = true
		}
	}
	if !sawTag || !sawReading {
		t.Errorf("tag=%v reading=%v, want both", sawTag, sawReading)
	}

	// Match counts: r1 and r2 matched one message each, r3 none.
	if r, _ := dbs.GetRule(ctx, "r1"); r.Matches != 1 {
		t.Errorf("r1 matches = %d, want 1", r.Matches)
	}
	if r, _ := dbs.GetRule(ctx, "r2"); r.Matches != 1 {
		t.Errorf("r2 matches = %d, want 1", r.Matches)
	}
	if r, _ := dbs.GetRule(ctx, "r3"); r.Matches != 0 {
		t.Errorf("r3 matches = %d, want 0", r.Matches)
	}

	// The Reading tag exists and is reserved.
	if _, err := dbs.TagBySlug(ctx, store.ReadingSlug); err != nil {
		t.Errorf("reading tag missing: %v", err)
	}

	// A second pass evaluates nothing new and enqueues nothing more.
	n, err = a.Evaluate(ctx, "acct-1", store.MaxRuleEvalBatch)
	if err != nil {
		t.Fatalf("second Evaluate: %v", err)
	}
	if n != 0 {
		t.Errorf("second pass evaluated %d, want 0", n)
	}
	ops2, _ := dbs.OutboxByAccount(ctx, "acct-1")
	if len(ops2) != len(ops) {
		t.Errorf("second pass added outbox ops: %d -> %d", len(ops), len(ops2))
	}
}

func TestEvaluateAppliesSnoozeLocally(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := newTestDB(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "inbox-1")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m1", "ck:1", "billing@invoices.test", "Invoice 42")
	now, at := fixedClock()
	mk := func() {
		if _, err := dbs.CreateRule(ctx, "r1", store.RuleInput{
			Conditions: []store.RuleCondition{{Field: store.RuleFieldFrom, Value: "invoices.test"}},
			Actions:    []store.RuleAction{{Type: store.RuleActionSnooze, Snooze: store.SnoozeTomorrow}},
			Enabled:    true,
		}, at); err != nil {
			t.Fatal(err)
		}
	}
	mk()
	if _, err := NewApplier(dbs, now, counterIDs()).Evaluate(ctx, "acct-1", store.MaxRuleEvalBatch); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	until, ok, err := dbs.SnoozeFor(ctx, "acct-1", "ck:1", at)
	if err != nil {
		t.Fatalf("SnoozeFor: %v", err)
	}
	if !ok || !until.After(at) {
		t.Errorf("snooze = %s ok=%v, want a future time", until, ok)
	}
	// A snooze wins even if the rule is disabled later: it is already applied.
}

func TestApplyToExistingCountsAndApplies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := newTestDB(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "inbox-1")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m1", "ck:1", "a@cloudflare.com", "one")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m2", "ck:2", "b@cloudflare.com", "two")
	seedMessage(t, dbs, "acct-1", "inbox-1", "m3", "ck:3", "c@example.org", "three")

	now, at := fixedClock()
	a := NewApplier(dbs, now, counterIDs())

	// Dry run first: matches without writing.
	dry, err := a.DryRun(ctx, []store.RuleCondition{{Field: store.RuleFieldFrom, Value: "cloudflare.com"}}, []string{"acct-1"})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if dry.Matched != 2 || dry.Total != 3 {
		t.Errorf("dry run = %+v, want 2 of 3", dry)
	}
	if _, err := dbs.GetRule(ctx, "r1"); err == nil {
		t.Error("dry run created a rule")
	}
	// No rule_eval or outbox writes from a dry run.
	if ops, _ := dbs.OutboxByAccount(ctx, "acct-1"); len(ops) != 0 {
		t.Errorf("dry run wrote %d outbox ops", len(ops))
	}

	if err := dbs.UpsertTag(ctx, store.Tag{ID: "t1", Slug: "cloudflare", Name: "cloudflare", Color: "sky"}); err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.CreateRule(ctx, "r1", store.RuleInput{
		Conditions: []store.RuleCondition{{Field: store.RuleFieldFrom, Value: "cloudflare.com"}},
		Actions:    []store.RuleAction{{Type: store.RuleActionTag, TagID: "t1"}},
		Enabled:    true,
	}, at); err != nil {
		t.Fatal(err)
	}
	applied, err := a.ApplyToExisting(ctx, "r1", []string{"acct-1"})
	if err != nil {
		t.Fatalf("ApplyToExisting: %v", err)
	}
	if applied != 2 {
		t.Errorf("applied %d, want 2", applied)
	}
	ops, _ := dbs.OutboxByAccount(ctx, "acct-1")
	if len(ops) != 2 {
		t.Errorf("%d outbox ops after apply, want 2", len(ops))
	}
	if r, _ := dbs.GetRule(ctx, "r1"); r.Matches != 2 {
		t.Errorf("matches = %d, want 2", r.Matches)
	}
	// Applying again is idempotent: no new hits or ops.
	applied, err = a.ApplyToExisting(ctx, "r1", []string{"acct-1"})
	if err != nil {
		t.Fatalf("second ApplyToExisting: %v", err)
	}
	if applied != 2 {
		t.Errorf("second applied %d, want 2", applied)
	}
	if r, _ := dbs.GetRule(ctx, "r1"); r.Matches != 2 {
		t.Errorf("matches after re-apply = %d, want 2", r.Matches)
	}
	if ops, _ := dbs.OutboxByAccount(ctx, "acct-1"); len(ops) != 2 {
		t.Errorf("%d outbox ops after re-apply, want 2", len(ops))
	}
}
