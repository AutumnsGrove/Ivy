package sync

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Worker keeps one account's mirror fresh for as long as its context lives. It
// reconciles the account, then idles on INBOX until the server says something
// changed, the idle timeout fires, or the context is cancelled; then it
// reconciles again. The work and the IDLE each use their own connection, so a
// quiet mailbox never blocks a sync and the account stays within two connections
// (ARCHITECTURE.md 4). A failed reconcile or IDLE backs off and is retried, so
// a provider outage never stops the worker and never spins.
type Worker struct {
	fetcher     *Fetcher
	acct        Account
	idleTimeout time.Duration
	backoffBase time.Duration
	backoffMax  time.Duration
	jitter      func(time.Duration) time.Duration
	onSync      func(Result, error)
	onIdle      func()
}

// WorkerOption customises a Worker.
type WorkerOption func(*Worker)

// WithIdleTimeout is how long a quiet INBOX may idle before the worker polls
// anyway, so a missed notification cannot leave the mirror stale.
func WithIdleTimeout(d time.Duration) WorkerOption {
	return func(w *Worker) {
		if d > 0 {
			w.idleTimeout = d
		}
	}
}

// WithBackoff sets the retry delay bounds after a failed connection.
func WithBackoff(base, ceiling time.Duration) WorkerOption {
	return func(w *Worker) {
		if base > 0 {
			w.backoffBase = base
		}
		if ceiling > 0 && ceiling >= base {
			w.backoffMax = ceiling
		}
	}
}

// WithJitter replaces the random backoff jitter. Tests pass an identity so the
// delay is deterministic; the default spreads retries by ±25%.
func WithJitter(fn func(time.Duration) time.Duration) WorkerOption {
	return func(w *Worker) {
		if fn != nil {
			w.jitter = fn
		}
	}
}

// WithWorkerSyncFunc observes every reconcile. The SSE hub uses it to publish a
// `sync.state` hint; tests use it to wait for progress without polling the DB.
func WithWorkerSyncFunc(fn func(Result, error)) WorkerOption {
	return func(w *Worker) { w.onSync = fn }
}

// WithWorkerIdleFunc runs once the IDLE is established, for tests that need to
// deliver a message while the worker is actually listening.
func WithWorkerIdleFunc(fn func()) WorkerOption {
	return func(w *Worker) { w.onIdle = fn }
}

// Defaults chosen so a dropped connection retries within seconds and a quiet
// mailbox still polls well inside a provider's idle disconnect.
const (
	defaultIdleTimeout = 5 * time.Minute
	defaultBackoffBase = 5 * time.Second
	defaultBackoffMax  = 5 * time.Minute
)

// NewWorker builds a Worker for one account over the fetcher's databases.
func NewWorker(fetcher *Fetcher, acct Account, opts ...WorkerOption) *Worker {
	w := &Worker{
		fetcher: fetcher, acct: acct,
		idleTimeout: defaultIdleTimeout,
		backoffBase: defaultBackoffBase, backoffMax: defaultBackoffMax,
		jitter: defaultJitter,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Run reconciles and idles until ctx is cancelled; it returns ctx.Err().
func (w *Worker) Run(ctx context.Context) error {
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := w.fetcher.Fetch(ctx, w.acct)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if w.onSync != nil {
				w.onSync(res, err)
			}
			attempt++
			if !w.sleep(ctx, w.backoffDelay(attempt)) {
				return ctx.Err()
			}
			continue
		}
		attempt = 0
		if w.onSync != nil {
			w.onSync(res, nil)
		}
		// A notification, the timeout or a dropped connection all mean the same
		// thing: reconcile again. A drop fails the next Fetch and takes the
		// backoff path there.
		if err := w.idleOnInbox(ctx); err != nil && ctx.Err() == nil {
			attempt++
			if !w.sleep(ctx, w.backoffDelay(attempt)) {
				return ctx.Err()
			}
		}
	}
}

// idleOnInbox holds an IDLE on INBOX until the server reports a change, the
// idle timeout fires or ctx is cancelled. A server without the IDLE extension
// is polled on the idle timeout instead, so the worker still refreshes.
func (w *Worker) idleOnInbox(ctx context.Context) error {
	changed := make(chan struct{}, 1)
	c, err := w.fetcher.dialWith(ctx, w.acct, &imapclient.Options{UnilateralDataHandler: &imapclient.UnilateralDataHandler{
		Mailbox: func(*imapclient.UnilateralDataMailbox) { signal(changed) },
		Expunge: func(uint32) { signal(changed) },
		Fetch:   func(*imapclient.FetchMessageData) { signal(changed) },
	}})
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	// The client's commands take no context, so a server that goes quiet during
	// login or SELECT would hold the worker past cancellation; closing the
	// connection is what unblocks them (as in fetch).
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	cmd, err := w.startIdle(c)
	if err != nil {
		return err
	}
	if cmd == nil {
		if !w.sleep(ctx, w.idleTimeout) {
			return ctx.Err()
		}
		return nil
	}
	if w.onIdle != nil {
		w.onIdle()
	}
	timer := time.NewTimer(w.idleTimeout)
	defer timer.Stop()
	select {
	case <-changed:
	case <-timer.C:
	case <-ctx.Done():
	}
	// Only the wait for the server to acknowledge DONE is guarded: the IDLE
	// itself is silent by design.
	stopWatch := c.watch()
	_ = cmd.Close()
	_ = cmd.Wait()
	stopWatch()
	return ctx.Err()
}

// startIdle logs in, selects INBOX and starts the IDLE, all under the stall
// guard. It returns a nil command for a server without IDLE, which the caller
// polls instead.
func (w *Worker) startIdle(c *session) (*imapclient.IdleCommand, error) {
	defer c.watch()()
	if err := c.Login(w.acct.Username, w.acct.Password).Wait(); err != nil {
		return nil, err
	}
	caps, err := c.Capability().Wait()
	if err != nil {
		return nil, err
	}
	if !caps.Has(imap.CapIdle) {
		return nil, nil
	}
	if _, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, err
	}
	return c.Idle()
}

// signal wakes the IDLE waiter without ever blocking the client's read loop,
// which invokes the unilateral-data handler.
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// backoffDelay is exponential from base to max, then jittered.
func (w *Worker) backoffDelay(attempt int) time.Duration {
	delay := w.backoffBase
	for i := 1; i < attempt; i++ {
		if delay >= w.backoffMax/2 {
			delay = w.backoffMax
			break
		}
		delay *= 2
	}
	if delay > w.backoffMax {
		delay = w.backoffMax
	}
	return w.jitter(delay)
}

// sleep waits out d unless ctx ends first; it reports whether the wait finished.
func (w *Worker) sleep(ctx context.Context, d time.Duration) bool {
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

// defaultJitter spreads a delay by ±25%, so a fleet of accounts does not retry
// in lockstep after a provider outage.
func defaultJitter(d time.Duration) time.Duration {
	delta := int64(d) / 4
	if delta <= 0 {
		return d
	}
	//nolint:gosec // G404: jitter is not security-sensitive
	return d + time.Duration(rand.Int64N(2*delta+1)) - time.Duration(delta)
}
