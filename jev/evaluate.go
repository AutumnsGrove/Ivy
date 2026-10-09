package jev

import (
	"context"
	"fmt"
	"sort"

	"github.com/AutumnsGrove/Ivy/store"
)

// Verdict is what one stored answer means under the registry as it stands now. It is
// computed on read from the full probability vector, so retuning a threshold or a
// suppression changes the verdicts without a new call.
type Verdict struct {
	QuestionID    string
	Choice        string
	Probabilities map[string]float64
	Confidence    float64
	Threshold     float64
	QuietOption   string
	// Fires: the choice is not the quiet option and both its probability and the
	// answer's confidence reach the threshold.
	Fires bool
	// Suppressed: another question that fires lists this one in Suppresses.
	Suppressed bool
	CostUSD    float64
}

// Acts reports whether the verdict may drive a chip, a local tag or a sort order. It
// is the only question a feature should ask of a Verdict, and even then it is a hint:
// nothing here moves, hides or deletes mail (brief invariant 6).
func (v Verdict) Acts() bool { return v.Fires && !v.Suppressed }

// Evaluate turns cached answers into verdicts. Only answers to the question's
// current wording and the given model count, so an edited question reads as
// unanswered until it is asked again, and only enabled questions are judged.
// Suppression is decided from who fires before it is applied to anyone, so two
// questions that suppress each other do not depend on evaluation order.
func Evaluate(reg *Registry, ds []store.Decision, model string) []Verdict {
	var out []Verdict
	for _, q := range reg.All() {
		if !q.IsEnabled() {
			continue
		}
		hash := q.Hash()
		for _, d := range ds {
			if d.QuestionID != q.ID || d.InstructionHash != hash || d.Model != model {
				continue
			}
			out = append(out, Verdict{
				QuestionID: q.ID, Choice: d.Choice, Probabilities: d.Probabilities,
				Confidence: d.Confidence, Threshold: q.Threshold, QuietOption: q.QuietOption,
				Fires:   d.Choice != q.QuietOption && d.Probabilities[d.Choice] >= q.Threshold && d.Confidence >= q.Threshold,
				CostUSD: d.CostUSD,
			})
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QuestionID < out[j].QuestionID })

	suppressed := map[string]bool{}
	for _, v := range out {
		if !v.Fires {
			continue
		}
		q, _ := reg.Get(v.QuestionID)
		for _, s := range q.Suppresses {
			suppressed[s] = true
		}
	}
	for i := range out {
		out[i].Suppressed = suppressed[out[i].QuestionID]
	}
	return out
}

// Unanswered is a question that was asked, or considered, and has no answer, with
// the reason recorded at the time.
type Unanswered struct {
	QuestionID string
	Reason     string
}

// Odds is everything the odds sheet shows for one message.
type Odds struct {
	Model      string
	Answers    []Verdict
	Unanswered []Unanswered
}

// Odds reads a message's cached answers as verdicts under the registry as it stands.
// It makes no request and is available for every classified message; a message never
// classified, or an engine with no gate, yields an empty sheet rather than an error.
func (e *Engine) Odds(ctx context.Context, accountID, contentKey string) (Odds, error) {
	if e.gate == nil {
		return Odds{}, nil
	}
	model, err := e.gate.ModelFor(ctx, CallFeature)
	if err != nil {
		return Odds{}, fmt.Errorf("jev model: %w", err)
	}
	ds, err := e.store.DecisionsFor(ctx, accountID, contentKey)
	if err != nil {
		return Odds{}, err
	}
	ms, err := e.store.DecisionMissesFor(ctx, accountID, contentKey)
	if err != nil {
		return Odds{}, err
	}
	out := Odds{Model: model.Slug, Answers: Evaluate(e.reg, ds, model.Slug)}
	for _, q := range e.reg.All() {
		if !q.IsEnabled() {
			continue
		}
		for _, m := range ms {
			if m.QuestionID == q.ID && m.InstructionHash == q.Hash() && m.Model == model.Slug {
				out.Unanswered = append(out.Unanswered, Unanswered{QuestionID: q.ID, Reason: m.Reason})
				break
			}
		}
	}
	return out, nil
}
