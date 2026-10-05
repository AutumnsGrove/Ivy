package rules

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
)

// applyBatch bounds how many existing messages one on-demand apply reads, and
// sampleSize how many matched messages the dry-run preview returns.
const (
	applyBatch = store.MaxRuleApply
	sampleSize = 20
)

// Applier runs rules and their local actions against the store. It is the one
// place a rule reaches the outbox or the snooze table, so the ingest pass, the
// on-demand apply and the dev seed all behave the same. now and newID are
// injected so tests are deterministic.
type Applier struct {
	dbs   *store.DBs
	now   func() time.Time
	newID func() string
}

// NewApplier builds an applier over one pair of databases.
func NewApplier(dbs *store.DBs, now func() time.Time, newID func() string) *Applier {
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = func() string { return "" }
	}
	return &Applier{dbs: dbs, now: now, newID: newID}
}

// DryRun is the free local preview of a rule: how many of the most recent
// messages a condition set would match, and a sample for the review screen.
type DryRun struct {
	Matched int
	Total   int
	Sample  []store.RuleMessage
}

// Evaluate runs the ingest pass for one account: every enabled rule scoped to
// it is matched against the messages the pass has not seen yet, the hits are
// recorded, the local actions applied, and the messages marked evaluated. It
// returns how many messages were evaluated. A rule created after mail arrived
// therefore does not reach back; ApplyToExisting is the deliberate way to.
func (a *Applier) Evaluate(ctx context.Context, accountID string, limit int) (int, error) {
	if limit <= 0 || limit > store.MaxRuleEvalBatch {
		limit = store.MaxRuleEvalBatch
	}
	msgs, err := a.dbs.RuleMessages(ctx, accountID, limit, true)
	if err != nil {
		return 0, err
	}
	if len(msgs) == 0 {
		return 0, nil
	}
	all, err := a.dbs.ListRules(ctx)
	if err != nil {
		return 0, err
	}
	now := a.now()
	for _, rule := range scopedRules(all, accountID) {
		var hits []store.RuleHit
		for _, m := range msgs {
			if !Match(rule.Conditions, m) {
				continue
			}
			hits = append(hits, store.RuleHit{AccountID: accountID, ContentKey: m.ContentKey})
			if err := a.applyActions(ctx, accountID, rule, m); err != nil {
				return 0, err
			}
		}
		if err := a.dbs.RecordRuleHits(ctx, rule.ID, hits, now); err != nil {
			return 0, err
		}
	}
	keys := make([]string, len(msgs))
	for i, m := range msgs {
		keys[i] = m.ContentKey
	}
	if err := a.dbs.MarkRuleEvaluated(ctx, accountID, keys); err != nil {
		return 0, err
	}
	return len(msgs), nil
}

// ApplyToExisting applies one rule to the mail already mirrored, accounting the
// matches and applying the local actions, and returns how many messages it
// matched. It is idempotent: re-running changes neither the count nor the mail.
func (a *Applier) ApplyToExisting(ctx context.Context, ruleID string, accountIDs []string) (int, error) {
	rule, err := a.dbs.GetRule(ctx, ruleID)
	if err != nil {
		return 0, err
	}
	accounts := accountIDs
	if rule.AccountID != "" {
		accounts = []string{rule.AccountID}
	}
	now := a.now()
	applied := 0
	for _, accountID := range accounts {
		msgs, err := a.dbs.RuleMessages(ctx, accountID, applyBatch, false)
		if err != nil {
			return applied, err
		}
		var hits []store.RuleHit
		for _, m := range msgs {
			if !Match(rule.Conditions, m) {
				continue
			}
			hits = append(hits, store.RuleHit{AccountID: accountID, ContentKey: m.ContentKey})
			if err := a.applyActions(ctx, accountID, rule, m); err != nil {
				return applied, err
			}
			applied++
		}
		if err := a.dbs.RecordRuleHits(ctx, rule.ID, hits, now); err != nil {
			return applied, err
		}
	}
	return applied, nil
}

// DryRun counts matches over the most recent messages, writing nothing.
func (a *Applier) DryRun(ctx context.Context, conditions []store.RuleCondition, accountIDs []string) (DryRun, error) {
	var out DryRun
	for _, accountID := range accountIDs {
		msgs, err := a.dbs.RuleMessages(ctx, accountID, store.MaxRuleDryRun, false)
		if err != nil {
			return DryRun{}, err
		}
		out.Total += len(msgs)
		for _, m := range msgs {
			if !Match(conditions, m) {
				continue
			}
			out.Matched++
			if len(out.Sample) < sampleSize {
				out.Sample = append(out.Sample, m)
			}
		}
	}
	return out, nil
}

// applyActions runs a matched rule's local-only actions. A tag is a keyword
// write through the outbox, so it obeys "writes go to IMAP first"; Reading is
// the same tag mechanism; a snooze is local state.
func (a *Applier) applyActions(ctx context.Context, accountID string, rule store.Rule, m store.RuleMessage) error {
	for _, action := range rule.Actions {
		switch action.Type {
		case store.RuleActionTag:
			if err := a.addKeyword(ctx, accountID, m, action.TagID); err != nil {
				return err
			}
		case store.RuleActionReading:
			tag, err := a.dbs.EnsureReadingTag(ctx)
			if err != nil {
				return err
			}
			if err := a.addKeyword(ctx, accountID, m, tag.ID); err != nil {
				return err
			}
		case store.RuleActionSnooze:
			until := SnoozeUntil(action.Snooze, a.now())
			if err := a.dbs.SnoozeMessage(ctx, accountID, m.ContentKey, until, a.now()); err != nil {
				return err
			}
		}
	}
	return nil
}

// addKeyword queues the tag's IMAP keyword for one message. A tag that was
// deleted while the rule waited is skipped; a full outbox is logged, not fatal,
// because an ingest pass must never fail the sync over a label.
func (a *Applier) addKeyword(ctx context.Context, accountID string, m store.RuleMessage, tagID string) error {
	tag, err := a.dbs.GetTag(ctx, tagID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, _, err = a.dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: a.newID(), AccountID: accountID, Kind: store.OutboxFlags,
		ContentKey: m.ContentKey, SourceFolderID: m.FolderID,
		Expect:    store.OutboxExpect{FlagsAdd: []string{store.TagKeyword(tag.Slug)}},
		CreatedAt: a.now(),
	})
	if errors.Is(err, store.ErrOutboxFull) {
		slog.WarnContext(ctx, "rules: the outbox is full; a rule's tag was not queued",
			"account", accountID, "tag", tag.Slug)
		return nil
	}
	return err
}

// scopedRules selects the enabled rules that apply to one account: a rule with
// no account is global, one with an account id runs only there.
func scopedRules(all []store.Rule, accountID string) []store.Rule {
	var out []store.Rule
	for _, r := range all {
		if r.Enabled && (r.AccountID == "" || r.AccountID == accountID) {
			out = append(out, r)
		}
	}
	return out
}
