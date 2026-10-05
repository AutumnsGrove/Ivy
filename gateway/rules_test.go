package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// rulesServer is a gateway with one account, one inbox and three messages that
// a rule can tell apart by sender and subject.
func rulesServer(t *testing.T) (*httptest.Server, *store.DBs) {
	t.Helper()
	n := 0
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		s.WithIDFunc(func() string { n++; return fmt.Sprintf("op-%d", n) })
		s.WithClock(func() time.Time { return testNow })
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox, UIDValidity: 1, LastSyncAt: testNow})
	msg := func(uid int, id, from, subject string) store.Message {
		return store.Message{
			ID: id, AccountID: "acct-1", FolderID: "inbox-1", UID: uint32(uid),
			ContentKey: "ck:" + id, Subject: subject, Snippet: "Snippet " + id,
			From: store.Address{Address: from}, Date: testNow,
		}
	}
	mustMessage(t, dbs, msg(1, "m1", "billing@cloudflare.com", "Receipt for your renewal"))
	mustMessage(t, dbs, msg(2, "m2", "news@wildflowers.test", "Garden Weekly #3"))
	mustMessage(t, dbs, msg(3, "m3", "friend@example.org", "Lunch?"))
	return srv, dbs
}

func TestRuleCRUDAndSentence(t *testing.T) {
	t.Parallel()
	srv, dbs := rulesServer(t)
	tag := mustTag(t, dbs, "t1", "receipts")

	body := map[string]any{
		"conditions": []map[string]any{{"field": "from", "value": "cloudflare.com"}},
		"actions":    []map[string]any{{"type": "tag", "tagId": tag.ID}},
		"enabled":    true,
	}
	var created api.Rule
	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/rules", body, &created); code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", code)
	}
	if created.Id == "" || !created.On || created.Matches != 0 {
		t.Errorf("created = %+v", created)
	}
	if created.When != "mail is from" || created.WhenToken == nil || *created.WhenToken != "cloudflare.com" {
		t.Errorf("when = %q/%v, want mail is from/cloudflare.com", created.When, created.WhenToken)
	}
	if created.Then != "tag it" || created.ThenToken != "receipts" || created.ThenColor == nil || *created.ThenColor != api.Sky {
		t.Errorf("then = %q/%q/%v, want tag it/receipts/sky", created.Then, created.ThenToken, created.ThenColor)
	}
	if len(created.Conditions) != 1 || created.Conditions[0].Field != api.RuleConditionFieldFrom || len(created.Actions) != 1 || created.Actions[0].Type != api.Tag {
		t.Errorf("structured = %+v / %+v", created.Conditions, created.Actions)
	}

	var list []api.Rule
	if code := getJSON(t, srv.URL+"/api/v1/rules", &list); code != http.StatusOK || len(list) != 1 || list[0].Id != created.Id {
		t.Fatalf("list = %d %+v, want one rule", code, list)
	}

	// The toggle changes only `enabled`.
	var toggled api.Rule
	if code := sendJSON(t, http.MethodPatch, srv.URL+"/api/v1/rules/"+created.Id, map[string]any{"enabled": false}, &toggled); code != http.StatusOK || toggled.On {
		t.Errorf("toggle = %d on=%v, want 200 and off", code, toggled.On)
	}

	// A PUT replaces conditions and actions.
	updated := map[string]any{
		"conditions": []map[string]any{{"field": "subject", "value": "Garden Weekly"}},
		"actions":    []map[string]any{{"type": "reading"}},
		"enabled":    true,
	}
	var after api.Rule
	if code := sendJSON(t, http.MethodPut, srv.URL+"/api/v1/rules/"+created.Id, updated, &after); code != http.StatusOK {
		t.Fatalf("update status = %d, want 200", code)
	}
	if after.Then != "show it in" || after.ThenToken != "Reading" {
		t.Errorf("after update then = %q/%q, want show it in/Reading", after.Then, after.ThenToken)
	}
	if after.When != "mail is about" {
		t.Errorf("after update when = %q, want mail is about", after.When)
	}

	if code := sendJSON(t, http.MethodDelete, srv.URL+"/api/v1/rules/"+created.Id, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", code)
	}
	if code := getJSON(t, srv.URL+"/api/v1/rules/"+created.Id, nil); code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", code)
	}
}

func TestCreateRuleRejectsBadVocabulary(t *testing.T) {
	t.Parallel()
	srv, _ := rulesServer(t)
	cases := []map[string]any{
		{"conditions": []map[string]any{{"field": "body", "value": "x"}}, "actions": []map[string]any{{"type": "reading"}}},
		{"conditions": []map[string]any{{"field": "from", "value": "x"}}, "actions": []map[string]any{{"type": "move"}}},
		{"conditions": []map[string]any{{"field": "from", "value": "x"}}, "actions": []map[string]any{{"type": "tag"}}},
	}
	for i, body := range cases {
		if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/rules", body, nil); code != http.StatusBadRequest {
			t.Errorf("case %d status = %d, want 400", i, code)
		}
	}
}

func TestDryRunAndApply(t *testing.T) {
	t.Parallel()
	srv, dbs := rulesServer(t)
	tag := mustTag(t, dbs, "t1", "cloudflare")

	// Dry run: two of the three messages are from cloudflare, and nothing is
	// written.
	conditions := []map[string]any{{"field": "from", "value": "cloudflare.com"}}
	var dry api.RuleDryRunResult
	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/rules/dry-run", map[string]any{"conditions": conditions}, &dry); code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want 200", code)
	}
	if dry.Matched != 1 || dry.Total != 3 {
		t.Errorf("dry run = %+v, want 1 of 3", dry)
	}
	if n := countRules(t, dbs); n != 0 {
		t.Errorf("dry run created %d rules", n)
	}
	if ops, _ := dbs.OutboxByAccount(context.Background(), "acct-1"); len(ops) != 0 {
		t.Errorf("dry run queued %d ops", len(ops))
	}

	body := map[string]any{
		"conditions": conditions,
		"actions":    []map[string]any{{"type": "tag", "tagId": tag.ID}},
		"enabled":    true,
	}
	var created api.Rule
	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/rules", body, &created); code != http.StatusCreated {
		t.Fatalf("create status = %d", code)
	}

	var applied api.RuleApplyResult
	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/rules/"+created.Id+"/apply", nil, &applied); code != http.StatusOK {
		t.Fatalf("apply status = %d, want 200", code)
	}
	if applied.Applied != 1 {
		t.Errorf("applied = %d, want 1", applied.Applied)
	}
	ops, _ := dbs.OutboxByAccount(context.Background(), "acct-1")
	if len(ops) != 1 {
		t.Errorf("apply queued %d ops, want 1", len(ops))
	}
	if r, _ := dbs.GetRule(context.Background(), created.Id); r.Matches != 1 {
		t.Errorf("matches = %d, want 1", r.Matches)
	}

	// A second apply is idempotent.
	applied = api.RuleApplyResult{}
	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/rules/"+created.Id+"/apply", nil, &applied); code != http.StatusOK || applied.Applied != 1 {
		t.Errorf("second apply = %d %+v, want 200 and 1", code, applied)
	}
	if ops, _ := dbs.OutboxByAccount(context.Background(), "acct-1"); len(ops) != 1 {
		t.Errorf("second apply queued %d ops, want still 1", len(ops))
	}
}

func TestActiveRulesCountAndReservedTag(t *testing.T) {
	t.Parallel()
	srv, dbs := rulesServer(t)
	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/rules", map[string]any{
		"conditions": []map[string]any{{"field": "from", "value": "x"}},
		"actions":    []map[string]any{{"type": "reading"}},
		"enabled":    true,
	}, nil); code != http.StatusCreated {
		t.Fatalf("create status = %d", code)
	}
	var overview api.TagsOverview
	if code := getJSON(t, srv.URL+"/api/v1/tags", &overview); code != http.StatusOK || overview.ActiveRules != 1 {
		t.Errorf("activeRules = %d, want 1", overview.ActiveRules)
	}

	reading, err := dbs.EnsureReadingTag(context.Background())
	if err != nil {
		t.Fatalf("EnsureReadingTag: %v", err)
	}
	if code := sendJSON(t, http.MethodDelete, srv.URL+"/api/v1/tags/"+reading.ID, nil, nil); code != http.StatusConflict {
		t.Errorf("delete reserved tag = %d, want 409", code)
	}
}

func countRules(t *testing.T, dbs *store.DBs) int {
	t.Helper()
	rules, err := dbs.ListRules(context.Background())
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	return len(rules)
}
