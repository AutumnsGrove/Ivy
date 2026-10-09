package jev

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// SettingSince is the per-account arrival watermark: mail that reached the server
// before it is never classified automatically. It is set the first time the worker
// finds the feature on, so "turn it on" means "from now on" and never a surprise
// bill for history. Empty means unset.
const SettingSince = "jev.since"

// headerAllowance is what a state's headers add to its body when pricing a backfill.
const headerAllowance = 512

// Window is how far back a backfill reaches.
type Window time.Duration

// Days is a backfill window of n days.
func Days(n int) Window { return Window(time.Duration(n) * 24 * time.Hour) }

// AllMail reaches back over the whole mirror.
const AllMail Window = -1

// WorkerOptions bound the worker. Zero values take the defaults.
type WorkerOptions struct {
	// Batch is the most messages one pass takes per account: the burst bound.
	Batch int
	// Concurrency is how many calls are in flight at once. It is below the gate's own
	// slot count, so classification never starves the interactive features.
	Concurrency int
	// Interval is the wait when idle; BusyInterval when a pass did work.
	Interval, BusyInterval time.Duration
	// MaxBackoff caps the wait after repeated errors.
	MaxBackoff time.Duration
	// CapPause is the wait after a spending cap: nothing changes until the period
	// rolls over, so polling sooner would only write refusals.
	CapPause time.Duration
	Now      func() time.Time
}

func (o *WorkerOptions) applyDefaults() {
	if o.Batch <= 0 {
		o.Batch = 10
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 2
	}
	if o.Interval <= 0 {
		o.Interval = 30 * time.Second
	}
	if o.BusyInterval <= 0 {
		o.BusyInterval = 2 * time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 10 * time.Minute
	}
	if o.CapPause <= 0 {
		o.CapPause = 30 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// nextDelay is how long Run waits after a pass: the cap pause, an exponential
// backoff on other errors, a short wait after real work, else the idle interval.
func (o WorkerOptions) nextDelay(err error, processed, failures int) time.Duration {
	switch {
	case errors.Is(err, llm.ErrCapReached):
		return o.CapPause
	case err != nil:
		d := o.Interval
		for i := 0; i < failures && i < 16 && d < o.MaxBackoff; i++ {
			d *= 2
		}
		if d > o.MaxBackoff {
			d = o.MaxBackoff
		}
		return d
	case processed > 0:
		return o.BusyInterval
	}
	return o.Interval
}

// WorkerGate is what the worker needs of llm.Gate.
type WorkerGate interface {
	CanRun(ctx context.Context, accountID, feature string) bool
	Estimate(ctx context.Context, req llm.EstimateRequest) (llm.Estimate, error)
}

// WorkerStore is the queue and the watermark.
type WorkerStore interface {
	ListAccounts(ctx context.Context) ([]store.Account, error)
	UnclassifiedMessages(ctx context.Context, accountID string, since time.Time, limit int) ([]store.Candidate, error)
	CountUnclassified(ctx context.Context, accountID string, since time.Time) (int, error)
	MeanBodyBytes(ctx context.Context, accountID string, since time.Time) (int, error)
	MarkClassified(ctx context.Context, accountID string, contentKeys []string, at time.Time) error
	ListAttachments(ctx context.Context, messageID string) ([]store.Attachment, error)
	GetSetting(ctx context.Context, accountID, key string) (string, bool, error)
	SetSetting(ctx context.Context, accountID, key, value string) error
}

// Worker classifies new mail in the background: low priority, a bounded batch per
// pass, bounded concurrency, and silent for an account that is off.
type Worker struct {
	engine *Engine
	gate   WorkerGate
	store  WorkerStore
	opts   WorkerOptions
}

// NewWorker builds the queue worker.
func NewWorker(e *Engine, gate WorkerGate, st WorkerStore, opts WorkerOptions) *Worker {
	opts.applyDefaults()
	return &Worker{engine: e, gate: gate, store: st, opts: opts}
}

// Run passes until the context ends, waiting between passes as nextDelay says.
func (w *Worker) Run(ctx context.Context) error {
	failures := 0
	for {
		n, err := w.RunOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			failures++
			slog.WarnContext(ctx, "jev: a classification pass failed", "error", err, "failures", failures)
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.opts.nextDelay(err, n, failures)):
		}
	}
}

// RunOnce makes one bounded pass over every account and returns how many messages it
// considered. An account that is off, or has nothing to ask, is skipped without a
// call or a ledger row. The first error is returned after every account has had its
// turn, so one account's cap does not stop another's work.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	accounts, err := w.store.ListAccounts(ctx)
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
	now := w.opts.Now()
	if !w.gate.CanRun(ctx, id, CallFeature) {
		// Off: forget the watermark, so turning it back on starts from then.
		return 0, w.setSince(ctx, id, time.Time{})
	}
	if !w.engine.Applicable(id) {
		// Nothing to ask yet: the watermark follows the clock, so a question that
		// ships later does not reach back over mail that arrived before it.
		return 0, w.setSince(ctx, id, now)
	}
	since, err := w.since(ctx, id)
	if err != nil {
		return 0, err
	}
	if since.IsZero() {
		return 0, w.setSince(ctx, id, now)
	}
	batch, err := w.store.UnclassifiedMessages(ctx, id, since, w.opts.Batch)
	if err != nil || len(batch) == 0 {
		return 0, err
	}
	return w.process(ctx, id, batch)
}

// process classifies a batch with bounded concurrency. A refusal that means "this
// account is off right now" ends the batch quietly; a cap or any other failure ends
// it with an error. Whatever was not answered is not marked, so it waits.
func (w *Worker) process(ctx context.Context, account string, batch []store.Candidate) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		done     int
		firstErr error
	)
	slots := make(chan struct{}, w.opts.Concurrency)
	stop := func(err error) {
		mu.Lock()
		if firstErr == nil && err != nil {
			firstErr = err
		}
		mu.Unlock()
		cancel()
	}
	for _, c := range batch {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(c store.Candidate) {
			defer wg.Done()
			defer func() { <-slots }()
			atts, err := w.store.ListAttachments(ctx, c.Message.ID)
			if err != nil {
				// Names are a hint; classify without them rather than not at all.
				slog.WarnContext(ctx, "jev: cannot list attachments", "message", c.Message.ID, "error", err)
			}
			_, err = w.engine.Decide(ctx, FromStored(c.Message, c.Role, atts))
			switch {
			case err == nil:
				if err := w.store.MarkClassified(ctx, account, []string{c.Message.ContentKey}, w.opts.Now()); err != nil {
					stop(err)
					return
				}
				mu.Lock()
				done++
				mu.Unlock()
			case isOffRefusal(err):
				cancel() // off right now: not an error, and nothing is marked
			default:
				stop(err)
			}
		}(c)
	}
	wg.Wait()
	return done, firstErr
}

// isOffRefusal is a gate refusal that says the account or feature is not available
// right now, which is a state to wait out, not a failure to report.
func isOffRefusal(err error) bool {
	return errors.Is(err, llm.ErrNotEnabled) || errors.Is(err, llm.ErrFeatureOff) ||
		errors.Is(err, llm.ErrWithheld) || errors.Is(err, llm.ErrNoProvider)
}

func (w *Worker) since(ctx context.Context, account string) (time.Time, error) {
	v, ok, err := w.store.GetSetting(ctx, account, SettingSince)
	if err != nil || !ok || v == "" {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		// A corrupt watermark must never mean "everything": start from now.
		slog.WarnContext(ctx, "jev: unreadable watermark; starting from now", "account", account, "error", err)
		return time.Time{}, nil
	}
	return t, nil
}

func (w *Worker) setSince(ctx context.Context, account string, t time.Time) error {
	v := ""
	if !t.IsZero() {
		v = t.UTC().Format(time.RFC3339Nano)
	}
	if old, ok, _ := w.store.GetSetting(ctx, account, SettingSince); ok && old == v {
		return nil
	}
	return w.store.SetSetting(ctx, account, SettingSince, v)
}

// BackfillEstimate prices reaching back over history before anyone agrees to it.
type BackfillEstimate struct {
	// Items is how many messages the backfill would read at most.
	Items int
	// Since is the watermark the backfill would set.
	Since    time.Time
	Estimate llm.Estimate
}

func (w *Worker) windowStart(win Window) time.Time {
	if win == AllMail {
		return time.Time{}
	}
	return w.opts.Now().Add(-time.Duration(win))
}

// EstimateBackfill says what a backfill over this window would read and cost at
// most. It never calls a provider and changes nothing.
func (w *Worker) EstimateBackfill(ctx context.Context, account string, win Window) (BackfillEstimate, error) {
	since := w.windowStart(win)
	if cur, err := w.since(ctx, account); err == nil && !cur.IsZero() && cur.Before(since) {
		since = cur // never narrower than what is already eligible
	}
	items, err := w.store.CountUnclassified(ctx, account, since)
	if err != nil {
		return BackfillEstimate{}, err
	}
	out := BackfillEstimate{Items: items, Since: since}
	if items == 0 {
		out.Estimate = llm.Estimate{Fits: true}
		return out, nil
	}
	mean, err := w.store.MeanBodyBytes(ctx, account, since)
	if err != nil {
		return BackfillEstimate{}, err
	}
	size := mean + headerAllowance
	if size > llm.MaxDecideBytes {
		size = llm.MaxDecideBytes
	}
	out.Estimate, err = w.gate.Estimate(ctx, llm.EstimateRequest{
		Feature: CallFeature, AccountID: account, Items: items, BytesPerItem: size,
		Questions: w.engine.QuestionCount(account),
	})
	return out, err
}

// StartBackfill makes history within the window eligible by lowering the watermark.
// It only ever reaches further back, never forward, and the spending caps still
// apply as the worker reads. The caller shows EstimateBackfill first.
func (w *Worker) StartBackfill(ctx context.Context, account string, win Window) error {
	since := w.windowStart(win)
	cur, err := w.since(ctx, account)
	if err != nil {
		return err
	}
	if !cur.IsZero() && !since.IsZero() && !since.Before(cur) {
		return nil
	}
	if since.IsZero() {
		// AllMail has no instant, but an unset watermark means "off", so use the
		// earliest the store can compare.
		since = time.Date(1, 1, 2, 0, 0, 0, 0, time.UTC)
	}
	return w.setSince(ctx, account, since)
}

// Depth is how many messages are waiting for an account, for the stats panel.
func (w *Worker) Depth(ctx context.Context, account string) (int, error) {
	since, err := w.since(ctx, account)
	if err != nil || since.IsZero() {
		return 0, err
	}
	return w.store.CountUnclassified(ctx, account, since)
}

// FromStored reduces a mirrored message to what a question may see. The folder role
// picks which questions apply. Authentication verdicts are the ones the mirror kept,
// which come only from the provider's own authserv-id.
func FromStored(m store.Message, role string, atts []store.Attachment) Message {
	folder := FolderInbox
	if role == store.RoleJunk {
		folder = FolderJunk
	}
	in := StateInput{
		From:    Party{Name: m.From.Name, Address: m.From.Address},
		Subject: m.Subject,
		Date:    m.Date,
		Body:    m.BodyText,
		Auth:    Auth{SPF: m.AuthResults.SPF, DKIM: m.AuthResults.DKIM, DMARC: m.AuthResults.DMARC},
	}
	for _, a := range m.To {
		in.To = append(in.To, Party{Name: a.Name, Address: a.Address})
	}
	for _, a := range m.CC {
		in.CC = append(in.CC, Party{Name: a.Name, Address: a.Address})
	}
	for _, a := range atts {
		in.Attachments = append(in.Attachments, Attachment{Name: a.Filename, Type: a.MIMEType})
	}
	return Message{AccountID: m.AccountID, ContentKey: m.ContentKey, Folder: folder, State: in}
}
