package gateway

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// smartServer is a server whose gate is the one the screen reads and writes, with two
// accounts: a1 has smart features on, a2 does not.
func smartServer(t *testing.T) (string, *store.DBs, *llm.Gate) {
	t.Helper()
	var gate *llm.Gate
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		s.WithClock(func() time.Time { return spendNow })
		gate = llm.NewGate(s.dbs, llm.WithGateClock(func() time.Time { return spendNow }), llm.WithDefaultCaps(5, 10))
		s.WithSmartControls(gate)
	})
	mustAccount(t, dbs, store.Account{ID: "a1", Address: "me@example.com", LLMEnabled: true})
	mustAccount(t, dbs, store.Account{ID: "a2", Address: "work@example.com"})
	return srv.URL + "/api/v1/smart", dbs, gate
}

func getSmart(t *testing.T, url string) api.SmartSettings {
	t.Helper()
	var got api.SmartSettings
	if code := getJSON(t, url, &got); code != http.StatusOK {
		t.Fatalf("GET smart status = %d, want 200", code)
	}
	return got
}

func accountOf(t *testing.T, s api.SmartSettings, id string) api.SmartAccount {
	t.Helper()
	for _, a := range s.Accounts {
		if a.Id == id {
			return a
		}
	}
	t.Fatalf("no account %q in %+v", id, s.Accounts)
	return api.SmartAccount{}
}

func TestTheSmartScreenReadsWhatTheGateIsApplying(t *testing.T) {
	t.Parallel()
	url, dbs, _ := smartServer(t)
	record(t, dbs, store.APICall{
		At: spendNow.Add(-time.Hour), Provider: "openrouter", Endpoint: llm.EndpointEmbeddings, Model: "m",
		Feature: "search", AccountID: "a1", ContentKey: "k", CostUSD: 0.4, Outcome: "ok", CallID: "c1",
	})

	got := getSmart(t, url)
	if got.GlobalCapUsd != 10 || got.GlobalMonthUsd != 0.4 {
		t.Errorf("global = cap %v spent %v, want 10 and 0.4", got.GlobalCapUsd, got.GlobalMonthUsd)
	}
	a1, a2 := accountOf(t, got, "a1"), accountOf(t, got, "a2")
	if a1.CapUsd != 5 || a1.MonthUsd != 0.4 || !a1.Smart || a2.Smart || a2.MonthUsd != 0 {
		t.Errorf("accounts = %+v / %+v", a1, a2)
	}
	if on, listed := a1.Features["search"]; !listed || !on {
		t.Errorf("a1 features = %v, want meaning search on by default", a1.Features)
	}
	if len(got.Features) == 0 || got.Features[0].Name != "search" || got.Features[0].Label != "Meaning search" {
		t.Errorf("features = %+v, want meaning search listed", got.Features)
	}
	if got.ChatModel != llm.DefaultChatModelID || len(got.FeatureModels) != 0 {
		t.Errorf("models = %q with overrides %v, want the default and none", got.ChatModel, got.FeatureModels)
	}
	var sawDefault bool
	for _, m := range got.Models {
		sawDefault = sawDefault || m.Id == llm.DefaultChatModelID
		if m.Id == llm.JevModelID {
			t.Errorf("the helper decision model is offered as a chat model: %+v", m)
		}
	}
	if !sawDefault {
		t.Errorf("models = %+v, want the default among them", got.Models)
	}
}

func TestAChangeIsSavedAndTheGateAppliesIt(t *testing.T) {
	t.Parallel()
	url, _, gate := smartServer(t)
	ctx := context.Background()
	other := "mercury"

	global, cap1 := 20.0, 3.0
	off := false
	patch := api.SmartSettingsPatch{
		GlobalCapUsd: &global,
		ChatModel:    &other,
		Accounts: &map[string]api.SmartAccountPatch{
			"a1": {CapUsd: &cap1, Features: &map[string]bool{"search": off}},
		},
	}
	var got api.SmartSettings
	if code := doJSON(t, http.MethodPatch, url, patch, &got, nil); code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200", code)
	}
	if got.GlobalCapUsd != 20 || got.ChatModel != other || accountOf(t, got, "a1").CapUsd != 3 {
		t.Errorf("response = %+v, want the new values", got)
	}
	if accountOf(t, got, "a1").Features["search"] {
		t.Error("the response still shows search on")
	}
	// What the gate enforces is what was saved.
	if gate.GlobalCapUSD(ctx) != 20 || gate.AccountCapUSD(ctx, "a1") != 3 || gate.FeatureEnabled(ctx, "a1", "search") {
		t.Error("the gate is not applying the saved values")
	}
	if !reflect.DeepEqual(getSmart(t, url), got) {
		t.Error("a fresh read differs from the PATCH response")
	}
	if accountOf(t, got, "a2").CapUsd != 5 || !accountOf(t, got, "a2").Features["search"] {
		t.Error("a change to a1 reached a2")
	}
}

func TestAFeatureModelOverrideCanBeSetAndCleared(t *testing.T) {
	t.Parallel()
	url, _, _ := smartServer(t)
	set := map[string]string{"digest": "mercury"}
	var got api.SmartSettings
	if code := doJSON(t, http.MethodPatch, url, api.SmartSettingsPatch{FeatureModels: &set}, &got, nil); code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200", code)
	}
	if got.FeatureModels["digest"] != "mercury" {
		t.Errorf("overrides = %v, want digest on mercury", got.FeatureModels)
	}
	cleared := map[string]string{"digest": ""}
	// A fresh value: decoding into the old one would merge into its map.
	var after api.SmartSettings
	doJSON(t, http.MethodPatch, url, api.SmartSettingsPatch{FeatureModels: &cleared}, &after, nil)
	if _, still := after.FeatureModels["digest"]; still {
		t.Errorf("overrides = %v, want digest cleared", after.FeatureModels)
	}
}

// A request is checked whole before anything is written, so a bad part cannot
// leave the good part half applied.
func TestARefusedChangeChangesNothing(t *testing.T) {
	t.Parallel()
	url, _, _ := smartServer(t)
	before := getSmart(t, url)

	global, bad, huge := 20.0, -1.0, 1e9
	unknownModel, text := "retired", "mercury"
	cases := map[string]struct {
		patch api.SmartSettingsPatch
		code  int
	}{
		"a negative account cap": {api.SmartSettingsPatch{
			GlobalCapUsd: &global,
			Accounts:     &map[string]api.SmartAccountPatch{"a1": {CapUsd: &bad}},
		}, http.StatusBadRequest},
		"a cap over the ceiling": {api.SmartSettingsPatch{GlobalCapUsd: &huge}, http.StatusBadRequest},
		"an unknown model":       {api.SmartSettingsPatch{GlobalCapUsd: &global, ChatModel: &unknownModel}, http.StatusBadRequest},
		"an unknown feature": {api.SmartSettingsPatch{
			GlobalCapUsd: &global,
			Accounts:     &map[string]api.SmartAccountPatch{"a1": {Features: &map[string]bool{"mystery": true}}},
		}, http.StatusBadRequest},
		"a model that cannot serve the feature": {api.SmartSettingsPatch{
			GlobalCapUsd:  &global,
			FeatureModels: &map[string]string{"vision": text},
		}, http.StatusBadRequest},
		"an unknown account": {api.SmartSettingsPatch{
			GlobalCapUsd: &global,
			Accounts:     &map[string]api.SmartAccountPatch{"nobody": {CapUsd: &global}},
		}, http.StatusNotFound},
	}
	for name, c := range cases {
		var errBody api.Error
		if code := doJSON(t, http.MethodPatch, url, c.patch, &errBody, nil); code != c.code {
			t.Errorf("%s: status = %d, want %d", name, code, c.code)
		}
		if !reflect.DeepEqual(getSmart(t, url), before) {
			t.Fatalf("%s changed the settings", name)
		}
	}
}

func TestAnEmptyOrMalformedPatch(t *testing.T) {
	t.Parallel()
	url, _, _ := smartServer(t)
	before := getSmart(t, url)

	var got api.SmartSettings
	if code := doJSON(t, http.MethodPatch, url, map[string]any{}, &got, nil); code != http.StatusOK || !reflect.DeepEqual(got, before) {
		t.Errorf("an empty patch = %d, want 200 and the settings unchanged", code)
	}
	for name, body := range map[string]string{
		"not json":       `{"globalCapUsd":`,
		"the wrong type": `{"globalCapUsd":"ten"}`,
		"too large":      `{"chatModel":"` + strings.Repeat("x", maxSmartBodyBytes) + `"}`,
	} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPatch, url, bytes.NewReader([]byte(body)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
	}
	if !reflect.DeepEqual(getSmart(t, url), before) {
		t.Error("a malformed patch changed the settings")
	}
}
