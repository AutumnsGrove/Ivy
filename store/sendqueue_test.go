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

	if _, err := dbs.NextQueuedSend(ctx, "acct-1", sendNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("next while the undo window is open = %v, want ErrNotFound", err)
	}
	got, err := dbs.NextQueuedSend(ctx, "acct-1", sendNow.Add(11*time.Second))
	if err != nil {
		t.Fatalf("next after the window: %v", err)
	}
	if got.ID != "send-1" {
		t.Errorf("next = %s, want send-1 (strict FIFO)", got.ID)
	}

	if err := dbs.RetrySend(ctx, "send-1", "transient", "later", sendNow.Add(time.Minute), sendNow); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := dbs.NextQueuedSend(ctx, "acct-1", sendNow.Add(11*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("next during the backoff = %v, want ErrNotFound", err)
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
	if err := dbs.MarkSendDone(ctx, "send-2", "", "", sendNow.Add(-MaxSendTerminalRetention-time.Hour)); err != nil {
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
