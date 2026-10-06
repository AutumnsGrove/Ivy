package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Send queue states (docs/handoffs/2026-10-06-G2-send-queue-design.md). queued,
// submitting and submitted are live; the rest are terminal.
const (
	// SendQueued is committed and not yet sent; the undo window is still open.
	SendQueued = "queued"
	// SendSubmitting is written after the last RCPT and immediately before DATA.
	// It means "may have been sent": a crash from here is unconfirmed, never
	// automatically retried.
	SendSubmitting = "submitting"
	// SendSubmitted is set only after the server answered 250 for the whole
	// transaction. The Sent copy is still pending.
	SendSubmitted = "submitted"
	// SendAppended is a terminal success: the message was sent and filed.
	SendAppended = "appended"
	// SendDone is a terminal success when no Sent copy was needed.
	SendDone = "done"
	// SendFailed is a permanent SMTP rejection or the retry cap.
	SendFailed = "failed"
	// SendUnconfirmed is "may have been sent, unknown". Terminal and never
	// automatically resent (round 60).
	SendUnconfirmed = "unconfirmed"
	// SendCancelled is a queued send the operator undid before its deadline.
	// Terminal; the draft is returned to the compose screen (4c).
	SendCancelled = "cancelled"
)

// Send queue limits (STANDARDS.md 4a). They are constants so a beyond-limit path
// is a test, not a guess.
const (
	// MaxSendAttempts bounds SMTP attempts on one row.
	MaxSendAttempts = 8
	// MaxSendAge is how long a live row may wait before it fails (`expired`).
	MaxSendAge = 24 * time.Hour
	// MaxQueuedSends bounds one account's live rows before an enqueue is refused.
	MaxQueuedSends = 500
	// MaxSendTerminalRetention is how long a terminal row survives.
	MaxSendTerminalRetention = 7 * 24 * time.Hour
	// MaxSendErrorDetail bounds the stored error text.
	MaxSendErrorDetail = 500
)

// Undo send (CHUNK4-BRIEF 1.3). The window is a setting, global and per account.
const (
	// UndoSendDelayKey is the settings key, in seconds.
	UndoSendDelayKey = "compose.undo_delay_seconds"
	// DefaultUndoSendDelay is the window when the operator has not chosen one.
	DefaultUndoSendDelay = 10
	// MaxUndoSendDelay bounds the window; a longer one would only hold mail.
	MaxUndoSendDelay = 120
)

// ErrSendFull reports an enqueue refused because the account already holds
// MaxQueuedSends live rows.
var ErrSendFull = errors.New("send queue full")

// ErrSendTooLate reports an undo after the deadline, or once the message has
// left the queue: it is no longer cancellable.
var ErrSendTooLate = errors.New("send is no longer cancellable")

// SendMessage is one queued outgoing message. The wire and Sent bodies are both
// stored so a retry is byte-identical and the Sent copy keeps its Bcc header.
type SendMessage struct {
	ID           string
	AccountID    string
	Seq          int64
	MessageID    string
	ContentKey   string
	EnvelopeFrom string
	Recipients   []string
	WireBody     []byte
	SentBody     []byte
	// Draft is the original compose request, kept so undo can hand it back.
	Draft           []byte
	State           string
	Attempts        int
	NextAttemptAt   time.Time
	LastErrorCode   string
	LastErrorDetail string
	UndoDeadline    time.Time
	SentAppendID    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     time.Time
}

const sendSelect = `SELECT id, account_id, seq, message_id, content_key, envelope_from,
	recipients, wire_body, sent_body, compose_json, state, attempts, next_attempt_at,
	last_error_code, last_error_detail, undo_deadline, sent_append_id,
	created_at, updated_at, completed_at FROM send_queue`

func scanSend(s scanner) (SendMessage, error) {
	var (
		m          SendMessage
		recipients string
		next       sql.NullString
		undo       sql.NullString
		created    string
		updated    sql.NullString
		completed  sql.NullString
	)
	if err := s.Scan(
		&m.ID, &m.AccountID, &m.Seq, &m.MessageID, &m.ContentKey, &m.EnvelopeFrom,
		&recipients, &m.WireBody, &m.SentBody, &m.Draft, &m.State, &m.Attempts, &next,
		&m.LastErrorCode, &m.LastErrorDetail, &undo, &m.SentAppendID,
		&created, &updated, &completed,
	); err != nil {
		return SendMessage{}, err
	}
	if err := json.Unmarshal([]byte(recipients), &m.Recipients); err != nil {
		return SendMessage{}, fmt.Errorf("send %s recipients: %w", m.ID, err)
	}
	var err error
	if m.CreatedAt, err = parseTime(created); err != nil {
		return SendMessage{}, fmt.Errorf("send %s created_at: %w", m.ID, err)
	}
	for _, tc := range []struct {
		raw  sql.NullString
		dest *time.Time
		name string
	}{
		{updated, &m.UpdatedAt, "updated_at"},
		{completed, &m.CompletedAt, "completed_at"},
		{next, &m.NextAttemptAt, "next_attempt_at"},
		{undo, &m.UndoDeadline, "undo_deadline"},
	} {
		if !tc.raw.Valid {
			continue
		}
		if *tc.dest, err = parseTime(tc.raw.String); err != nil {
			return SendMessage{}, fmt.Errorf("send %s %s: %w", m.ID, tc.name, err)
		}
	}
	return m, nil
}

// EnqueueSend commits a new message in `queued` and returns it. It is idempotent
// on (account, message_id) while the row is live: the second call returns the
// existing row with created=false. Beyond MaxQueuedSends it returns ErrSendFull.
func (d *DBs) EnqueueSend(ctx context.Context, m SendMessage) (SendMessage, bool, error) {
	switch {
	case m.ID == "", m.AccountID == "", m.MessageID == "", m.ContentKey == "", m.EnvelopeFrom == "":
		return SendMessage{}, false, errors.New("enqueue send: id, account, message id, content key and envelope from are required")
	case len(m.Recipients) == 0:
		return SendMessage{}, false, errors.New("enqueue send: no recipients")
	}
	if m.State == "" {
		m.State = SendQueued
	}
	now := m.UpdatedAt
	if now.IsZero() {
		now = m.CreatedAt
	}
	if now.IsZero() {
		return SendMessage{}, false, errors.New("enqueue send: the row carries no timestamp")
	}
	recipients, err := json.Marshal(m.Recipients)
	if err != nil {
		return SendMessage{}, false, fmt.Errorf("enqueue send: recipients: %w", err)
	}

	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return SendMessage{}, false, fmt.Errorf("enqueue send: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// A repeat of the same row id is the same send: the handler mints a new
	// Message-ID for every request, so the id is what a double tap shares.
	byID, err := scanSend(tx.QueryRowContext(ctx, sendSelect+` WHERE id = ?`, m.ID))
	switch {
	case err == nil:
		if byID.AccountID != m.AccountID {
			return SendMessage{}, false, fmt.Errorf("enqueue send: id %s belongs to another account", m.ID)
		}
		return byID, false, tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return SendMessage{}, false, fmt.Errorf("enqueue send: %w", err)
	}

	// A repeat of a live send is the same send, answered before the cap so a
	// double tap never reads as "queue full".
	existing, err := liveSendByMessage(ctx, tx, m.AccountID, m.MessageID)
	switch {
	case err == nil:
		return existing, false, tx.Commit()
	case !errors.Is(err, ErrNotFound):
		return SendMessage{}, false, err
	}

	var queued int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM send_queue WHERE account_id = ? AND state NOT IN (?, ?, ?, ?, ?)`,
		m.AccountID, SendAppended, SendDone, SendFailed, SendUnconfirmed, SendCancelled).Scan(&queued); err != nil {
		return SendMessage{}, false, fmt.Errorf("enqueue send: count queue: %w", err)
	}
	if queued >= MaxQueuedSends {
		return SendMessage{}, false, ErrSendFull
	}

	var seq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM send_queue WHERE account_id = ?`, m.AccountID).Scan(&seq); err != nil {
		return SendMessage{}, false, fmt.Errorf("enqueue send: next seq: %w", err)
	}

	m.Seq = seq
	m.CreatedAt = now
	m.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO send_queue (
			id, account_id, seq, message_id, content_key, envelope_from, recipients,
			wire_body, sent_body, compose_json, state, attempts, next_attempt_at, last_error_code,
			last_error_detail, undo_deadline, sent_append_id, created_at, updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.AccountID, m.Seq, m.MessageID, m.ContentKey, m.EnvelopeFrom, string(recipients),
		m.WireBody, m.SentBody, m.Draft, m.State, m.Attempts, nullableTime(m.NextAttemptAt),
		m.LastErrorCode, m.LastErrorDetail, nullableTime(m.UndoDeadline), m.SentAppendID,
		formatTime(m.CreatedAt), formatTime(m.UpdatedAt), nullableTime(m.CompletedAt)); err != nil {
		return SendMessage{}, false, fmt.Errorf("enqueue send %s: %w", m.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return SendMessage{}, false, fmt.Errorf("enqueue send %s: %w", m.ID, err)
	}
	return m, true, nil
}

func liveSendByMessage(ctx context.Context, tx *sql.Tx, accountID, messageID string) (SendMessage, error) {
	row := tx.QueryRowContext(ctx, sendSelect+`
		WHERE account_id = ? AND message_id = ? AND state NOT IN (?, ?, ?, ?, ?)`,
		accountID, messageID, SendAppended, SendDone, SendFailed, SendUnconfirmed, SendCancelled)
	m, err := scanSend(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SendMessage{}, ErrNotFound
	}
	if err != nil {
		return SendMessage{}, fmt.Errorf("live send by message: %w", err)
	}
	return m, nil
}

// GetSend returns one row, terminal or not.
func (d *DBs) GetSend(ctx context.Context, id string) (SendMessage, error) {
	m, err := scanSend(d.State.Read.QueryRowContext(ctx, sendSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SendMessage{}, ErrNotFound
	}
	if err != nil {
		return SendMessage{}, fmt.Errorf("get send %s: %w", id, err)
	}
	return m, nil
}

// NextQueuedSend returns the lowest-sequence queued row that may be sent now, or
// ErrNotFound. FIFO is strict: a row still inside its undo window or its retry
// backoff blocks the ones behind it.
func (d *DBs) NextQueuedSend(ctx context.Context, accountID string, now time.Time) (SendMessage, error) {
	m, err := scanSend(d.State.Read.QueryRowContext(ctx, sendSelect+`
		WHERE account_id = ? AND state = ? ORDER BY seq LIMIT 1`, accountID, SendQueued))
	if errors.Is(err, sql.ErrNoRows) {
		return SendMessage{}, ErrNotFound
	}
	if err != nil {
		return SendMessage{}, fmt.Errorf("next queued send: %w", err)
	}
	if !m.UndoDeadline.IsZero() && m.UndoDeadline.After(now) {
		return SendMessage{}, ErrNotFound
	}
	if !m.NextAttemptAt.IsZero() && m.NextAttemptAt.After(now) {
		return SendMessage{}, ErrNotFound
	}
	return m, nil
}

// NextSubmittedSend returns the lowest-sequence submitted row whose Sent copy is
// still being tracked, or ErrNotFound.
func (d *DBs) NextSubmittedSend(ctx context.Context, accountID string) (SendMessage, error) {
	m, err := scanSend(d.State.Read.QueryRowContext(ctx, sendSelect+`
		WHERE account_id = ? AND state = ? ORDER BY seq LIMIT 1`, accountID, SendSubmitted))
	if errors.Is(err, sql.ErrNoRows) {
		return SendMessage{}, ErrNotFound
	}
	if err != nil {
		return SendMessage{}, fmt.Errorf("next submitted send: %w", err)
	}
	return m, nil
}

// SendsByAccount returns one account's live rows and recent terminal ones,
// newest first, for the compose screen and its history. An empty accountID
// returns every account's.
func (d *DBs) SendsByAccount(ctx context.Context, accountID string, limit int) ([]SendMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.State.Read.QueryContext(ctx, sendSelect+`
		WHERE (? = '' OR account_id = ?)
		ORDER BY (state IN (?, ?, ?)) DESC, seq DESC LIMIT ?`,
		accountID, accountID, SendQueued, SendSubmitting, SendSubmitted, limit)
	if err != nil {
		return nil, fmt.Errorf("sends for %s: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []SendMessage
	for rows.Next() {
		m, err := scanSend(rows)
		if err != nil {
			return nil, fmt.Errorf("scan send: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sends for %s: %w", accountID, err)
	}
	return out, nil
}

// SetSendSubmitting is the durable "may have been sent" point, written after
// the last RCPT and before DATA. Only a queued row can enter it.
func (d *DBs) SetSendSubmitting(ctx context.Context, id string, now time.Time) error {
	return d.updateSend(ctx, `
		UPDATE send_queue SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		SendSubmitting, formatTime(now), id, SendQueued)
}

// MarkSendSubmitted records the server's 250. Only a submitting row can enter
// it: the 250 follows the DATA the submitting state announced.
func (d *DBs) MarkSendSubmitted(ctx context.Context, id string, now time.Time) error {
	return d.updateSend(ctx, `
		UPDATE send_queue SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		SendSubmitted, formatTime(now), id, SendSubmitting)
}

// SetSendAppendID records the Sent copy's outbox op. A crash between the submit
// and this write is healed by recovery, which enqueues the idempotent append op.
func (d *DBs) SetSendAppendID(ctx context.Context, id, appendID string, now time.Time) error {
	return d.updateSend(ctx, `
		UPDATE send_queue SET sent_append_id = ?, updated_at = ? WHERE id = ? AND state = ?`,
		appendID, formatTime(now), id, SendSubmitted)
}

// RetrySend returns a transiently failed row to queued, counting the attempt and
// recording the backoff so a flapping provider never spins.
func (d *DBs) RetrySend(ctx context.Context, id, code, detail string, next, now time.Time) error {
	return d.updateSend(ctx, `
		UPDATE send_queue SET state = ?, attempts = attempts + 1, next_attempt_at = ?,
			last_error_code = ?, last_error_detail = ?, updated_at = ?, completed_at = NULL
		WHERE id = ? AND state IN (?, ?)`,
		SendQueued, nullableTime(next), code, truncateSendDetail(detail), formatTime(now), id,
		SendQueued, SendSubmitting)
}

// FailSend marks a permanent failure: the message was not sent.
func (d *DBs) FailSend(ctx context.Context, id, code, detail string, now time.Time) error {
	return d.updateSend(ctx, `
		UPDATE send_queue SET state = ?, last_error_code = ?, last_error_detail = ?,
			updated_at = ?, completed_at = ?
		WHERE id = ? AND state IN (?, ?)`,
		SendFailed, code, truncateSendDetail(detail), formatTime(now), formatTime(now), id,
		SendQueued, SendSubmitting)
}

// MarkSendUnconfirmed records the unknown outcome after a crash or drop at or
// after DATA. It is terminal; only the operator may send again.
func (d *DBs) MarkSendUnconfirmed(ctx context.Context, id, detail string, now time.Time) error {
	return d.updateSend(ctx, `
		UPDATE send_queue SET state = ?, last_error_code = ?, last_error_detail = ?,
			updated_at = ?, completed_at = ?
		WHERE id = ? AND state = ?`,
		SendUnconfirmed, "unconfirmed", truncateSendDetail(detail), formatTime(now), formatTime(now), id,
		SendSubmitting)
}

// MarkSendAppended records that the Sent copy exists. Terminal success.
func (d *DBs) MarkSendAppended(ctx context.Context, id string, now time.Time) error {
	return d.updateSend(ctx, `
		UPDATE send_queue SET state = ?, updated_at = ?, completed_at = ? WHERE id = ? AND state = ?`,
		SendAppended, formatTime(now), formatTime(now), id, SendSubmitted)
}

// MarkSendDone records a terminal success when no Sent copy was needed, or when
// the copy failed permanently (the mail was sent; only Ivy's copy is missing).
func (d *DBs) MarkSendDone(ctx context.Context, id, code, detail string, now time.Time) error {
	return d.updateSend(ctx, `
		UPDATE send_queue SET state = ?, last_error_code = ?, last_error_detail = ?,
			updated_at = ?, completed_at = ? WHERE id = ? AND state = ?`,
		SendDone, code, truncateSendDetail(detail), formatTime(now), formatTime(now), id, SendSubmitted)
}

// CancelSend undoes a queued send before its deadline. It returns the cancelled
// row, whose Draft is the original compose request. A row that is no longer
// queued, or whose deadline has passed (or that had no window), is ErrSendTooLate.
func (d *DBs) CancelSend(ctx context.Context, id string, now time.Time) (SendMessage, error) {
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return SendMessage{}, fmt.Errorf("cancel send %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	m, err := scanSend(tx.QueryRowContext(ctx, sendSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SendMessage{}, ErrNotFound
	}
	if err != nil {
		return SendMessage{}, fmt.Errorf("cancel send %s: %w", id, err)
	}
	if m.State != SendQueued || m.UndoDeadline.IsZero() || !now.Before(m.UndoDeadline) {
		return SendMessage{}, ErrSendTooLate
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE send_queue SET state = ?, updated_at = ?, completed_at = ?
		WHERE id = ? AND state = ?`, SendCancelled, formatTime(now), formatTime(now), id, SendQueued); err != nil {
		return SendMessage{}, fmt.Errorf("cancel send %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return SendMessage{}, fmt.Errorf("cancel send %s: %w", id, err)
	}
	m.State, m.UpdatedAt, m.CompletedAt = SendCancelled, now, now
	return m, nil
}

// UndoSendDelay returns the effective undo window in seconds for an account,
// preferring its own setting over the global one and then the default. A stored
// value outside the bounds is clamped, so a hand-edited row cannot send early.
func (d *DBs) UndoSendDelay(ctx context.Context, accountID string) (int, error) {
	for _, scope := range []string{accountID, ""} {
		raw, ok, err := d.GetSetting(ctx, scope, UndoSendDelayKey)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			continue // a malformed value falls through to the next scope
		}
		return clampUndoDelay(n), nil
	}
	return DefaultUndoSendDelay, nil
}

// SetUndoSendDelay stores the window for a scope; an empty accountID is global.
func (d *DBs) SetUndoSendDelay(ctx context.Context, accountID string, seconds int) error {
	if seconds < 0 || seconds > MaxUndoSendDelay {
		return fmt.Errorf("undo delay %d is outside 0-%d", seconds, MaxUndoSendDelay)
	}
	return d.SetSetting(ctx, accountID, UndoSendDelayKey, strconv.Itoa(seconds))
}

func clampUndoDelay(seconds int) int {
	switch {
	case seconds < 0:
		return 0
	case seconds > MaxUndoSendDelay:
		return MaxUndoSendDelay
	default:
		return seconds
	}
}

// RecoverSubmittingSends marks every row that was mid-attempt when the process
// stopped as unconfirmed. It runs once at worker startup. A submitting row means
// DATA may have begun, and SMTP cannot be asked what happened.
func (d *DBs) RecoverSubmittingSends(ctx context.Context, accountID string, now time.Time) (int, error) {
	res, err := d.State.Write.ExecContext(ctx, `
		UPDATE send_queue SET state = ?, last_error_code = ?, last_error_detail = ?,
			updated_at = ?, completed_at = ?
		WHERE account_id = ? AND state = ?`,
		SendUnconfirmed, "unconfirmed", "Ivy restarted while this message was being sent",
		formatTime(now), formatTime(now), accountID, SendSubmitting)
	if err != nil {
		return 0, fmt.Errorf("recover submitting sends: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("recover submitting sends: %w", err)
	}
	return int(n), nil
}

// PruneSendQueue removes terminal rows older than the retention. It is the
// queue's only deletion and never touches a live row.
func (d *DBs) PruneSendQueue(ctx context.Context, now time.Time) (int, error) {
	cutoff := formatTime(now.Add(-MaxSendTerminalRetention))
	res, err := d.State.Write.ExecContext(ctx, `
		DELETE FROM send_queue
		WHERE state IN (?, ?, ?, ?, ?) AND completed_at IS NOT NULL AND completed_at < ?`,
		SendAppended, SendDone, SendFailed, SendUnconfirmed, SendCancelled, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune send queue: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune send queue: %w", err)
	}
	return int(n), nil
}

// SendBody is the material an append op files: the account, the Message-ID the
// idempotency search uses, and the bytes (the Sent copy, Bcc kept).
type SendBody struct {
	AccountID string
	MessageID string
	Body      []byte
}

// SendBodyForAppend returns the copy a send's append op should file. A missing
// row is ErrNotFound; the op then fails rather than appending nothing.
func (d *DBs) SendBodyForAppend(ctx context.Context, sendID string) (SendBody, error) {
	var b SendBody
	err := d.State.Read.QueryRowContext(ctx,
		`SELECT account_id, message_id, sent_body FROM send_queue WHERE id = ?`, sendID).
		Scan(&b.AccountID, &b.MessageID, &b.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return SendBody{}, ErrNotFound
	}
	if err != nil {
		return SendBody{}, fmt.Errorf("send body for append %s: %w", sendID, err)
	}
	return b, nil
}

// updateSend runs a fixed single-row UPDATE and reports an unknown or
// wrong-state id as ErrNotFound.
func (d *DBs) updateSend(ctx context.Context, query string, args ...any) error {
	res, err := d.State.Write.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update send queue: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update send queue: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// truncateSendDetail keeps the stored error under the limit, cutting on a rune
// boundary so the text stays valid UTF-8.
func truncateSendDetail(s string) string {
	if len(s) <= MaxSendErrorDetail {
		return s
	}
	return strings.ToValidUTF8(s[:MaxSendErrorDetail], "")
}
