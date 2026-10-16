package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/jev"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

type startedBackfill struct {
	account string
	window  jev.Window
}

// fakeBackfill stands in for the Jev worker. It is the outside edge of the gateway
// (the worker has its own tests); what is under test here is the HTTP contract.
type fakeBackfill struct {
	est     jev.BackfillEstimate
	depth   map[string]int
	started []startedBackfill
}

func (f *fakeBackfill) EstimateBackfill(_ context.Context, _ string, _ jev.Window) (jev.BackfillEstimate, error) {
	return f.est, nil
}

func (f *fakeBackfill) StartBackfill(_ context.Context, account string, win jev.Window) error {
	f.started = append(f.started, startedBackfill{account, win})
	return nil
}

// backfillServer has a gate that can reach a provider (a1 is ready) and one that
// cannot (a2 is opted in, and the other server below has no key at all).
func backfillServer(t *testing.T, withKey bool) (string, *fakeBackfill) {
	t.Helper()
	fake := &fakeBackfill{est: jev.BackfillEstimate{
		Items: 41,
		Estimate: llm.Estimate{
			Items: 41, USD: 0.01, AccountHeadroomUSD: 4.5, GlobalHeadroomUSD: 9, Fits: true,
		},
	}}
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		s.WithClock(func() time.Time { return spendNow })
		opts := []llm.GateOption{llm.WithGateClock(func() time.Time { return spendNow })}
		if withKey {
			opts = append(opts, llm.WithProviders(llm.ProviderConfig{OpenRouterBase: "http://127.0.0.1:1", APIKey: "k"}))
		}
		s.WithSmartControls(llm.NewGate(s.dbs, opts...))
		s.WithBackfill(fake)
	})
	mustAccount(t, dbs, store.Account{ID: "a1", Address: "me@example.com", LLMEnabled: true})
	mustAccount(t, dbs, store.Account{ID: "off", Address: "work@example.com"})
	return srv.URL + "/api/v1/smart/backfill", fake
}

func TestTheBackfillEstimateIsPricedAndStartsNothing(t *testing.T) {
	t.Parallel()
	url, fake := backfillServer(t, true)

	var got api.SmartBackfill
	if code := getJSON(t, url+"?account=a1&window=30d", &got); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Account != "a1" || got.Window != "30d" || got.Items != 41 || got.Usd != 0.01 || !got.Fits ||
		got.AccountHeadroomUsd != 4.5 || got.GlobalHeadroomUsd != 9 {
		t.Errorf("estimate = %+v", got)
	}
	if len(fake.started) != 0 {
		t.Errorf("asking the price started %d backfills", len(fake.started))
	}
}

func TestTheBackfillEstimateRefusesWhatItCannotPrice(t *testing.T) {
	t.Parallel()
	url, _ := backfillServer(t, true)
	cases := map[string]int{
		"?account=a1&window=forever": http.StatusBadRequest,
		"?account=a1":                http.StatusBadRequest,
		"?account=nobody&window=7d":  http.StatusNotFound,
	}
	for query, want := range cases {
		if code := getJSON(t, url+query, &api.Error{}); code != want {
			t.Errorf("%s: status = %d, want %d", query, code, want)
		}
	}
}

func TestStartingABackfillReachesBackOverTheWindow(t *testing.T) {
	t.Parallel()
	url, fake := backfillServer(t, true)

	for _, c := range []struct {
		window string
		want   jev.Window
	}{{"7d", jev.Days(7)}, {"90d", jev.Days(90)}, {"all", jev.AllMail}} {
		var got api.SmartBackfill
		code := sendJSON(t, http.MethodPost, url, api.SmartBackfillRequest{Account: "a1", Window: api.BackfillWindow(c.window)}, &got)
		if code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", c.window, code)
		}
		if got.Items != 41 || string(got.Window) != c.window {
			t.Errorf("%s: reply = %+v", c.window, got)
		}
		last := fake.started[len(fake.started)-1]
		if last.account != "a1" || last.window != c.want {
			t.Errorf("%s: started %+v, want a1 over %v", c.window, last, c.want)
		}
	}
}

func TestABackfillIsRefusedWhileNothingCouldRunItAndSaysWhy(t *testing.T) {
	t.Parallel()
	body := api.SmartBackfillRequest{Account: "a1", Window: "30d"}

	t.Run("no key loaded", func(t *testing.T) {
		url, fake := backfillServer(t, false)
		var e api.Error
		if code := sendJSON(t, http.MethodPost, url, body, &e); code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", code)
		}
		if e.Code != "not_ready" || !strings.Contains(e.Message, "OPENROUTER_API_KEY") {
			t.Errorf("error = %+v, want not_ready naming the key", e)
		}
		if len(fake.started) != 0 {
			t.Error("a backfill started with no way to run it")
		}
	})
	t.Run("smart features off for the account", func(t *testing.T) {
		url, fake := backfillServer(t, true)
		var e api.Error
		code := sendJSON(t, http.MethodPost, url, api.SmartBackfillRequest{Account: "off", Window: "30d"}, &e)
		if code != http.StatusConflict || e.Code != "not_ready" {
			t.Fatalf("status = %d error = %+v, want 409 not_ready", code, e)
		}
		if len(fake.started) != 0 {
			t.Error("a backfill started for an account that has smart features off")
		}
	})
	t.Run("bad window and unknown account", func(t *testing.T) {
		url, fake := backfillServer(t, true)
		if code := sendJSON(t, http.MethodPost, url, api.SmartBackfillRequest{Account: "a1", Window: "forever"}, &api.Error{}); code != http.StatusBadRequest {
			t.Errorf("bad window: status = %d, want 400", code)
		}
		if code := sendJSON(t, http.MethodPost, url, api.SmartBackfillRequest{Account: "nobody", Window: "7d"}, &api.Error{}); code != http.StatusNotFound {
			t.Errorf("unknown account: status = %d, want 404", code)
		}
		if len(fake.started) != 0 {
			t.Error("a refused request still started a backfill")
		}
	})
}

func (f *fakeBackfill) Depth(_ context.Context, account string) (int, error) {
	return f.depth[account], nil
}

// While a backfill is being worked through the screen has to show it moving, so the
// settings carry how many messages each account still has waiting.
func TestTheSettingsSayHowManyMessagesAreStillWaiting(t *testing.T) {
	t.Parallel()
	var fake *fakeBackfill
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		fake = &fakeBackfill{depth: map[string]int{"a1": 12}}
		s.WithClock(func() time.Time { return spendNow })
		s.WithSmartControls(llm.NewGate(s.dbs, llm.WithGateClock(func() time.Time { return spendNow })))
		s.WithBackfill(fake)
	})
	mustAccount(t, dbs, store.Account{ID: "a1", Address: "me@example.com", LLMEnabled: true})
	mustAccount(t, dbs, store.Account{ID: "a2", Address: "work@example.com", LLMEnabled: true})

	got := getSmart(t, srv.URL+"/api/v1/smart")
	if a1, a2 := accountOf(t, got, "a1"), accountOf(t, got, "a2"); a1.Waiting != 12 || a2.Waiting != 0 {
		t.Fatalf("waiting = %d and %d, want 12 and 0", a1.Waiting, a2.Waiting)
	}
	fake.depth["a1"] = 3
	if a1 := accountOf(t, getSmart(t, srv.URL+"/api/v1/smart"), "a1"); a1.Waiting != 3 {
		t.Fatalf("waiting after some were done = %d, want 3", a1.Waiting)
	}
}
