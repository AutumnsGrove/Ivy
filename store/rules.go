package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Rule limits (STANDARDS.md 4a). Conditions are matched against sender
// controlled headers, so every value is bounded and no engine but substring
// matching is offered.
const (
	// MaxRules is how many rules may exist; creating one more is ErrRuleLimit.
	MaxRules = 100
	// MaxRuleConditions is how many conditions one rule may carry.
	MaxRuleConditions = 16
	// MaxRuleActions is how many actions one rule may carry.
	MaxRuleActions = 8
	// MaxRuleValueLen is the longest condition value in bytes.
	MaxRuleValueLen = 256
	// MaxRuleEvalBatch bounds how many messages one ingest pass evaluates, so a
	// first sync over a large mailbox stays in small batches.
	MaxRuleEvalBatch = 200
	// MaxRuleDryRun is how many recent messages a dry run reads.
	MaxRuleDryRun = 200
)

// Rule condition fields. This is the closed vocabulary the store validates
// against (JEV.md 3F); fuzzy `check` conditions arrive with Jev in chunk 5.
const (
	RuleFieldFrom          = "from"
	RuleFieldSubject       = "subject"
	RuleFieldAccount       = "account"
	RuleFieldHasAttachment = "has_attachment"
)

// Rule action types. Every action is local-only (PLAN.md round 19): a tag or
// Reading membership is written through the outbox, a snooze is local state.
const (
	RuleActionTag     = "tag"
	RuleActionReading = "reading"
	RuleActionSnooze  = "snooze"
)

// Snooze presets (JEV.md 3E). The concrete time is resolved when the snooze is
// applied, in the operator's local time.
const (
	SnoozeLaterToday = "later_today"
	SnoozeTomorrow   = "tomorrow"
	SnoozeWeekend    = "weekend"
	SnoozeNextWeek   = "next_week"
)

var (
	// ErrRuleCondition reports a condition outside the vocabulary, or a bad value.
	ErrRuleCondition = errors.New("bad rule condition")
	// ErrRuleAction reports an action outside the vocabulary, or a missing field.
	ErrRuleAction = errors.New("bad rule action")
	// ErrRuleLimit reports a rule created past MaxRules.
	ErrRuleLimit = errors.New("too many rules")
)

// ValidSnoozePreset reports whether a snooze action names a known preset.
func ValidSnoozePreset(preset string) bool {
	switch preset {
	case SnoozeLaterToday, SnoozeTomorrow, SnoozeWeekend, SnoozeNextWeek:
		return true
	}
	return false
}

// RuleCondition is one header matcher. Value is a case-insensitive substring
// for `from` and `subject`, an account id for `account`, and "true"/"false" for
// `has_attachment`.
type RuleCondition struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// RuleAction is one local-only consequence of a match.
type RuleAction struct {
	Type   string `json:"type"`
	TagID  string `json:"tagId,omitempty"`
	Snooze string `json:"snooze,omitempty"`
}

// RuleInput is a rule as the operator writes it, before validation and storage.
type RuleInput struct {
	AccountID  string
	Conditions []RuleCondition
	Actions    []RuleAction
	Enabled    bool
}

// Rule is a stored rule plus its lifetime match count.
type Rule struct {
	ID         string
	AccountID  string
	Conditions []RuleCondition
	Actions    []RuleAction
	Enabled    bool
	Matches    int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// RuleHit records that one rule matched one message.
type RuleHit struct {
	AccountID  string
	ContentKey string
}

// RuleMessage is the header projection the rule pass evaluates. It carries no
// body, so a pass over a large mailbox stays small.
type RuleMessage struct {
	ContentKey    string
	AccountID     string
	FolderID      string
	From          string
	FromName      string
	Subject       string
	HasAttachment bool
}

// validateRuleInput checks a rule against the closed vocabulary. It is the only
// place conditions and actions are accepted, so both the ingest pass and the API
// can trust a stored rule.
func validateRuleInput(in RuleInput) error {
	if len(in.Conditions) == 0 || len(in.Conditions) > MaxRuleConditions {
		return fmt.Errorf("%w: %d conditions", ErrRuleCondition, len(in.Conditions))
	}
	if len(in.Actions) == 0 || len(in.Actions) > MaxRuleActions {
		return fmt.Errorf("%w: %d actions", ErrRuleAction, len(in.Actions))
	}
	for _, c := range in.Conditions {
		switch c.Field {
		case RuleFieldFrom, RuleFieldSubject, RuleFieldAccount:
			if !validRuleValue(c.Value) {
				return fmt.Errorf("%w: %s value", ErrRuleCondition, c.Field)
			}
		case RuleFieldHasAttachment:
			if c.Value != "true" && c.Value != "false" {
				return fmt.Errorf("%w: has_attachment value", ErrRuleCondition)
			}
		default:
			return fmt.Errorf("%w: field %q", ErrRuleCondition, c.Field)
		}
	}
	for _, a := range in.Actions {
		switch a.Type {
		case RuleActionTag:
			if a.TagID == "" {
				return fmt.Errorf("%w: tag without a tag id", ErrRuleAction)
			}
		case RuleActionReading:
		case RuleActionSnooze:
			if !ValidSnoozePreset(a.Snooze) {
				return fmt.Errorf("%w: unknown snooze preset", ErrRuleAction)
			}
		default:
			return fmt.Errorf("%w: type %q", ErrRuleAction, a.Type)
		}
	}
	return nil
}

func validRuleValue(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && utf8.ValidString(v) && len(v) <= MaxRuleValueLen
}

// CreateRule validates and stores a new rule.
func (d *DBs) CreateRule(ctx context.Context, id string, in RuleInput, now time.Time) (Rule, error) {
	if err := validateRuleInput(in); err != nil {
		return Rule{}, err
	}
	var count int
	if err := d.State.Read.QueryRowContext(ctx, `SELECT count(*) FROM rules`).Scan(&count); err != nil {
		return Rule{}, fmt.Errorf("create rule: count: %w", err)
	}
	if count >= MaxRules {
		return Rule{}, ErrRuleLimit
	}
	conditions, actions, err := marshalRuleParts(in)
	if err != nil {
		return Rule{}, err
	}
	enabled := boolInt(in.Enabled)
	_, err = d.State.Write.ExecContext(ctx,
		`INSERT INTO rules (id, account_id, conditions_json, actions_json, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, in.AccountID, conditions, actions, enabled, formatTime(now), formatTime(now))
	if err != nil {
		return Rule{}, fmt.Errorf("create rule: %w", err)
	}
	return d.GetRule(ctx, id)
}

// UpdateRule replaces a rule's conditions, actions, scope and enabled flag. Its
// hits and match count are kept.
func (d *DBs) UpdateRule(ctx context.Context, id string, in RuleInput, now time.Time) (Rule, error) {
	if err := validateRuleInput(in); err != nil {
		return Rule{}, err
	}
	conditions, actions, err := marshalRuleParts(in)
	if err != nil {
		return Rule{}, err
	}
	res, err := d.State.Write.ExecContext(ctx,
		`UPDATE rules SET account_id = ?, conditions_json = ?, actions_json = ?, enabled = ?, updated_at = ?
		 WHERE id = ?`,
		in.AccountID, conditions, actions, boolInt(in.Enabled), formatTime(now), id)
	if err != nil {
		return Rule{}, fmt.Errorf("update rule %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Rule{}, ErrNotFound
	}
	return d.GetRule(ctx, id)
}

// SetRuleEnabled turns a rule on or off without touching its conditions.
func (d *DBs) SetRuleEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	res, err := d.State.Write.ExecContext(ctx,
		`UPDATE rules SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), formatTime(now), id)
	if err != nil {
		return fmt.Errorf("set rule %s enabled: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetRule reads one rule with its match count.
func (d *DBs) GetRule(ctx context.Context, id string) (Rule, error) {
	row := d.State.Read.QueryRowContext(ctx, ruleSelect+` WHERE r.id = ?`, id)
	rule, err := scanRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	if err != nil {
		return Rule{}, fmt.Errorf("get rule %s: %w", id, err)
	}
	return rule, nil
}

// ListRules returns every rule, newest first.
func (d *DBs) ListRules(ctx context.Context) ([]Rule, error) {
	rows, err := d.State.Read.QueryContext(ctx, ruleSelect+` ORDER BY r.created_at DESC, r.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Rule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("list rules: %w", err)
		}
		out = append(out, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	return out, nil
}

// DeleteRule removes a rule and its hits. Rules carry no mail, so this erases
// nothing on the server.
func (d *DBs) DeleteRule(ctx context.Context, id string) error {
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM rule_hits WHERE rule_id = ?`, id); err != nil {
		return fmt.Errorf("delete rule hits: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// RecordRuleHits records idempotently that each rule matched each message, so a
// re-evaluation never inflates a match count.
func (d *DBs) RecordRuleHits(ctx context.Context, ruleID string, hits []RuleHit, now time.Time) error {
	if len(hits) == 0 {
		return nil
	}
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record rule hits: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, h := range hits {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO rule_hits (rule_id, account_id, content_key, matched_at) VALUES (?, ?, ?, ?)`,
			ruleID, h.AccountID, h.ContentKey, formatTime(now)); err != nil {
			return fmt.Errorf("record rule hit: %w", err)
		}
	}
	return tx.Commit()
}

// MarkRuleEvaluated records that the ingest pass has run every enabled rule over
// a message, so a later pass skips it. It is a rebuildable mirror cache, not
// backed-up state, and carries no timestamp so it stays reproducible.
func (d *DBs) MarkRuleEvaluated(ctx context.Context, accountID string, contentKeys []string) error {
	if len(contentKeys) == 0 {
		return nil
	}
	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mark rule evaluated: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, key := range contentKeys {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO rule_eval (account_id, content_key) VALUES (?, ?)`,
			accountID, key); err != nil {
			return fmt.Errorf("mark rule evaluated: %w", err)
		}
	}
	return tx.Commit()
}

// RuleMessages returns the newest `limit` visible candidates for one account,
// one row per content key (N8 means a message can sit in two folders). When
// onlyUnevaluated is true it skips messages the ingest pass already ran over.
func (d *DBs) RuleMessages(ctx context.Context, accountID string, limit int, onlyUnevaluated bool) ([]RuleMessage, error) {
	if limit <= 0 || limit > MaxRuleDryRun {
		limit = MaxRuleDryRun
	}
	only := 0
	if onlyUnevaluated {
		only = 1
	}
	rows, err := d.Mirror.Read.QueryContext(ctx, ruleMessageQuery, accountID, only, limit)
	if err != nil {
		return nil, fmt.Errorf("rule messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []RuleMessage
	for rows.Next() {
		var (
			m      RuleMessage
			from   string
			date   string
			hasAtt bool
		)
		if err := rows.Scan(&m.ContentKey, &m.AccountID, &m.FolderID, &from, &m.Subject, &hasAtt, &date); err != nil {
			return nil, fmt.Errorf("rule messages: %w", err)
		}
		m.HasAttachment = hasAtt
		if from != "" {
			var addr Address
			if err := json.Unmarshal([]byte(from), &addr); err == nil {
				m.From, m.FromName = addr.Address, addr.Name
			}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rule messages: %w", err)
	}
	return out, nil
}

// ruleSelect counts hits in the same query so the list and the reader agree on
// "matched N times".
const ruleSelect = `
	SELECT r.id, r.account_id, r.conditions_json, r.actions_json, r.enabled,
	       r.created_at, r.updated_at,
	       (SELECT count(*) FROM rule_hits h WHERE h.rule_id = r.id)
	FROM rules r`

const ruleMessageQuery = `
	SELECT m.content_key, m.account_id, m.folder_id, COALESCE(m.from_json, ''),
	       COALESCE(m.subject, ''), m.has_attachments, COALESCE(m.date, '')
	FROM messages m
	WHERE m.account_id = ?
	  AND m.disabled_at IS NULL
	  AND (? = 0 OR NOT EXISTS (
	        SELECT 1 FROM rule_eval e
	        WHERE e.account_id = m.account_id AND e.content_key = m.content_key))
	GROUP BY m.content_key
	HAVING m.date = MAX(m.date)
	ORDER BY m.date DESC, m.id DESC
	LIMIT ?`

func marshalRuleParts(in RuleInput) (conditions, actions string, err error) {
	c, err := json.Marshal(in.Conditions)
	if err != nil {
		return "", "", fmt.Errorf("marshal rule conditions: %w", err)
	}
	a, err := json.Marshal(in.Actions)
	if err != nil {
		return "", "", fmt.Errorf("marshal rule actions: %w", err)
	}
	return string(c), string(a), nil
}

func scanRule(s scanner) (Rule, error) {
	var (
		r          Rule
		conditions string
		actions    string
		enabled    bool
		created    string
		updated    string
	)
	if err := s.Scan(&r.ID, &r.AccountID, &conditions, &actions, &enabled, &created, &updated, &r.Matches); err != nil {
		return Rule{}, err
	}
	if err := json.Unmarshal([]byte(conditions), &r.Conditions); err != nil {
		return Rule{}, fmt.Errorf("decode rule conditions: %w", err)
	}
	if err := json.Unmarshal([]byte(actions), &r.Actions); err != nil {
		return Rule{}, fmt.Errorf("decode rule actions: %w", err)
	}
	r.Enabled = enabled
	var err error
	if r.CreatedAt, err = parseTime(created); err != nil {
		return Rule{}, err
	}
	if r.UpdatedAt, err = parseTime(updated); err != nil {
		return Rule{}, err
	}
	return r, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
