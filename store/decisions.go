package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// Why a question went unanswered for a message.
const (
	// MissNothingToRead: the state builder found no text, so no call was made.
	MissNothingToRead = "nothing_to_read"
	// MissRejected: the provider refused this input (a 400), so asking again would
	// only be refused again.
	MissRejected = "rejected"
	// MissInvalidAnswer: the provider answered outside the question's options.
	MissInvalidAnswer = "invalid_answer"
	// MissNoAnswer: the provider's reply left this question out.
	MissNoAnswer = "no_answer"
	// MissTooLarge: the clipped state plus the questions still overran the call.
	MissTooLarge = "too_large"
)

// Decision is Jev's cached answer to one question about one message.
type Decision struct {
	AccountID       string
	ContentKey      string
	QuestionID      string
	InstructionHash string
	Model           string
	Choice          string
	Probabilities   map[string]float64
	Confidence      float64
	CostUSD         float64
	DecidedAt       time.Time
}

// DecisionMiss records that a question was not answered, and why.
type DecisionMiss struct {
	AccountID       string
	ContentKey      string
	QuestionID      string
	InstructionHash string
	Model           string
	Reason          string
	At              time.Time
}

// PutDecisions stores a batch of answers in one transaction, replacing any row with
// the same key, so a retry after a crash cannot duplicate one and a bad row stores
// none of the batch.
func (d *DBs) PutDecisions(ctx context.Context, ds []Decision) error {
	if len(ds) == 0 {
		return nil
	}
	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put decisions: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, x := range ds {
		if x.AccountID == "" || x.ContentKey == "" || x.QuestionID == "" || x.InstructionHash == "" || x.Model == "" {
			return errors.New("put decisions: a decision needs an account, content key, question, hash and model")
		}
		if !finite(x.Confidence) || !finite(x.CostUSD) {
			return errors.New("put decisions: confidence and cost must be finite")
		}
		for opt, p := range x.Probabilities {
			if !finite(p) {
				return fmt.Errorf("put decisions: probability of %q is not finite", opt)
			}
		}
		probs, err := json.Marshal(x.Probabilities)
		if err != nil {
			return fmt.Errorf("put decisions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO decisions (account_id, content_key, question_id, instruction_hash, model,
				choice, probabilities, confidence, cost_usd, decided_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(account_id, content_key, question_id, instruction_hash, model) DO UPDATE SET
				choice=excluded.choice, probabilities=excluded.probabilities,
				confidence=excluded.confidence, cost_usd=excluded.cost_usd, decided_at=excluded.decided_at`,
			x.AccountID, x.ContentKey, x.QuestionID, x.InstructionHash, x.Model,
			x.Choice, string(probs), x.Confidence, x.CostUSD, x.DecidedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("put decisions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("put decisions: %w", err)
	}
	return nil
}

// DecisionsFor returns every cached answer for a message, whatever its wording or
// model, so the caller can pick the ones that are current.
func (d *DBs) DecisionsFor(ctx context.Context, accountID, contentKey string) ([]Decision, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT question_id, instruction_hash, model, choice, probabilities, confidence, cost_usd, decided_at
		FROM decisions WHERE account_id = ? AND content_key = ?
		ORDER BY question_id, instruction_hash, model`, accountID, contentKey)
	if err != nil {
		return nil, fmt.Errorf("read decisions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Decision
	for rows.Next() {
		x := Decision{AccountID: accountID, ContentKey: contentKey}
		var probs, at string
		if err := rows.Scan(&x.QuestionID, &x.InstructionHash, &x.Model, &x.Choice, &probs,
			&x.Confidence, &x.CostUSD, &at); err != nil {
			return nil, fmt.Errorf("read decisions: %w", err)
		}
		if err := json.Unmarshal([]byte(probs), &x.Probabilities); err != nil {
			return nil, fmt.Errorf("read decisions: probabilities of %s: %w", x.QuestionID, err)
		}
		if x.DecidedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("read decisions: time of %s: %w", x.QuestionID, err)
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read decisions: %w", err)
	}
	return out, nil
}

// PutDecisionMisses records unanswered questions, replacing a row with the same key.
func (d *DBs) PutDecisionMisses(ctx context.Context, ms []DecisionMiss) error {
	if len(ms) == 0 {
		return nil
	}
	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put decision misses: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, m := range ms {
		if m.AccountID == "" || m.ContentKey == "" || m.QuestionID == "" || m.InstructionHash == "" || m.Model == "" || m.Reason == "" {
			return errors.New("put decision misses: a miss needs an account, content key, question, hash, model and reason")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO decision_misses (account_id, content_key, question_id, instruction_hash, model, reason, at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(account_id, content_key, question_id, instruction_hash, model) DO UPDATE SET
				reason=excluded.reason, at=excluded.at`,
			m.AccountID, m.ContentKey, m.QuestionID, m.InstructionHash, m.Model, m.Reason,
			m.At.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("put decision misses: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("put decision misses: %w", err)
	}
	return nil
}

// DecisionMissesFor returns every recorded miss for a message.
func (d *DBs) DecisionMissesFor(ctx context.Context, accountID, contentKey string) ([]DecisionMiss, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT question_id, instruction_hash, model, reason, at
		FROM decision_misses WHERE account_id = ? AND content_key = ?
		ORDER BY question_id, instruction_hash, model`, accountID, contentKey)
	if err != nil {
		return nil, fmt.Errorf("read decision misses: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DecisionMiss
	for rows.Next() {
		m := DecisionMiss{AccountID: accountID, ContentKey: contentKey}
		var at string
		if err := rows.Scan(&m.QuestionID, &m.InstructionHash, &m.Model, &m.Reason, &at); err != nil {
			return nil, fmt.Errorf("read decision misses: %w", err)
		}
		if m.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("read decision misses: time of %s: %w", m.QuestionID, err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read decision misses: %w", err)
	}
	return out, nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
