package store

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

// sendNow keeps every queue timestamp deterministic so due times and retention
// are asserted without sleeping.
var sendNow = time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)

func newSend(id, messageID string) SendMessage {
	return SendMessage{
		ID: id, AccountID: "acct-1", MessageID: messageID,
		ContentKey: "key-" + id, EnvelopeFrom: "me@example.test",
		Recipients: []string{"you@example.test"},
		WireBody:   []byte("wire body"), SentBody: []byte("sent body"),
		CreatedAt: sendNow, UpdatedAt: sendNow,
	}
}

// A new send is a durable queued row in sequence, and a repeat of the same
// Message-ID returns it rather than queueing a second copy.
func TestEnqueueSendIsSequencedAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	first, created, err := dbs.EnqueueSend(ctx, newSend("send-1", "<m1@example.test>"))
	if err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	if !created || first.State != SendQueued || first.Seq != 1 {
		t.Fatalf("first = %+v created=%v, want a queued seq 1", first, created)
	}
	second, created, err := dbs.EnqueueSend(ctx, newSend("send-2", "<m2@example.test>"))
	if err != nil {
		t.Fatalf("enqueue second: %v", err)
	}
	if !created || second.Seq != 2 {
		t.Fatalf("second seq = %d, want 2", second.Seq)
	}

	// Same Message-ID, different row id: the existing live row is returned.
	again, created, err := dbs.EnqueueSend(ctx, newSend("send-3", "<m1@example.test>"))
	if err != nil {
		t.Fatalf("enqueue repeat: %v", err)
	}
	if created || again.ID != "send-1" {
		t.Errorf("repeat = %+v created=%v, want the existing send-1", again, created)
	}

	// A terminal row never blocks a resend: it is a new row.
	if err := dbs.FailSend(ctx, "send-1", "rejected", "no", sendNow); err != nil {
		t.Fatalf("fail send-1: %v", err)
	}
	resend, created, err := dbs.EnqueueSend(ctx, newSend("send-4", "<m1@example.test>"))
	if err != nil {
		t.Fatalf("resend: %v", err)
	}
	if !created || resend.ID != "send-4" {
		t.Errorf("resend = %+v created=%v, want a new row", resend, created)
	}
}

func TestEnqueueSendRefusesBeyondTheQueueCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	for i := range MaxQueuedSends {
		m := newSend("send-"+strconv.Itoa(i), "<m"+strconv.Itoa(i)+"@example.test>")
		m.ContentKey = "key-" + strconv.Itoa(i)
		if _, _, err := dbs.EnqueueSend(ctx, m); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	_, _, err := dbs.EnqueueSend(ctx, newSend("over", "<over@example.test>"))
	if !errors.Is(err, ErrSendFull) {
		t.Fatalf("enqueue over the cap = %v, want ErrSendFull", err)
	}
}

// The undo window and the retry backoff both hold a row back, and FIFO means the
// lowest-sequence row outranks a later one.
func TestNextQueuedSendRespectsUndoAndBackoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	held := newSend("send-1", "<m1@example.test>")
	held.UndoDeadline = sendNow.Add(10 * time.Second)
	if _, _, err := dbs.EnqueueSend(ctx, held); err != nil {
		t.Fatalf("enqueue held: %v", err)
	}
	if _, _, err := dbs.EnqueueSend(ctx, newSend("send-2", "<m2@example.test>")); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}

	// A row inside its undo window is not due, but it does not hold back a due row
	// behind it: one greylisted message must not stall the rest of the outbox.
	got, err := dbs.NextQueuedSend(ctx, "acct-1", sendNow)
	if err != nil || got.ID != "send-2" {
		t.Fatalf("next while send-1's window is open = %+v, %v, want send-2", got.ID, err)
	}
	// Among due rows the lowest sequence goes first.
	got, err = dbs.NextQueuedSend(ctx, "acct-1", sendNow.Add(11*time.Second))
	if err != nil || got.ID != "send-1" {
		t.Fatalf("next after the window = %+v, %v, want send-1 first", got.ID, err)
	}

	if err := dbs.RetrySend(ctx, "send-1", "transient", "later", sendNow.Add(time.Minute), sendNow); err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, err = dbs.NextQueuedSend(ctx, "acct-1", sendNow.Add(11*time.Second))
	if err != nil || got.ID != "send-2" {
		t.Fatalf("next during send-1's backoff = %+v, %v, want send-2", got.ID, err)
	}
	if err := dbs.FailSend(ctx, "send-2", "rejected", "no", sendNow); err != nil {
		t.Fatalf("fail send-2: %v", err)
	}
	if _, err := dbs.NextQueuedSend(ctx, "acct-1", sendNow.Add(11*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("next with only a backed-off row = %v, want ErrNotFound", err)
	}
}

// The durable points: submitting only from queued, submitted only from queued or
// submitting, and recovery turning a submitting row into unconfirmed.
func TestSendStateMachineDurablePoints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if _, _, err := dbs.EnqueueSend(ctx, newSend("send-1", "<m1@example.test>")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := dbs.MarkSendSubmitted(ctx, "send-1", sendNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("submitted from queued = %v, want ErrNotFound (the 250 must come first)", err)
	}
	if err := dbs.SetSendSubmitting(ctx, "send-1", sendNow); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	if err := dbs.SetSendSubmitting(ctx, "send-1", sendNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("submitting a second time = %v, want ErrNotFound", err)
	}
	if err := dbs.MarkSendSubmitted(ctx, "send-1", sendNow); err != nil {
		t.Fatalf("submitted: %v", err)
	}
	if err := dbs.SetSendAppendID(ctx, "send-1", "op-1", sendNow); err != nil {
		t.Fatalf("append id: %v", err)
	}
	got, err := dbs.GetSend(ctx, "send-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != SendSubmitted || got.SentAppendID != "op-1" {
		t.Errorf("row = %+v, want submitted with op-1", got)
	}

	if err := dbs.MarkSendAppended(ctx, "send-1", sendNow); err != nil {
		t.Fatalf("appended: %v", err)
	}
	if got, _ = dbs.GetSend(ctx, "send-1"); got.State != SendAppended {
		t.Errorf("state = %q, want appended", got.State)
	}
}

func TestRecoverSubmittingSendsIsUnconfirmedAndNeverRetried(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if _, _, err := dbs.EnqueueSend(ctx, newSend("send-1", "<m1@example.test>")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := dbs.SetSendSubmitting(ctx, "send-1", sendNow); err != nil {
		t.Fatalf("submitting: %v", err)
	}

	n, err := dbs.RecoverSubmittingSends(ctx, "acct-1", sendNow)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if n != 1 {
		t.Fatalf("recovered %d rows, want 1", n)
	}
	got, _ := dbs.GetSend(ctx, "send-1")
	if got.State != SendUnconfirmed {
		t.Fatalf("state = %q, want unconfirmed", got.State)
	}
	if got.LastErrorCode != "unconfirmed" {
		t.Errorf("code = %q, want unconfirmed", got.LastErrorCode)
	}
	if _, err := dbs.NextQueuedSend(ctx, "acct-1", sendNow); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unconfirmed row is live to the worker: %v", err)
	}
}

func TestPruneSendQueueRemovesOnlyOldTerminalRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if _, _, err := dbs.EnqueueSend(ctx, newSend("send-1", "<m1@example.test>")); err != nil {
		t.Fatalf("enqueue live: %v", err)
	}
	if _, _, err := dbs.EnqueueSend(ctx, newSend("send-2", "<m2@example.test>")); err != nil {
		t.Fatalf("enqueue done: %v", err)
	}
	old := sendNow.Add(-MaxSendTerminalRetention - time.Hour)
	if err := dbs.SetSendSubmitting(ctx, "send-2", old); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	if err := dbs.MarkSendSubmitted(ctx, "send-2", old); err != nil {
		t.Fatalf("submitted: %v", err)
	}
	if err := dbs.MarkSendDone(ctx, "send-2", "", "", old); err != nil {
		t.Fatalf("done: %v", err)
	}

	n, err := dbs.PruneSendQueue(ctx, sendNow)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned %d rows, want 1", n)
	}
	if _, err := dbs.GetSend(ctx, "send-1"); err != nil {
		t.Errorf("prune took a live row: %v", err)
	}
	if _, err := dbs.GetSend(ctx, "send-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old terminal row survived: %v", err)
	}
}

// undoMessage is a live send with a 10-second window and a stored draft.
func undoMessage() SendMessage {
	m := newSend("send-1", "<m1@example.test>")
	m.UndoDeadline = sendNow.Add(10 * time.Second)
	m.Draft = []byte(`{"to":"you@example.test","subject":"hi"}`)
	return m
}

// Undo works the second before the deadline, hands the draft back, and cannot
// run twice.
func TestCancelSendUndoesBeforeTheDeadline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if _, _, err := dbs.EnqueueSend(ctx, undoMessage()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	cancelled, err := dbs.CancelSend(ctx, "send-1", sendNow.Add(9*time.Second))
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.State != SendCancelled {
		t.Fatalf("state = %q, want cancelled", cancelled.State)
	}
	if string(cancelled.Draft) != `{"to":"you@example.test","subject":"hi"}` {
		t.Errorf("draft = %q, want the stored request back", cancelled.Draft)
	}
	if _, err := dbs.CancelSend(ctx, "send-1", sendNow.Add(9*time.Second)); !errors.Is(err, ErrSendTooLate) {
		t.Errorf("second undo = %v, want ErrSendTooLate", err)
	}
	if _, err := dbs.NextQueuedSend(ctx, "acct-1", sendNow.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Errorf("a cancelled send is still queued: %v", err)
	}
}

// At the deadline the send is no longer cancellable.
func TestCancelSendIsTooLateAtTheDeadline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if _, _, err := dbs.EnqueueSend(ctx, undoMessage()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := dbs.CancelSend(ctx, "send-1", sendNow.Add(10*time.Second)); !errors.Is(err, ErrSendTooLate) {
		t.Fatalf("cancel at the deadline = %v, want ErrSendTooLate", err)
	}
	if got, _ := dbs.GetSend(ctx, "send-1"); got.State != SendQueued {
		t.Errorf("state = %q, want still queued", got.State)
	}
}

// No window (delay 0) means no undo, and an unknown id is not found.
func TestCancelSendWithoutAWindowAndUnknownID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if _, _, err := dbs.EnqueueSend(ctx, newSend("send-1", "<m1@example.test>")); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := dbs.CancelSend(ctx, "send-1", sendNow); !errors.Is(err, ErrSendTooLate) {
		t.Errorf("cancel with no window = %v, want ErrSendTooLate", err)
	}
	if _, err := dbs.CancelSend(ctx, "missing", sendNow); !errors.Is(err, ErrNotFound) {
		t.Errorf("cancel unknown = %v, want ErrNotFound", err)
	}
}

func TestUndoSendDelayPrecedenceAndBounds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if n, err := dbs.UndoSendDelay(ctx, "acct-1"); err != nil || n != DefaultUndoSendDelay {
		t.Fatalf("default = %d, %v; want %d", n, err, DefaultUndoSendDelay)
	}
	if err := dbs.SetUndoSendDelay(ctx, "", 30); err != nil {
		t.Fatalf("set global: %v", err)
	}
	if n, _ := dbs.UndoSendDelay(ctx, "acct-1"); n != 30 {
		t.Errorf("global = %d, want 30", n)
	}
	if err := dbs.SetUndoSendDelay(ctx, "acct-1", 0); err != nil {
		t.Fatalf("set account: %v", err)
	}
	if n, _ := dbs.UndoSendDelay(ctx, "acct-1"); n != 0 {
		t.Errorf("account override = %d, want 0", n)
	}
	if n, _ := dbs.UndoSendDelay(ctx, "other"); n != 30 {
		t.Errorf("another account = %d, want the global 30", n)
	}
	if err := dbs.SetUndoSendDelay(ctx, "", MaxUndoSendDelay+1); err == nil {
		t.Error("an out-of-bounds value was accepted")
	}
	// A hand-edited database value is clamped on read, never trusted.
	if err := dbs.SetSetting(ctx, "acct-1", UndoSendDelayKey, "99999"); err != nil {
		t.Fatalf("hand edit: %v", err)
	}
	if n, _ := dbs.UndoSendDelay(ctx, "acct-1"); n != MaxUndoSendDelay {
		t.Errorf("clamped = %d, want %d", n, MaxUndoSendDelay)
	}
}

// The Sent append op is keyed on its send id, so a retried enqueue is the same
// op and one send files exactly one copy.
func TestAppendOutboxOpIsIdempotentPerSend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	op := OutboxOp{
		ID: "append-1", AccountID: "acct-1", Kind: OutboxAppend,
		ContentKey: "key-a", SourceFolderID: "",
		Expect:    OutboxExpect{DestFolderID: "folder-sent", FlagsAdd: []string{`\Seen`}, SendID: "send-1"},
		CreatedAt: sendNow,
	}
	first, created, err := dbs.EnqueueOutbox(ctx, op)
	if err != nil {
		t.Fatalf("enqueue append: %v", err)
	}
	if !created {
		t.Fatal("first append enqueue reported it already existed")
	}
	op.ID = "append-2"
	second, created, err := dbs.EnqueueOutbox(ctx, op)
	if err != nil {
		t.Fatalf("repeat append: %v", err)
	}
	if created || second.ID != first.ID {
		t.Errorf("repeat = %+v created=%v, want the existing op", second, created)
	}
	// A different send is a different op.
	op.ID = "append-3"
	op.Expect.SendID = "send-2"
	third, created, err := dbs.EnqueueOutbox(ctx, op)
	if err != nil {
		t.Fatalf("other send: %v", err)
	}
	if !created || third.ID != "append-3" {
		t.Errorf("other send = %+v created=%v, want a new op", third, created)
	}
}

// A double tap carries the same client id but the handler mints a new Message-ID
// for each request, so the repeat must be answered by the row id too. Without
// that the second enqueue hits the primary key and the operator sees an error
// for a message that is in fact queued.
func TestEnqueueSendIsIdempotentOnTheRowID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	first, _, err := dbs.EnqueueSend(ctx, newSend("send-1", "<m1@example.test>"))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	again, created, err := dbs.EnqueueSend(ctx, newSend("send-1", "<m2@example.test>"))
	if err != nil {
		t.Fatalf("repeat with the same row id = %v, want the existing row", err)
	}
	if created || again.MessageID != first.MessageID {
		t.Errorf("repeat = %+v created=%v, want the first row unchanged", again, created)
	}

	// The same id on another account is a different send and must not be handed
	// back across accounts.
	other := newSend("send-1", "<m3@example.test>")
	other.AccountID = "acct-2"
	if _, _, err := dbs.EnqueueSend(ctx, other); err == nil {
		t.Errorf("a row id used by another account was accepted")
	}
}

// The state machine only moves forward: a write that arrives late (a worker that
// chose a row before the operator undid it) must not revive a terminal row,
// least of all an undone one, which would send mail the operator took back.
func TestSendStateWritesNeverReviveATerminalRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	undo := newSend("send-1", "<m1@example.test>")
	undo.UndoDeadline = sendNow.Add(10 * time.Second)
	if _, _, err := dbs.EnqueueSend(ctx, undo); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := dbs.CancelSend(ctx, "send-1", sendNow); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	late := sendNow.Add(time.Minute)
	for name, err := range map[string]error{
		"retry":       dbs.RetrySend(ctx, "send-1", "timeout", "late", late, late),
		"fail":        dbs.FailSend(ctx, "send-1", "rejected", "late", late),
		"unconfirmed": dbs.MarkSendUnconfirmed(ctx, "send-1", "late", late),
		"appended":    dbs.MarkSendAppended(ctx, "send-1", late),
		"done":        dbs.MarkSendDone(ctx, "send-1", "", "", late),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s on a cancelled row = %v, want ErrNotFound", name, err)
		}
	}
	got, err := dbs.GetSend(ctx, "send-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != SendCancelled {
		t.Errorf("state = %q, want cancelled to stay cancelled", got.State)
	}
}

// The send list is bounded: a caller-supplied limit above the cap is clamped
// (STANDARDS 4a, no unbounded reads), and a missing one means the default.
func TestSendsByAccountClampsTheLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	for i := range MaxSendListLimit + 5 {
		n := strconv.Itoa(i)
		m := newSend("send-"+n, "<m"+n+"@example.test>")
		if _, _, err := dbs.EnqueueSend(ctx, m); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	got, err := dbs.SendsByAccount(ctx, "acct-1", 1_000_000)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != MaxSendListLimit {
		t.Errorf("a limit of a million returned %d rows, want the cap of %d", len(got), MaxSendListLimit)
	}
	if got, _ := dbs.SendsByAccount(ctx, "acct-1", 0); len(got) != DefaultSendListLimit {
		t.Errorf("no limit returned %d rows, want the default %d", len(got), DefaultSendListLimit)
	}
}

// A send records the draft version it came from, and the removal op that clears
// that copy once the message is accepted.
func TestSendCarriesTheDraftItCameFrom(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	m := newSend("send-1", "<m1@example.test>")
	m.DraftMessageID = "<draft@example.test>"
	stored, _, err := dbs.EnqueueSend(ctx, m)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if stored.DraftMessageID != "<draft@example.test>" {
		t.Fatalf("stored draft message id = %q, want the draft version", stored.DraftMessageID)
	}
	got, err := dbs.GetSend(ctx, "send-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DraftMessageID != "<draft@example.test>" {
		t.Fatalf("round-tripped draft message id = %q", got.DraftMessageID)
	}

	if err := dbs.SetSendDraftRemoveID(ctx, "send-1", "op-remove", sendNow); err != nil {
		t.Fatalf("set remove id: %v", err)
	}
	got, err = dbs.GetSend(ctx, "send-1")
	if err != nil {
		t.Fatalf("get after remove: %v", err)
	}
	if got.DraftRemoveID != "op-remove" {
		t.Fatalf("draft remove id = %q, want op-remove", got.DraftRemoveID)
	}
}
