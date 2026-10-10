package jev

import (
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }
func flag(v bool) *bool      { return &v }

func tunedRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Set(mustParse(t, checksYAML), nil); err != nil {
		t.Fatal(err)
	}
	return r
}

// Tuning moves a threshold or switches a question off without touching its wording,
// so the answer hash is unchanged and nothing already paid for is asked again.
func TestTuningChangesTheThresholdButNotTheHash(t *testing.T) {
	r := tunedRegistry(t)
	before, _ := r.Get(QuestionTripwire)
	if err := r.SetTuning(Tuning{QuestionTripwire: {Threshold: f64(0.3)}}); err != nil {
		t.Fatal(err)
	}
	after, _ := r.Get(QuestionTripwire)
	if after.Threshold != 0.3 {
		t.Fatalf("threshold = %v, want 0.3", after.Threshold)
	}
	if after.Hash() != before.Hash() {
		t.Fatal("tuning a threshold changed the hash, which would re-ask every cached message")
	}
	other, _ := r.Get(QuestionSensitive)
	if other.Threshold != 0.5 {
		t.Fatalf("an untuned question moved to %v", other.Threshold)
	}
}

func TestTuningCanSwitchAQuestionOffAndOn(t *testing.T) {
	r := tunedRegistry(t)
	if err := r.SetTuning(Tuning{QuestionSensitive: {Enabled: flag(false)}}); err != nil {
		t.Fatal(err)
	}
	if q, _ := r.Get(QuestionSensitive); q.IsEnabled() {
		t.Fatal("still enabled")
	}
	if err := r.SetTuning(Tuning{}); err != nil {
		t.Fatal(err)
	}
	if q, _ := r.Get(QuestionSensitive); !q.IsEnabled() {
		t.Fatal("clearing the tuning did not restore the shipped setting")
	}
}

// A bad tuning is refused whole and the live registry keeps what it had, so a typo
// can neither leave a check unconfigured nor zero a threshold.
func TestABadTuningLeavesTheRegistryUntouched(t *testing.T) {
	r := tunedRegistry(t)
	if err := r.SetTuning(Tuning{QuestionTripwire: {Threshold: f64(0.2)}}); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]Tuning{
		"zero":      {QuestionTripwire: {Threshold: f64(0)}},
		"over one":  {QuestionTripwire: {Threshold: f64(1.5)}},
		"unknown":   {"no_such_question": {Threshold: f64(0.5)}},
		"half good": {QuestionSensitive: {Threshold: f64(0.9)}, QuestionTripwire: {Threshold: f64(-1)}},
	} {
		if err := r.SetTuning(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if q, _ := r.Get(QuestionTripwire); q.Threshold != 0.2 {
		t.Errorf("threshold = %v after refused tunings, want the earlier 0.2", q.Threshold)
	}
	if q, _ := r.Get(QuestionSensitive); q.Threshold != 0.5 {
		t.Errorf("a refused tuning half-applied: %v", q.Threshold)
	}
}

// Replacing the question set keeps the operator's tuning, so a reload does not
// quietly undo it.
func TestTuningSurvivesAQuestionSetReload(t *testing.T) {
	r := tunedRegistry(t)
	if err := r.SetTuning(Tuning{QuestionTripwire: {Threshold: f64(0.3)}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(mustParse(t, checksYAML), nil); err != nil {
		t.Fatal(err)
	}
	if q, _ := r.Get(QuestionTripwire); q.Threshold != 0.3 {
		t.Fatalf("threshold = %v after a reload, want 0.3", q.Threshold)
	}
}

func TestParseTuning(t *testing.T) {
	got, err := ParseTuning(`{"injection_tripwire":{"threshold":0.4},"sensitive_content":{"enabled":false}}`)
	if err != nil {
		t.Fatal(err)
	}
	if *got[QuestionTripwire].Threshold != 0.4 || *got[QuestionSensitive].Enabled {
		t.Fatalf("parsed wrong: %+v", got)
	}
	if empty, err := ParseTuning(""); err != nil || len(empty) != 0 {
		t.Fatalf("an unset setting = %v, %v; want empty", empty, err)
	}
	for _, bad := range []string{`{`, `{"x":{"colour":1}}`, `[]`, strings.Repeat(" ", MaxFileBytes+1)} {
		if _, err := ParseTuning(bad); err == nil {
			t.Errorf("accepted %.20q", bad)
		}
	}
}
