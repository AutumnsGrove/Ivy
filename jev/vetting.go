package jev

import (
	"context"
	"fmt"

	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// The two safety checks. A message is vetted, and so may be shown to a model that
// reads mail text, only when both have answered for the current wording and neither
// fired. Their wording lives in builtin.yaml like every other question's.
const (
	QuestionTripwire  = "injection_tripwire"
	QuestionSensitive = "sensitive_content"
)

var checkQuestions = []string{QuestionTripwire, QuestionSensitive}

// VettingStore is what the vetter reads: the cached answers and the operator's
// overrides.
type VettingStore interface {
	DecisionsFor(ctx context.Context, accountID, contentKey string) ([]store.Decision, error)
	VettingOverridden(ctx context.Context, accountID, contentKey string) (bool, error)
}

// Vetter implements llm.Vetting from what the Jev layer already cached. It stores
// nothing of its own: whether a message is withheld is a reading of its two check
// answers under the registry as it stands, so retuning a threshold or editing a
// check's wording changes the result with no flag to invalidate.
type Vetter struct {
	reg *Registry
	st  VettingStore
}

// NewVetter builds the vetter over the live registry.
func NewVetter(reg *Registry, st VettingStore) *Vetter { return &Vetter{reg: reg, st: st} }

var _ llm.Vetting = (*Vetter)(nil)

// Vetted reports whether a model that reads mail text may see this message. It fails
// closed everywhere: a check that is missing, switched off, scoped away from the
// account, unanswered, answered for older wording or another model, or recorded as
// a miss leaves the message withheld, and so does an error. Only the operator's
// override lifts it.
//
// Suppression is deliberately not applied: another question firing must never
// excuse a check that fired.
func (v *Vetter) Vetted(ctx context.Context, accountID, contentKey string) (bool, error) {
	s, err := v.Status(ctx, accountID, contentKey)
	if err != nil {
		return false, err
	}
	return s.State == VetClear || s.Overridden, nil
}

// VetState is what the checks found.
type VetState string

const (
	// VetClear: both checks answered and neither fired.
	VetClear VetState = "clear"
	// VetUnchecked: the layer cannot say yet (a check is missing, off, unanswered or
	// stale). Nothing was found, so the reader stays quiet; the gate still withholds.
	VetUnchecked VetState = "unchecked"
	// VetWithheld: a check fired.
	VetWithheld VetState = "withheld"
)

// VetStatus is a message's standing with the safety checks, for the reader.
type VetStatus struct {
	State VetState
	// Reasons are codes for the checks that fired: "injection", or the sensitive
	// check's own option ("credentials", "financial", "health", "id_document").
	Reasons []string
	// Overridden: the operator let helpers read it anyway. Reasons are kept, so the
	// reader can still say why it was withheld.
	Overridden bool
}

// Status reads where a message stands without changing anything. It is what Vetted
// is built on, so the reader and the gate cannot disagree.
func (v *Vetter) Status(ctx context.Context, accountID, contentKey string) (VetStatus, error) {
	overridden, err := v.st.VettingOverridden(ctx, accountID, contentKey)
	if err != nil {
		return VetStatus{}, fmt.Errorf("vetting: %w", err)
	}
	out := VetStatus{State: VetUnchecked, Overridden: overridden}
	runs := map[string]bool{}
	for _, q := range v.reg.Active(accountID, FolderInbox) {
		runs[q.ID] = true
	}
	for _, id := range checkQuestions {
		if !runs[id] {
			return out, nil
		}
	}
	model, ok := llm.LookupModel(llm.JevModelID)
	if !ok {
		return out, nil
	}
	ds, err := v.st.DecisionsFor(ctx, accountID, contentKey)
	if err != nil {
		return VetStatus{}, fmt.Errorf("vetting: %w", err)
	}
	passed := 0
	for _, verdict := range Evaluate(v.reg, ds, model.Slug) {
		switch {
		case !isCheck(verdict.QuestionID):
		case verdict.Fires && verdict.QuestionID == QuestionTripwire:
			out.Reasons = append(out.Reasons, "injection")
		case verdict.Fires:
			out.Reasons = append(out.Reasons, verdict.Choice)
		default:
			passed++
		}
	}
	switch {
	case len(out.Reasons) > 0:
		out.State = VetWithheld
	case passed == len(checkQuestions):
		out.State = VetClear
	}
	return out, nil
}

func isCheck(id string) bool { return id == QuestionTripwire || id == QuestionSensitive }
