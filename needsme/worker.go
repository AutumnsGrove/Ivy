package needsme

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/AutumnsGrove/Ivy/jev"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

const (
	// Feature is the gate feature stage two runs under: its switch, ledger rows and caps.
	Feature = "needs_me_stage2"
	// StageOne is the Jev question whose flag feeds this stage.
	StageOne = "needs_me"

	// stateBudget bounds the text one message contributes to a chat call. The state is
	// already stripped of markup and clipped; this keeps a long thread cheap.
	stateBudget = 24 << 10
	// maxReplyTokens bounds the model's answer: one short object.
	maxReplyTokens = 200
	// withheldPause is how long a message the safety checks withheld is left alone
	// before it is looked at again. The operator's override ends the pause at once.
	withheldPause = 15 * time.Minute
)

// promptVersion identifies the instructions stage two sends, so changing them makes
// earlier failed attempts count as for a different prompt and the messages try again.
func promptVersion() string {
	sum := sha256.Sum256([]byte(system))
	return hex.EncodeToString(sum[:8])
}

// Gate is what the worker needs of llm.Gate, declared here at the point of use.
type Gate interface {
	Complete(ctx context.Context, req llm.CompleteRequest) (llm.CompleteResult, error)
	ModelFor(ctx context.Context, feature string) (llm.Model, error)
	CanRun(ctx context.Context, accountID, feature string) bool
}

// Store is the queue and the verdicts.
type Store interface {
	ListAccounts(ctx context.Context) ([]store.Account, error)
	Stage2Candidates(ctx context.Context, accountID string, q store.Stage2Query, limit int) ([]store.Candidate, error)
	PutNeedsMe(ctx context.Context, r store.NeedsMeRow) error
	PlaceNeedsTag(ctx context.Context, accountID, contentKey string) error
	PutDecisionMisses(ctx context.Context, ms []store.DecisionMiss) error
}

// Worker turns Jev's cheap flags into verdicts and reasons, in the background:
// a bounded batch per pass, one message per request, silent for an account that is
// off, and it only ever writes a verdict row. It moves, hides and deletes nothing.
type Worker struct {
	reg  *jev.Registry
	gate Gate
	st   Store
	vet  llm.Vetting
	opts jev.WorkerOptions
	now  func() time.Time
}

// NewWorker builds the stage-two worker. vet is the same vetting the gate applies; the
// worker asks it first only so withheld mail is skipped without a refused-call ledger
// row each pass. The gate still enforces it. A nil gate is safe: nothing is asked.
func NewWorker(reg *jev.Registry, gate Gate, st Store, vet llm.Vetting, opts jev.WorkerOptions) *Worker {
	return &Worker{reg: reg, gate: gate, st: st, vet: vet, opts: opts.Resolved(), now: time.Now}
}

// Run passes until the context ends, pacing itself like the classifier.
func (w *Worker) Run(ctx context.Context) error {
	failures := 0
	for {
		n, err := w.RunOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			failures++
			slog.WarnContext(ctx, "needsme: a stage two pass failed", "error", err, "failures", failures)
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.opts.NextDelay(err, n, failures)):
		}
	}
}

// RunOnce makes one bounded pass over every account and returns how many messages got
// an outcome that stays (a verdict, or a recorded failure). The first error is returned
// after every account has had its turn, so one account's cap does not stop another's.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	if w.gate == nil {
		return 0, nil
	}
	accounts, err := w.st.ListAccounts(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	var first error
	for _, a := range accounts {
		n, err := w.account(ctx, a.ID)
		total += n
		if err != nil && first == nil {
			first = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return total, first
}

func (w *Worker) account(ctx context.Context, id string) (int, error) {
	if !w.gate.CanRun(ctx, id, Feature) {
		return 0, nil
	}
	q, ok := w.reg.Get(StageOne)
	if !ok || !q.IsEnabled() {
		return 0, nil
	}
	jevModel, ok := llm.LookupModel(llm.JevModelID)
	if !ok {
		return 0, nil
	}
	chat, err := w.gate.ModelFor(ctx, Feature)
	if err != nil {
		return 0, err
	}
	query := store.Stage2Query{
		QuestionID: q.ID, Hash: q.Hash(), Model: jevModel.Slug, Quiet: q.QuietOption, Threshold: q.Threshold,
		StageQuestion: Feature, StageHash: promptVersion(), StageModel: chat.Slug,
		RetryWithheldBefore: w.now().Add(-withheldPause),
	}
	batch, err := w.st.Stage2Candidates(ctx, id, query, w.opts.Batch)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, c := range batch {
		settled, err := w.one(ctx, id, c, chat, query)
		if settled {
			done++
		}
		if errors.Is(err, errStop) {
			return done, nil
		}
		if err != nil {
			return done, err
		}
		if ctx.Err() != nil {
			return done, nil
		}
	}
	return done, nil
}

// one decides a single message. settled is true when it now has an outcome that keeps
// it out of the queue and counts as work done. A non-nil error ends the account's pass.
func (w *Worker) one(ctx context.Context, account string, c store.Candidate, chat llm.Model, q store.Stage2Query) (settled bool, err error) {
	key := c.Message.ContentKey
	miss := func(reason string) error {
		return w.st.PutDecisionMisses(ctx, []store.DecisionMiss{{
			AccountID: account, ContentKey: key, QuestionID: Feature,
			InstructionHash: q.StageHash, Model: chat.Slug, Reason: reason, At: w.now(),
		}})
	}

	vetted, verr := false, error(nil)
	if w.vet != nil {
		vetted, verr = w.vet.Vetted(ctx, account, key)
	}
	if verr != nil || !vetted {
		if verr != nil {
			slog.WarnContext(ctx, "needsme: vetting failed; treating the message as withheld", "account", account, "error", verr)
		}
		// Not settled: it is a pause, so it does not count as work and a pass that
		// only skipped withheld mail goes back to the idle interval.
		return false, miss(store.MissWithheld)
	}

	in := jev.FromStored(c.Message, c.Role, nil)
	state, ok := jev.BuildState(in.State, stateBudget)
	if !ok {
		return true, miss(store.MissNothingToRead)
	}
	system, prompt := Prompt(state.Text)
	res, err := w.gate.Complete(ctx, llm.CompleteRequest{
		Feature: Feature, AccountIDs: []string{account}, ContentKeys: []string{key},
		Model: chat.Slug, System: system, Prompt: prompt, MaxTokens: maxReplyTokens,
	})
	switch {
	case err == nil:
	case errors.Is(err, llm.ErrWithheld):
		return false, miss(store.MissWithheld)
	case jev.IsOffRefusal(err):
		return false, errStop
	case errors.Is(err, llm.ErrTooLarge):
		return true, miss(store.MissTooLarge)
	case jev.IsInputRefusal(err):
		return true, miss(store.MissRejected)
	default:
		return false, err
	}

	v, perr := ParseReply(res.Text, state.Text)
	if perr != nil {
		// The call is paid for and the answer is unusable: say so, and leave the message
		// unmarked. It is not asked again until the prompt or the model changes.
		return true, miss(store.MissInvalidAnswer)
	}
	row := store.NeedsMeRow{AccountID: account, ContentKey: key, Verdict: VerdictNot, Model: chat.Slug, State: "open"}
	if v.Needs {
		row.Verdict, row.Reason = VerdictNeeds, v.Reason
	}
	if err := w.st.PutNeedsMe(ctx, row); err != nil {
		slog.ErrorContext(ctx, "needsme: a paid verdict could not be stored", "account", account, "content_key", key, "error", err)
		return false, err
	}
	if v.Needs {
		// The "Needs you" tag is local, removable and never an IMAP keyword. The
		// verdict is already kept, so a failure here is reported without losing it.
		if err := w.st.PlaceNeedsTag(ctx, account, key); err != nil {
			slog.WarnContext(ctx, "needsme: could not place the Needs you tag", "account", account, "content_key", key, "error", err)
		}
	}
	return true, nil
}

// errStop ends an account's pass quietly: the account or feature went off mid-pass,
// which is a state to wait out, not a failure to report.
var errStop = errors.New("needsme: stop")
