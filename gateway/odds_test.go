package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

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

func looksRealAnswer(choice string, fires, suppressed bool) jev.Odds {
	return jev.Odds{Answers: []jev.Verdict{{
		QuestionID: "junk_rescue", Choice: choice, Probabilities: map[string]float64{choice: 0.95},
		Confidence: 0.95, Threshold: 0.9, QuietOption: "junk", Fires: fires, Suppressed: suppressed,
	}}}
}

// junkServer has one message in Junk (j1) and one in the Inbox (i1).
func junkServer(t *testing.T, src OddsSource) string {
	t.Helper()
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		if src != nil {
			s.WithOdds(src)
		}
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustFolder(t, dbs, store.Folder{ID: "junk-1", AccountID: "acct-1", Name: "Junk", Role: store.RoleJunk})
	mustMessage(t, dbs, inboxMessage("j1", "acct-1", "junk-1", testNow, false))
	mustMessage(t, dbs, inboxMessage("i1", "acct-1", "inbox-1", testNow, false))
	return srv.URL + "/api/v1"
}

// 5d.4: the chip is a hint on a Junk message and nothing else. It needs the answer to
// act, and it follows the folder: once the operator moves a message out of Junk the
// stale answer must not keep calling it rescued.
func TestAJunkMessageThatLooksRealCarriesTheChipHint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		id   string
		odds jev.Odds
		want bool
	}{
		{"looks real in Junk", "j1", looksRealAnswer("looks_real", true, false), true},
		{"unsure does not fire", "j1", looksRealAnswer("unsure", false, false), false},
		{"junk does not fire", "j1", looksRealAnswer("junk", false, false), false},
		{"a suppressed answer does not act", "j1", looksRealAnswer("looks_real", true, true), false},
		{"the same answer after a move to the Inbox", "i1", looksRealAnswer("looks_real", true, false), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			base := junkServer(t, &fakeOdds{odds: c.odds})
			var got api.MailMessage
			if code := getJSON(t, base+"/messages/"+c.id, &got); code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			if has := got.LooksReal != nil && *got.LooksReal; has != c.want {
				t.Fatalf("looksReal = %v, want %v", got.LooksReal, c.want)
			}
		})
	}
}

func TestNoChipHintWhenNothingIsWired(t *testing.T) {
	t.Parallel()
	base := junkServer(t, nil)
	var got api.MailMessage
	if code := getJSON(t, base+"/messages/j1", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got.LooksReal != nil && *got.LooksReal {
		t.Fatal("a chip appeared with no Jev wired")
	}
}

// oddsByKey answers per content key, so one page can hold a message that looks real and
// one that does not.
type oddsByKey map[string]jev.Odds

func (o oddsByKey) Odds(_ context.Context, _, key string) (jev.Odds, error) { return o[key], nil }

func junkListServer(t *testing.T, src OddsSource) string {
	t.Helper()
	srv, dbs := newConfiguredServer(t, func(s *Server) { s.WithOdds(src) })
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustFolder(t, dbs, store.Folder{ID: "junk-1", AccountID: "acct-1", Name: "Junk", Role: store.RoleJunk})
	mustMessage(t, dbs, inboxMessage("j1", "acct-1", "junk-1", testNow, false))
	mustMessage(t, dbs, inboxMessage("j2", "acct-1", "junk-1", testNow.Add(-time.Minute), false))
	mustMessage(t, dbs, inboxMessage("i1", "acct-1", "inbox-1", testNow, false))
	return srv.URL + "/api/v1"
}

func rowFlags(in api.Inbox) map[string]bool {
	out := map[string]bool{}
	for _, m := range in.Items {
		out[m.Id] = m.LooksReal != nil && *m.LooksReal
	}
	return out
}

// 5d.4: the Junk list carries the same hint as the reader, so a rescued message can be
// found without opening each one. It is a property of the Junk view alone.
func TestTheJunkListMarksOnlyTheRowsThatLookReal(t *testing.T) {
	t.Parallel()
	base := junkListServer(t, oddsByKey{
		"ck:j1": looksRealAnswer("looks_real", true, false),
		"ck:j2": looksRealAnswer("junk", false, false),
		"ck:i1": looksRealAnswer("looks_real", true, false),
	})
	var junk api.Inbox
	if code := getJSON(t, base+"/inbox?folder=junk", &junk); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	got := rowFlags(junk)
	if len(got) != 2 || !got["j1"] || got["j2"] {
		t.Fatalf("junk rows = %v, want only j1 marked", got)
	}

	var inbox api.Inbox
	if code := getJSON(t, base+"/inbox", &inbox); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := rowFlags(inbox); got["i1"] {
		t.Fatal("an Inbox row carries the Junk hint: the hint belongs to the Junk view only")
	}
}

func phishingAnswer(choice string, fires, suppressed bool) jev.Odds {
	return jev.Odds{Answers: []jev.Verdict{{
		QuestionID: "phishing_risk", Choice: choice, Probabilities: map[string]float64{choice: 0.95},
		Confidence: 0.95, Threshold: 0.8, QuietOption: "none", Fires: fires, Suppressed: suppressed,
	}}}
}

// phishingServer has Inbox message p1 whose provider verdicts are as given.
func phishingServer(t *testing.T, src OddsSource, auth store.AuthResults) string {
	t.Helper()
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		if src != nil {
			s.WithOdds(src)
		}
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	m := inboxMessage("p1", "acct-1", "inbox-1", testNow, false)
	m.AuthResults = auth
	mustMessage(t, dbs, m)
	return srv.URL + "/api/v1"
}

// 5d.6: the banner is a warning the model's acting answer creates; the provider's verdicts
// only raise it, and nothing about the message moves.
func TestAMessageTheModelDistrustsCarriesAWarningThatOnlyEverGrows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		odds jev.Odds
		auth store.AuthResults
		want string
	}{
		{"suspicious", phishingAnswer("suspicious", true, false), store.AuthResults{}, "suspicious"},
		{"likely", phishingAnswer("likely", true, false), store.AuthResults{}, "likely"},
		{"a DMARC fail raises suspicious", phishingAnswer("suspicious", true, false), store.AuthResults{DMARC: "fail"}, "likely"},
		{"none shows nothing", phishingAnswer("none", false, false), store.AuthResults{DMARC: "fail"}, ""},
		{"a suppressed answer shows nothing", phishingAnswer("likely", true, true), store.AuthResults{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			base := phishingServer(t, &fakeOdds{odds: c.odds}, c.auth)
			var got api.MailMessage
			if code := getJSON(t, base+"/messages/p1", &got); code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			have := ""
			if got.PhishingRisk != nil {
				have = string(*got.PhishingRisk)
			}
			if have != c.want {
				t.Fatalf("phishingRisk = %q, want %q", have, c.want)
			}
		})
	}
}

func TestNoWarningWhenNothingIsWiredOrOddsCannotBeRead(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]OddsSource{
		"nothing wired": nil,
		"odds fail":     &fakeOdds{err: errors.New("db down")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := phishingServer(t, src, store.AuthResults{DMARC: "fail"})
			var got api.MailMessage
			if code := getJSON(t, base+"/messages/p1", &got); code != http.StatusOK {
				t.Fatalf("status = %d: the warning is only a hint, so the page must still load", code)
			}
			if got.PhishingRisk != nil {
				t.Fatalf("phishingRisk = %q with no readable answer", *got.PhishingRisk)
			}
		})
	}
}
