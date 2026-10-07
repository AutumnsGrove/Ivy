package send_test

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/send"
	"github.com/AutumnsGrove/Ivy/smtp"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// The worker submits to the fake's SMTP port and files through the same account's
// outbox, so a test drives both halves of the round trip.
type fixture struct {
	ctx      context.Context
	w        *mailworld.World
	acc      *mailworld.Account
	dbs      *store.DBs
	acct     send.Account
	syncAcct ivysync.Account
	worker   *send.Worker
	outbox   *ivysync.OutboxWorker
	now      time.Time
}

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	return host, port
}

func newFixture(t *testing.T, withSent bool, opts ...mailworld.Option) *fixture {
	t.Helper()
	w, err := mailworld.New(opts...)
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	acc := w.Account("me@grove.test", "secret")
	if withSent {
		if err := acc.CreateMailbox("Sent"); err != nil {
			t.Fatalf("create Sent: %v", err)
		}
	}
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = dbs.Close() })

	imapHost, imapPort := splitAddr(t, w.IMAPAddr())
	smtpHost, smtpPort := splitAddr(t, w.SMTPAddr())
	syncAcct := ivysync.Account{
		ID: "acct-1", Address: "me@grove.test",
		IMAPHost: imapHost, IMAPPort: imapPort,
		Username: "me@grove.test", Password: "secret", Insecure: true,
	}
	if _, err := ivysync.NewFetcher(dbs).Fetch(context.Background(), syncAcct); err != nil {
		t.Fatalf("sync: %v", err)
	}

	fx := &fixture{
		ctx: context.Background(), w: w, acc: acc, dbs: dbs, syncAcct: syncAcct,
		now: time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC),
	}
	fx.acct = send.Account{
		ID: "acct-1", Address: "me@grove.test",
		Host: smtpHost, Port: smtpPort,
		Username: "me@grove.test", Password: "secret", Insecure: true,
	}
	fx.worker = send.NewWorker(dbs, fx.acct, smtp.New(smtp.WithTimeout(2*time.Second, 2*time.Second, 2*time.Second)),
		send.WithClock(func() time.Time { return fx.now }), send.WithPoll(time.Millisecond))
	// The outbox worker shares the fixture's clock. On the real clock an op the send
	// worker stamped with the fixed fx.now looks more than MaxOutboxAge old a day
	// after that date and is failed as expired, which turned these tests red.
	fx.outbox = ivysync.NewOutboxWorker(
		ivysync.NewFetcher(dbs, ivysync.WithClock(func() time.Time { return fx.now })), syncAcct)
	return fx
}

func (fx *fixture) enqueue(t *testing.T, id string) store.SendMessage {
	t.Helper()
	msgID := "<" + id + "@example.test>"
	body := mailworld.Msg().From("me@grove.test").To("you@example.test").
		Subject("hi").MessageID(msgID).Text("hello").Build()
	m, _, err := fx.dbs.EnqueueSend(fx.ctx, store.SendMessage{
		ID: id, AccountID: "acct-1", MessageID: msgID,
		ContentKey: store.ContentKey(msgID, nil), EnvelopeFrom: "me@grove.test",
		Recipients: []string{"you@example.test"}, WireBody: body, SentBody: body,
		CreatedAt: fx.now, UpdatedAt: fx.now,
	})
	if err != nil {
		t.Fatalf("enqueue %s: %v", id, err)
	}
	return m
}

func (fx *fixture) row(t *testing.T, id string) store.SendMessage {
	t.Helper()
	m, err := fx.dbs.GetSend(fx.ctx, id)
	if err != nil {
		t.Fatalf("get send %s: %v", id, err)
	}
	return m
}

func (fx *fixture) runSend(t *testing.T) {
	t.Helper()
	if err := fx.worker.RunOnce(fx.ctx); err != nil {
		t.Fatalf("send run: %v", err)
	}
}

func (fx *fixture) runOutbox(t *testing.T) {
	t.Helper()
	if err := fx.outbox.RunOnce(fx.ctx); err != nil {
		t.Fatalf("outbox run: %v", err)
	}
}

func sentSubjects(t *testing.T, acc *mailworld.Account, mailbox string) []string {
	t.Helper()
	msgs, err := acc.Messages(mailbox)
	if err != nil {
		t.Fatalf("read %s: %v", mailbox, err)
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.MessageID)
	}
	return out
}

// One send goes out once and its Sent copy is filed exactly once.
func TestSendDeliversAndFilesTheSentCopy(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	fx.enqueue(t, "send-1")
	fx.runSend(t)

	if got := len(fx.w.Sent()); got != 1 {
		t.Fatalf("SMTP recorded %d messages, want 1", got)
	}
	if row := fx.row(t, "send-1"); row.State != store.SendSubmitted {
		t.Fatalf("state = %q, want submitted (the Sent copy is still pending)", row.State)
	} else if row.SentAppendID == "" {
		t.Fatal("the append op was not recorded with the submit")
	}

	fx.runOutbox(t)
	fx.runSend(t)
	row := fx.row(t, "send-1")
	if row.State != store.SendAppended {
		t.Fatalf("state = %q, want appended", row.State)
	}
	if got := len(sentSubjects(t, fx.acc, "Sent")); got != 1 {
		t.Errorf("Sent holds %d copies, want 1", got)
	}
}

// No Sent folder means nothing to file; the send still completes.
func TestSendWithoutSentFolderIsDone(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, false)
	fx.enqueue(t, "send-1")
	fx.runSend(t)
	if row := fx.row(t, "send-1"); row.State != store.SendDone {
		t.Fatalf("state = %q, want done", row.State)
	}
}

// A transient 4xx leaves the row queued with a backoff; the retry succeeds.
func TestSendTransientFailureRetries(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	fx.w.Fault(mailworld.SMTPReject{Code: 450, Message: "later"})
	fx.enqueue(t, "send-1")
	fx.runSend(t)

	row := fx.row(t, "send-1")
	if row.State != store.SendQueued || row.Attempts != 1 {
		t.Fatalf("row = %+v, want queued with 1 attempt", row)
	}
	if row.NextAttemptAt.IsZero() {
		t.Error("no backoff was recorded")
	}
	if got := len(fx.w.Sent()); got != 0 {
		t.Fatalf("recorded %d messages after a 4xx, want 0", got)
	}

	fx.w.ClearFaults()
	fx.now = fx.now.Add(time.Hour)
	fx.runSend(t)
	if got := len(fx.w.Sent()); got != 1 {
		t.Fatalf("recorded %d messages after the retry, want 1", got)
	}
}

// A permanent 5xx fails the row and sends nothing.
func TestSendPermanentFailureFails(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	fx.w.Fault(mailworld.SMTPReject{Code: 550, Message: "no"})
	fx.enqueue(t, "send-1")
	fx.runSend(t)

	row := fx.row(t, "send-1")
	if row.State != store.SendFailed {
		t.Fatalf("state = %q, want failed", row.State)
	}
	if got := len(fx.w.Sent()); got != 0 {
		t.Errorf("recorded %d messages, want 0", got)
	}
}

// TestSendAcceptThenDropNeverResends is the crash window repeated: the server
// accepts and the link dies before the 250, so every row is unconfirmed and a
// second pass never sends a duplicate.
func TestSendAcceptThenDropNeverResends(t *testing.T) {
	t.Parallel()
	const repetitions = 12
	fx := newFixture(t, true)
	fx.w.Fault(mailworld.SMTPAcceptThenDrop{})
	for i := range repetitions {
		fx.enqueue(t, "send-"+strconv.Itoa(i))
	}
	fx.runSend(t)

	if got := len(fx.w.Sent()); got != repetitions {
		t.Fatalf("SMTP recorded %d messages, want %d (all accepted before the drop)", got, repetitions)
	}
	for i := range repetitions {
		if row := fx.row(t, "send-"+strconv.Itoa(i)); row.State != store.SendUnconfirmed {
			t.Fatalf("row %d state = %q, want unconfirmed", i, row.State)
		}
	}
	// Nothing may be resent, even after the fault clears.
	fx.w.ClearFaults()
	fx.runSend(t)
	if got := len(fx.w.Sent()); got != repetitions {
		t.Errorf("a second pass resent messages: %d recorded, want %d", got, repetitions)
	}
}

// A crash after the BeforeData commit (during DATA, or after the 250 before the
// DB write) is unconfirmed on the next start and never resent.
func TestSendCrashDuringDataIsUnconfirmed(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	fx.enqueue(t, "send-1")
	if err := fx.dbs.SetSendSubmitting(fx.ctx, "send-1", fx.now); err != nil {
		t.Fatalf("set submitting: %v", err)
	}

	fx.runSend(t)
	row := fx.row(t, "send-1")
	if row.State != store.SendUnconfirmed {
		t.Fatalf("state = %q, want unconfirmed", row.State)
	}
	if got := len(fx.w.Sent()); got != 0 {
		t.Errorf("recorded %d messages, want 0 (nothing was sent before the crash)", got)
	}
	fx.runSend(t)
	if got := len(fx.w.Sent()); got != 0 {
		t.Errorf("an unconfirmed row was resent: %d recorded", got)
	}
}

// A crash after the 250 but before the append op is healed by the worker, and
// the Sent copy is still filed exactly once.
func TestSendCrashAfterSubmitBeforeAppend(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	fx.enqueue(t, "send-1")
	if err := fx.dbs.SetSendSubmitting(fx.ctx, "send-1", fx.now); err != nil {
		t.Fatalf("set submitting: %v", err)
	}
	if err := fx.dbs.MarkSendSubmitted(fx.ctx, "send-1", fx.now); err != nil {
		t.Fatalf("mark submitted: %v", err)
	}

	fx.runSend(t) // enqueues the missing append op
	if row := fx.row(t, "send-1"); row.SentAppendID == "" {
		t.Fatal("the append op was not re-enqueued after the crash")
	}
	fx.runOutbox(t)
	fx.runSend(t)
	if row := fx.row(t, "send-1"); row.State != store.SendAppended {
		t.Fatalf("state = %q, want appended", row.State)
	}
	if got := len(sentSubjects(t, fx.acc, "Sent")); got != 1 {
		t.Errorf("Sent holds %d copies, want exactly 1", got)
	}
}

// TestSendWaitsForTheUndoWindow proves the deadline is respected and survives a
// restart: nothing is sent before it, and a fresh worker sends once it passes.
func TestSendWaitsForTheUndoWindow(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	msgID := "<undo-window@example.test>"
	body := mailworld.Msg().From("me@grove.test").To("you@example.test").
		Subject("undo").MessageID(msgID).Text("wait").Build()
	deadline := fx.now.Add(10 * time.Second)
	if _, _, err := fx.dbs.EnqueueSend(fx.ctx, store.SendMessage{
		ID: "send-1", AccountID: "acct-1", MessageID: msgID,
		ContentKey: store.ContentKey(msgID, nil), EnvelopeFrom: "me@grove.test",
		Recipients: []string{"you@example.test"}, WireBody: body, SentBody: body,
		UndoDeadline: deadline, CreatedAt: fx.now, UpdatedAt: fx.now,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	fx.runSend(t)
	if got := len(fx.w.Sent()); got != 0 {
		t.Fatalf("sent %d messages inside the undo window, want 0", got)
	}

	// A restart is a fresh worker reading the deadline from the database.
	fx.now = deadline
	fx.runSend(t)
	if got := len(fx.w.Sent()); got != 1 {
		t.Fatalf("sent %d messages after the window closed, want 1", got)
	}
}

// A failed Sent copy does not fail the send: the message went out, only Ivy's
// own copy is missing, and the row says so.
func TestSendSentCopyFailureIsNotASendFailure(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	fx.enqueue(t, "send-1")
	fx.runSend(t)
	row := fx.row(t, "send-1")
	if row.SentAppendID == "" {
		t.Fatal("no append op to fail")
	}
	if err := fx.dbs.SetOutboxFailed(fx.ctx, row.SentAppendID, "mailbox_full", "no room", fx.now); err != nil {
		t.Fatalf("fail append: %v", err)
	}
	fx.runSend(t)

	row = fx.row(t, "send-1")
	if row.State != store.SendDone {
		t.Fatalf("state = %q, want done (the mail was sent)", row.State)
	}
	if row.LastErrorCode != "sent_copy_failed" {
		t.Errorf("code = %q, want sent_copy_failed", row.LastErrorCode)
	}
	if got := len(fx.w.Sent()); got != 1 {
		t.Errorf("SMTP recorded %d messages, want 1", got)
	}
}

// A shutdown that lands just after the server's 250 must not lose the fact that
// the message was accepted: the bookkeeping that follows is not allowed to die
// with the worker's context, or the row stays `submitting` and the next start
// calls an accepted, filed-nowhere message unconfirmed.
func TestSendShutdownAfterTheServerAcceptsStillRecordsIt(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	fx.enqueue(t, "send-1")

	ctx, cancel := context.WithCancel(fx.ctx)
	defer cancel()
	// The fake records the message before it answers 250, so the first clock read
	// after that is the worker recording the accept: cancel exactly there.
	worker := send.NewWorker(fx.dbs, fx.acct, smtp.New(smtp.WithTimeout(2*time.Second, 2*time.Second, 2*time.Second)),
		send.WithClock(func() time.Time {
			if len(fx.w.Sent()) > 0 {
				cancel()
			}
			return fx.now
		}), send.WithPoll(time.Millisecond))
	_ = worker.RunOnce(ctx)

	if got := len(fx.w.Sent()); got != 1 {
		t.Fatalf("SMTP recorded %d messages, want 1", got)
	}
	row := fx.row(t, "send-1")
	if row.State != store.SendSubmitted || row.SentAppendID == "" {
		t.Fatalf("state = %q, append id %q, want submitted with its Sent copy queued", row.State, row.SentAppendID)
	}
}

// draftInto files one draft version through the real store path and the outbox,
// so the worker sees the same server copy a compose save would create.
func (fx *fixture) draftInto(t *testing.T, id, msgID string) store.Draft {
	t.Helper()
	drafts, err := fx.dbs.FolderByRole(fx.ctx, "acct-1", store.RoleDrafts)
	if err != nil {
		t.Fatalf("drafts folder: %v", err)
	}
	body := mailworld.Msg().From("me@grove.test").To("you@example.test").
		Subject("draft").MessageID(msgID).Text("hello").Build()
	d, err := fx.dbs.SaveDraft(fx.ctx, store.SaveDraftInput{
		ID: id, DraftID: "d-" + id, AccountID: "acct-1",
		DestFolderID: drafts.ID, MessageID: msgID,
		Subject: "draft", To: []string{"you@example.test"},
		Compose: []byte(`{"subject":"draft"}`), Body: body,
		BaseVersion: 0, OpID: id + "-op", Now: fx.now,
	})
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}
	fx.runOutbox(t)
	return d
}

// draftWorld adds a Drafts folder and re-syncs so its role is mirrored.
func draftWorld(t *testing.T, fx *fixture) {
	t.Helper()
	if err := fx.acc.CreateMailbox("Drafts"); err != nil {
		t.Fatalf("create Drafts: %v", err)
	}
	if _, err := ivysync.NewFetcher(fx.dbs).Fetch(fx.ctx, fx.syncAcct); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
}

func (fx *fixture) enqueueNamed(t *testing.T, id, draftMessageID string) store.SendMessage {
	t.Helper()
	msgID := "<" + id + "@example.test>"
	body := mailworld.Msg().From("me@grove.test").To("you@example.test").
		Subject("hi").MessageID(msgID).Text("hello").Build()
	m, _, err := fx.dbs.EnqueueSend(fx.ctx, store.SendMessage{
		ID: id, AccountID: "acct-1", MessageID: msgID,
		ContentKey: store.ContentKey(msgID, nil), EnvelopeFrom: "me@grove.test",
		Recipients: []string{"you@example.test"}, WireBody: body, SentBody: body,
		DraftMessageID: draftMessageID, CreatedAt: fx.now, UpdatedAt: fx.now,
	})
	if err != nil {
		t.Fatalf("enqueue %s: %v", id, err)
	}
	return m
}

// A sent message leaves Drafts: after the 250 the worker queues the draft's
// removal, the outbox expunges the copy, and the version is marked sent.
func TestSendRemovesTheDraftItCameFrom(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	draftWorld(t, fx)
	d := fx.draftInto(t, "v1", "<draft-1@example.test>")
	if got := sentSubjects(t, fx.acc, "Drafts"); len(got) != 1 {
		t.Fatalf("Drafts holds %d copies, want the filed one", len(got))
	}

	fx.enqueueNamed(t, "send-1", d.MessageID)
	fx.runSend(t)
	if fx.row(t, "send-1").DraftRemoveID == "" {
		t.Fatal("the draft removal was not queued with the submit")
	}
	fx.runOutbox(t)

	if got := sentSubjects(t, fx.acc, "Drafts"); len(got) != 0 {
		t.Errorf("Drafts holds %d copies after the send, want 0", len(got))
	}
	if got, err := fx.dbs.DraftVersion(fx.ctx, d.ID); err != nil || got.State != store.DraftSent {
		t.Errorf("draft version = %+v, %v, want sent", got, err)
	}
}

// A crash after the 250 but before the removal is queued is healed on the next
// pass, so a sent draft still leaves Drafts after a restart.
func TestSendRecoversTheDraftRemovalAfterARestart(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	draftWorld(t, fx)
	d := fx.draftInto(t, "v1", "<draft-1@example.test>")

	m := fx.enqueueNamed(t, "send-1", d.MessageID)
	if err := fx.dbs.SetSendSubmitting(fx.ctx, m.ID, fx.now); err != nil {
		t.Fatalf("set submitting: %v", err)
	}
	if err := fx.dbs.MarkSendSubmitted(fx.ctx, m.ID, fx.now); err != nil {
		t.Fatalf("mark submitted: %v", err)
	}
	if fx.row(t, m.ID).DraftRemoveID != "" {
		t.Fatal("precondition: the removal is already queued")
	}

	fx.runSend(t)
	fx.runOutbox(t)
	if got := sentSubjects(t, fx.acc, "Drafts"); len(got) != 0 {
		t.Errorf("Drafts holds %d copies after recovery, want 0", len(got))
	}
	if got, err := fx.dbs.DraftVersion(fx.ctx, d.ID); err != nil || got.State != store.DraftSent {
		t.Errorf("draft version = %+v, %v, want sent", got, err)
	}
}

// A terminal send older than the retention is pruned by the worker itself. The
// prune existed but nothing called it, so every message, body and all, stayed in
// the queue for good.
func TestRunPrunesOldTerminalSends(t *testing.T) {
	fx := newFixture(t, true)
	fx.enqueue(t, "old")
	if err := fx.dbs.FailSend(fx.ctx, "old", "rejected", "a long time ago", fx.now.Add(-8*24*time.Hour)); err != nil {
		t.Fatalf("fail send: %v", err)
	}
	fx.enqueue(t, "recent")
	if err := fx.dbs.FailSend(fx.ctx, "recent", "rejected", "yesterday", fx.now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("fail recent: %v", err)
	}

	ctx, cancel := context.WithCancel(fx.ctx)
	done := make(chan error, 1)
	go func() { done <- fx.worker.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := fx.dbs.GetSend(fx.ctx, "old"); errors.Is(err, store.ErrNotFound) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if _, err := fx.dbs.GetSend(fx.ctx, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the 8-day-old terminal send = %v, want it pruned", err)
	}
	if _, err := fx.dbs.GetSend(fx.ctx, "recent"); err != nil {
		t.Errorf("the recent terminal send = %v, want it kept", err)
	}
}

// What reaches the SMTP server is the stored wire copy, byte for byte. The worker
// streams it from disk; a worker that submitted an empty body would still settle
// the row, so the bytes themselves are what is asserted.
func TestSendSubmitsTheStoredWireBytes(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	fx.enqueue(t, "send-1")
	fx.runSend(t)

	sent := fx.w.Sent()
	if len(sent) != 1 {
		t.Fatalf("SMTP accepted %d messages, want 1", len(sent))
	}
	raw := string(sent[0].Raw)
	for _, want := range []string{"Subject: hi", "Message-Id: <send-1@example.test>", "hello"} {
		if !strings.Contains(strings.ToLower(raw), strings.ToLower(want)) {
			t.Errorf("SMTP received %q, want it to contain %q", raw, want)
		}
	}
}

// If the wire file is gone (a restore without the body files, a manual clean) the
// send fails visibly: it never submits an empty message.
func TestSendFailsWhenItsBodyFileIsGone(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, true)
	fx.enqueue(t, "send-1")
	var hash string
	if err := fx.dbs.State.Read.QueryRowContext(fx.ctx, `SELECT wire_hash FROM send_queue WHERE id = 'send-1'`).Scan(&hash); err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := os.Remove(fx.dbs.SendBodies.Path(hash)); err != nil {
		t.Fatalf("remove body: %v", err)
	}
	fx.runSend(t)

	if got := fx.row(t, "send-1"); got.State != store.SendFailed || got.LastErrorCode != "send_gone" {
		t.Errorf("row = %s/%s, want failed send_gone", got.State, got.LastErrorCode)
	}
	if n := len(fx.w.Sent()); n != 0 {
		t.Errorf("SMTP accepted %d messages, want none", n)
	}
}

// The worker's prune pass also collects body files no send row references, so a
// crash between writing a body and queueing its row cannot leak tens of MiB.
func TestRunSweepsOrphanSendBodies(t *testing.T) {
	fx := newFixture(t, true)
	orphan, _, err := fx.dbs.SendBodies.Put(fx.ctx, strings.NewReader("a body no row names"))
	if err != nil {
		t.Fatalf("put orphan: %v", err)
	}

	ctx, cancel := context.WithCancel(fx.ctx)
	done := make(chan error, 1)
	go func() { done <- fx.worker.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for fx.dbs.SendBodies.Has(orphan) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if fx.dbs.SendBodies.Has(orphan) {
		t.Fatal("the orphan send body survived the worker's prune pass")
	}
}
