package jev

import (
	"context"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

func shipped(t *testing.T) map[string]Question {
	t.Helper()
	qs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Question{}
	for _, q := range qs {
		out[q.ID] = q
	}
	return out
}

func wireBytes(q Question) int {
	n := len(q.ID) + len(q.Instructions)
	for k, v := range q.Criteria {
		n += len(k) + len(v)
	}
	return n
}

// The cost estimate prices a question at a fixed size. A shipped question that is
// longer would make every estimate understate, so each must fit the budget the
// estimate assumes.
func TestEveryShippedQuestionFitsTheEstimatesBudget(t *testing.T) {
	for id, q := range shipped(t) {
		if n := wireBytes(q); n > llm.QuestionBytesBudget {
			t.Errorf("%s is %d bytes on the wire, over the estimate's %d per question", id, n, llm.QuestionBytesBudget)
		}
	}
}

// 5c.1: the needs-me question and the companions that order and qualify it. All of
// them are written as what does not count, then the exceptions (docs/JEV.md).
func TestTheNeedsMeQuestionsAreShippedAndDark(t *testing.T) {
	want := map[string]struct {
		quiet     string
		options   []string
		feature   string
		threshold float64
	}{
		"needs_me":      {"none", []string{"none", "maybe", "likely"}, "needs_me", 0.65},
		"urgency":       {"normal", []string{"low", "normal", "high"}, "needs_me", 0.80},
		"has_deadline":  {"no", []string{"no", "yes"}, "needs_me", 0.80},
		"asks_question": {"no", []string{"no", "yes"}, "needs_me", 0.80},
		// Protects personal correspondence from every automated behaviour, so it is
		// useful beyond needs-me and rides the shared call on its own.
		"is_automated": {"no", []string{"no", "yes"}, "", 0.80},
	}
	got := shipped(t)
	for id, w := range want {
		q, ok := got[id]
		if !ok {
			t.Errorf("%s is not shipped", id)
			continue
		}
		if q.QuietOption != w.quiet || len(q.Criteria) != len(w.options) {
			t.Errorf("%s options = %v quiet %q; want %v quiet %q", id, q.Criteria, q.QuietOption, w.options, w.quiet)
		}
		for _, o := range w.options {
			if _, ok := q.Criteria[o]; !ok {
				t.Errorf("%s has no option %q", id, o)
			}
		}
		if q.Feature != w.feature {
			t.Errorf("%s feature = %q, want %q: needs-me ships dark behind its own switch", id, q.Feature, w.feature)
		}
		if q.Threshold != w.threshold {
			t.Errorf("%s threshold = %v, want %v", id, q.Threshold, w.threshold)
		}
		if !strings.Contains(strings.ToLower(q.Instructions), "do not count") {
			t.Errorf("%s instructions do not say what does not count", id)
		}
		if len(q.Suppresses) != 0 {
			t.Errorf("%s suppresses %v: a bill is automated and still needs the owner", id, q.Suppresses)
		}
	}
}

// With the shipped set, an account that has only turned classification on is asked
// the two checks and is_automated; switching needs_me on adds its four. Nothing in
// the needs-me group is asked before that.
func TestNeedsMeIsDarkUntilItsSwitchIsOn(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	qs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.reg.Set(qs, nil); err != nil {
		t.Fatal(err)
	}

	r.decide(mail("k1"))
	if got := len(r.jev.last().Questions); got != 3 {
		t.Fatalf("with needs_me off the call carries %d questions, want 3 (the checks and is_automated)", got)
	}
	if _, asked := r.jev.last().Questions["needs_me"]; asked {
		t.Fatal("needs_me was asked while its switch was off")
	}

	if err := r.gate.SetFeatureEnabled(ctx, rigAccount, "needs_me", true); err != nil {
		t.Fatal(err)
	}
	r.decide(mail("k2"))
	req := r.jev.last()
	if len(req.Questions) != 7 {
		t.Fatalf("with needs_me on the call carries %d questions, want 7", len(req.Questions))
	}
	for _, id := range []string{"needs_me", "urgency", "has_deadline", "asks_question"} {
		if _, ok := req.Questions[id]; !ok {
			t.Errorf("%s was not asked with needs_me on", id)
		}
	}
	if n := r.jev.count(); n != 2 {
		t.Errorf("provider calls = %d, want one per message", n)
	}
}

// The cost of the full shipped set, stated and held: 1,000 messages of a typical
// size at all seven questions is about a dime at worst ($0.11 when written, after
// the per-question budget went to 900 bytes), so turning needs-me on is not a money
// decision. This is the worst case the estimate gives, rounded up to the cent, not
// the average.
func TestTheShippedSetCostsCentsPerThousandMessages(t *testing.T) {
	r := newRig(t)
	est, err := r.gate.Estimate(context.Background(), llm.EstimateRequest{
		Feature: CallFeature, AccountID: rigAccount, Items: 1000, BytesPerItem: 4 << 10, Questions: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if est.USD <= 0 || est.USD > 0.15 {
		t.Fatalf("1,000 messages at 7 questions estimate $%.2f, want no more than $0.15", est.USD)
	}
	t.Logf("1,000 messages x 7 questions, 4 KiB each: worst case $%.2f", est.USD)
}

// The Needs attention view orders by the live urgency question: its current wording,
// the Jev model and the bar as tuned. Switched off or missing, nothing counts as urgent.
func TestTheUrgencyBarFollowsTheLiveQuestion(t *testing.T) {
	r := NewRegistry()
	qs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Set(qs, nil); err != nil {
		t.Fatal(err)
	}
	q, _ := r.Get("urgency")
	model, _ := llm.LookupModel(llm.JevModelID)
	want := store.UrgencyBar{QuestionID: "urgency", Hash: q.Hash(), Model: model.Slug, High: "high", Threshold: 0.80}
	if got := r.UrgencyBar(); got != want {
		t.Fatalf("bar = %+v, want %+v", got, want)
	}

	if err := r.SetTuning(Tuning{"urgency": {Threshold: f64(0.6)}}); err != nil {
		t.Fatal(err)
	}
	if got := r.UrgencyBar(); got.Threshold != 0.6 || got.Hash != q.Hash() {
		t.Fatalf("a retuned bar = %+v, want threshold 0.6 and the same wording hash", got)
	}

	if err := r.SetTuning(Tuning{"urgency": {Enabled: flag(false)}}); err != nil {
		t.Fatal(err)
	}
	if got := r.UrgencyBar(); got != (store.UrgencyBar{}) {
		t.Fatalf("with urgency off the bar = %+v, want none", got)
	}
	if got := NewRegistry().UrgencyBar(); got != (store.UrgencyBar{}) {
		t.Fatalf("an empty registry's bar = %+v, want none", got)
	}
}
