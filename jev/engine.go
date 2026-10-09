package jev

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// CallFeature is the gate feature every shared Jev call runs under: its switch,
// its ledger rows and its caps. A question that names a feature of its own is asked
// only while that switch is also on.
const CallFeature = "classify"

// stateMargin keeps the request clear of the gate's limit after JSON framing.
const stateMargin = 2 << 10

// ErrQuestionsTooLarge means the questions alone leave no room for a message. It is
// a configuration problem, not a property of any one message, so nothing is recorded
// against the message.
var ErrQuestionsTooLarge = errors.New("jev: the question set leaves no room for a message")

// Gate is what the engine needs of llm.Gate, declared here at the point of use.
type Gate interface {
	Decide(ctx context.Context, req llm.DecideRequest) (llm.DecideResult, error)
	ModelFor(ctx context.Context, feature string) (llm.Model, error)
	FeatureEnabled(ctx context.Context, accountID, feature string) bool
}

// Store is the cache the engine reads and writes.
type Store interface {
	DecisionsFor(ctx context.Context, accountID, contentKey string) ([]store.Decision, error)
	DecisionMissesFor(ctx context.Context, accountID, contentKey string) ([]store.DecisionMiss, error)
	PutDecisions(ctx context.Context, ds []store.Decision) error
	PutDecisionMisses(ctx context.Context, ms []store.DecisionMiss) error
}

// Message is one message to classify. The account and content key identify it in
// the cache; the folder only selects which questions apply.
type Message struct {
	AccountID  string
	ContentKey string
	Folder     string
	State      StateInput
}

// Outcome says what a Decide did, for the worker and the stats panel.
type Outcome struct {
	// Asked is how many questions went to Jev; Cached is how many were already known.
	Asked, Cached int
	// Called is true when a provider call was made, whatever it returned.
	Called  bool
	CostUSD float64
}

// Engine asks the registry's questions about a message, once. It owns no provider
// client: everything it spends goes through the gate.
type Engine struct {
	reg   *Registry
	gate  Gate
	store Store
	now   func() time.Time
}

// NewEngine builds an engine. A nil gate is safe: nothing is asked, which is the
// nil-client rule (the app is otherwise unchanged).
func NewEngine(reg *Registry, gate Gate, st Store) *Engine {
	return &Engine{reg: reg, gate: gate, store: st, now: time.Now}
}

// pending returns the active questions that may be asked for this message: right
// account, right folder, and their own feature switch on.
func (e *Engine) pending(ctx context.Context, m Message) []Question {
	var out []Question
	for _, q := range e.reg.Active(m.AccountID, m.Folder) {
		if q.Feature != "" && (e.gate == nil || !e.gate.FeatureEnabled(ctx, m.AccountID, q.Feature)) {
			continue
		}
		out = append(out, q)
	}
	return out
}

// QuestionCount is how many distinct questions could be asked about this account's
// mail in the folders that are classified, for sizing a backfill.
func (e *Engine) QuestionCount(accountID string) int {
	ids := map[string]bool{}
	for _, folder := range []string{FolderInbox, FolderJunk} {
		for _, q := range e.reg.Active(accountID, folder) {
			ids[q.ID] = true
		}
	}
	return len(ids)
}

// Applicable reports whether any question could be asked about this account's mail.
func (e *Engine) Applicable(accountID string) bool { return e.QuestionCount(accountID) > 0 }

// Decide answers every applicable question about a message that is not already
// answered for the current wording and model, in one provider call. Re-running it
// on a known message makes no request. A gate refusal (the account or feature is
// off, a cap is reached) is returned and nothing is recorded, so the message is
// still asked once the refusal lifts.
func (e *Engine) Decide(ctx context.Context, m Message) (Outcome, error) {
	var out Outcome
	if e.gate == nil {
		return out, nil
	}
	active := e.pending(ctx, m)
	if len(active) == 0 {
		return out, nil
	}
	model, err := e.gate.ModelFor(ctx, CallFeature)
	if err != nil {
		return out, fmt.Errorf("jev model: %w", err)
	}
	settled, err := e.settled(ctx, m, model.Slug)
	if err != nil {
		return out, err
	}
	var todo []Question
	for _, q := range active {
		if settled[q.ID+"\x00"+q.Hash()] {
			out.Cached++
			continue
		}
		todo = append(todo, q)
	}
	if len(todo) == 0 {
		return out, nil
	}

	wire := make(map[string]llm.Question, len(todo))
	questionBytes := 0
	for _, q := range todo {
		w := q.ToLLM()
		wire[q.ID] = w
		questionBytes += len(q.ID) + len(w.Type) + len(w.Instructions)
		for k, v := range w.Criteria {
			questionBytes += len(k) + len(v)
		}
	}
	budget := llm.MaxDecideBytes - questionBytes - stateMargin
	state, ok := BuildState(m.State, budget)
	if !ok {
		if budget < MinStateBytes {
			return out, ErrQuestionsTooLarge
		}
		return out, e.miss(ctx, m, model.Slug, todo, store.MissNothingToRead)
	}

	res, err := e.gate.Decide(ctx, llm.DecideRequest{
		Feature: CallFeature, AccountIDs: []string{m.AccountID}, ContentKeys: []string{m.ContentKey},
		Model: model.Slug, State: state.Text, Questions: wire,
	})
	out.Asked = len(todo)
	switch {
	case err == nil:
	case errors.Is(err, llm.ErrTooLarge):
		return out, e.miss(ctx, m, model.Slug, todo, store.MissTooLarge)
	case isInputRefusal(err):
		out.Called = true
		return out, e.miss(ctx, m, model.Slug, todo, store.MissRejected)
	default:
		out.Asked = 0
		return out, err
	}
	out.Called, out.CostUSD = true, res.CostUSD
	return out, e.record(ctx, m, model.Slug, todo, res)
}

// isInputRefusal is a provider 400, 413 or 422: it will not take this input, and
// asking again only repeats the refusal. An outage, a rate limit or a bad key is
// not, and stays an error to retry.
func isInputRefusal(err error) bool {
	var status *llm.StatusError
	if !errors.As(err, &status) {
		return false
	}
	switch status.Status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// settled lists the (question, wording) pairs already answered or recorded as
// unanswerable for this model.
func (e *Engine) settled(ctx context.Context, m Message, model string) (map[string]bool, error) {
	ds, err := e.store.DecisionsFor(ctx, m.AccountID, m.ContentKey)
	if err != nil {
		return nil, err
	}
	ms, err := e.store.DecisionMissesFor(ctx, m.AccountID, m.ContentKey)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ds)+len(ms))
	for _, d := range ds {
		if d.Model == model {
			out[d.QuestionID+"\x00"+d.InstructionHash] = true
		}
	}
	for _, x := range ms {
		if x.Model == model {
			out[x.QuestionID+"\x00"+x.InstructionHash] = true
		}
	}
	return out, nil
}

func (e *Engine) miss(ctx context.Context, m Message, model string, qs []Question, reason string) error {
	return e.store.PutDecisionMisses(ctx, e.misses(m, model, qs, reason))
}

func (e *Engine) misses(m Message, model string, qs []Question, reason string) []store.DecisionMiss {
	at := e.now()
	out := make([]store.DecisionMiss, 0, len(qs))
	for _, q := range qs {
		out = append(out, store.DecisionMiss{
			AccountID: m.AccountID, ContentKey: m.ContentKey, QuestionID: q.ID,
			InstructionHash: q.Hash(), Model: model, Reason: reason, At: at,
		})
	}
	return out
}

// record validates each asked question's answer against its own option set and
// stores it, or records why it could not. An answer for a question that was not
// asked is ignored. Model output is data: a choice or probability outside what the
// question allows is never stored as an answer.
func (e *Engine) record(ctx context.Context, m Message, model string, qs []Question, res llm.DecideResult) error {
	at := e.now()
	share := res.CostUSD / float64(len(qs))
	var ds []store.Decision
	var bad []store.DecisionMiss
	for _, q := range qs {
		a, ok := res.Answers[q.ID]
		switch {
		case !ok:
			bad = append(bad, e.misses(m, model, []Question{q}, store.MissNoAnswer)...)
		case !validAnswer(q, a):
			bad = append(bad, e.misses(m, model, []Question{q}, store.MissInvalidAnswer)...)
		default:
			ds = append(ds, store.Decision{
				AccountID: m.AccountID, ContentKey: m.ContentKey, QuestionID: q.ID,
				InstructionHash: q.Hash(), Model: model, Choice: a.Choice,
				Probabilities: a.Probabilities, Confidence: a.Confidence, CostUSD: share, DecidedAt: at,
			})
		}
	}
	// The call is already paid for, so a failed write is logged with the ids that
	// matter and returned; the cap bounds what a retry can cost.
	if err := e.store.PutDecisions(ctx, ds); err != nil {
		slog.ErrorContext(ctx, "jev: a paid answer could not be stored", "account", m.AccountID, "content_key", m.ContentKey, "error", err)
		return err
	}
	return e.store.PutDecisionMisses(ctx, bad)
}

// validAnswer holds the answer to the question's own options: the choice is one of
// them, every probability is for one of them and between 0 and 1, and confidence is
// between 0 and 1. It checks shape, not calibration.
func validAnswer(q Question, a llm.Answer) bool {
	if _, ok := q.Criteria[a.Choice]; !ok {
		return false
	}
	if !unit(a.Confidence) {
		return false
	}
	for opt, p := range a.Probabilities {
		if _, ok := q.Criteria[opt]; !ok || !unit(p) {
			return false
		}
	}
	return true
}

func unit(f float64) bool { return !math.IsNaN(f) && f >= 0 && f <= 1 }
