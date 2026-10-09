package jev

import (
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/store"
)

func evalRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Set(mustParse(t, strings.Replace(validYAML, "enabled: false", "enabled: true", 1)), nil); err != nil {
		t.Fatal(err)
	}
	return r
}

func dec(r *Registry, id, model, choice string, p, conf float64) store.Decision {
	q, _ := r.Get(id)
	probs := map[string]float64{choice: p}
	return store.Decision{
		AccountID: "a", ContentKey: "k", QuestionID: id, InstructionHash: q.Hash(), Model: model,
		Choice: choice, Probabilities: probs, Confidence: conf,
	}
}

func verdictFor(vs []Verdict, id string) Verdict {
	for _, v := range vs {
		if v.QuestionID == id {
			return v
		}
	}
	return Verdict{}
}

func TestAQuestionFiresOnlyOnANonQuietChoiceThatClearsTheThreshold(t *testing.T) {
	r := evalRegistry(t) // needs_me: threshold 0.8, quiet "none"
	cases := []struct {
		name      string
		choice    string
		p, conf   float64
		wantFires bool
	}{
		{"clears both", "likely", 0.9, 0.9, true},
		{"exactly at the threshold", "likely", 0.8, 0.8, true},
		{"probability just under", "likely", 0.79, 0.95, false},
		{"confidence just under", "likely", 0.95, 0.79, false},
		{"the quiet option never fires", "none", 1, 1, false},
		{"maybe is a real option", "maybe", 0.85, 0.85, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vs := Evaluate(r, []store.Decision{dec(r, "needs_me", "m", c.choice, c.p, c.conf)}, "m")
			v := verdictFor(vs, "needs_me")
			if v.Fires != c.wantFires || v.Acts() != c.wantFires {
				t.Fatalf("fires=%v acts=%v, want %v", v.Fires, v.Acts(), c.wantFires)
			}
		})
	}
}

func TestAFiringQuestionSuppressesTheOnesItNames(t *testing.T) {
	r := evalRegistry(t) // needs_me suppresses urgency; urgency threshold 0.75, quiet "low"
	both := []store.Decision{
		dec(r, "needs_me", "m", "likely", 0.95, 0.95),
		dec(r, "urgency", "m", "high", 0.95, 0.95),
	}
	vs := Evaluate(r, both, "m")
	if u := verdictFor(vs, "urgency"); !u.Fires || !u.Suppressed || u.Acts() {
		t.Fatalf("urgency should fire but be held back: %+v", u)
	}
	if n := verdictFor(vs, "needs_me"); !n.Acts() {
		t.Fatalf("the suppressor itself should act: %+v", n)
	}

	// A suppressor that does not clear its own threshold suppresses nothing.
	weak := []store.Decision{
		dec(r, "needs_me", "m", "likely", 0.5, 0.95),
		dec(r, "urgency", "m", "high", 0.95, 0.95),
	}
	if u := verdictFor(Evaluate(r, weak, "m"), "urgency"); u.Suppressed || !u.Acts() {
		t.Fatalf("a quiet suppressor held something back: %+v", u)
	}
	// Nor does one whose answer is the quiet option.
	quiet := []store.Decision{
		dec(r, "needs_me", "m", "none", 1, 1),
		dec(r, "urgency", "m", "high", 0.95, 0.95),
	}
	if u := verdictFor(Evaluate(r, quiet, "m"), "urgency"); u.Suppressed {
		t.Fatalf("a quiet answer suppressed: %+v", u)
	}
}

func TestSuppressionIsDecidedBeforeItIsApplied(t *testing.T) {
	// Two questions that suppress each other must not cancel by evaluation order:
	// both fire, so both are held back, whichever is looked at first.
	a := Question{ID: "a", Instructions: "x", Criteria: map[string]string{"no": "n", "yes": "y"}, QuietOption: "no", Threshold: 0.5, Suppresses: []string{"b"}}
	b := Question{ID: "b", Instructions: "x2", Criteria: map[string]string{"no": "n", "yes": "y"}, QuietOption: "no", Threshold: 0.5, Suppresses: []string{"a"}}
	r := NewRegistry()
	if err := r.Set([]Question{a, b}, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		vs := Evaluate(r, []store.Decision{dec(r, "a", "m", "yes", 1, 1), dec(r, "b", "m", "yes", 1, 1)}, "m")
		if verdictFor(vs, "a").Acts() || verdictFor(vs, "b").Acts() {
			t.Fatalf("mutual suppression depended on order: %+v", vs)
		}
	}
}

func TestEvaluateUsesOnlyCurrentWordingAndModel(t *testing.T) {
	r := evalRegistry(t)
	stale := dec(r, "needs_me", "m", "likely", 1, 1)
	stale.InstructionHash = "an-older-wording"
	otherModel := dec(r, "needs_me", "other", "likely", 1, 1)
	if vs := Evaluate(r, []store.Decision{stale, otherModel}, "m"); len(vs) != 0 {
		t.Fatalf("an answer to a different wording or model was used: %+v", vs)
	}
}

func TestEvaluateIgnoresUnknownAndDisabledQuestions(t *testing.T) {
	r := NewRegistry()
	if err := r.Set(mustParse(t, validYAML), nil); err != nil { // urgency stays disabled
		t.Fatal(err)
	}
	q, _ := r.Get("urgency")
	d := dec(r, "urgency", "m", "high", 1, 1)
	d.InstructionHash = q.Hash()
	gone := dec(r, "needs_me", "m", "likely", 1, 1)
	gone.QuestionID = "deleted_question"
	vs := Evaluate(r, []store.Decision{d, gone}, "m")
	if len(vs) != 0 {
		t.Fatalf("verdicts for a disabled or deleted question: %+v", vs)
	}
}

func TestEvaluateAppliesTheCurrentThresholdToTheStoredOdds(t *testing.T) {
	// The odds are stored once; raising a threshold flips the verdict with no new call.
	r := evalRegistry(t)
	d := dec(r, "needs_me", "m", "likely", 0.85, 0.85)
	if !verdictFor(Evaluate(r, []store.Decision{d}, "m"), "needs_me").Fires {
		t.Fatal("should fire at 0.8")
	}
	q, _ := r.Get("needs_me")
	q.Threshold = 0.9
	if err := r.Set(mustParse(t, strings.Replace(validYAML, "enabled: false", "enabled: true", 1)), []Question{q}); err != nil {
		t.Fatal(err)
	}
	if verdictFor(Evaluate(r, []store.Decision{d}, "m"), "needs_me").Fires {
		t.Fatal("should be quiet at 0.9 with the same stored answer")
	}
}

func TestEvaluateReturnsVerdictsInQuestionOrder(t *testing.T) {
	r := evalRegistry(t)
	vs := Evaluate(r, []store.Decision{dec(r, "urgency", "m", "low", 1, 1), dec(r, "needs_me", "m", "none", 1, 1)}, "m")
	if len(vs) != 2 || vs[0].QuestionID != "needs_me" || vs[1].QuestionID != "urgency" {
		t.Fatalf("order = %+v", vs)
	}
}
