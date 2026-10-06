package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/AutumnsGrove/Ivy/store"
)

// errAmbiguous marks a server search that matched more than one message. Ivy
// never guesses which one an op meant, so the op fails and the operator sees it.
var errAmbiguous = errors.New("more than one message matched")

// OutboxWorker is the one writer to IMAP. It drains one account's outbox in
// strict FIFO, one op at a time, on its own connection (never the sync worker's
// IDLE connection), and recovers a crashed op by asking the server what really
// happened before deciding. Every command an op needs carries the same stall
// guard as sync (docs/handoffs/2026-10-04-C3-outbox.md).
type OutboxWorker struct {
	fetcher   *Fetcher
	acct      Account
	poll      time.Duration
	idleClose time.Duration
	onOp      func(store.OutboxOp)

	conn      *session
	stopWatch func() bool // detaches conn's close-on-cancel hook
	outage    int         // consecutive connection failures, for the backoff only
	lastBusy  time.Time
	jitter    func(time.Duration) time.Duration
	// keywordsOK is whether the folder most recently selected keeps custom
	// keywords (\* in PERMANENTFLAGS); the worker is one goroutine, so it is
	// read right after the select that set it.
	keywordsOK bool

	// afterAck is a test seam: it runs after a command is acknowledged and before
	// the mirror update, so a test can simulate the process dying in that window
	// (the C4 crash test). Nil in production.
	afterAck func(store.OutboxOp) error
}

// OutboxWorkerOption customises an OutboxWorker.
type OutboxWorkerOption func(*OutboxWorker)

// WithOutboxPoll sets how often the idle worker checks for new ops.
func WithOutboxPoll(d time.Duration) OutboxWorkerOption {
	return func(w *OutboxWorker) {
		if d > 0 {
			w.poll = d
		}
	}
}

// WithOutboxIdleClose sets how long the queue may be empty before the worker
// closes its connection, so a quiet mailbox does not hold a socket open.
func WithOutboxIdleClose(d time.Duration) OutboxWorkerOption {
	return func(w *OutboxWorker) {
		if d > 0 {
			w.idleClose = d
		}
	}
}

// WithOutboxNotify observes every op that changes state, for the SSE
// outbox.state hint and for tests.
func WithOutboxNotify(fn func(store.OutboxOp)) OutboxWorkerOption {
	return func(w *OutboxWorker) { w.onOp = fn }
}

// WithOutboxJitter replaces the retry jitter. Tests pass an identity so the
// backoff is deterministic; the default spreads retries by ±20%.
func WithOutboxJitter(fn func(time.Duration) time.Duration) OutboxWorkerOption {
	return func(w *OutboxWorker) {
		if fn != nil {
			w.jitter = fn
		}
	}
}

const (
	defaultOutboxPoll      = time.Second
	defaultOutboxIdleClose = 60 * time.Second
	outboxBackoffBase      = 5 * time.Second
	outboxBackoffMax       = 15 * time.Minute
)

// NewOutboxWorker builds a worker for one account over the fetcher's databases.
func NewOutboxWorker(fetcher *Fetcher, acct Account, opts ...OutboxWorkerOption) *OutboxWorker {
	w := &OutboxWorker{
		fetcher: fetcher, acct: acct,
		poll: defaultOutboxPoll, idleClose: defaultOutboxIdleClose,
		jitter: outboxJitter,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// backoff is exponential from 5 s to 15 min, then jittered (C3 limits table).
func (w *OutboxWorker) backoff(attempt int) time.Duration {
	d := outboxBackoffBase
	for i := 1; i < attempt; i++ {
		if d >= outboxBackoffMax/2 {
			d = outboxBackoffMax
			break
		}
		d *= 2
	}
	if d > outboxBackoffMax {
		d = outboxBackoffMax
	}
	return w.jitter(d)
}

// outboxJitter spreads a retry by ±20% so a fleet of accounts does not retry in
// lockstep after a provider outage.
func outboxJitter(d time.Duration) time.Duration {
	delta := int64(d) / 5
	if delta <= 0 {
		return d
	}
	//nolint:gosec // G404: jitter is not security-sensitive
	return d + time.Duration(rand.Int64N(2*delta+1)) - time.Duration(delta)
}

// RunOnce drains every currently-due op and returns. It closes its connection,
// so a caller that only wants one pass (a test, or a wiring probe) leaks none.
func (w *OutboxWorker) RunOnce(ctx context.Context) error {
	defer w.closeConn()
	return w.drain(ctx)
}

// Run drains the outbox as long as its context lives, polling when the queue is
// empty and closing the connection after it has been idle. It returns ctx.Err().
func (w *OutboxWorker) Run(ctx context.Context) error {
	lastPrune := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			w.closeConn()
			return err
		}
		if err := w.drain(ctx); err != nil && ctx.Err() == nil {
			// A drain error is unexpected; log it and keep the worker alive,
			// because a stuck outbox must never take the process down.
			slog.WarnContext(ctx, "outbox: drain failed", "account", w.acct.ID, "error", err)
		}
		if err := ctx.Err(); err != nil {
			w.closeConn()
			return err
		}
		if lastPrune.IsZero() || w.fetcher.now().Sub(lastPrune) > outboxPruneInterval {
			if _, err := w.fetcher.dbs.PruneOutbox(ctx, w.fetcher.now()); err != nil {
				slog.WarnContext(ctx, "outbox: prune failed", "account", w.acct.ID, "error", err)
			}
			if _, err := w.fetcher.dbs.PruneDrafts(ctx, w.fetcher.now()); err != nil {
				slog.WarnContext(ctx, "outbox: draft prune failed", "account", w.acct.ID, "error", err)
			}
			lastPrune = w.fetcher.now()
		}
		if w.conn != nil && w.fetcher.now().Sub(w.lastBusy) > w.idleClose {
			w.closeConn()
		}
		if !w.sleep(ctx, w.poll) {
			w.closeConn()
			return ctx.Err()
		}
	}
}

// outboxPruneInterval is how often Run clears old terminal rows; the retention
// itself is seven days.
const outboxPruneInterval = 6 * time.Hour

// drain processes due ops in FIFO until none is due.
func (w *OutboxWorker) drain(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		op, err := w.fetcher.dbs.NextOutbox(ctx, w.acct.ID, w.fetcher.now())
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := w.process(ctx, op); err != nil {
			return err
		}
		w.lastBusy = w.fetcher.now()
	}
}

// process applies the caps, then dispatches a pending op or recovers an
// in-flight one. Op-level failures (a rejection, a retry, a rollback) are
// recorded on the row and return nil; only a store failure bubbles.
func (w *OutboxWorker) process(ctx context.Context, op store.OutboxOp) error {
	if op.State == store.OutboxPending {
		if !op.CreatedAt.IsZero() && w.fetcher.now().Sub(op.CreatedAt) >= store.MaxOutboxAge {
			return w.fail(ctx, op, "expired", "the action waited more than 24 hours")
		}
		if op.Attempts >= store.MaxOutboxAttempts {
			return w.fail(ctx, op, "retries_exhausted", "the action failed too many times")
		}
	}
	c, err := w.ensureConn(ctx)
	if err != nil {
		return w.transient(ctx, op, err)
	}
	switch op.Kind {
	case store.OutboxMove:
		if op.State == store.OutboxInFlight {
			return w.recoverMove(ctx, c, op)
		}
		return w.dispatchMove(ctx, c, op)
	case store.OutboxFlags:
		if op.State == store.OutboxInFlight {
			return w.recoverFlags(ctx, c, op)
		}
		return w.dispatchFlags(ctx, c, op)
	case store.OutboxExpunge:
		if op.State == store.OutboxInFlight {
			return w.recoverExpunge(ctx, c, op)
		}
		return w.dispatchExpunge(ctx, c, op)
	case store.OutboxAppend:
		if op.State == store.OutboxInFlight {
			return w.recoverAppend(ctx, c, op)
		}
		return w.dispatchAppend(ctx, c, op)
	case store.OutboxDraft:
		if op.State == store.OutboxInFlight {
			return w.recoverDraft(ctx, c, op)
		}
		return w.dispatchDraft(ctx, c, op)
	default:
		return w.fail(ctx, op, "unknown_kind", fmt.Sprintf("unknown op kind %q", op.Kind))
	}
}

// ---------------------------------------------------------------- dispatch

func (w *OutboxWorker) dispatchMove(ctx context.Context, c *session, op store.OutboxOp) error {
	src, ok, err := w.folder(ctx, op.SourceFolderID)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the source folder is gone")
	}
	dst, ok, err := w.folder(ctx, op.Expect.DestFolderID)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the destination folder is gone")
	}
	rowID, msgID, ok, err := w.mirrorRow(ctx, op)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the message is not in the mirror")
	}
	if err := w.requireCapability(ctx, c, imap.CapMove, op, "MOVE"); err != nil {
		return err
	}

	uidvalidity, uid, found, err := w.locate(c, op, src.Name, msgID)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if !found {
		inDest, err := w.folderHasMessageID(c, dst.Name, msgID)
		if err != nil {
			return w.serverError(ctx, op, err)
		}
		if inDest {
			return w.finishMove(ctx, op, rowID)
		}
		return w.fail(ctx, op, "message_gone", "the message is no longer on the server")
	}
	if err := w.fetcher.dbs.SetOutboxInFlight(ctx, op.ID, uidvalidity, uid, w.fetcher.now()); err != nil {
		return err
	}
	w.notify(ctx, op.ID)
	moved, err := w.moveMessage(c, uid, dst.Name)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if err := w.afterAckCrash(op); err != nil {
		return err
	}
	w.mirrorArrival(ctx, c, dst, moved)
	return w.finishMove(ctx, op, rowID)
}

// mirrorArrival stores the moved message in the destination's mirror straight
// away, using the UID the server reported (COPYUID), so an Undo can act on it
// before the next sync pass. It is best effort: the move already happened, and
// sync mirrors the arrival later if this cannot.
func (w *OutboxWorker) mirrorArrival(ctx context.Context, c *session, dst store.Folder, moved *imapclient.MoveData) {
	if moved == nil || moved.UIDValidity == 0 {
		return
	}
	set, ok := moved.DestUIDs.(imap.UIDSet)
	if !ok || len(set) == 0 {
		return
	}
	if _, err := w.selectFolder(c, dst.Name); err != nil {
		slog.WarnContext(ctx, "outbox: could not select the destination to mirror a move", "account", w.acct.ID, "error", err)
		return
	}
	if _, err := w.fetcher.fetchBatch(ctx, c, w.acct, dst.ID, moved.UIDValidity, set); err != nil {
		slog.WarnContext(ctx, "outbox: could not mirror a moved message", "account", w.acct.ID, "error", err)
	}
}

func (w *OutboxWorker) dispatchFlags(ctx context.Context, c *session, op store.OutboxOp) error {
	src, ok, err := w.folder(ctx, op.SourceFolderID)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the folder is gone")
	}
	rowID, msgID, ok, err := w.mirrorRow(ctx, op)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the message is not in the mirror")
	}
	uidvalidity, uid, found, err := w.locate(c, op, src.Name, msgID)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if !found {
		return w.fail(ctx, op, "message_gone", "the message is no longer on the server")
	}
	server, local := w.serverSide(op)
	if local {
		return w.finishLocalTags(ctx, op)
	}
	flags, err := w.fetchFlags(c, uid)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if flagsHold(server, flags) {
		return w.finishFlags(ctx, op, rowID, flags)
	}
	if err := w.fetcher.dbs.SetOutboxInFlight(ctx, op.ID, uidvalidity, uid, w.fetcher.now()); err != nil {
		return err
	}
	w.notify(ctx, op.ID)
	if err := w.applyFlags(c, uid, server.Expect); err != nil {
		return w.serverError(ctx, op, err)
	}
	flags, err = w.fetchFlags(c, uid)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if err := w.afterAckCrash(op); err != nil {
		return err
	}
	return w.finishFlags(ctx, op, rowID, flags)
}

func (w *OutboxWorker) dispatchExpunge(ctx context.Context, c *session, op store.OutboxOp) error {
	folder, ok, err := w.folder(ctx, op.SourceFolderID)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the folder is gone")
	}
	// A single tap must never erase mail: expunge is only for the Trash role.
	if folder.Role != store.RoleTrash {
		return w.fail(ctx, op, "not_trash", "only the trash folder may be emptied")
	}
	rowID, msgID, ok, err := w.mirrorRow(ctx, op)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the message is not in the mirror")
	}
	if err := w.requireCapability(ctx, c, imap.CapUIDPlus, op, "UIDPLUS"); err != nil {
		return err
	}
	uidvalidity, uid, found, err := w.locate(c, op, folder.Name, msgID)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if !found {
		return w.finishExpunge(ctx, op, rowID)
	}
	if err := w.fetcher.dbs.SetOutboxInFlight(ctx, op.ID, uidvalidity, uid, w.fetcher.now()); err != nil {
		return err
	}
	w.notify(ctx, op.ID)
	if err := w.expungeMessage(c, uid); err != nil {
		return w.serverError(ctx, op, err)
	}
	if err := w.afterAckCrash(op); err != nil {
		return err
	}
	return w.finishExpunge(ctx, op, rowID)
}

// dispatchAppend files a copy from the send queue into a folder (the Sent copy).
// It never files twice: the destination is searched by Message-ID first, so a
// retried enqueue or a lost acknowledgement cannot create a duplicate. Unlike
// the mail ops it has no source message; the send row is its payload.
func (w *OutboxWorker) dispatchAppend(ctx context.Context, c *session, op store.OutboxOp) error {
	dst, ok, err := w.folder(ctx, op.Expect.DestFolderID)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the destination folder is gone")
	}
	ref, err := w.fetcher.dbs.SendBodyForAppend(ctx, op.Expect.SendID)
	if errors.Is(err, store.ErrNotFound) {
		return w.fail(ctx, op, "send_gone", "the unsent message is gone")
	}
	if err != nil {
		return err
	}
	if _, err := w.selectFolder(c, dst.Name); err != nil {
		return w.serverError(ctx, op, err)
	}
	if _, found, err := w.searchMessageID(c, ref.MessageID); err != nil {
		return w.serverError(ctx, op, err)
	} else if found {
		return w.done(ctx, op)
	}
	// In-flight before the APPEND: a crash is then recovered by the search above,
	// where finding the Message-ID proves the append applied.
	if err := w.fetcher.dbs.SetOutboxInFlight(ctx, op.ID, 0, 0, w.fetcher.now()); err != nil {
		return err
	}
	w.notify(ctx, op.ID)
	if err := w.appendMessage(c, dst.Name, ref.Body, op.Expect.FlagsAdd); err != nil {
		return w.serverError(ctx, op, err)
	}
	if err := w.afterAckCrash(op); err != nil {
		return err
	}
	return w.done(ctx, op)
}

// recoverAppend decides whether a possibly-sent APPEND applied. It cannot know
// from the wire, so it asks the folder: present means done, absent means the
// append never landed and is safe to re-issue.
func (w *OutboxWorker) recoverAppend(ctx context.Context, c *session, op store.OutboxOp) error {
	dst, ok, err := w.folder(ctx, op.Expect.DestFolderID)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the destination folder is gone")
	}
	ref, err := w.fetcher.dbs.SendBodyForAppend(ctx, op.Expect.SendID)
	if errors.Is(err, store.ErrNotFound) {
		return w.fail(ctx, op, "send_gone", "the unsent message is gone")
	}
	if err != nil {
		return err
	}
	found, err := w.folderHasMessageID(c, dst.Name, ref.MessageID)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if found {
		return w.done(ctx, op)
	}
	return w.requeue(ctx, op)
}

// dispatchDraft files one immutable compose version into the Drafts folder and
// removes the version it supersedes, all in one op. A Remove op only clears the
// copies named in Supersedes (the draft was sent or discarded). A version is
// never filed twice: its Message-ID is searched before the APPEND, and a fresh
// Message-ID is minted for every save (docs/handoffs/2026-10-06-4d-drafts-design.md).
func (w *OutboxWorker) dispatchDraft(ctx context.Context, c *session, op store.OutboxOp) error {
	dst, ok, err := w.draftFolder(ctx, op)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err := w.selectFolder(c, dst.Name); err != nil {
		return w.serverError(ctx, op, err)
	}
	if op.Expect.Remove {
		if err := w.expungeDraftCopies(ctx, c, op); err != nil {
			return err
		}
		return w.done(ctx, op)
	}
	ref, err := w.fetcher.dbs.DraftBodyForOp(ctx, op.Expect.DraftVersionID)
	if errors.Is(err, store.ErrNotFound) {
		return w.fail(ctx, op, "draft_gone", "the draft version is gone")
	}
	if err != nil {
		return err
	}
	_, found, err := w.searchMessageID(c, ref.MessageID)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if !found {
		// In-flight before the APPEND: a crash is then recovered by the search in
		// recoverDraft, where finding the Message-ID proves the append applied.
		if err := w.fetcher.dbs.SetOutboxInFlight(ctx, op.ID, 0, 0, w.fetcher.now()); err != nil {
			return err
		}
		w.notify(ctx, op.ID)
		if err := w.appendMessage(c, dst.Name, ref.Body, op.Expect.FlagsAdd); err != nil {
			return w.serverError(ctx, op, err)
		}
		if err := w.afterAckCrash(op); err != nil {
			return err
		}
	}
	if err := w.expungeDraftCopies(ctx, c, op); err != nil {
		return err
	}
	if err := w.markDraftSaved(ctx, op); err != nil {
		return err
	}
	return w.done(ctx, op)
}

// recoverDraft decides a possibly-applied draft save. The new Message-ID is the
// evidence: present means the append applied (finish it), absent means it never
// landed and is safe to re-issue.
func (w *OutboxWorker) recoverDraft(ctx context.Context, c *session, op store.OutboxOp) error {
	dst, ok, err := w.draftFolder(ctx, op)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err := w.selectFolder(c, dst.Name); err != nil {
		return w.serverError(ctx, op, err)
	}
	if op.Expect.Remove {
		if err := w.expungeDraftCopies(ctx, c, op); err != nil {
			return err
		}
		return w.done(ctx, op)
	}
	ref, err := w.fetcher.dbs.DraftBodyForOp(ctx, op.Expect.DraftVersionID)
	if errors.Is(err, store.ErrNotFound) {
		return w.fail(ctx, op, "draft_gone", "the draft version is gone")
	}
	if err != nil {
		return err
	}
	_, found, err := w.searchMessageID(c, ref.MessageID)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if !found {
		return w.requeue(ctx, op)
	}
	if err := w.expungeDraftCopies(ctx, c, op); err != nil {
		return err
	}
	if err := w.markDraftSaved(ctx, op); err != nil {
		return err
	}
	return w.done(ctx, op)
}

// draftFolder resolves and guards the Drafts folder a draft op names. ok=false
// means the op was already failed.
func (w *OutboxWorker) draftFolder(ctx context.Context, op store.OutboxOp) (store.Folder, bool, error) {
	dst, ok, err := w.folder(ctx, op.Expect.DestFolderID)
	if err != nil {
		return store.Folder{}, false, err
	}
	if !ok {
		return store.Folder{}, false, w.fail(ctx, op, "message_gone", "the drafts folder is gone")
	}
	if dst.Role != store.RoleDrafts {
		return store.Folder{}, false, w.fail(ctx, op, "not_drafts", "a draft may only be filed in the drafts folder")
	}
	return dst, true, nil
}

// draftCapability requires UIDPLUS before any superseded copy is expunged, so
// the removal is precise; it never falls back to a bare EXPUNGE.
func (w *OutboxWorker) draftCapability(ctx context.Context, c *session, op store.OutboxOp) error {
	return w.requireCapability(ctx, c, imap.CapUIDPlus, op, "UIDPLUS")
}

// expungeDraftCopies removes the copies a draft op names by Message-ID. A copy
// that is already gone is fine: an earlier attempt may have removed it.
func (w *OutboxWorker) expungeDraftCopies(ctx context.Context, c *session, op store.OutboxOp) error {
	if len(op.Expect.Supersedes) == 0 {
		return nil
	}
	if err := w.draftCapability(ctx, c, op); err != nil {
		return err
	}
	for _, msgID := range op.Expect.Supersedes {
		if msgID == "" {
			continue
		}
		uid, found, err := w.searchMessageID(c, msgID)
		if err != nil {
			return w.serverError(ctx, op, err)
		}
		if !found {
			continue
		}
		if err := w.expungeMessage(c, uid); err != nil {
			return w.serverError(ctx, op, err)
		}
	}
	return nil
}

// markDraftSaved records that the version is on the server. A version already
// saved (a crash between the mark and the op's done) is not an error.
func (w *OutboxWorker) markDraftSaved(ctx context.Context, op store.OutboxOp) error {
	err := w.fetcher.dbs.MarkDraftSaved(ctx, op.Expect.DraftVersionID, w.fetcher.now())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

func (w *OutboxWorker) appendMessage(c *session, folder string, body []byte, flags []string) error {
	defer c.watch()()
	cmd := c.Append(folder, int64(len(body)), &imap.AppendOptions{Flags: imapFlags(flags)})
	if _, err := cmd.Write(body); err != nil {
		_ = cmd.Close()
		return err
	}
	if err := cmd.Close(); err != nil {
		return err
	}
	_, err := cmd.Wait()
	return err
}

// locate resolves the UID an op acts on. For a pending op it searches the
// Message-ID (the UID is never resolved at enqueue); for an in-flight recovery
// it first trusts the stored (UIDVALIDITY, UID), because that is the message the
// command actually named.
func (w *OutboxWorker) locate(c *session, op store.OutboxOp, folder, msgID string) (uidvalidity, uid uint32, found bool, err error) {
	uidvalidity, err = w.selectFolder(c, folder)
	if err != nil {
		if permanentIMAPError(err) {
			return 0, 0, false, nil
		}
		return 0, 0, false, err
	}
	if op.State == store.OutboxInFlight && op.SourceUID != 0 && op.SourceUIDValidity == uidvalidity {
		present, err := w.uidPresent(c, op.SourceUID)
		if err != nil {
			return 0, 0, false, err
		}
		if present {
			return uidvalidity, op.SourceUID, true, nil
		}
		return uidvalidity, 0, false, nil
	}
	uid, found, err = w.searchMessageID(c, msgID)
	if err != nil {
		return 0, 0, false, err
	}
	return uidvalidity, uid, found, nil
}

// ---------------------------------------------------------------- recovery

// recoverMove decides what a possibly-sent MOVE did, without ever guessing. A
// UIDVALIDITY change makes the stored UID meaningless, so it re-derives the
// answer from the Message-ID in both folders.
func (w *OutboxWorker) recoverMove(ctx context.Context, c *session, op store.OutboxOp) error {
	rowID, msgID, ok, err := w.mirrorRow(ctx, op)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the message is not in the mirror")
	}
	src, ok, err := w.folder(ctx, op.SourceFolderID)
	if err != nil {
		return err
	}
	dst, okDst, err := w.folder(ctx, op.Expect.DestFolderID)
	if err != nil {
		return err
	}
	if !ok || !okDst {
		return w.fail(ctx, op, "message_gone", "the folder is gone")
	}
	uidvalidity, err := w.selectFolder(c, src.Name)
	if err != nil && !permanentIMAPError(err) {
		return w.serverError(ctx, op, err)
	}
	// Same validity and the named UID is still in the source: the command never
	// applied. Anything else needs the Message-ID to decide.
	if err == nil && uidvalidity == op.SourceUIDValidity && op.SourceUID != 0 {
		present, perr := w.uidPresent(c, op.SourceUID)
		if perr != nil {
			return w.serverError(ctx, op, perr)
		}
		if present {
			return w.requeue(ctx, op)
		}
		inDest, derr := w.folderHasMessageID(c, dst.Name, msgID)
		if derr != nil {
			return w.serverError(ctx, op, derr)
		}
		if inDest {
			return w.finishMove(ctx, op, rowID)
		}
		return w.fail(ctx, op, "message_gone", "the message left the source but did not arrive")
	}

	// The validity changed, so the stored UID proves nothing. Ask both folders.
	srcHas, serr := w.folderHasMessageID(c, src.Name, msgID)
	if serr != nil && !permanentIMAPError(serr) {
		return w.serverError(ctx, op, serr)
	}
	dstHas, derr := w.folderHasMessageID(c, dst.Name, msgID)
	if derr != nil && !permanentIMAPError(derr) {
		return w.serverError(ctx, op, derr)
	}
	switch {
	case srcHas && dstHas:
		return w.fail(ctx, op, "ambiguous", "the message is in both the source and the destination")
	case srcHas:
		return w.requeue(ctx, op)
	case dstHas:
		return w.finishMove(ctx, op, rowID)
	default:
		return w.fail(ctx, op, "message_gone", "the message is in neither the source nor the destination")
	}
}

// recoverFlags re-reads the flag set. Re-sending a flag change is idempotent, so
// the op only finishes when the postcondition already holds and otherwise goes
// back to the queue.
func (w *OutboxWorker) recoverFlags(ctx context.Context, c *session, op store.OutboxOp) error {
	rowID, msgID, ok, err := w.mirrorRow(ctx, op)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the message is not in the mirror")
	}
	src, ok, err := w.folder(ctx, op.SourceFolderID)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the folder is gone")
	}
	uid, found, err := w.locateRecoveryUID(c, op, src.Name, msgID)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if !found {
		return w.fail(ctx, op, "message_gone", "the message is no longer on the server")
	}
	server, local := w.serverSide(op)
	if local {
		return w.finishLocalTags(ctx, op)
	}
	flags, err := w.fetchFlags(c, uid)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if flagsHold(server, flags) {
		return w.finishFlags(ctx, op, rowID, flags)
	}
	return w.requeue(ctx, op)
}

// recoverExpunge treats "the UID is absent" as the postcondition, so a re-issued
// UID EXPUNGE is never needed, and any doubt goes back to the queue.
func (w *OutboxWorker) recoverExpunge(ctx context.Context, c *session, op store.OutboxOp) error {
	rowID, msgID, ok, err := w.mirrorRow(ctx, op)
	if err != nil {
		return err
	}
	if !ok {
		return w.fail(ctx, op, "message_gone", "the message is not in the mirror")
	}
	src, ok, err := w.folder(ctx, op.SourceFolderID)
	if err != nil {
		return err
	}
	if !ok {
		return w.finishExpunge(ctx, op, rowID)
	}
	_, found, err := w.locateRecoveryUID(c, op, src.Name, msgID)
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if !found {
		return w.finishExpunge(ctx, op, rowID)
	}
	return w.requeue(ctx, op)
}

// locateRecoveryUID is locate for recovery, where a vanished source folder means
// the UID is simply not there.
func (w *OutboxWorker) locateRecoveryUID(c *session, op store.OutboxOp, folder, msgID string) (uint32, bool, error) {
	uidvalidity, err := w.selectFolder(c, folder)
	if err != nil {
		if permanentIMAPError(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if op.SourceUID != 0 && op.SourceUIDValidity == uidvalidity {
		present, err := w.uidPresent(c, op.SourceUID)
		if err != nil {
			return 0, false, err
		}
		if present {
			return op.SourceUID, true, nil
		}
		return 0, false, nil
	}
	uid, found, err := w.searchMessageID(c, msgID)
	if err != nil {
		return 0, false, err
	}
	return uid, found, nil
}

// ---------------------------------------------------------------- finishes

// finishMove hides the source row as moved through sync's own disable path (so
// the blob store copy still happens), then marks the op done.
func (w *OutboxWorker) finishMove(ctx context.Context, op store.OutboxOp, rowID string) error {
	if err := w.hideRow(ctx, rowID, store.DisabledMoved); err != nil {
		return err
	}
	return w.done(ctx, op)
}

// finishExpunge hides the row as server_removed.
func (w *OutboxWorker) finishExpunge(ctx context.Context, op store.OutboxOp, rowID string) error {
	if err := w.hideRow(ctx, rowID, store.DisabledRemoved); err != nil {
		return err
	}
	return w.done(ctx, op)
}

// finishFlags writes the server's flag set to the mirror row. Flags are real
// state, so a missing row (purged mid-op) is not fatal; the op is done.
func (w *OutboxWorker) finishFlags(ctx context.Context, op store.OutboxOp, rowID string, flags []string) error {
	if err := w.fetcher.dbs.SetMessageFlags(ctx, rowID, flags); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := w.reflectTags(ctx, op); err != nil {
		return err
	}
	return w.done(ctx, op)
}

func (w *OutboxWorker) hideRow(ctx context.Context, rowID, reason string) error {
	if err := w.fetcher.disableRefReason(ctx, store.SyncMessageRef{ID: rowID}, reason); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

// afterAckCrash is the C4 test seam: it returns nil in production and the
// injected error (a simulated crash) in the crash-window test.
func (w *OutboxWorker) afterAckCrash(op store.OutboxOp) error {
	if w.afterAck == nil {
		return nil
	}
	return w.afterAck(op)
}

// ---------------------------------------------------------------- outcomes

func (w *OutboxWorker) done(ctx context.Context, op store.OutboxOp) error {
	w.outage = 0
	if err := w.fetcher.dbs.SetOutboxDone(ctx, op.ID, w.fetcher.now()); err != nil {
		return err
	}
	w.notify(ctx, op.ID)
	return nil
}

// requeue returns an in-flight op to pending after recovery proved the command
// did not apply. The UID is re-resolved on the next pass.
func (w *OutboxWorker) requeue(ctx context.Context, op store.OutboxOp) error {
	if err := w.fetcher.dbs.RequeueOutbox(ctx, op.ID, w.fetcher.now()); err != nil {
		return err
	}
	w.notify(ctx, op.ID)
	return nil
}

// transient records a retryable failure, or fails the op once the attempt cap is
// reached. It returns nil because the outcome is on the row, not in a Go error.
func (w *OutboxWorker) transient(ctx context.Context, op store.OutboxOp, cause error) error {
	// The failure may have killed the connection (a drop, a stall, BYE). Reusing
	// it would fail every retry against a healthy server until the op exhausted
	// its attempts, so the next attempt dials afresh.
	w.closeConn()
	// A shutdown that closed the connection under a command is not the op's
	// failure: it must not cost an attempt, and recovery handles an in-flight op
	// on the next start.
	if err := ctx.Err(); err != nil {
		return err
	}
	detail := cause.Error()
	// Only a server that answered NO to this op counts against its attempts. A
	// failed dial, login, drop or stall says nothing about the op, so an outage
	// must not exhaust every queued action; the 24 h age cap still bounds it.
	var imapErr *imap.Error
	if !errors.As(cause, &imapErr) || imapErr.Type != imap.StatusResponseTypeNo {
		w.outage++
		next := w.fetcher.now().Add(w.backoff(w.outage))
		if err := w.fetcher.dbs.SetOutboxPending(ctx, op.ID, op.Attempts, next, outboxErrorCode(cause), detail, w.fetcher.now()); err != nil {
			return err
		}
		w.notify(ctx, op.ID)
		return nil
	}
	attempts := op.Attempts + 1
	if attempts >= store.MaxOutboxAttempts {
		return w.fail(ctx, op, "retries_exhausted", detail)
	}
	code := outboxErrorCode(cause)
	next := w.fetcher.now().Add(w.backoff(attempts))
	if err := w.fetcher.dbs.SetOutboxPending(ctx, op.ID, attempts, next, code, detail, w.fetcher.now()); err != nil {
		return err
	}
	w.notify(ctx, op.ID)
	return nil
}

// fail marks an op terminal and visible; it is never silently dropped.
func (w *OutboxWorker) fail(ctx context.Context, op store.OutboxOp, code, detail string) error {
	if err := w.fetcher.dbs.SetOutboxFailed(ctx, op.ID, code, detail, w.fetcher.now()); err != nil {
		return err
	}
	w.notify(ctx, op.ID)
	return nil
}

// serverError classifies an IMAP failure: a permanent rejection fails the op,
// everything else (a drop, a stall, BYE, a bare NO) retries with backoff.
func (w *OutboxWorker) serverError(ctx context.Context, op store.OutboxOp, err error) error {
	var imapErr *imap.Error
	if errors.As(err, &imapErr) {
		switch imapErr.Code {
		case imap.ResponseCodeNonExistent, imap.ResponseCodeTryCreate, imap.ResponseCodeOverQuota,
			imap.ResponseCodeNoPerm, imap.ResponseCodeCannot, imap.ResponseCodeClientBug:
			return w.fail(ctx, op, strings.ToLower(string(imapErr.Code)), err.Error())
		default: // any other code is worth another try
		}
		if imapErr.Type == imap.StatusResponseTypeBad {
			return w.fail(ctx, op, "protocol_error", err.Error())
		}
	}
	return w.transient(ctx, op, err)
}

// ---------------------------------------------------------------- connection

func (w *OutboxWorker) ensureConn(ctx context.Context) (*session, error) {
	if w.conn != nil {
		return w.conn, nil
	}
	c, err := w.fetcher.dial(ctx, w.acct)
	if err != nil {
		return nil, err
	}
	// The IMAP client's commands take no context, so closing the connection is
	// the only way to stop one that waits on a silent server (as sync does).
	w.stopWatch = context.AfterFunc(ctx, func() { _ = c.Close() })
	if err := func() error {
		defer c.watch()()
		return c.Login(w.acct.Username, w.acct.Password).Wait()
	}(); err != nil {
		w.stopWatch()
		_ = c.Close()
		return nil, fmt.Errorf("outbox account %s: login: %w", w.acct.ID, err)
	}
	w.conn = c
	return c, nil
}

func (w *OutboxWorker) closeConn() {
	if w.stopWatch != nil {
		w.stopWatch()
		w.stopWatch = nil
	}
	if w.conn != nil {
		_ = w.conn.Close()
		w.conn = nil
	}
}

// ---------------------------------------------------------------- IMAP

func (w *OutboxWorker) selectFolder(c *session, name string) (uint32, error) {
	defer c.watch()()
	data, err := c.Select(name, &imap.SelectOptions{ReadOnly: false}).Wait()
	if err != nil {
		return 0, err
	}
	w.keywordsOK = slices.Contains(data.PermanentFlags, imap.FlagWildcard)
	return data.UIDValidity, nil
}

// searchMessageID searches the currently selected folder for a Message-ID and
// returns exactly one hit; more than one is an error Ivy refuses to guess past.
func (w *OutboxWorker) searchMessageID(c *session, msgID string) (uint32, bool, error) {
	if msgID == "" {
		return 0, false, nil
	}
	defer c.watch()()
	data, err := c.UIDSearch(&imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: msgID}},
	}, &imap.SearchOptions{}).Wait()
	if err != nil {
		return 0, false, err
	}
	uids := data.AllUIDs()
	switch len(uids) {
	case 0:
		return 0, false, nil
	case 1:
		return uint32(uids[0]), true, nil
	default:
		return 0, false, errAmbiguous
	}
}

func (w *OutboxWorker) fetchFlags(c *session, uid uint32) ([]string, error) {
	defer c.watch()()
	bufs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil {
		return nil, err
	}
	if len(bufs) == 0 {
		return nil, store.ErrNotFound
	}
	return flagStrings(bufs[0].Flags), nil
}

func (w *OutboxWorker) uidPresent(c *session, uid uint32) (bool, error) {
	defer c.watch()()
	bufs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true}).Collect()
	if err != nil {
		return false, err
	}
	return len(bufs) > 0, nil
}

func (w *OutboxWorker) folderHasMessageID(c *session, folder, msgID string) (bool, error) {
	if msgID == "" {
		return false, nil
	}
	if _, err := w.selectFolder(c, folder); err != nil {
		if permanentIMAPError(err) {
			return false, nil
		}
		return false, err
	}
	_, found, err := w.searchMessageID(c, msgID)
	if errors.Is(err, errAmbiguous) {
		return true, nil
	}
	return found, err
}

func (w *OutboxWorker) applyFlags(c *session, uid uint32, expect store.OutboxExpect) error {
	defer c.watch()()
	set := imap.UIDSetNum(imap.UID(uid))
	if len(expect.FlagsAdd) > 0 {
		if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: imapFlags(expect.FlagsAdd)}, nil).Close(); err != nil {
			return err
		}
	}
	if len(expect.FlagsClear) > 0 {
		if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsDel, Flags: imapFlags(expect.FlagsClear)}, nil).Close(); err != nil {
			return err
		}
	}
	return nil
}

func (w *OutboxWorker) moveMessage(c *session, uid uint32, dest string) (*imapclient.MoveData, error) {
	defer c.watch()()
	return c.Move(imap.UIDSetNum(imap.UID(uid)), dest).Wait()
}

func (w *OutboxWorker) expungeMessage(c *session, uid uint32) error {
	defer c.watch()()
	set := imap.UIDSetNum(imap.UID(uid))
	if err := c.Store(set, &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted},
	}, nil).Close(); err != nil {
		return err
	}
	return c.UIDExpunge(set).Close()
}

// requireCapability refuses an op the server cannot run atomically rather than
// emulating it (C3: a MOVE is not COPY+STORE+EXPUNGE; expunge needs UIDPLUS).
func (w *OutboxWorker) requireCapability(ctx context.Context, c *session, capability imap.Cap, op store.OutboxOp, name string) error {
	defer c.watch()()
	caps, err := c.Capability().Wait()
	if err != nil {
		return w.serverError(ctx, op, err)
	}
	if !caps.Has(capability) {
		return w.fail(ctx, op, "unsupported", "the server does not support "+name)
	}
	return nil
}

// ---------------------------------------------------------------- misc

// folder looks up a mirror folder by id, reporting a missing one as ok=false.
func (w *OutboxWorker) folder(ctx context.Context, id string) (store.Folder, bool, error) {
	f, err := w.fetcher.dbs.GetFolderByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Folder{}, false, nil
	}
	if err != nil {
		return store.Folder{}, false, err
	}
	return f, true, nil
}

// mirrorRow returns the mirror row id and Message-ID for the op's mail. ok=false
// means the row is gone and the op can no longer act on it.
func (w *OutboxWorker) mirrorRow(ctx context.Context, op store.OutboxOp) (id, messageID string, ok bool, err error) {
	id, messageID, err = w.fetcher.dbs.MessageRowRef(ctx, op.AccountID, op.ContentKey, op.SourceFolderID)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return id, messageID, true, nil
}

func (w *OutboxWorker) notify(ctx context.Context, id string) {
	if w.onOp == nil {
		return
	}
	op, err := w.fetcher.dbs.GetOutbox(ctx, id)
	if err != nil {
		return
	}
	w.onOp(op)
}

func (w *OutboxWorker) sleep(ctx context.Context, d time.Duration) bool {
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

// flagsHold reports whether a message's flags already satisfy the op.
func flagsHold(op store.OutboxOp, flags []string) bool {
	have := canonicalFlags(flags)
	for _, add := range op.Expect.FlagsAdd {
		if !slices.Contains(have, add) {
			return false
		}
	}
	for _, clear := range op.Expect.FlagsClear {
		if slices.Contains(have, clear) {
			return false
		}
	}
	return true
}

func imapFlags(flags []string) []imap.Flag {
	out := make([]imap.Flag, len(flags))
	for i, f := range flags {
		out[i] = imap.Flag(f)
	}
	return out
}

// permanentIMAPError reports whether an IMAP failure will not improve on retry.
func permanentIMAPError(err error) bool {
	var imapErr *imap.Error
	if !errors.As(err, &imapErr) {
		return false
	}
	switch imapErr.Code {
	case imap.ResponseCodeNonExistent, imap.ResponseCodeTryCreate, imap.ResponseCodeOverQuota,
		imap.ResponseCodeNoPerm, imap.ResponseCodeCannot, imap.ResponseCodeClientBug:
		return true
	default: // an unlisted code may clear on its own
	}
	return imapErr.Type == imap.StatusResponseTypeBad
}

// outboxErrorCode gives the UI a stable, terse reason for a retry.
func outboxErrorCode(err error) string {
	var imapErr *imap.Error
	if errors.As(err, &imapErr) {
		if imapErr.Code != "" {
			return strings.ToLower(string(imapErr.Code))
		}
		switch imapErr.Type {
		case imap.StatusResponseTypeBye:
			return "bye"
		case imap.StatusResponseTypeNo:
			return "unavailable"
		default: // falls through to the network classification below
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, errServerStalled) {
		return "unreachable"
	}
	return "unavailable"
}
