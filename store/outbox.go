package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Outbox op kinds. Tags (3e) are another op kind over flags; compose and APPEND
// arrive in chunk 4.
const (
	OutboxFlags   = "flags"
	OutboxMove    = "move"
	OutboxExpunge = "expunge"
)

// Outbox op states. pending and in_flight are the live ones sync defers to;
// done, failed and cancelled are terminal.
const (
	OutboxPending   = "pending"
	OutboxInFlight  = "in_flight"
	OutboxDone      = "done"
	OutboxFailed    = "failed"
	OutboxCancelled = "cancelled"
)

// Outbox limits (docs/handoffs/2026-10-04-C3-outbox.md). They are constants so
// a beyond-limit path is a test, not a guess.
const (
	// MaxQueuedOps is how many non-terminal ops one account may hold before an
	// enqueue is refused with ErrOutboxFull.
	MaxQueuedOps = 500
	// MaxOutboxAttempts is the retry cap; past it an op is failed
	// (retries_exhausted) and stays visible.
	MaxOutboxAttempts = 8
	// MaxOutboxAge is how long a non-terminal op may live before it is failed
	// (expired); a queue that cannot drain must not hide a stuck action forever.
	MaxOutboxAge = 24 * time.Hour
	// OutboxTerminalRetention is how long a terminal row survives, so an undo
	// toast and a short history outlive a restart.
	OutboxTerminalRetention = 7 * 24 * time.Hour
)

// ErrOutboxFull reports an enqueue refused because the account already holds
// MaxQueuedOps non-terminal ops. Handlers map it to a 429/409; the UI shows the
// failure rather than dropping the action silently.
var ErrOutboxFull = errors.New("outbox full")

// ErrOutboxLive reports an attempt to dismiss an op that is still pending or
// in flight. Only a terminal row may be dismissed; a live one is never silently
// dropped.
var ErrOutboxLive = errors.New("outbox op is still live")

// OutboxExpect is the postcondition an op asks for, in the op's own terms. It
// is canonical JSON, so the same intention always hashes to the same
// idempotency key. Only the fields the kind uses are set.
type OutboxExpect struct {
	// DestFolderID is the destination for a move.
	DestFolderID string `json:"dest_folder_id,omitempty"`
	// FlagsAdd and FlagsClear are the flags the message should gain and lose.
	FlagsAdd   []string `json:"flags_add,omitempty"`
	FlagsClear []string `json:"flags_clear,omitempty"`
}

// OutboxKey identifies the mail an op acts on. A message is identified by its
// content key *and* its source folder, never the content key alone: N8 means
// identical Message-IDs share a key, and mail you sent yourself lives in INBOX
// and Sent under one key.
type OutboxKey struct {
	ContentKey     string
	SourceFolderID string
}

// OutboxOp is one row of the outbox. It names a postcondition, not a command:
// the UID is resolved at dispatch and only then stored, with the UIDVALIDITY it
// was resolved against.
type OutboxOp struct {
	ID                string
	AccountID         string
	Seq               int64
	Kind              string
	ContentKey        string
	SourceFolderID    string
	Expect            OutboxExpect
	SourceUIDValidity uint32
	SourceUID         uint32
	State             string
	Attempts          int
	NextAttemptAt     time.Time
	LastErrorCode     string
	LastErrorDetail   string
	IdempotencyKey    string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	CompletedAt       time.Time
}

// OutboxIdempotencyKey is the stable identity of an action:
// SHA-256(account ‖ kind ‖ content key ‖ source folder ‖ canonical expect). It
// is unique over non-terminal rows only, so a repeat of a finished action is a
// new op.
func OutboxIdempotencyKey(op OutboxOp) (string, error) {
	expect, err := canonicalExpectJSON(op.Expect)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, part := range []string{op.AccountID, op.Kind, op.ContentKey, op.SourceFolderID, expect} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// EnqueueOutbox commits one action to the backed-up state database and returns
// the stored op. It is idempotent while a matching op is live: the second call
// returns the existing row with created=false. A flag op whose exact inverse is
// already pending cancels both (the net change is nothing), returning the new
// cancelled row with created=true. Beyond MaxQueuedOps it returns ErrOutboxFull.
func (d *DBs) EnqueueOutbox(ctx context.Context, op OutboxOp) (OutboxOp, bool, error) {
	expect, err := canonicalExpectJSON(op.Expect)
	if err != nil {
		return OutboxOp{}, false, err
	}
	op.Expect, err = decodeExpect(expect)
	if err != nil {
		return OutboxOp{}, false, err
	}
	key, err := OutboxIdempotencyKey(op)
	if err != nil {
		return OutboxOp{}, false, err
	}
	now := op.UpdatedAt
	if now.IsZero() {
		now = op.CreatedAt
	}
	if now.IsZero() {
		return OutboxOp{}, false, errors.New("enqueue outbox: the op carries no timestamp")
	}

	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return OutboxOp{}, false, fmt.Errorf("enqueue outbox: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// A repeat of a queued action is the same action, so it is answered before
	// the cap: a double tap on a full queue must not read as "queue full".
	existing, err := outboxByKey(ctx, tx, key)
	switch {
	case err == nil:
		return existing, false, tx.Commit()
	case !errors.Is(err, ErrNotFound):
		return OutboxOp{}, false, err
	}

	var queued int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox WHERE account_id = ? AND state IN (?, ?)`,
		op.AccountID, OutboxPending, OutboxInFlight).Scan(&queued); err != nil {
		return OutboxOp{}, false, fmt.Errorf("enqueue outbox: count queue: %w", err)
	}
	if queued >= MaxQueuedOps {
		return OutboxOp{}, false, ErrOutboxFull
	}

	var seq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM outbox WHERE account_id = ?`, op.AccountID).Scan(&seq); err != nil {
		return OutboxOp{}, false, fmt.Errorf("enqueue outbox: next seq: %w", err)
	}

	// A flag op whose exact inverse is the only pending flag op on the same mail
	// is nothing at all: record both as cancelled so the history is honest and
	// the worker sends neither.
	if op.Kind == OutboxFlags {
		inverse, ok, err := pendingFlagInverse(ctx, tx, op)
		if err != nil {
			return OutboxOp{}, false, err
		}
		if ok {
			stored := OutboxOp{
				ID: op.ID, AccountID: op.AccountID, Seq: seq, Kind: op.Kind,
				ContentKey: op.ContentKey, SourceFolderID: op.SourceFolderID,
				Expect: op.Expect, State: OutboxCancelled, IdempotencyKey: key,
				CreatedAt: now, UpdatedAt: now, CompletedAt: now,
			}
			if err := insertOutbox(ctx, tx, stored); err != nil {
				return OutboxOp{}, false, err
			}
			if err := cancelOutboxTx(ctx, tx, inverse.ID, now); err != nil {
				return OutboxOp{}, false, err
			}
			return stored, true, tx.Commit()
		}
	}

	stored := OutboxOp{
		ID: op.ID, AccountID: op.AccountID, Seq: seq, Kind: op.Kind,
		ContentKey: op.ContentKey, SourceFolderID: op.SourceFolderID,
		Expect: op.Expect, State: OutboxPending, IdempotencyKey: key,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := insertOutbox(ctx, tx, stored); err != nil {
		return OutboxOp{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return OutboxOp{}, false, fmt.Errorf("enqueue outbox: %w", err)
	}
	return stored, true, nil
}

// insertOutbox writes one row as given; the caller owns state and sequence.
func insertOutbox(ctx context.Context, tx *sql.Tx, op OutboxOp) error {
	expect, err := canonicalExpectJSON(op.Expect)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbox (
			id, account_id, seq, kind, content_key, source_folder_id, expect,
			source_uidvalidity, source_uid, state, attempts, next_attempt_at,
			last_error_code, last_error_detail, idempotency_key,
			created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.ID, op.AccountID, op.Seq, op.Kind, op.ContentKey, op.SourceFolderID, expect,
		op.SourceUIDValidity, op.SourceUID, op.State, op.Attempts, nullableTime(op.NextAttemptAt),
		op.LastErrorCode, op.LastErrorDetail, op.IdempotencyKey,
		formatTime(op.CreatedAt), formatTime(op.UpdatedAt), nullableTime(op.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("insert outbox op %s: %w", op.ID, err)
	}
	return nil
}

// pendingFlagInverse finds the single pending flag op on the same mail whose
// postcondition is the exact inverse of op's, if there is exactly one.
func pendingFlagInverse(ctx context.Context, tx *sql.Tx, op OutboxOp) (OutboxOp, bool, error) {
	rows, err := tx.QueryContext(ctx, outboxSelect+`
		WHERE account_id = ? AND content_key = ? AND source_folder_id = ? AND kind = ? AND state = ?
		ORDER BY seq`,
		op.AccountID, op.ContentKey, op.SourceFolderID, OutboxFlags, OutboxPending)
	if err != nil {
		return OutboxOp{}, false, fmt.Errorf("pending flag ops: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var pending []OutboxOp
	for rows.Next() {
		p, err := scanOutbox(rows)
		if err != nil {
			return OutboxOp{}, false, fmt.Errorf("pending flag ops: %w", err)
		}
		pending = append(pending, p)
	}
	if err := rows.Err(); err != nil {
		return OutboxOp{}, false, fmt.Errorf("pending flag ops: %w", err)
	}
	if len(pending) != 1 {
		return OutboxOp{}, false, nil
	}
	if !slices.Equal(pending[0].Expect.FlagsAdd, op.Expect.FlagsClear) ||
		!slices.Equal(pending[0].Expect.FlagsClear, op.Expect.FlagsAdd) {
		return OutboxOp{}, false, nil
	}
	return pending[0], true, nil
}

// outboxByKey returns the live op with an idempotency key, or ErrNotFound.
func outboxByKey(ctx context.Context, tx *sql.Tx, key string) (OutboxOp, error) {
	row := tx.QueryRowContext(ctx, outboxSelect+`
		WHERE idempotency_key = ? AND state IN (?, ?)`, key, OutboxPending, OutboxInFlight)
	op, err := scanOutbox(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxOp{}, ErrNotFound
	}
	if err != nil {
		return OutboxOp{}, fmt.Errorf("outbox by key: %w", err)
	}
	return op, nil
}

// GetOutbox returns one op or ErrNotFound.
func (d *DBs) GetOutbox(ctx context.Context, id string) (OutboxOp, error) {
	row := d.State.Read.QueryRowContext(ctx, outboxSelect+` WHERE id = ?`, id)
	op, err := scanOutbox(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxOp{}, ErrNotFound
	}
	if err != nil {
		return OutboxOp{}, fmt.Errorf("get outbox op %s: %w", id, err)
	}
	return op, nil
}

// NextOutbox returns the lowest-sequence non-terminal op the worker should act
// on now, or ErrNotFound. Order is strict FIFO: an in-flight op is always
// returned (recovery must see it), and a pending op only once its backoff has
// elapsed. A not-yet-due op blocks the ones behind it, so a flapping server
// cannot let a later action overtake an earlier one.
func (d *DBs) NextOutbox(ctx context.Context, accountID string, now time.Time) (OutboxOp, error) {
	row := d.State.Read.QueryRowContext(ctx, outboxSelect+`
		WHERE account_id = ? AND state IN (?, ?)
		ORDER BY seq LIMIT 1`, accountID, OutboxInFlight, OutboxPending)
	op, err := scanOutbox(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxOp{}, ErrNotFound
	}
	if err != nil {
		return OutboxOp{}, fmt.Errorf("next outbox op: %w", err)
	}
	if op.State == OutboxPending && !op.NextAttemptAt.IsZero() && op.NextAttemptAt.After(now) {
		return OutboxOp{}, ErrNotFound
	}
	return op, nil
}

// OutboxByAccount returns one account's non-terminal ops, lowest sequence first,
// so the UI can overlay them and sync can defer to them. An empty accountID
// returns every account's, for the combined view.
func (d *DBs) OutboxByAccount(ctx context.Context, accountID string) ([]OutboxOp, error) {
	rows, err := d.State.Read.QueryContext(ctx, outboxSelect+`
		WHERE (? = '' OR account_id = ?) AND state IN (?, ?) ORDER BY seq`,
		accountID, accountID, OutboxPending, OutboxInFlight)
	if err != nil {
		return nil, fmt.Errorf("outbox for %s: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanOutboxRows(rows)
}

// OutboxHistory returns one account's terminal ops newest first, for the queue
// screen's recent failures and undo history. An empty accountID returns every
// account's.
func (d *DBs) OutboxHistory(ctx context.Context, accountID string, limit int) ([]OutboxOp, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.State.Read.QueryContext(ctx, outboxSelect+`
		WHERE (? = '' OR account_id = ?) AND state NOT IN (?, ?)
		ORDER BY completed_at DESC LIMIT ?`, accountID, accountID, OutboxPending, OutboxInFlight, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox history for %s: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanOutboxRows(rows)
}

// OutboxActiveKeys is the set of (content key, source folder) pairs sync must
// not touch: a row with a live op is owned by the outbox until it is terminal.
func (d *DBs) OutboxActiveKeys(ctx context.Context, accountID string) (map[OutboxKey]bool, error) {
	rows, err := d.State.Read.QueryContext(ctx,
		`SELECT content_key, source_folder_id FROM outbox
		 WHERE account_id = ? AND state IN (?, ?)`, accountID, OutboxPending, OutboxInFlight)
	if err != nil {
		return nil, fmt.Errorf("active outbox keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	keys := make(map[OutboxKey]bool)
	for rows.Next() {
		var k OutboxKey
		if err := rows.Scan(&k.ContentKey, &k.SourceFolderID); err != nil {
			return nil, fmt.Errorf("active outbox keys: %w", err)
		}
		keys[k] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("active outbox keys: %w", err)
	}
	return keys, nil
}

// SetOutboxInFlight records that a command may have been sent. It commits the
// resolved identity in the same statement, so recovery asks about exactly the
// message the command named.
func (d *DBs) SetOutboxInFlight(ctx context.Context, id string, uidvalidity, uid uint32, now time.Time) error {
	return d.updateOutbox(ctx, `
		UPDATE outbox SET state = ?, source_uidvalidity = ?, source_uid = ?, updated_at = ?
		WHERE id = ?`, OutboxInFlight, uidvalidity, uid, formatTime(now), id)
}

// RequeueOutbox returns an in-flight op to pending after recovery decided the
// command did not apply. It clears the resolved identity so dispatch resolves
// the UID again, and does not count an attempt: the crash was not the op's fault.
func (d *DBs) RequeueOutbox(ctx context.Context, id string, now time.Time) error {
	return d.updateOutbox(ctx, `
		UPDATE outbox SET state = ?, source_uidvalidity = 0, source_uid = 0,
			next_attempt_at = NULL, updated_at = ?, completed_at = NULL
		WHERE id = ?`, OutboxPending, formatTime(now), id)
}

// SetOutboxPending returns a transiently failed op to the queue. The attempt
// count and the backoff are recorded, so a flapping server never spins.
func (d *DBs) SetOutboxPending(ctx context.Context, id string, attempts int, next time.Time, code, detail string, now time.Time) error {
	return d.updateOutbox(ctx, `
		UPDATE outbox SET state = ?, attempts = ?, next_attempt_at = ?,
			last_error_code = ?, last_error_detail = ?, updated_at = ?, completed_at = NULL
		WHERE id = ?`,
		OutboxPending, attempts, nullableTime(next), code, truncateErrorDetail(detail), formatTime(now), id)
}

// SetOutboxDone marks a server-acknowledged op finished.
func (d *DBs) SetOutboxDone(ctx context.Context, id string, now time.Time) error {
	return d.updateOutbox(ctx, `
		UPDATE outbox SET state = ?, updated_at = ?, completed_at = ?
		WHERE id = ?`, OutboxDone, formatTime(now), formatTime(now), id)
}

// SetOutboxFailed marks an op terminal after a permanent rejection, an
// unreachable postcondition or the retry cap. It is never silently dropped.
func (d *DBs) SetOutboxFailed(ctx context.Context, id, code, detail string, now time.Time) error {
	return d.updateOutbox(ctx, `
		UPDATE outbox SET state = ?, last_error_code = ?, last_error_detail = ?,
			updated_at = ?, completed_at = ?
		WHERE id = ?`,
		OutboxFailed, code, truncateErrorDetail(detail), formatTime(now), formatTime(now), id)
}

// CancelOutbox marks an op terminal because it was superseded before dispatch.
func (d *DBs) CancelOutbox(ctx context.Context, id string, now time.Time) error {
	return d.updateOutbox(ctx, `
		UPDATE outbox SET state = ?, updated_at = ?, completed_at = ?
		WHERE id = ?`, OutboxCancelled, formatTime(now), formatTime(now), id)
}

// RetryOutbox returns a failed op to the queue now, clearing its attempt count
// and its backoff so the retry cap is measured afresh. A non-failed op is
// ErrOutboxLive; an unknown id is ErrNotFound.
func (d *DBs) RetryOutbox(ctx context.Context, id string, now time.Time) error {
	res, err := d.State.Write.ExecContext(ctx, `
		UPDATE outbox SET state = ?, attempts = 0, next_attempt_at = NULL,
			last_error_code = '', last_error_detail = '', updated_at = ?, completed_at = NULL
		WHERE id = ? AND state = ?`, OutboxPending, formatTime(now), id, OutboxFailed)
	if err != nil {
		return fmt.Errorf("retry outbox op %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("retry outbox op %s: %w", id, err)
	}
	if n == 0 {
		if _, gerr := d.GetOutbox(ctx, id); gerr != nil {
			return gerr
		}
		return ErrOutboxLive
	}
	return nil
}

// DeleteOutbox dismisses a terminal op. A live op is refused with ErrOutboxLive;
// an unknown id is ErrNotFound. This is not an erasure of mail, only of the op
// row the operator has seen.
func (d *DBs) DeleteOutbox(ctx context.Context, id string) error {
	res, err := d.State.Write.ExecContext(ctx, `
		DELETE FROM outbox WHERE id = ? AND state NOT IN (?, ?)`, id, OutboxPending, OutboxInFlight)
	if err != nil {
		return fmt.Errorf("delete outbox op %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete outbox op %s: %w", id, err)
	}
	if n == 0 {
		if _, gerr := d.GetOutbox(ctx, id); gerr != nil {
			return gerr
		}
		return ErrOutboxLive
	}
	return nil
}

func cancelOutboxTx(ctx context.Context, tx *sql.Tx, id string, now time.Time) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE outbox SET state = ?, updated_at = ?, completed_at = ?
		WHERE id = ?`, OutboxCancelled, formatTime(now), formatTime(now), id)
	if err != nil {
		return fmt.Errorf("cancel outbox op %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("cancel outbox op %s: %w", id, err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// updateOutbox runs a fixed single-row UPDATE and reports an unknown id.
func (d *DBs) updateOutbox(ctx context.Context, query string, args ...any) error {
	res, err := d.State.Write.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update outbox: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update outbox: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneOutbox removes terminal rows older than OutboxTerminalRetention and
// returns how many went. It is the only deletion the outbox does, and it never
// touches a pending or in-flight op.
func (d *DBs) PruneOutbox(ctx context.Context, now time.Time) (int, error) {
	cutoff := now.Add(-OutboxTerminalRetention)
	res, err := d.State.Write.ExecContext(ctx, `
		DELETE FROM outbox
		WHERE state NOT IN (?, ?) AND completed_at IS NOT NULL AND completed_at <= ?`,
		OutboxPending, OutboxInFlight, formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("prune outbox: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune outbox: %w", err)
	}
	return int(n), nil
}

// MaxOutboxErrorDetail bounds the stored failure text, matching sync_state's
// discipline so a hostile server message cannot grow the state database.
const MaxOutboxErrorDetail = 500

func truncateErrorDetail(s string) string {
	if len(s) <= MaxOutboxErrorDetail {
		return s
	}
	return strings.ToValidUTF8(s[:MaxOutboxErrorDetail], "")
}

const outboxSelect = `
	SELECT id, account_id, seq, kind, content_key, source_folder_id, expect,
	       source_uidvalidity, source_uid, state, attempts, next_attempt_at,
	       last_error_code, last_error_detail, idempotency_key,
	       created_at, updated_at, completed_at
	FROM outbox`

func scanOutbox(s scanner) (OutboxOp, error) {
	var (
		op                 OutboxOp
		uidvalidity, uid   int64
		expectJSON         string
		created            string
		next               sql.NullString
		updated, completed sql.NullString
	)
	if err := s.Scan(
		&op.ID, &op.AccountID, &op.Seq, &op.Kind, &op.ContentKey, &op.SourceFolderID, &expectJSON,
		&uidvalidity, &uid, &op.State, &op.Attempts, &next,
		&op.LastErrorCode, &op.LastErrorDetail, &op.IdempotencyKey,
		&created, &updated, &completed,
	); err != nil {
		return OutboxOp{}, err
	}
	expect, err := decodeExpect(expectJSON)
	if err != nil {
		return OutboxOp{}, fmt.Errorf("expect: %w", err)
	}
	op.Expect = expect
	if uidvalidity < 0 || uidvalidity > int64(^uint32(0)) || uid < 0 || uid > int64(^uint32(0)) {
		return OutboxOp{}, fmt.Errorf("outbox op %s: stored uid %d/%d is out of range", op.ID, uidvalidity, uid)
	}
	op.SourceUIDValidity = uint32(uidvalidity) //nolint:gosec // G115: range checked above
	op.SourceUID = uint32(uid)                 //nolint:gosec // G115: range checked above
	for _, tc := range []struct {
		raw  string
		dest *time.Time
		name string
	}{{created, &op.CreatedAt, "created_at"}} {
		t, err := parseTime(tc.raw)
		if err != nil {
			return OutboxOp{}, fmt.Errorf("%s: %w", tc.name, err)
		}
		*tc.dest = t
	}
	for _, tc := range []struct {
		raw  sql.NullString
		dest *time.Time
		name string
	}{{updated, &op.UpdatedAt, "updated_at"}, {completed, &op.CompletedAt, "completed_at"}} {
		if !tc.raw.Valid {
			continue
		}
		t, err := parseTime(tc.raw.String)
		if err != nil {
			return OutboxOp{}, fmt.Errorf("%s: %w", tc.name, err)
		}
		*tc.dest = t
	}
	if next.Valid {
		t, err := parseTime(next.String)
		if err != nil {
			return OutboxOp{}, fmt.Errorf("next_attempt_at: %w", err)
		}
		op.NextAttemptAt = t
	}
	return op, nil
}

func scanOutboxRows(rows *sql.Rows) ([]OutboxOp, error) {
	var out []OutboxOp
	for rows.Next() {
		op, err := scanOutbox(rows)
		if err != nil {
			return nil, fmt.Errorf("outbox row: %w", err)
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox rows: %w", err)
	}
	return out, nil
}

// canonicalExpectJSON serialises an expectation deterministically: struct field
// order fixes the key order, and the flag sets are lower-cased, sorted and
// deduplicated, so the same intention always hashes the same.
func canonicalExpectJSON(e OutboxExpect) (string, error) {
	e.FlagsAdd = canonicalFlagSet(e.FlagsAdd)
	e.FlagsClear = canonicalFlagSet(e.FlagsClear)
	b, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("marshal outbox expect: %w", err)
	}
	return string(b), nil
}

func decodeExpect(raw string) (OutboxExpect, error) {
	var e OutboxExpect
	if raw == "" {
		return e, nil
	}
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return OutboxExpect{}, err
	}
	return e, nil
}

// canonicalFlagSet lower-cases and sorts a flag list. IMAP flags are
// case-insensitive, so this is what makes two spellings of one action the same.
func canonicalFlagSet(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, f := range in {
		out[i] = strings.ToLower(f)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// SettledMoveDestination returns the folder the newest finished move of a
// message (named by content key and the folder it left) delivered it to, or
// ErrNotFound. A reader's Undo arrives holding the id of the row the move
// hid, and this is how it finds where the message went without guessing from
// the content key alone, which a copy in another folder can share.
func (d *DBs) SettledMoveDestination(ctx context.Context, accountID, contentKey, sourceFolderID string) (string, error) {
	var raw string
	err := d.State.Read.QueryRowContext(ctx, `
		SELECT expect FROM outbox
		WHERE account_id = ? AND content_key = ? AND source_folder_id = ? AND kind = ? AND state = ?
		ORDER BY seq DESC LIMIT 1`,
		accountID, contentKey, sourceFolderID, OutboxMove, OutboxDone).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("settled move of %s: %w", contentKey, err)
	}
	expect, err := decodeExpect(raw)
	if err != nil {
		return "", fmt.Errorf("settled move of %s: expect: %w", contentKey, err)
	}
	return expect.DestFolderID, nil
}
