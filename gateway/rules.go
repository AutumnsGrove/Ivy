package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/rules"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxRuleBodyBytes bounds a rule request: at most 16 conditions and 8 actions of
// at most 256 bytes, so a hostile body cannot grow the parse.
const maxRuleBodyBytes = 64 << 10

// snoozeLabels word the snooze presets for the rule sentence.
var snoozeLabels = map[string]string{
	store.SnoozeLaterToday: "later today",
	store.SnoozeTomorrow:   "tomorrow",
	store.SnoozeWeekend:    "the weekend",
	store.SnoozeNextWeek:   "next week",
}

// handleListRules serves every rule, rendered as a plain sentence and with its
// structured conditions and actions so the editor can open it.
func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	list, err := s.dbs.ListRules(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tags, err := s.tagByID(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := make([]api.Rule, 0, len(list))
	for _, rule := range list {
		out = append(out, ruleView(rule, tags))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetRule(w http.ResponseWriter, r *http.Request) {
	rule, err := s.dbs.GetRule(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "rule")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tags, err := s.tagByID(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ruleView(rule, tags))
}

// handleCreateRule validates and stores a rule. Nothing runs until it is
// enabled; the review screen creates it already on.
func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeRuleBody(w, r)
	if !ok {
		return
	}
	rule, err := s.dbs.CreateRule(r.Context(), s.newID(), ruleInput(body), s.now())
	switch {
	case errors.Is(err, store.ErrRuleCondition), errors.Is(err, store.ErrRuleAction):
		writeError(w, http.StatusBadRequest, "bad_rule", "That rule is not one Ivy can run")
		return
	case errors.Is(err, store.ErrRuleLimit):
		writeError(w, http.StatusConflict, "too_many_rules", "There are too many rules; delete one first")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.writeRule(w, r, http.StatusCreated, rule)
}

func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeRuleBody(w, r)
	if !ok {
		return
	}
	rule, err := s.dbs.UpdateRule(r.Context(), r.PathValue("id"), ruleInput(body), s.now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "rule")
		return
	case errors.Is(err, store.ErrRuleCondition), errors.Is(err, store.ErrRuleAction):
		writeError(w, http.StatusBadRequest, "bad_rule", "That rule is not one Ivy can run")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.writeRule(w, r, http.StatusOK, rule)
}

// handleSetRuleEnabled is the list screen's toggle: it changes only `enabled`.
func (s *Server) handleSetRuleEnabled(w http.ResponseWriter, r *http.Request) {
	var body api.RuleToggle
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRuleBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That is not a valid request")
		return
	}
	id := r.PathValue("id")
	if err := s.dbs.SetRuleEnabled(r.Context(), id, body.Enabled, s.now()); errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "rule")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	rule, err := s.dbs.GetRule(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.writeRule(w, r, http.StatusOK, rule)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if err := s.dbs.DeleteRule(r.Context(), r.PathValue("id")); errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "rule")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDryRunRule is the free local preview: it counts over the most recent
// messages and writes nothing, not even a rule.
func (s *Server) handleDryRunRule(w http.ResponseWriter, r *http.Request) {
	var body api.RuleDryRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRuleBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That is not a valid request")
		return
	}
	conditions := storeConditions(body.Conditions)
	if err := store.ValidateConditions(conditions); err != nil {
		writeError(w, http.StatusBadRequest, "bad_rule", "That rule is not one Ivy can run")
		return
	}
	accounts, err := s.ruleAccounts(r.Context(), body.AccountIds)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	result, err := rules.NewApplier(s.dbs, s.now, s.newID).DryRun(r.Context(), conditions, accounts)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := api.RuleDryRunResult{Matched: result.Matched, Total: result.Total}
	sample := make([]api.MailSummary, 0, len(result.Sample))
	for _, m := range result.Sample {
		sample = append(sample, ruleSampleView(m))
	}
	out.Sample = &sample
	writeJSON(w, http.StatusOK, out)
}

// handleApplyRule applies one rule to the mail already mirrored, the deliberate
// way for a new rule to reach old mail.
func (s *Server) handleApplyRule(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.ruleAccounts(r.Context(), nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	applied, err := rules.NewApplier(s.dbs, s.now, s.newID).ApplyToExisting(r.Context(), r.PathValue("id"), accounts)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "rule")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.RuleApplyResult{Applied: applied})
}

// ruleAccounts resolves the accounts a dry run or apply covers: the requested
// ones when given, else every configured account.
func (s *Server) ruleAccounts(ctx context.Context, requested *[]string) ([]string, error) {
	if requested != nil && len(*requested) > 0 {
		return *requested, nil
	}
	accounts, err := s.dbs.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, a.ID)
	}
	return out, nil
}

// writeRule renders a rule with the sentence the list and detail screens show.
func (s *Server) writeRule(w http.ResponseWriter, r *http.Request, status int, rule store.Rule) {
	tags, err := s.tagByID(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, status, ruleView(rule, tags))
}

// tagByID reads every tag once, so a rule list resolves names and colours with
// one query rather than one per action.
func (s *Server) tagByID(ctx context.Context) (map[string]store.Tag, error) {
	tags, err := s.dbs.ListTags(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.Tag, len(tags))
	for _, t := range tags {
		out[t.ID] = t.Tag
	}
	return out, nil
}

func decodeRuleBody(w http.ResponseWriter, r *http.Request) (api.RuleInput, bool) {
	var body api.RuleInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRuleBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That rule is not valid")
		return api.RuleInput{}, false
	}
	return body, true
}

// ruleInput converts the contract's rule to the store's, defaulting a rule with
// no `enabled` to off: nothing runs until it is turned on.
func ruleInput(body api.RuleInput) store.RuleInput {
	in := store.RuleInput{
		Conditions: storeConditions(body.Conditions),
		Actions:    storeActions(body.Actions),
		Enabled:    body.Enabled != nil && *body.Enabled,
	}
	if body.AccountId != nil {
		in.AccountID = *body.AccountId
	}
	return in
}

func storeConditions(in []api.RuleCondition) []store.RuleCondition {
	out := make([]store.RuleCondition, 0, len(in))
	for _, c := range in {
		out = append(out, store.RuleCondition{Field: string(c.Field), Value: c.Value})
	}
	return out
}

func storeActions(in []api.RuleAction) []store.RuleAction {
	out := make([]store.RuleAction, 0, len(in))
	for _, a := range in {
		action := store.RuleAction{Type: string(a.Type)}
		if a.TagId != nil {
			action.TagID = *a.TagId
		}
		if a.Snooze != nil {
			action.Snooze = string(*a.Snooze)
		}
		out = append(out, action)
	}
	return out
}

// ruleView renders a stored rule: the structured half is what the editor edits,
// the sentence half is what the list shows. They are derived from the same
// conditions and actions, so they cannot disagree.
func ruleView(r store.Rule, tags map[string]store.Tag) api.Rule {
	when, whenToken, whenTail := whenSentence(r.Conditions)
	then, thenToken, thenColor := thenSentence(r.Actions, tags)
	out := api.Rule{
		Id:         r.ID,
		AccountId:  r.AccountID,
		Conditions: make([]api.RuleCondition, 0, len(r.Conditions)),
		Actions:    make([]api.RuleAction, 0, len(r.Actions)),
		When:       when,
		Then:       then,
		ThenToken:  thenToken,
		Matches:    r.Matches,
		On:         r.Enabled,
	}
	if whenToken != "" {
		out.WhenToken = &whenToken
	}
	if whenTail != "" {
		out.WhenTail = &whenTail
	}
	if thenColor != "" {
		color := api.TagColor(thenColor)
		out.ThenColor = &color
	}
	for _, c := range r.Conditions {
		out.Conditions = append(out.Conditions, api.RuleCondition{Field: api.RuleConditionField(c.Field), Value: c.Value})
	}
	for _, a := range r.Actions {
		action := api.RuleAction{Type: api.RuleActionType(a.Type)}
		if a.TagID != "" {
			tagID := a.TagID
			action.TagId = &tagID
		}
		if a.Snooze != "" {
			preset := api.RuleActionSnooze(a.Snooze)
			action.Snooze = &preset
		}
		out.Actions = append(out.Actions, action)
	}
	return out
}

// whenSentence turns conditions into the list's "When mail is from X and …".
func whenSentence(conds []store.RuleCondition) (lead, token, tail string) {
	if len(conds) == 0 {
		return "", "", ""
	}
	leads := make([]string, len(conds))
	tokens := make([]string, len(conds))
	for i, c := range conds {
		leads[i], tokens[i] = conditionPhrase(c)
	}
	lead, token = leads[0], tokens[0]
	rest := make([]string, 0, len(conds)-1)
	for i := 1; i < len(leads); i++ {
		if tokens[i] != "" {
			rest = append(rest, fmt.Sprintf("%s \"%s\"", leads[i], tokens[i]))
		} else {
			rest = append(rest, leads[i])
		}
	}
	if len(rest) > 0 {
		tail = " and " + strings.Join(rest, " and ")
	}
	return lead, token, tail
}

func conditionPhrase(c store.RuleCondition) (lead, token string) {
	switch c.Field {
	case store.RuleFieldFrom:
		return "mail is from", c.Value
	case store.RuleFieldSubject:
		return "mail is about", c.Value
	case store.RuleFieldAccount:
		return "mail is in", c.Value
	case store.RuleFieldHasAttachment:
		if c.Value == "true" {
			return "mail has an attachment", ""
		}
		return "mail has no attachment", ""
	}
	return "mail matches", c.Value
}

// thenSentence turns the first action into the list's "Then …". Extra actions
// are not lost: the detail editor shows all of them.
func thenSentence(actions []store.RuleAction, tags map[string]store.Tag) (then, token, color string) {
	if len(actions) == 0 {
		return "", "", ""
	}
	switch actions[0].Type {
	case store.RuleActionTag:
		then = "tag it"
		if t, ok := tags[actions[0].TagID]; ok {
			token, color = t.Name, t.Color
		}
	case store.RuleActionReading:
		then, token = "show it in", store.ReadingTagName
		if t, ok := tags[store.ReadingTagID]; ok {
			token = t.Name
			color = t.Color
		}
		if color == "" {
			color = "lilac"
		}
	case store.RuleActionSnooze:
		then = "snooze it"
		token = snoozeLabels[actions[0].Snooze]
	}
	return then, token, color
}

// ruleSampleView renders one dry-run match as a list row.
func ruleSampleView(m store.RuleMessage) api.MailSummary {
	from := m.FromName
	if from == "" {
		from = m.From
	}
	return api.MailSummary{
		Id:        m.ID,
		AccountId: m.AccountID,
		From:      from,
		Initials:  initials(m.FromName, m.From),
		Date:      m.Date.UTC(),
		Subject:   m.Subject,
		Preview:   m.Snippet,
	}
}
