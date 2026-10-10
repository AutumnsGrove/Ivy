package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/jev"
	"github.com/AutumnsGrove/Ivy/store"
)

type fakeOdds struct {
	odds    jev.Odds
	err     error
	account string
	key     string
}

func (f *fakeOdds) Odds(_ context.Context, account, key string) (jev.Odds, error) {
	f.account, f.key = account, key
	return f.odds, f.err
}

func oddsServer(t *testing.T, src OddsSource) string {
	t.Helper()
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		if src != nil {
			s.WithOdds(src)
		}
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, inboxMessage("m1", "acct-1", "inbox-1", testNow, false))
	return srv.URL + "/api/v1"
}

func TestOddsAreServedForAMessageByItsAccountAndContentKey(t *testing.T) {
	t.Parallel()
	src := &fakeOdds{odds: jev.Odds{
		Model: "jev-latest",
		Answers: []jev.Verdict{{
			QuestionID: "needs_me", Choice: "likely", Probabilities: map[string]float64{"likely": 0.9, "none": 0.1},
			Confidence: 0.95, Threshold: 0.8, QuietOption: "none", Fires: true,
		}, {
			QuestionID: "urgency", Choice: "high", Probabilities: map[string]float64{"high": 0.9},
			Confidence: 0.9, Threshold: 0.75, QuietOption: "low", Fires: true, Suppressed: true,
		}},
		Unanswered: []jev.Unanswered{{QuestionID: "category", Reason: "invalid_answer"}},
	}}
	base := oddsServer(t, src)

	var got api.MessageOdds
	if code := getJSON(t, base+"/messages/m1/odds", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if src.account != "acct-1" || src.key != "ck:m1" {
		t.Fatalf("asked for %q/%q; the cache is keyed by account and content key", src.account, src.key)
	}
	if got.Model != "jev-latest" || len(got.Answers) != 2 || len(got.Unanswered) != 1 {
		t.Fatalf("odds = %+v", got)
	}
	n := got.Answers[0]
	if n.QuestionId != "needs_me" || n.Choice != "likely" || n.Probabilities["likely"] != 0.9 ||
		n.Threshold != 0.8 || !n.Fires || n.Suppressed || !n.Acts {
		t.Fatalf("needs_me = %+v", n)
	}
	if u := got.Answers[1]; !u.Fires || !u.Suppressed || u.Acts {
		t.Fatalf("a suppressed answer must show that it fired and that it does not act: %+v", u)
	}
	if got.Unanswered[0].QuestionId != "category" || got.Unanswered[0].Reason != "invalid_answer" {
		t.Fatalf("unanswered = %+v", got.Unanswered)
	}
}

func TestOddsAreEmptyWhenNothingIsWired(t *testing.T) {
	t.Parallel()
	base := oddsServer(t, nil)
	var got api.MessageOdds
	if code := getJSON(t, base+"/messages/m1/odds", &got); code != http.StatusOK {
		t.Fatalf("status = %d; smart features being absent must not break the reading path", code)
	}
	if got.Answers == nil || got.Unanswered == nil || len(got.Answers)+len(got.Unanswered) != 0 {
		t.Fatalf("want empty lists (not null), got %+v", got)
	}
}

func TestOddsForAnUnknownMessageIsNotFound(t *testing.T) {
	t.Parallel()
	base := oddsServer(t, &fakeOdds{})
	var body map[string]any
	if code := getJSON(t, base+"/messages/nope/odds", &body); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestOddsFailureIsAServerErrorWithoutLeakingTheCause(t *testing.T) {
	t.Parallel()
	base := oddsServer(t, &fakeOdds{err: errors.New("secret internals")})
	var body map[string]any
	if code := getJSON(t, base+"/messages/m1/odds", &body); code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", code)
	}
	if s, _ := body["message"].(string); s == "secret internals" {
		t.Fatal("the error cause reached the client")
	}
}

// Stage two's verdict is read from the needs_me row, not the question cache, so the
// sheet can show both stages of the cascade for one message.
func TestOddsCarryStageTwoWhenItHasDecided(t *testing.T) {
	t.Parallel()
	srv, dbs := newConfiguredServer(t, func(s *Server) { s.WithOdds(&fakeOdds{}) })
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, inboxMessage("m1", "acct-1", "inbox-1", testNow, false))
	mustMessage(t, dbs, inboxMessage("m2", "acct-1", "inbox-1", testNow, false))
	if err := dbs.PutNeedsMe(context.Background(), store.NeedsMeRow{
		AccountID: "acct-1", ContentKey: "ck:m1", Verdict: "needs", Reason: "Dana asks you to confirm Friday",
		Model: "chat-model", State: store.NeedsOpen,
	}); err != nil {
		t.Fatal(err)
	}
	base := srv.URL + "/api/v1"

	var got api.MessageOdds
	if code := getJSON(t, base+"/messages/m1/odds", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	s := got.Stage2
	if s == nil || s.Verdict != "needs" || s.Reason == nil || *s.Reason != "Dana asks you to confirm Friday" ||
		s.Model != "chat-model" || s.Dismissed {
		t.Fatalf("stage2 = %+v", s)
	}

	if err := dbs.DismissNeedsMe(context.Background(), "acct-1", "ck:m1"); err != nil {
		t.Fatal(err)
	}
	got = api.MessageOdds{}
	getJSON(t, base+"/messages/m1/odds", &got)
	if got.Stage2 == nil || !got.Stage2.Dismissed {
		t.Fatalf("a dismissed verdict must still show, marked dismissed: %+v", got.Stage2)
	}

	got = api.MessageOdds{}
	getJSON(t, base+"/messages/m2/odds", &got)
	if got.Stage2 != nil {
		t.Fatalf("a message stage two never decided has no stage2: %+v", got.Stage2)
	}
}
