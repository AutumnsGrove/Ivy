package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/insight"
	"github.com/AutumnsGrove/Ivy/store"
)

type fakeInsight struct {
	res     insight.Result
	err     error
	account string
	message string
	calls   int
}

func (f *fakeInsight) ForMessage(_ context.Context, account, id string) (insight.Result, error) {
	f.calls++
	f.account, f.message = account, id
	return f.res, f.err
}

func insightServer(t *testing.T, src InsightSource) string {
	t.Helper()
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		if src != nil {
			s.WithInsight(src)
		}
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, inboxMessage("m1", "acct-1", "inbox-1", testNow, false))
	return srv.URL + "/api/v1"
}

func TestAnInsightIsServedForTheMessagesAccount(t *testing.T) {
	t.Parallel()
	src := &fakeInsight{res: insight.Result{Summary: "Thursday is settled.", Earlier: 3}}
	base := insightServer(t, src)

	var got api.MessageInsight
	if code := getJSON(t, base+"/messages/m1/insight", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if src.account != "acct-1" || src.message != "m1" {
		t.Fatalf("asked for %q/%q; the account comes from the stored message, never the request", src.account, src.message)
	}
	if got.Summary == nil || *got.Summary != "Thursday is settled." || got.Earlier == nil || *got.Earlier != 3 {
		t.Fatalf("insight = %+v", got)
	}
}

// With nothing to show, the field is absent, so the client has one thing to test.
func TestNoInsightIsAnEmptyObjectNotAnError(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]InsightSource{
		"not wired":    nil,
		"nothing":      &fakeInsight{},
		"failed":       &fakeInsight{err: errors.New("provider down")},
		"store failed": &fakeInsight{err: store.ErrNotFound},
	} {
		base := insightServer(t, src)
		var got api.MessageInsight
		if code := getJSON(t, base+"/messages/m1/insight", &got); code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200: a chip that cannot appear must not break the page", name, code)
		}
		if got.Summary != nil || got.Earlier != nil {
			t.Errorf("%s: insight = %+v, want empty", name, got)
		}
	}
}

func TestAnInsightForAMissingMessageIsNotFound(t *testing.T) {
	t.Parallel()
	src := &fakeInsight{res: insight.Result{Summary: "x"}}
	base := insightServer(t, src)
	var body map[string]any
	if code := getJSON(t, base+"/messages/nope/insight", &body); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	if src.calls != 0 {
		t.Fatal("the source was asked about a message that does not exist")
	}
}
