package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// fakeJev is a /systemone provider the test controls: it records every request and
// answers through reply, so a test can return a skewed, malformed or missing answer
// or a status, which the shared mailworld fake cannot.
type fakeJev struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []jevRequest
	status   int
	reply    func(jevRequest) map[string]answerJSON
}

type jevRequest struct {
	Model     string `json:"model"`
	State     string `json:"state"`
	Questions map[string]struct {
		Type         string            `json:"type"`
		Instructions string            `json:"instructions"`
		Criteria     map[string]string `json:"criteria"`
	} `json:"questions"`
	Raw []byte `json:"-"`
}

type answerJSON struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

func newFakeJev(t *testing.T) *fakeJev {
	t.Helper()
	f := &fakeJev{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jevRequest
		req.Raw, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(req.Raw, &req)
		f.mu.Lock()
		f.requests = append(f.requests, req)
		status, reply := f.status, f.reply
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"no"}`))
			return
		}
		answers := map[string]answerJSON{}
		if reply != nil {
			answers = reply(req)
		} else {
			for id, q := range req.Questions {
				answers[id] = quietAnswer(q.Criteria)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": answers,
			"usage":   map[string]any{"input_tokens": 500, "output_tokens": 0, "cost": 0.0001},
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJev) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeJev) last() jevRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func (f *fakeJev) setStatus(s int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = s
}

// quietAnswer picks the alphabetically first option at full confidence.
func quietAnswer(criteria map[string]string) answerJSON {
	first := ""
	for k := range criteria {
		if first == "" || k < first {
			first = k
		}
	}
	return answerJSON{Choice: first, Probabilities: map[string]float64{first: 1}, Confidence: 1}
}

type rig struct {
	t      *testing.T
	dbs    *store.DBs
	gate   *llm.Gate
	jev    *fakeJev
	reg    *Registry
	engine *Engine
}

const rigAccount = "acct-a"

// newRig builds the real store and gate against the fake provider, with the
// account opted in and the classify feature on, and two questions registered.
func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	dbs, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	if err := dbs.SaveAccountConfig(ctx, store.AccountConfig{
		ID: rigAccount, Address: "me@example.test", Username: "me",
		IMAPHost: "imap.example.test", IMAPPort: 993, SMTPHost: "smtp.example.test", SMTPPort: 465,
		LLMEnabled: true, EmbedProvider: "openrouter", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	fj := newFakeJev(t)
	gate := llm.NewGate(dbs, llm.WithProviders(llm.ProviderConfig{OpenRouterBase: fj.srv.URL, APIKey: "k"}), llm.WithDefaultCaps(5, 10))
	if err := gate.SetFeatureEnabled(ctx, rigAccount, CallFeature, true); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	if err := reg.Set(mustParse(t, strings.Replace(validYAML, "enabled: false", "enabled: true", 1)), nil); err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, dbs: dbs, gate: gate, jev: fj, reg: reg, engine: NewEngine(reg, gate, dbs)}
}

func mail(key string) Message {
	return Message{
		AccountID: rigAccount, ContentKey: key, Folder: FolderInbox,
		State: StateInput{
			From: Party{Name: "Ada", Address: "ada@example.com"}, Subject: "Lunch?",
			Date: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC), Body: "Are you free on Friday?",
		},
	}
}

func (r *rig) decide(m Message) Outcome {
	r.t.Helper()
	out, err := r.engine.Decide(context.Background(), m)
	if err != nil {
		r.t.Fatalf("Decide: %v", err)
	}
	return out
}

func (r *rig) decisions(key string) []store.Decision {
	r.t.Helper()
	ds, err := r.dbs.DecisionsFor(context.Background(), rigAccount, key)
	if err != nil {
		r.t.Fatal(err)
	}
	return ds
}

func (r *rig) misses(key string) []store.DecisionMiss {
	r.t.Helper()
	ms, err := r.dbs.DecisionMissesFor(context.Background(), rigAccount, key)
	if err != nil {
		r.t.Fatal(err)
	}
	return ms
}

func TestOneCallCarriesEveryQuestionAndStoresEachAnswer(t *testing.T) {
	r := newRig(t)
	out := r.decide(mail("ck1"))
	if r.jev.count() != 1 {
		t.Fatalf("%d provider calls for one message, want 1", r.jev.count())
	}
	req := r.jev.last()
	if len(req.Questions) != 2 || req.Questions["needs_me"].Type != "choice" || req.Model != "jev-latest" {
		t.Fatalf("request wrong: model=%q questions=%v", req.Model, req.Questions)
	}
	if !strings.Contains(req.State, "Subject: Lunch?") || strings.Contains(req.State, "<") && !strings.Contains(req.State, "<ada@example.com>") {
		t.Fatalf("state wrong: %q", req.State)
	}
	if !out.Called || out.Asked != 2 || out.Cached != 0 || out.CostUSD <= 0 {
		t.Fatalf("outcome = %+v", out)
	}
	ds := r.decisions("ck1")
	if len(ds) != 2 {
		t.Fatalf("stored %d decisions, want 2", len(ds))
	}
	for _, d := range ds {
		if d.Model != "jev-latest" || d.InstructionHash == "" || d.Probabilities[d.Choice] != 1 {
			t.Fatalf("decision wrong: %+v", d)
		}
	}
}

func TestACachedMessageIsNeverAskedAgain(t *testing.T) {
	r := newRig(t)
	r.decide(mail("ck1"))
	out := r.decide(mail("ck1"))
	if r.jev.count() != 1 || out.Called || out.Cached != 2 {
		t.Fatalf("second decide: calls=%d outcome=%+v", r.jev.count(), out)
	}
	// A move, archive, flag or tag change leaves the content key where it was, so the
	// folder a message sits in now makes no difference to what is already known.
	moved := mail("ck1")
	moved.Folder = FolderJunk
	r.decide(moved)
	if r.jev.count() != 1 {
		t.Fatalf("moving the message re-asked: %d calls", r.jev.count())
	}
}

func TestEditingOneQuestionReAsksOnlyThatQuestion(t *testing.T) {
	r := newRig(t)
	r.decide(mail("ck1"))

	edited, _ := r.reg.Get("needs_me")
	edited.Instructions += " Receipts do not count."
	if err := r.reg.Set(mustParse(t, strings.Replace(validYAML, "enabled: false", "enabled: true", 1)), []Question{edited}); err != nil {
		t.Fatal(err)
	}
	out := r.decide(mail("ck1"))
	if r.jev.count() != 2 || out.Asked != 1 || out.Cached != 1 {
		t.Fatalf("calls=%d outcome=%+v; want one more call asking one question", r.jev.count(), out)
	}
	if qs := r.jev.last().Questions; len(qs) != 1 || qs["needs_me"].Instructions != edited.Instructions {
		t.Fatalf("re-ask sent %v", qs)
	}
	// The old answer is kept beside the new one: nothing is erased.
	if n := len(r.decisions("ck1")); n != 3 {
		t.Fatalf("%d decision rows, want 3", n)
	}
}

func TestTuningAThresholdNeverReAsks(t *testing.T) {
	r := newRig(t)
	r.decide(mail("ck1"))
	tuned, _ := r.reg.Get("needs_me")
	tuned.Threshold = 0.95
	tuned.Suppresses = nil
	if err := r.reg.Set(mustParse(t, strings.Replace(validYAML, "enabled: false", "enabled: true", 1)), []Question{tuned}); err != nil {
		t.Fatal(err)
	}
	r.decide(mail("ck1"))
	if r.jev.count() != 1 {
		t.Fatalf("a threshold edit re-asked: %d calls", r.jev.count())
	}
}

// otherModel reports a different model for the call, as a pinned versioned id would.
type otherModel struct{ *llm.Gate }

func (otherModel) ModelFor(context.Context, string) (llm.Model, error) {
	return llm.Model{ID: "jev", Slug: "jev-1.13"}, nil
}

func TestChangingTheModelReAsks(t *testing.T) {
	r := newRig(t)
	r.decide(mail("ck1"))
	e := NewEngine(r.reg, otherModel{r.gate}, r.dbs)
	if _, err := e.Decide(context.Background(), mail("ck1")); err != nil {
		t.Fatal(err)
	}
	if r.jev.count() != 2 || r.jev.last().Model != "jev-1.13" {
		t.Fatalf("calls=%d model=%q", r.jev.count(), r.jev.last().Model)
	}
}

func TestOnlyQuestionsForThisAccountAndFolderAreAsked(t *testing.T) {
	r := newRig(t)
	m := mail("ck1")
	m.AccountID = "someone-else" // urgency is scoped to acct-a, and this one is neither
	// Not opted in, so the gate refuses; the point here is which questions are chosen.
	got := r.engine.pending(context.Background(), m)
	if len(got) != 1 || got[0].ID != "needs_me" {
		t.Fatalf("pending for another account = %v", got)
	}
	m.Folder = FolderJunk
	if got := r.engine.pending(context.Background(), m); len(got) != 0 {
		t.Fatalf("junk asked %v; only a question that names junk runs there", got)
	}
}

func TestAQuestionNamingAFeatureNeedsThatSwitchOn(t *testing.T) {
	r := newRig(t)
	junk := Question{
		ID: "junk_rescue", Instructions: "Is this real mail wrongly in Junk?", Feature: "junk_rescue",
		Criteria: map[string]string{"junk": "spam", "looks_real": "a person wrote it"}, QuietOption: "junk",
		Threshold: 0.9, Folders: []string{FolderJunk},
	}
	if err := r.reg.Set(nil, []Question{junk}); err != nil {
		t.Fatal(err)
	}
	m := mail("ck1")
	m.Folder = FolderJunk
	if r.decide(m); r.jev.count() != 0 {
		t.Fatalf("junk_rescue asked with its switch off: %d calls", r.jev.count())
	}
	if err := r.gate.SetFeatureEnabled(context.Background(), rigAccount, "junk_rescue", true); err != nil {
		t.Fatal(err)
	}
	r.decide(m)
	if r.jev.count() != 1 || len(r.jev.last().Questions) != 1 {
		t.Fatalf("calls=%d", r.jev.count())
	}
}

func TestAnAnswerOutsideTheOptionsIsRejectedAndRecorded(t *testing.T) {
	cases := map[string]answerJSON{
		"unknown choice":     {Choice: "delete_everything", Probabilities: map[string]float64{"delete_everything": 1}, Confidence: 1},
		"unknown option key": {Choice: "none", Probabilities: map[string]float64{"none": 0.5, "rm_rf": 0.5}, Confidence: 1},
		"probability over 1": {Choice: "none", Probabilities: map[string]float64{"none": 1.5}, Confidence: 1},
		"negative":           {Choice: "none", Probabilities: map[string]float64{"none": -0.1}, Confidence: 1},
		"confidence over 1":  {Choice: "none", Probabilities: map[string]float64{"none": 1}, Confidence: 2},
		"empty":              {},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.jev.reply = func(req jevRequest) map[string]answerJSON {
				return map[string]answerJSON{
					"needs_me": bad,
					"urgency":  quietAnswer(req.Questions["urgency"].Criteria),
				}
			}
			r.decide(mail("ck1"))
			ds, ms := r.decisions("ck1"), r.misses("ck1")
			if len(ds) != 1 || ds[0].QuestionID != "urgency" {
				t.Fatalf("decisions = %+v", ds)
			}
			if len(ms) != 1 || ms[0].QuestionID != "needs_me" || ms[0].Reason != store.MissInvalidAnswer {
				t.Fatalf("misses = %+v", ms)
			}
			// Recorded, so it is not paid for again.
			r.decide(mail("ck1"))
			if r.jev.count() != 1 {
				t.Fatalf("an invalid answer was re-asked: %d calls", r.jev.count())
			}
		})
	}
}

func TestAQuestionLeftOutOfTheReplyIsRecorded(t *testing.T) {
	r := newRig(t)
	r.jev.reply = func(req jevRequest) map[string]answerJSON {
		return map[string]answerJSON{"urgency": quietAnswer(req.Questions["urgency"].Criteria)}
	}
	r.decide(mail("ck1"))
	if ms := r.misses("ck1"); len(ms) != 1 || ms[0].Reason != store.MissNoAnswer {
		t.Fatalf("misses = %+v", ms)
	}
	// An answer for a question nobody asked is ignored, never stored.
	r2 := newRig(t)
	r2.jev.reply = func(req jevRequest) map[string]answerJSON {
		a := map[string]answerJSON{"smuggled": {Choice: "x", Probabilities: map[string]float64{"x": 1}, Confidence: 1}}
		for id, q := range req.Questions {
			a[id] = quietAnswer(q.Criteria)
		}
		return a
	}
	r2.decide(mail("ck1"))
	for _, d := range r2.decisions("ck1") {
		if d.QuestionID == "smuggled" {
			t.Fatal("an unasked question's answer was stored")
		}
	}
}

func TestAProviderRefusalOfTheInputIsRecordedAndNotRetried(t *testing.T) {
	r := newRig(t)
	r.jev.setStatus(http.StatusBadRequest)
	out, err := r.engine.Decide(context.Background(), mail("ck1"))
	if err != nil {
		t.Fatalf("a provider 400 is an outcome, not an error: %v", err)
	}
	if !out.Called {
		t.Fatal("outcome does not say a call was made")
	}
	ms := r.misses("ck1")
	if len(ms) != 2 || ms[0].Reason != store.MissRejected {
		t.Fatalf("misses = %+v", ms)
	}
	r.jev.setStatus(0)
	r.decide(mail("ck1"))
	if r.jev.count() != 1 {
		t.Fatalf("a rejected input was retried: %d calls", r.jev.count())
	}
}

func TestAnOutageIsAnErrorAndRecordsNothing(t *testing.T) {
	r := newRig(t)
	r.jev.setStatus(http.StatusServiceUnavailable)
	if _, err := r.engine.Decide(context.Background(), mail("ck1")); err == nil {
		t.Fatal("an outage reported success")
	}
	if len(r.decisions("ck1"))+len(r.misses("ck1")) != 0 {
		t.Fatal("an outage left rows behind; the message would never be asked again")
	}
	r.jev.setStatus(0)
	r.decide(mail("ck1"))
	if len(r.decisions("ck1")) != 2 {
		t.Fatal("the message was not answered once the provider came back")
	}
}

func TestNothingToReadMakesNoCallAndIsRecorded(t *testing.T) {
	r := newRig(t)
	m := mail("ck1")
	m.State = StateInput{Body: "> only a quote\n"}
	out := r.decide(m)
	if r.jev.count() != 0 || out.Called {
		t.Fatalf("a call was made for nothing: %d", r.jev.count())
	}
	ms := r.misses("ck1")
	if len(ms) != 2 || ms[0].Reason != store.MissNothingToRead {
		t.Fatalf("misses = %+v", ms)
	}
	r.decide(m)
	if r.jev.count() != 0 {
		t.Fatal("empty mail was reconsidered")
	}
}

func TestOffMeansOff(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(*rig){
		"account not opted in": func(r *rig) {
			if err := r.dbs.SetAccountSmart(ctx, rigAccount, false); err != nil {
				r.t.Fatal(err)
			}
		},
		"classify feature off": func(r *rig) {
			if err := r.gate.SetFeatureEnabled(ctx, rigAccount, CallFeature, false); err != nil {
				r.t.Fatal(err)
			}
		},
		"cap of zero": func(r *rig) {
			if err := r.gate.SetAccountCap(ctx, rigAccount, 0); err != nil {
				r.t.Fatal(err)
			}
		},
	}
	for name, off := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			off(r)
			_, err := r.engine.Decide(ctx, mail("ck1"))
			var refusal *llm.Refusal
			if !errors.As(err, &refusal) {
				t.Fatalf("err = %v, want a gate refusal", err)
			}
			if r.jev.count() != 0 {
				t.Fatalf("%d requests reached the provider", r.jev.count())
			}
			if len(r.decisions("ck1"))+len(r.misses("ck1")) != 0 {
				t.Fatal("a refusal left rows behind; turning the feature on later would find nothing to ask")
			}
		})
	}
}

func TestNilGateIsSafe(t *testing.T) {
	r := newRig(t)
	e := NewEngine(r.reg, nil, r.dbs)
	out, err := e.Decide(context.Background(), mail("ck1"))
	if err != nil || out.Called {
		t.Fatalf("nil gate: %+v, %v", out, err)
	}
}

func TestAHugeMessageIsClippedUnderTheCallLimit(t *testing.T) {
	r := newRig(t)
	m := mail("ck1")
	m.State.Body = strings.Repeat("a long sentence that goes on and on. ", 40_000) // ~1.4 MiB
	r.decide(m)
	if r.jev.count() != 1 {
		t.Fatalf("calls = %d", r.jev.count())
	}
	if n := len(r.jev.last().Raw); n > llm.MaxDecideBytes {
		t.Fatalf("request is %d bytes, over the %d the gate allows", n, llm.MaxDecideBytes)
	}
}

func TestEveryCallIsLedgered(t *testing.T) {
	r := newRig(t)
	r.decide(mail("ck1"))
	calls, err := r.dbs.RecentAPICalls(context.Background(), rigAccount, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Feature != CallFeature || calls[0].ContentKey != "ck1" || calls[0].CostUSD <= 0 {
		t.Fatalf("ledger = %+v", calls)
	}
}
