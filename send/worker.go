// Package send drains the send queue. It is the only code that opens an SMTP
// connection: it submits each queued message and has the outbox file the Sent
// copy. It never resends a message whose outcome is unknown, because SMTP cannot
// be asked what happened after DATA (docs/handoffs/2026-10-06-G2-send-queue-design.md).
package send

import (
	"context"
	crand "crypto/rand"
	"errors"
	"log/slog"
	mrand "math/rand/v2"
	"time"

	"github.com/AutumnsGrove/Ivy/smtp"
	"github.com/AutumnsGrove/Ivy/store"
)

// Account is the SMTP connection descriptor for one mailbox.
type Account struct {
	ID       string
	Address  string
	Host     string
	Port     int
	Username string
	Password string
	// Insecure dials plaintext. It exists only for the loopback mail world; the
	// zero value is implicit TLS (STANDARDS.md 4a.7).
	Insecure bool
}

// Worker drains one account's send queue and tracks the Sent copies.
type Worker struct {
	dbs       *store.DBs
	acct      Account
	submitter *smtp.Submitter
	now       func() time.Time

	poll        time.Duration
	backoffBase time.Duration
	backoffMax  time.Duration
	jitter      func(time.Duration) time.Duration
	newID       func() string
	onState     func(store.SendMessage)
}

// Option customises a Worker.
type Option func(*Worker)

// WithClock injects the clock the worker records.
func WithClock(fn func() time.Time) Option {
	return func(w *Worker) {
		if fn != nil {
			w.now = fn
		}
	}
}

// WithPoll sets how often an idle worker re-checks the queue.
func WithPoll(d time.Duration) Option {
	return func(w *Worker) {
		if d > 0 {
			w.poll = d
		}
	}
}

// WithBackoff sets the retry delay bounds after a transient failure.
func WithBackoff(base, ceiling time.Duration) Option {
	return func(w *Worker) {
		if base > 0 {
			w.backoffBase = base
		}
		if ceiling >= base {
			w.backoffMax = ceiling
		}
	}
}

// WithJitter replaces the random backoff jitter. Tests pass an identity so the
// delay is deterministic.
func WithJitter(fn func(time.Duration) time.Duration) Option {
	return func(w *Worker) {
		if fn != nil {
			w.jitter = fn
		}
	}
}

// WithIDFunc replaces the append op id generator, so a test can assert on ids.
func WithIDFunc(fn func() string) Option {
	return func(w *Worker) {
		if fn != nil {
			w.newID = fn
		}
	}
}

// WithStateFunc observes every row that changes state, for the send.state hint.
func WithStateFunc(fn func(store.SendMessage)) Option {
	return func(w *Worker) { w.onState = fn }
}

// settleTimeout bounds the writes that record an accepted message.
const settleTimeout = 10 * time.Second

const (
	defaultSendPoll       = time.Second
	defaultSendBackoff    = 5 * time.Second
	defaultSendBackoffMax = 15 * time.Minute
	// pruneInterval is how often Run ages out terminal rows; the retention itself
	// is store.MaxSendTerminalRetention.
	pruneInterval = time.Hour
)

// NewWorker builds a Worker for one account.
func NewWorker(dbs *store.DBs, acct Account, submitter *smtp.Submitter, opts ...Option) *Worker {
	w := &Worker{
		dbs: dbs, acct: acct, submitter: submitter,
		now: time.Now, poll: defaultSendPoll,
		backoffBase: defaultSendBackoff, backoffMax: defaultSendBackoffMax,
		jitter: sendJitter, newID: randomID,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Run drains the queue as long as its context lives, polling when it is empty.
func (w *Worker) Run(ctx context.Context) error {
	var lastPrune time.Time
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "send: drain failed", "account", w.acct.ID, "error", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Terminal rows (bodies included) are the queue's only growth; nothing else
		// deletes them, so the worker that owns the queue ages them out.
		if lastPrune.IsZero() || w.now().Sub(lastPrune) > pruneInterval {
			if _, err := w.dbs.PruneSendQueue(ctx, w.now()); err != nil {
				slog.WarnContext(ctx, "send: prune failed", "account", w.acct.ID, "error", err)
			}
			if _, err := w.dbs.SweepOrphanSendBodies(ctx); err != nil {
				slog.WarnContext(ctx, "send: orphan body sweep failed", "account", w.acct.ID, "error", err)
			}
			lastPrune = w.now()
		}
		if !sleep(ctx, w.poll) {
			return ctx.Err()
		}
	}
}

// RunOnce recovers unknown rows, drains every due send, and settles any Sent
// copies the outbox has finished. It is the test entry point and the body of Run.
func (w *Worker) RunOnce(ctx context.Context) error {
	if _, err := w.dbs.RecoverSubmittingSends(ctx, w.acct.ID, w.now()); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m, err := w.dbs.NextQueuedSend(ctx, w.acct.ID, w.now())
		switch {
		case err == nil:
			if err := w.attempt(ctx, m); err != nil {
				return err
			}
			continue
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
		changed, err := w.trackOneAppend(ctx)
		if err != nil {
			return err
		}
		if changed {
			continue
		}
		return nil
	}
}

// attempt submits one queued message. The cap and the age bound are checked
// first, so a stuck row fails visibly instead of retrying forever.
func (w *Worker) attempt(ctx context.Context, m store.SendMessage) error {
	if m.Attempts >= store.MaxSendAttempts {
		return w.fail(ctx, m, "retries_exhausted", errors.New("the message was not accepted after too many attempts"))
	}
	if !m.CreatedAt.IsZero() && w.now().Sub(m.CreatedAt) >= store.MaxSendAge {
		return w.fail(ctx, m, "expired", errors.New("the message waited more than 24 hours"))
	}
	acct := smtp.Account{
		Host: w.acct.Host, Port: w.acct.Port,
		Username: w.acct.Username, Password: w.acct.Password, Insecure: w.acct.Insecure,
	}
	env := smtp.Envelope{From: m.EnvelopeFrom, To: m.Recipients}
	// The body goes from disk to the socket; a message with attachments is never
	// held whole here.
	wire, size, err := w.dbs.OpenSendWire(ctx, m.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// The stored message is gone (a restore without the body files, a manual
		// clean). It cannot be sent, and sending nothing would be worse.
		return w.fail(ctx, m, "send_gone", errors.New("the stored message is gone"))
	case err != nil:
		return w.retry(ctx, m, "read_body", err)
	}
	err = w.submitter.Submit(ctx, acct, env, wire, size,
		smtp.WithBeforeData(func() error { return w.dbs.SetSendSubmitting(ctx, m.ID, w.now()) }))
	_ = wire.Close()
	if err == nil {
		return w.submitted(ctx, m)
	}
	var se *smtp.SendError
	if !errors.As(err, &se) {
		// A non-SendError from Submit is a contract violation; retry rather than
		// lose the message, and the age cap still bounds it.
		return w.retry(ctx, m, "unknown", err)
	}
	switch {
	case se.Ambiguous:
		if uerr := w.dbs.MarkSendUnconfirmed(ctx, m.ID, se.Error(), w.now()); uerr != nil {
			return uerr
		}
		w.notify(ctx, m.ID)
		return nil
	case se.Transient:
		return w.retry(ctx, m, string(se.Kind), se.Err)
	default:
		return w.fail(ctx, m, string(se.Kind), se.Err)
	}
}

// submitted records the server's 250, then makes sure the Sent copy is queued.
func (w *Worker) submitted(ctx context.Context, m store.SendMessage) error {
	// The server has the message, so this bookkeeping must outlive a shutdown that
	// lands in the window after the 250; it is bounded, not tied to ctx.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	if err := w.dbs.MarkSendSubmitted(ctx, m.ID, w.now()); err != nil {
		// The server accepted but the DB write was lost. The row is still
		// submitting, so recovery marks it unconfirmed; never resend from here.
		return err
	}
	// The draft removal is queued first, while the row is still `submitted`: a
	// missing Sent folder can settle the row terminal below.
	if err := w.ensureDraftRemoval(ctx, m); err != nil {
		return err
	}
	return w.ensureAppend(ctx, m)
}

// ensureDraftRemoval queues the outbox op that removes a sent message's draft
// copy, once. It runs only after the 250, so a message that was never accepted
// keeps its draft; a missing Drafts folder or a full outbox leaves it for a
// later pass. The removal is independent of the Sent copy and never blocks it.
func (w *Worker) ensureDraftRemoval(ctx context.Context, m store.SendMessage) error {
	if m.DraftMessageID == "" || m.DraftRemoveID != "" {
		return nil
	}
	drafts, err := w.dbs.FolderByRole(ctx, w.acct.ID, store.RoleDrafts)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	op, _, err := w.dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: w.newID(), AccountID: w.acct.ID, Kind: store.OutboxDraft,
		ContentKey: store.ContentKey(m.DraftMessageID, nil), SourceFolderID: drafts.ID,
		Expect: store.OutboxExpect{
			DestFolderID: drafts.ID,
			Supersedes:   []string{m.DraftMessageID},
			Remove:       true,
		},
		CreatedAt: w.now(),
	})
	if errors.Is(err, store.ErrOutboxFull) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := w.dbs.SetSendDraftRemoveID(ctx, m.ID, op.ID, w.now()); err != nil {
		return err
	}
	if err := w.dbs.MarkDraftSent(ctx, w.acct.ID, m.DraftMessageID, w.now()); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	w.notify(ctx, m.ID)
	return nil
}

// ensureAppend enqueues the Sent append op once. No Sent folder means nothing to
// file, so the send is done. A full outbox leaves the row submitted for a later
// pass; the message is already sent, so nothing is lost.
func (w *Worker) ensureAppend(ctx context.Context, m store.SendMessage) error {
	if m.SentAppendID != "" {
		return nil
	}
	sent, err := w.dbs.FolderByRole(ctx, w.acct.ID, store.RoleSent)
	if errors.Is(err, store.ErrNotFound) {
		if err := w.dbs.MarkSendDone(ctx, m.ID, "", "", w.now()); err != nil {
			return err
		}
		w.notify(ctx, m.ID)
		return nil
	}
	if err != nil {
		return err
	}
	stored, _, err := w.dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: w.newID(), AccountID: w.acct.ID, Kind: store.OutboxAppend,
		ContentKey: m.ContentKey,
		Expect:     store.OutboxExpect{DestFolderID: sent.ID, FlagsAdd: []string{`\Seen`}, SendID: m.ID},
		CreatedAt:  w.now(),
	})
	if errors.Is(err, store.ErrOutboxFull) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := w.dbs.SetSendAppendID(ctx, m.ID, stored.ID, w.now()); err != nil {
		return err
	}
	w.notify(ctx, m.ID)
	return nil
}

// trackOneAppend settles one submitted row against its Sent copy op. It reports
// whether the row changed, so RunOnce does not spin on an op still in flight.
func (w *Worker) trackOneAppend(ctx context.Context) (bool, error) {
	m, err := w.dbs.NextSubmittedSend(ctx, w.acct.ID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A crash between the 250 and the removal being queued is healed here, before
	// the Sent copy settles the row terminal.
	if err := w.ensureDraftRemoval(ctx, m); err != nil {
		return false, err
	}
	if m.SentAppendID == "" {
		if err := w.ensureAppend(ctx, m); err != nil {
			return false, err
		}
		m, err = w.dbs.GetSend(ctx, m.ID)
		if err != nil {
			return false, err
		}
		if m.SentAppendID == "" {
			return false, nil // the outbox is full; try again next pass
		}
	}
	op, err := w.dbs.GetOutbox(ctx, m.SentAppendID)
	if errors.Is(err, store.ErrNotFound) {
		// The op vanished (pruned); re-enqueue it on the next pass.
		return true, w.dbs.SetSendAppendID(ctx, m.ID, "", w.now())
	}
	if err != nil {
		return false, err
	}
	switch op.State {
	case store.OutboxDone:
		if err := w.dbs.MarkSendAppended(ctx, m.ID, w.now()); err != nil {
			return false, err
		}
		w.notify(ctx, m.ID)
		return true, nil
	case store.OutboxFailed, store.OutboxCancelled:
		if err := w.dbs.MarkSendDone(ctx, m.ID, "sent_copy_failed",
			"the message was sent, but Ivy could not file its Sent copy", w.now()); err != nil {
			return false, err
		}
		w.notify(ctx, m.ID)
		return true, nil
	default:
		return false, nil // still pending or in flight: the outbox worker owns it
	}
}

func (w *Worker) retry(ctx context.Context, m store.SendMessage, code string, cause error) error {
	detail := ""
	if cause != nil {
		detail = cause.Error()
	}
	if err := w.dbs.RetrySend(ctx, m.ID, code, detail, w.now().Add(w.backoff(m.Attempts+1)), w.now()); err != nil {
		return err
	}
	w.notify(ctx, m.ID)
	return nil
}

func (w *Worker) fail(ctx context.Context, m store.SendMessage, code string, cause error) error {
	detail := ""
	if cause != nil {
		detail = cause.Error()
	}
	if err := w.dbs.FailSend(ctx, m.ID, code, detail, w.now()); err != nil {
		return err
	}
	w.notify(ctx, m.ID)
	return nil
}

func (w *Worker) notify(ctx context.Context, id string) {
	if w.onState == nil {
		return
	}
	m, err := w.dbs.GetSend(ctx, id)
	if err != nil {
		return
	}
	w.onState(m)
}

// backoff is exponential from the base to the ceiling, then jittered.
func (w *Worker) backoff(attempt int) time.Duration {
	d := w.backoffBase
	for i := 1; i < attempt; i++ {
		if d >= w.backoffMax/2 {
			d = w.backoffMax
			break
		}
		d *= 2
	}
	if d > w.backoffMax {
		d = w.backoffMax
	}
	return w.jitter(d)
}

// sendJitter spreads a retry by ±20% so a fleet of accounts does not retry in
// lockstep after a provider outage.
func sendJitter(d time.Duration) time.Duration {
	delta := int64(d) / 5
	if delta <= 0 {
		return d
	}
	//nolint:gosec // G404: jitter is not security-sensitive
	return d + time.Duration(mrand.Int64N(2*delta+1)) - time.Duration(delta)
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// randomID is a 128-bit random hex string for an append op id. It mirrors the
// gateway's generator; a collision is harmless because the op id is not the
// idempotency key.
func randomID() string {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hex[c>>4]
		out[i*2+1] = hex[c&0x0f]
	}
	return string(out)
}
