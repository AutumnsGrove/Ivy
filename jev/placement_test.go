package jev

import (
	"context"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
)

// placementRig runs the real worker over the shipped questions with the category
// switch on, answering `category` as the test says and everything else quietly.
func placementRig(t *testing.T, answer answerJSON) *workerRig {
	t.Helper()
	w := newWorkerRig(t, WorkerOptions{})
	qs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.reg.Set(qs, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.gate.SetFeatureEnabled(context.Background(), rigAccount, "category", true); err != nil {
		t.Fatal(err)
	}
	w.jev.reply = func(req jevRequest) map[string]answerJSON {
		out := map[string]answerJSON{}
		for id, q := range req.Questions {
			if id == "category" {
				out[id] = answer
				continue
			}
			for _, quiet := range []string{"clear", "none", "no", "other"} {
				if _, ok := q.Criteria[quiet]; ok {
					out[id] = answerJSON{Choice: quiet, Probabilities: map[string]float64{quiet: 1}, Confidence: 1}
					break
				}
			}
		}
		return out
	}
	return w
}

func (w *workerRig) classifyOne(key string) {
	w.t.Helper()
	w.tick() // the first pass only sets the watermark
	w.arrive("m-"+key, key, store.RoleInbox, epoch.Add(time.Minute), "Subject "+key, "Some body text")
	w.clock = epoch.Add(time.Hour)
	if n := w.tick(); n != 1 {
		w.t.Fatalf("processed %d messages, want 1", n)
	}
}

func (w *workerRig) tagNames(key string) []string {
	w.t.Helper()
	got, err := w.dbs.TagsForMessages(context.Background(), rigAccount, []string{key})
	if err != nil {
		w.t.Fatal(err)
	}
	var names []string
	for _, tag := range got[key] {
		names = append(names, tag.Name)
	}
	return names
}

func TestACategoryThatClearsItsBarPlacesItsTag(t *testing.T) {
	w := placementRig(t, answerJSON{Choice: "newsletter", Probabilities: map[string]float64{"newsletter": 0.95, "other": 0.05}, Confidence: 0.95})
	w.classifyOne("k1")
	if got := w.tagNames("k1"); len(got) != 1 || got[0] != "Newsletter" {
		t.Fatalf("tags = %v, want [Newsletter]", got)
	}
}

func TestACategoryThatDoesNotPlaceNothingOrFallsShortPlacesNothing(t *testing.T) {
	cases := map[string]answerJSON{
		"below the bar":            {Choice: "newsletter", Probabilities: map[string]float64{"newsletter": 0.6, "other": 0.4}, Confidence: 0.9},
		"unsure of itself":         {Choice: "newsletter", Probabilities: map[string]float64{"newsletter": 0.9, "other": 0.1}, Confidence: 0.5},
		"personal is never tagged": {Choice: "personal", Probabilities: map[string]float64{"personal": 0.99, "other": 0.01}, Confidence: 1},
		"other says nothing":       {Choice: "other", Probabilities: map[string]float64{"other": 1}, Confidence: 1},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			w := placementRig(t, answer)
			w.classifyOne("k1")
			if got := w.tagNames("k1"); len(got) != 0 {
				t.Fatalf("tags = %v, want none", got)
			}
		})
	}
}

func TestNothingIsPlacedWhileCategoriesAreOff(t *testing.T) {
	w := placementRig(t, answerJSON{Choice: "newsletter", Probabilities: map[string]float64{"newsletter": 1}, Confidence: 1})
	if err := w.gate.SetFeatureEnabled(context.Background(), rigAccount, "category", false); err != nil {
		t.Fatal(err)
	}
	w.classifyOne("k1")
	if got := w.tagNames("k1"); len(got) != 0 {
		t.Fatalf("tags = %v with the switch off", got)
	}
}

// Removing a placed tag is the operator saying no, so it must stay removed: the
// message is decided once and never placed again.
func TestARemovedCategoryTagStaysRemoved(t *testing.T) {
	w := placementRig(t, answerJSON{Choice: "receipt", Probabilities: map[string]float64{"receipt": 0.97}, Confidence: 0.97})
	w.classifyOne("k1")
	if got := w.tagNames("k1"); len(got) != 1 || got[0] != "Receipt" {
		t.Fatalf("tags = %v, want [Receipt]", got)
	}
	if err := w.dbs.UntagMessage(context.Background(), rigAccount, "k1", "t-cat-receipt"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		w.clock = w.clock.Add(time.Hour)
		w.tick()
	}
	if got := w.tagNames("k1"); len(got) != 0 {
		t.Fatalf("tags = %v after removal and more passes; the tag came back", got)
	}
	if w.jev.count() != 1 {
		t.Fatalf("%d provider calls, want 1: the message is decided once", w.jev.count())
	}
}
