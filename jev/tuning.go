package jev

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// SettingTuning is the global setting that holds the operator's tuning as JSON.
const SettingTuning = "jev.tuning"

// Tune is the operator's adjustment of one question: how sure Jev must be, and
// whether it is asked at all. A nil field leaves the shipped value alone.
type Tune struct {
	Threshold *float64 `json:"threshold,omitempty"`
	Enabled   *bool    `json:"enabled,omitempty"`
}

// Tuning adjusts questions by id. It is deliberately the two knobs that never
// change what Jev is asked, so a retune takes effect at once, re-reads nothing and
// costs nothing: the wording and options, which do change the answer hash, stay in
// the shipped file (or the operator's own questions).
type Tuning map[string]Tune

// ParseTuning reads the setting's JSON. An empty value is no tuning. Unknown keys are
// refused, as in the question file, so a typo cannot silently drop a threshold.
func ParseTuning(s string) (Tuning, error) {
	if len(s) > MaxFileBytes {
		return nil, fmt.Errorf("tuning is %d bytes, over the %d limit", len(s), MaxFileBytes)
	}
	if strings.TrimSpace(s) == "" {
		return Tuning{}, nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	var t Tuning
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf("parse tuning: %w", err)
	}
	if t == nil {
		return nil, errors.New("parse tuning: not an object")
	}
	return t, nil
}

// apply returns the questions with the tuning laid over them, or an error naming the
// first thing wrong. It never changes its input.
func (t Tuning) apply(qs map[string]Question) (map[string]Question, error) {
	out := make(map[string]Question, len(qs))
	for id, q := range qs {
		out[id] = q
	}
	for id, tune := range t {
		q, ok := out[id]
		if !ok {
			return nil, fmt.Errorf("tuning names %q, which is not a question", id)
		}
		if tune.Threshold != nil {
			th := *tune.Threshold
			if !(th > 0 && th <= 1) {
				return nil, fmt.Errorf("tuning of %q: threshold %v is outside (0, 1]", id, th)
			}
			q.Threshold = th
		}
		if tune.Enabled != nil {
			on := *tune.Enabled
			q.Enabled = &on
		}
		out[id] = q
	}
	return out, nil
}

// UrgencyBar is how the Needs attention view recognises an urgent message under the
// live registry: the urgency question's current wording hash, the Jev model and the
// bar as tuned. It is the zero value, ranking nothing, when the question is missing
// or switched off.
func (r *Registry) UrgencyBar() store.UrgencyBar {
	q, ok := r.Get("urgency")
	if !ok || !q.IsEnabled() {
		return store.UrgencyBar{}
	}
	model, ok := llm.LookupModel(llm.JevModelID)
	if !ok {
		return store.UrgencyBar{}
	}
	return store.UrgencyBar{QuestionID: q.ID, Hash: q.Hash(), Model: model.Slug, High: "high", Threshold: q.Threshold}
}

// SetTuning replaces the live tuning. It is checked whole against the current
// questions first; on any error the registry is untouched.
func (r *Registry) SetTuning(t Tuning) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	live, err := t.apply(r.base)
	if err != nil {
		return err
	}
	r.tuning, r.set = t, live
	return nil
}
