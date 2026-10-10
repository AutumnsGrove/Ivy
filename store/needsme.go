package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// NeedsMeRow is stage two's verdict on a message. It is derived, regenerable data
// in the mirror; the inbox query flags a row whose Verdict is "needs".
type NeedsMeRow struct {
	AccountID  string
	ContentKey string
	// Verdict is "needs" or "not".
	Verdict string
	// Reason is one short plain sentence, empty unless the verdict is "needs".
	Reason string
	// Model is the chat model that decided.
	Model string
	// State is "open" until the operator dismisses it or handles the message.
	State string
}

// A verdict is open until the operator dismisses it or handles the message. The
// inbox flag shows only open verdicts; a dismissed row is kept so stage two does not
// decide the message again and the flag does not return on a re-read.
const (
	NeedsOpen      = "open"
	NeedsDismissed = "dismissed"
)

// DismissNeedsMe clears the flag for one message and keeps the verdict. Dismissing
// twice, or a message with no verdict, changes nothing.
func (d *DBs) DismissNeedsMe(ctx context.Context, accountID, contentKey string) error {
	_, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE needs_me SET state = ? WHERE account_id = ? AND content_key = ? AND state <> ?`,
		NeedsDismissed, accountID, contentKey, NeedsDismissed)
	if err != nil {
		return fmt.Errorf("dismiss needs_me: %w", err)
	}
	// Removing the placed tag and dismissing the flag are the same action (decision 5),
	// so whichever way the flag is cleared the tag goes with it. The tag lives in
	// state.db, so this is a second write; it is idempotent.
	return d.UntagMessage(ctx, accountID, contentKey, NeedsTagID)
}

// PutNeedsMe stores a verdict, replacing any earlier one for the message. A
// dismissal is the operator's and is never undone by a newer verdict.
func (d *DBs) PutNeedsMe(ctx context.Context, r NeedsMeRow) error {
	if r.AccountID == "" || r.ContentKey == "" {
		return errors.New("put needs_me: a verdict needs an account and a content key")
	}
	if r.Verdict != "needs" && r.Verdict != "not" {
		return fmt.Errorf("put needs_me: verdict %q is not needs or not", r.Verdict)
	}
	_, err := d.Mirror.Write.ExecContext(ctx, `
		INSERT INTO needs_me (account_id, content_key, verdict, reason, stage2_model, state)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, content_key) DO UPDATE SET
			verdict=excluded.verdict, reason=excluded.reason,
			stage2_model=excluded.stage2_model,
			state=CASE WHEN needs_me.state = 'dismissed' THEN needs_me.state ELSE excluded.state END`,
		r.AccountID, r.ContentKey, r.Verdict, r.Reason, r.Model, r.State)
	if err != nil {
		return fmt.Errorf("put needs_me: %w", err)
	}
	return nil
}

// GetNeedsMe reads a message's verdict and reports whether there is one.
func (d *DBs) GetNeedsMe(ctx context.Context, accountID, contentKey string) (NeedsMeRow, bool, error) {
	r := NeedsMeRow{AccountID: accountID, ContentKey: contentKey}
	var verdict, reason, model sql.NullString
	err := d.Mirror.Read.QueryRowContext(ctx,
		`SELECT verdict, reason, stage2_model, state FROM needs_me WHERE account_id=? AND content_key=?`,
		accountID, contentKey).Scan(&verdict, &reason, &model, &r.State)
	if errors.Is(err, sql.ErrNoRows) {
		return NeedsMeRow{}, false, nil
	}
	if err != nil {
		return NeedsMeRow{}, false, fmt.Errorf("get needs_me: %w", err)
	}
	r.Verdict, r.Reason, r.Model = verdict.String, reason.String, model.String
	return r, true, nil
}

// Stage2Query says which cached stage-one answers count as a flag, and which stage-two
// attempt is current. Both come from the caller's registry and prompt, so an edited
// question or prompt changes the queue without a migration.
type Stage2Query struct {
	// QuestionID, Hash and Model pick the stage-one answers: the current wording,
	// from the current Jev model.
	QuestionID string
	Hash       string
	Model      string
	// Quiet is the option that never flags; Threshold is the bar the chosen option's
	// probability and the answer's confidence must both reach.
	Quiet     string
	Threshold float64
	// StageQuestion, StageHash and StageModel identify a stage-two attempt that
	// already failed for this prompt and model, so it is not retried.
	StageQuestion string
	StageHash     string
	StageModel    string
	// RetryWithheldBefore lets a "withheld" skip recorded at or before this instant
	// queue the message again. Withheld is a pause (the checks may be re-run), not a
	// settled outcome like an invalid reply.
	RetryWithheldBefore time.Time
}

// stage2Queue selects the Inbox mail stage one flagged that stage two has not
// settled. Settled means a stored verdict (of either kind) or a recorded failed
// attempt at the current prompt and model. One row per content key.
//
// CROSS JOIN pins the order: with no statistics the planner would start from every
// message of the account and probe decisions one by one, when the few flagged
// answers (found through idx_decisions_question) are the small side.
const stage2Queue = `
	FROM decisions d
	CROSS JOIN messages m ON m.account_id = d.account_id AND m.content_key = d.content_key
	JOIN folders f ON f.id = m.folder_id
	WHERE d.account_id = ? AND d.question_id = ? AND d.instruction_hash = ? AND d.model = ?
	  AND d.choice <> ?
	  AND d.confidence >= ?
	  AND json_extract(d.probabilities, '$."' || d.choice || '"') >= ?
	  AND f.role = 'inbox' AND m.disabled_at IS NULL
	  AND NOT EXISTS (SELECT 1 FROM needs_me n
	                  WHERE n.account_id = d.account_id AND n.content_key = d.content_key)
	  AND NOT EXISTS (SELECT 1 FROM decision_misses x
	                  WHERE x.account_id = d.account_id AND x.content_key = d.content_key
	                    AND x.question_id = ? AND x.instruction_hash = ? AND x.model = ?
	                    AND (x.reason <> 'withheld' OR julianday(x.at) > julianday(?)))`

// Stage2Candidates returns up to limit Inbox messages stage one flagged and stage two
// has not settled, newest first. A message filed in two folders appears once.
func (d *DBs) Stage2Candidates(ctx context.Context, accountID string, q Stage2Query, limit int) ([]Candidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT m.id, f.role, COALESCE(NULLIF(m.internaldate, ''), m.date) AS arrived `+stage2Queue+`
		GROUP BY m.content_key
		ORDER BY arrived DESC, m.content_key
		LIMIT ?`,
		accountID, q.QuestionID, q.Hash, q.Model, q.Quiet, q.Threshold, q.Threshold,
		q.StageQuestion, q.StageHash, q.StageModel, q.RetryWithheldBefore.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, fmt.Errorf("list stage two candidates: %w", err)
	}
	var found []Candidate
	for rows.Next() {
		var id, role, arrived string
		if err := rows.Scan(&id, &role, &arrived); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("list stage two candidates: %w", err)
		}
		found = append(found, Candidate{Message: Message{ID: id}, Role: role})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("list stage two candidates: %w", err)
	}
	_ = rows.Close()
	for i := range found {
		m, err := d.GetMessage(ctx, found[i].Message.ID)
		if err != nil {
			return nil, fmt.Errorf("list stage two candidates: %w", err)
		}
		found[i].Message = m
	}
	return found, nil
}
