// Package sync is Ivy's read path: it fetches folders and messages from an IMAP
// server into the mirror, keyed by (folder, uid) and the stable content key.
//
// Fetcher.Fetch reconciles one account in a single pass (QRESYNC deltas where
// the server has them, a full read otherwise) and Worker keeps it fresh with
// IDLE on INBOX. There is no write path yet (chunk 3d). Nothing here deletes: a
// message that vanishes from the server is disabled, with its row and bytes kept.
package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	mailmime "github.com/AutumnsGrove/Ivy/mime"
	"github.com/AutumnsGrove/Ivy/render"
	"github.com/AutumnsGrove/Ivy/store"
	"github.com/AutumnsGrove/Ivy/thread"
)

// defaultBatchSize bounds a FETCH so a large folder never arrives in one
// unbounded read (STANDARDS.md section 7).
const defaultBatchSize = 200

// maxDateSkew is how far ahead of now a Date header may be before it is treated
// as wrong; senders' clocks drift, but a message cannot be dated tomorrow-plus.
const maxDateSkew = 24 * time.Hour

// Account is the connection descriptor for one mirror account. Password comes
// from the environment, is never persisted and never logged.
type Account struct {
	ID       string
	Address  string
	IMAPHost string
	IMAPPort int
	SMTPHost string
	SMTPPort int
	Username string
	Password string
	// Insecure sends the login in plaintext. It exists for the loopback fake
	// mail world only; the zero value is implicit TLS so a forgotten field can
	// never put a real password on the wire unencrypted.
	Insecure bool
	// TrustedAuthservIDs lists the Authentication-Results authserv-ids whose
	// verdicts may be believed (RFC 8601; N9 in papercuts.md). Empty means no
	// header is trusted, so the message has no auth signal rather than a forged
	// one. Purelymail adds no SPF/DKIM/DMARC verdicts, so for it this stays empty.
	TrustedAuthservIDs []string
}

// Result summarizes one read fetch.
type Result struct {
	Folders int // mailboxes selected and mirrored
	Stored  int // messages fetched and written
	Skipped int // messages already mirrored, so not re-downloaded
	// MassDisabled names folders whose sweep in this pass tripped the
	// mass-disable alert. The caller publishes one `health.alert` per entry.
	MassDisabled []MassDisable
}

// Fetcher mirrors accounts into the store. It is stateless between runs, so a
// crashed run resumes by asking the store which UIDs it already holds.
type Fetcher struct {
	dbs    *store.DBs
	now    func() time.Time
	batch  int
	inline int64         // largest message fetched into memory
	max    int64         // largest message downloaded at all
	stall  time.Duration // silence from the server that ends a command
}

// Message size limits (STANDARDS.md 4a). A message up to InlineMessageBytes is
// fetched into memory and kept in the database. Up to MaxMessageBytes it is
// streamed to a file in the spool directory and parsed from there, so neither
// the message nor its attachments are ever held whole in memory. Above that it
// is not downloaded: the envelope is mirrored with body_status too_large.
const (
	InlineMessageBytes = 2 << 20
	MaxMessageBytes    = 64 << 20
)

// WithMessageLimits overrides the size tiers; tests use small ones so a few
// kilobytes exercise every path.
func WithMessageLimits(inlineBytes, maxBytes int64) Option {
	return func(f *Fetcher) {
		if inlineBytes > 0 && maxBytes >= inlineBytes {
			f.inline, f.max = inlineBytes, maxBytes
		}
	}
}

// Option customises a Fetcher.
type Option func(*Fetcher)

// WithClock injects the clock used for account and folder timestamps.
func WithClock(now func() time.Time) Option {
	return func(f *Fetcher) { f.now = now }
}

// WithBatchSize caps how many UIDs one FETCH covers.
func WithBatchSize(n int) Option {
	return func(f *Fetcher) {
		if n > 0 {
			f.batch = n
		}
	}
}

// WithStallTimeout sets how long a command may go without a byte from the
// server before the connection is given up on.
func WithStallTimeout(d time.Duration) Option {
	return func(f *Fetcher) {
		if d > 0 {
			f.stall = d
		}
	}
}

// NewFetcher builds a Fetcher over the mirror.
func NewFetcher(dbs *store.DBs, opts ...Option) *Fetcher {
	f := &Fetcher{dbs: dbs, now: time.Now, batch: defaultBatchSize, inline: InlineMessageBytes, max: MaxMessageBytes, stall: defaultStallTimeout}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Fetch reconciles one account and records the outcome in `sync_state`, so
// Mirror health can tell "never synced", a live backfill and each failure state
// apart (ARCHITECTURE.md 9b). The state write itself is best effort: a database
// error is logged, never allowed to mask the sync's own result.
func (f *Fetcher) Fetch(ctx context.Context, acct Account) (Result, error) {
	if err := f.EnsureAccount(ctx, acct); err != nil {
		return Result{}, fmt.Errorf("sync account %s: %w", acct.ID, err)
	}
	f.recordSyncState(ctx, acct.ID, store.SyncState{Status: store.SyncSyncing, UpdatedAt: f.now()})

	res, err := f.fetch(ctx, acct)
	// Record the outcome on a context detached from the pass, so a pass that was
	// cancelled or timed out still settles its own sync_state instead of leaving
	// the row at "syncing" until the next start (N21 in papercuts.md). The timeout
	// bounds the write even if another writer holds the connection.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), syncStateWriteTimeout)
	defer cancel()
	f.recordSyncState(writeCtx, acct.ID, f.syncOutcome(acct.ID, res, err))
	return res, err
}

// syncStateWriteTimeout bounds the best-effort sync_state write a cancelled pass
// makes on its detached context.
const syncStateWriteTimeout = 5 * time.Second

// fetch connects to one account and mirrors every selectable mailbox. A
// message the mirror already holds is skipped, so a resumed run never
// re-downloads and never duplicates.
func (f *Fetcher) fetch(ctx context.Context, acct Account) (Result, error) {
	// Housekeeping first, so leftovers from a crashed run never outlive the next.
	if _, err := SweepSpool(ctx, f.dbs, f.now()); err != nil {
		return Result{}, fmt.Errorf("sync account %s: %w", acct.ID, err)
	}
	if err := f.EnsureAccount(ctx, acct); err != nil {
		return Result{}, fmt.Errorf("sync account %s: %w", acct.ID, err)
	}
	c, err := f.dial(ctx, acct)
	if err != nil {
		return Result{}, fmt.Errorf("sync account %s: dial: %w", acct.ID, err)
	}
	defer func() { _ = c.Close() }()
	// The IMAP client's commands take no context, so a server that goes quiet
	// would block them forever. Closing the connection is what unblocks them.
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()

	res, ferr := f.fetchAll(ctx, c, acct)
	if ferr != nil && c.stall.didStall() {
		ferr = fmt.Errorf("%w: %w", errServerStalled, ferr)
	}
	// Re-thread even after a partial fetch: the messages already mirrored are
	// usable, and the next run rebuilds the assignment from scratch anyway. A
	// cancelled run skips it, since the connection is gone and nothing more can
	// be read.
	if ctx.Err() == nil {
		if serr := f.Settle(ctx, acct.ID); serr != nil && ferr == nil {
			ferr = serr
		}
	}
	if ferr != nil && ctx.Err() != nil {
		// The connection error is a symptom; report the cancellation that caused it.
		return res, fmt.Errorf("sync account %s: %w", acct.ID, ctx.Err())
	}
	return res, ferr
}

// syncOutcome is the sync_state row a finished run leaves behind. Backfill
// progress counts every message the run now holds; a completed run is done ==
// total. A failure keeps the previous last_ok_at (the store coalesces it) and
// carries a stable code.
func (f *Fetcher) syncOutcome(accountID string, res Result, err error) store.SyncState {
	have := res.Stored + res.Skipped
	s := store.SyncState{AccountID: accountID, UpdatedAt: f.now(), BackfillDone: have, BackfillTotal: have}
	if err == nil {
		s.Status = store.SyncOK
		s.LastOKAt = f.now()
		return s
	}
	s.Status, s.LastErrorCode, s.LastErrorDetail = classifySyncError(err)
	return s
}

func (f *Fetcher) recordSyncState(ctx context.Context, accountID string, s store.SyncState) {
	s.AccountID = accountID
	if err := f.dbs.SetSyncState(ctx, s); err != nil {
		slog.WarnContext(ctx, "sync: cannot record sync state", "account", accountID, "error", err)
	}
}

// classifySyncError maps a failed run to the API's SyncState and a stable code.
// An auth verdict wins over reachability, and a network error is "unreachable"
// rather than a generic failure so the banner can say the host is down.
func classifySyncError(err error) (store.SyncStatus, string, string) {
	detail := truncateSyncDetail(err.Error())
	var imapErr *imap.Error
	if errors.As(err, &imapErr) &&
		(imapErr.Code == imap.ResponseCodeAuthenticationFailed || imapErr.Code == imap.ResponseCodeAuthorizationFailed) {
		return store.SyncAuthFailed, "auth_failed", detail
	}
	var opErr *net.OpError
	if errors.Is(err, errServerStalled) || errors.As(err, &opErr) {
		return store.SyncUnreachable, "unreachable", detail
	}
	return store.SyncError, "sync_failed", detail
}

// truncateSyncDetail keeps the stored error under the limit SetSyncState
// enforces, cutting on a rune boundary so the text stays valid UTF-8.
func truncateSyncDetail(s string) string {
	if len(s) <= store.MaxSyncErrorDetail {
		return s
	}
	return strings.ToValidUTF8(s[:store.MaxSyncErrorDetail], "")
}

// Settle runs the passes that follow any batch of stored messages: heal derived
// data, then rebuild the account's threads. The fetch and the fast dev seeder
// both end with it, so they cannot disagree about either.
func (f *Fetcher) Settle(ctx context.Context, accountID string) error {
	// Heal derived data first: it can change has_attachments, and a message
	// fixed by a newer parser should read correctly from the next screen on.
	_, rerr := f.Rederive(ctx, accountID, rederiveBatch)
	// Thread even when healing failed: the rows already mirrored are usable.
	if terr := f.threadAccount(ctx, accountID); rerr == nil {
		return terr
	}
	return rerr
}

// threadAccount rebuilds one account's conversations from the mirrored headers
// and writes them back (chunk 2e). Threading is a whole-account pass because
// JWZ merges across folders, so a reply filed in Archive still joins its inbox
// root; it reads only headers, never bodies.
func (f *Fetcher) threadAccount(ctx context.Context, accountID string) error {
	msgs, err := f.dbs.MessagesForThreading(ctx, accountID)
	if err != nil {
		return fmt.Errorf("thread account %s: %w", accountID, err)
	}
	inputs := make([]thread.Message, len(msgs))
	for i, m := range msgs {
		inputs[i] = thread.Message{
			ID: m.ID, Key: m.ContentKey, MessageID: m.MessageID,
			References: m.References, InReplyTo: m.InReplyTo, Subject: m.Subject, Date: m.Date,
		}
	}
	built := thread.Build(inputs)
	records := make([]store.Thread, len(built))
	for i, t := range built {
		records[i] = store.Thread{
			ID: t.ID, AccountID: accountID, RootMessageID: t.RootMessageID,
			SubjectNorm: t.SubjectNorm, LastDate: t.LastDate, MessageIDs: t.MessageIDs,
		}
	}
	if err := f.dbs.ReplaceThreads(ctx, accountID, records); err != nil {
		return fmt.Errorf("thread account %s: %w", accountID, err)
	}
	return nil
}

// fetchAll reconciles every selectable mailbox on an open connection. It reads
// the server's whole account once so a message that moved between folders can be
// told apart from one that was removed (CHUNK3-BRIEF.md 1.5), then updates the
// mirror folder by folder: flags and new messages for live folders, disabled
// rows (never deleted) for what vanished.
func (f *Fetcher) fetchAll(ctx context.Context, c *session, acct Account) (Result, error) {
	mailboxes, useDelta, err := f.negotiate(ctx, c, acct)
	if err != nil {
		return Result{}, err
	}

	var snaps []folderSnapshot
	for _, mb := range mailboxes {
		if !selectable(mb) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("sync account %s: %w", acct.ID, err)
		}
		existing, found, err := f.folderRow(ctx, acct.ID, mb.Mailbox)
		if err != nil {
			return Result{}, err
		}
		snap, err := f.snapshotFolder(c, acct, mb, existing, found, useDelta)
		if err != nil {
			return Result{}, err
		}
		snaps = append(snaps, snap)
	}

	var res Result
	seen := make(map[string]bool, len(snaps))
	var churn []folderChurn
	for _, snap := range snaps {
		seen[snap.Name] = true
		stored, skipped, fr, err := f.reconcileFolder(ctx, c, acct, snap)
		if err != nil {
			return res, err
		}
		res.Folders++
		res.Stored += stored
		res.Skipped += skipped
		churn = append(churn, fr)
	}
	gone, err := f.markGoneFolders(ctx, acct.ID, seen)
	if err != nil {
		return res, err
	}
	churn = append(churn, gone...)
	// Move vs removal is decided once the whole account is known: a disabled row
	// whose Message-ID is still live elsewhere is a move (round 37). Settling every
	// pending row, not only this pass's, is what makes a pass that died earlier
	// harmless, and doing it after the pass is what lets the QRESYNC delta skip a
	// full-account scan.
	if err := f.dbs.SettlePendingDisabled(ctx, acct.ID); err != nil {
		return res, err
	}
	// Only a completed pass may alert: a pending row is not yet a disable the
	// operator can act on (N22), and a pass that died has no verdict to report.
	res.MassDisabled = massDisables(churn)
	return res, nil
}

// negotiate logs in, lists the mailboxes and decides whether the account can
// use QRESYNC deltas.
func (f *Fetcher) negotiate(ctx context.Context, c *session, acct Account) ([]*imap.ListData, bool, error) {
	defer c.watch()()
	if err := c.Login(acct.Username, acct.Password).Wait(); err != nil {
		return nil, false, fmt.Errorf("sync account %s: login: %w", acct.ID, err)
	}
	mailboxes, err := c.List("", "*", nil).Collect()
	if err != nil {
		return nil, false, fmt.Errorf("sync account %s: list: %w", acct.ID, err)
	}
	caps, err := c.Capability().Wait()
	if err != nil {
		return nil, false, fmt.Errorf("sync account %s: capability: %w", acct.ID, err)
	}
	useDelta := caps.Has(imap.CapQResync)
	if useDelta {
		if _, err := c.Enable(imap.CapQResync).Wait(); err != nil {
			// The server claimed QRESYNC but would not enable it. The full scan is
			// always correct, so fall back rather than fail the account.
			slog.WarnContext(ctx, "sync: server refused QRESYNC, using a full scan", "account", acct.ID, "error", err)
			useDelta = false
		}
	}
	return mailboxes, useDelta, nil
}

// folderSnapshot is what one SELECT plus a metadata FETCH read from a mailbox.
// The body is not read here: a message already mirrored keeps its bytes, and a
// new one is fetched by size tier in fetchBatch. When Delta is set, Messages
// holds only the messages that changed or arrived since the stored modseq and
// Vanished holds the UIDs expunged since it, so an unchanged mirrored row must
// be left alone rather than disabled for being absent.
type folderSnapshot struct {
	Name          string
	Attrs         []imap.MailboxAttr
	UIDValidity   uint32
	UIDNext       uint32
	HighestModSeq uint64
	Messages      []*imapclient.FetchMessageBuffer
	Vanished      imap.UIDSet
	Delta         bool
	Existing      store.Folder
	Found         bool
}

// canUseDelta reports whether a folder has a stored UIDVALIDITY and modseq to
// resume from. Whether the server supports QRESYNC is decided once per account.
// A folder marked gone has had its messages disabled, so the server's "nothing
// changed since the modseq" no longer describes the mirror: when the name comes
// back with the same UIDVALIDITY (a rename away and back) it needs a full read.
func canUseDelta(existing store.Folder, found bool) bool {
	return found && existing.GoneAt.IsZero() && existing.UIDValidity != 0 && existing.HighestModSeq > 0
}

// folderRow looks up a mailbox's mirror row. found is false for a folder the
// account has never seen, which cannot use a delta.
func (f *Fetcher) folderRow(ctx context.Context, accountID, name string) (store.Folder, bool, error) {
	existing, err := f.dbs.GetFolderByName(ctx, accountID, name)
	switch {
	case err == nil:
		return existing, true, nil
	case errors.Is(err, store.ErrNotFound):
		return store.Folder{}, false, nil
	default:
		return store.Folder{}, false, fmt.Errorf("sync account %s: look up folder %s: %w", accountID, name, err)
	}
}

// snapshotFolder selects a mailbox read-only and reads every message's
// envelope, flags, size and internal date, but no bodies. When the account can
// and should use QRESYNC it asks for the stored UIDVALIDITY and modseq, so the
// server answers with only what changed (or, if the validity no longer matches,
// the current validity and no VANISHED, which forces a full scan).
func (f *Fetcher) snapshotFolder(c *session, acct Account, mb *imap.ListData, existing store.Folder, found, useDelta bool) (folderSnapshot, error) {
	defer c.watch()()
	snap := folderSnapshot{Name: mb.Mailbox, Attrs: mb.Attrs, Existing: existing, Found: found}
	var (
		data *imap.SelectData
		err  error
	)
	if useDelta && canUseDelta(existing, found) {
		data, err = c.Select(mb.Mailbox, &imap.SelectOptions{
			ReadOnly: true,
			QResync:  &imap.QResyncOptions{UIDValidity: existing.UIDValidity, ModSeq: existing.HighestModSeq},
		}).Wait()
		if err != nil {
			return folderSnapshot{}, fmt.Errorf("sync account %s: qresync select %s: %w", acct.ID, mb.Mailbox, err)
		}
		if data.UIDValidity == existing.UIDValidity {
			snap.Delta = true
			snap.Vanished = data.Vanished
		}
	} else {
		data, err = c.Select(mb.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			return folderSnapshot{}, fmt.Errorf("sync account %s: select %s: %w", acct.ID, mb.Mailbox, err)
		}
	}
	snap.UIDValidity = data.UIDValidity
	snap.UIDNext = uint32(data.UIDNext)
	snap.HighestModSeq = data.HighestModSeq
	if data.NumMessages == 0 {
		return snap, nil
	}
	// Only presence and flags: reconcileFolder needs nothing else, and the
	// envelope, date and size of a message that is new are fetched with its body
	// in fetchBatch. Asking for them here held about four times the memory per
	// message for every message of the folder, every pass.
	opts := &imap.FetchOptions{UID: true, Flags: true}
	if snap.Delta {
		opts.ChangedSince = existing.HighestModSeq
	}
	all := imap.UIDSet{imap.UIDRange{Start: 1, Stop: 0}}
	metas, err := c.Fetch(all, opts).Collect()
	if err != nil {
		return folderSnapshot{}, fmt.Errorf("sync account %s: list messages in %s: %w", acct.ID, mb.Mailbox, err)
	}
	snap.Messages = metas
	return snap, nil
}

// reconcileFolder brings one server folder's rows up to date and returns how
// many bodies it stored and how many messages it already held. Nothing is
// deleted: a row the server no longer holds in this folder is disabled, with
// "moved" when its content still lives elsewhere on the server and
// "server_removed" otherwise (rounds 37 and 38).
func (f *Fetcher) reconcileFolder(ctx context.Context, c *session, acct Account, snap folderSnapshot) (stored, skipped int, churn folderChurn, err error) {
	churn.name = snap.Name
	folder := store.Folder{
		ID:            folderRowID(acct.ID, snap.Name),
		AccountID:     acct.ID,
		Name:          snap.Name,
		Role:          RoleFor(snap.Name, snap.Attrs),
		UIDValidity:   snap.UIDValidity,
		HighestModSeq: 0,
		LastSyncAt:    f.now(),
	}
	if snap.Found {
		folder.ID = snap.Existing.ID
	}
	validityChanged := snap.Found && snap.Existing.UIDValidity != snap.UIDValidity
	// A partial run must not advance the stored modseq past the bodies it never
	// fetched: a later delta would then skip them. Keep the old modseq (or 0 for
	// a folder whose validity just changed) until every body in this folder is
	// stored, below.
	if snap.Found && !validityChanged {
		folder.HighestModSeq = snap.Existing.HighestModSeq
	}
	// The folder row must exist before its messages (foreign keys), and writing
	// it first also revives a name the server has brought back.
	if err := f.dbs.UpsertFolder(ctx, folder); err != nil {
		return 0, 0, churn, err
	}

	refs, err := f.dbs.SyncMessageRefs(ctx, folder.ID)
	if err != nil {
		return 0, 0, churn, err
	}
	// The live rows this folder held when the pass began, before anything is
	// hidden: the denominator the mass-disable fraction is measured against.
	churn.held = len(refs)

	// A UIDVALIDITY change invalidates every UID the folder held. Disable what
	// the old validity left behind and read the new validity as if it were new.
	if !snap.Found || validityChanged {
		n, err := f.disableRefs(ctx, refs)
		churn.hidden += n
		if err != nil {
			return 0, 0, churn, err
		}
		refs = nil
	}

	mirrored := make(map[uint32]store.SyncMessageRef, len(refs))
	for _, ref := range refs {
		if ref.UIDValidity != snap.UIDValidity {
			// A leftover from an earlier validity that was never disabled.
			if err := f.disableRef(ctx, ref); err != nil {
				return 0, 0, churn, err
			}
			churn.hidden++
			continue
		}
		mirrored[ref.UID] = ref
	}

	present := make(map[uint32]*imapclient.FetchMessageBuffer, len(snap.Messages))
	for _, meta := range snap.Messages {
		present[uint32(meta.UID)] = meta
	}

	var newUIDs []uint32
	for uid, ref := range mirrored {
		if snap.Delta && snap.Vanished.Contains(imap.UID(uid)) {
			if err := f.disableRef(ctx, ref); err != nil {
				return 0, 0, churn, err
			}
			churn.hidden++
			continue
		}
		meta, ok := present[uid]
		if !ok {
			if snap.Delta {
				// A delta sent nothing for an unchanged message: it is still live.
				skipped++
				continue
			}
			if err := f.disableRef(ctx, ref); err != nil {
				return 0, 0, churn, err
			}
			churn.hidden++
			continue
		}
		skipped++
		if sameFlags(flagStrings(meta.Flags), ref.Flags) {
			continue
		}
		if err := f.dbs.SetMessageFlags(ctx, ref.ID, flagStrings(meta.Flags)); err != nil {
			return 0, 0, churn, fmt.Errorf("sync account %s: flags %s/%d: %w", acct.ID, snap.Name, uid, err)
		}
	}
	for uid := range present {
		if _, ok := mirrored[uid]; !ok {
			newUIDs = append(newUIDs, uid)
		}
	}

	// Bodies are fetched newest first, in bounded batches, so a dropped
	// connection leaves a resumable newest-prefix checkpoint (CHUNK3-BRIEF.md 4)
	// and the next run skips what it already holds.
	slices.SortFunc(newUIDs, func(a, b uint32) int {
		switch {
		case a > b:
			return -1
		case a < b:
			return 1
		default:
			return 0
		}
	})
	for start := 0; start < len(newUIDs); start += f.batch {
		end := min(start+f.batch, len(newUIDs))
		set := imap.UIDSet{}
		for _, uid := range newUIDs[start:end] {
			set.AddNum(imap.UID(uid))
		}
		// The snapshot pass left another mailbox selected; fetching bodies reads
		// whatever is selected, so select this folder again first.
		if err := reselect(c, snap.Name); err != nil {
			return stored, skipped, churn, fmt.Errorf("sync account %s: reselect %s: %w", acct.ID, snap.Name, err)
		}
		n, err := f.fetchBatch(ctx, c, acct, folder.ID, snap.UIDValidity, set)
		stored += n
		if err != nil {
			return stored, skipped, churn, err
		}
	}
	// Every body this folder needed is stored, so the modseq may advance. A
	// failure above returns before this and leaves the old modseq for the retry.
	folder.HighestModSeq = snap.HighestModSeq
	if err := f.dbs.UpsertFolder(ctx, folder); err != nil {
		return stored, skipped, churn, err
	}
	return stored, skipped, churn, nil
}

func reselect(c *session, name string) error {
	defer c.watch()()
	_, err := c.Select(name, &imap.SelectOptions{ReadOnly: true}).Wait()
	return err
}

// markGoneFolders hides a mirror folder the server no longer lists and disables
// its live messages, exactly as a vanished UID is handled. Rows, raw bytes and
// spool files stay (CHUNK3-BRIEF.md 1).
func (f *Fetcher) markGoneFolders(ctx context.Context, accountID string, seen map[string]bool) ([]folderChurn, error) {
	folders, err := f.dbs.AllFolders(ctx, accountID)
	if err != nil {
		return nil, err
	}
	var churn []folderChurn
	for _, folder := range folders {
		if seen[folder.Name] || !folder.GoneAt.IsZero() {
			continue
		}
		refs, err := f.dbs.SyncMessageRefs(ctx, folder.ID)
		if err != nil {
			return nil, err
		}
		n, err := f.disableRefs(ctx, refs)
		if err != nil {
			return nil, err
		}
		if err := f.dbs.SetFolderGone(ctx, folder.ID, f.now()); err != nil {
			return nil, err
		}
		churn = append(churn, folderChurn{name: folder.Name, held: len(refs), hidden: n})
	}
	return churn, nil
}

// disableRefs hides every ref and returns how many it hid, so a caller can feed
// the count to the mass-disable alert without counting its own loop.
func (f *Fetcher) disableRefs(ctx context.Context, refs []store.SyncMessageRef) (int, error) {
	n := 0
	for _, ref := range refs {
		if err := f.disableRef(ctx, ref); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// disableRef hides a vanished row as pending: whether it moved or was removed is
// only known once the whole account has been read, and the label lives in the
// row so a pass that dies first leaves it for the next one to settle. The raw
// bytes are copied into the blob store first, because a hidden message is the
// one thing a mirror rebuild cannot bring back (ARCHITECTURE.md 9).
func (f *Fetcher) disableRef(ctx context.Context, ref store.SyncMessageRef) error {
	hash, err := f.storeDisabledBlob(ctx, ref.ID)
	if err != nil {
		// The mirror still holds the bytes and the backup reconcile retries, so a
		// blob copy failure does not fail the sweep, but it is never silent.
		slog.WarnContext(ctx, "could not store a disabled message's blob",
			"account", ref.ID, "error", err)
		hash = ""
	}
	if err := f.dbs.DisableMessage(ctx, ref.ID, store.DisabledPending, f.now(), hash); err != nil {
		return fmt.Errorf("disable message %s: %w", ref.ID, err)
	}
	return nil
}

// storeDisabledBlob copies a message's raw bytes into the blob store and returns
// its content hash. A message that was never downloaded has none, which is not
// an error. The store dedupes by hash, so a repeated message costs one file.
func (f *Fetcher) storeDisabledBlob(ctx context.Context, id string) (string, error) {
	raw, err := f.dbs.MessageRawReader(ctx, id)
	if errors.Is(err, store.ErrNoRaw) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = raw.Close() }()
	hash, _, err := f.dbs.Blobs.Put(ctx, raw)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// canonicalFlags lower-cases and sorts a flag list, so two listings of the same
// flags compare equal whatever order or case the server used.
func canonicalFlags(flags []string) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = strings.ToLower(f)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func sameFlags(a, b []string) bool {
	return slices.Equal(canonicalFlags(a), canonicalFlags(b))
}

// EnsureAccount creates the mirror account row the first time sync runs. An
// existing row is left alone: display name, icon, colour and photo belong to
// the settings UI (2g), not to the read path.
func (f *Fetcher) EnsureAccount(ctx context.Context, acct Account) error {
	if _, err := f.dbs.GetAccount(ctx, acct.ID); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("get account: %w", err)
	}
	err := f.dbs.UpsertAccount(ctx, store.Account{
		ID: acct.ID, Address: acct.Address,
		IMAPHost: acct.IMAPHost, IMAPPort: acct.IMAPPort,
		SMTPHost: acct.SMTPHost, SMTPPort: acct.SMTPPort,
		Username: acct.Username, CreatedAt: f.now(),
	})
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}
	return nil
}

// newFolder is the row a synced mailbox gets, whichever way its numbers arrived.
func (f *Fetcher) newFolder(acct Account, name string, attrs []imap.MailboxAttr, uidValidity uint32, modSeq uint64) store.Folder {
	return store.Folder{
		ID:            folderRowID(acct.ID, name),
		AccountID:     acct.ID,
		Name:          name,
		Role:          RoleFor(name, attrs),
		UIDValidity:   uidValidity,
		HighestModSeq: modSeq,
		LastSyncAt:    f.now(),
	}
}

// RecordFolder writes a mailbox's row without a connection, for the fast dev
// seeder, which already knows the numbers a SELECT would have returned. A row
// that exists keeps its id, as a re-sync would.
func (f *Fetcher) RecordFolder(ctx context.Context, acct Account, name string, attrs []imap.MailboxAttr, uidValidity uint32, modSeq uint64) (store.Folder, error) {
	folder := f.newFolder(acct, name, attrs, uidValidity, modSeq)
	existing, err := f.dbs.GetFolderByName(ctx, acct.ID, name)
	switch {
	case err == nil:
		folder.ID = existing.ID
	case !errors.Is(err, store.ErrNotFound):
		return store.Folder{}, fmt.Errorf("look up folder %s: %w", name, err)
	}
	if err := f.dbs.UpsertFolder(ctx, folder); err != nil {
		return store.Folder{}, err
	}
	return folder, nil
}

// fetchBatch mirrors one UID set. It first asks for every message's envelope,
// flags and size, which is small whatever the messages weigh, then fetches each
// body by size tier (see InlineMessageBytes). Each message is upserted as soon as
// it is complete, so a dropped connection keeps the progress it made rather than
// losing the whole batch.
func (f *Fetcher) fetchBatch(ctx context.Context, c *session, acct Account, folderID string, uidvalidity uint32, set imap.UIDSet) (int, error) {
	defer c.watch()()
	metas, err := c.Fetch(set, &imap.FetchOptions{
		UID:          true,
		Flags:        true,
		Envelope:     true,
		InternalDate: true,
		RFC822Size:   true,
	}).Collect()
	if err != nil {
		return 0, fmt.Errorf("fetch envelopes: %w", err)
	}

	var inline imap.UIDSet
	var spooled []*imapclient.FetchMessageBuffer
	byUID := make(map[imap.UID]*imapclient.FetchMessageBuffer, len(metas))
	stored := 0
	for _, meta := range metas {
		byUID[meta.UID] = meta
		switch {
		case meta.RFC822Size > f.max:
			if err := f.dbs.UpsertMessage(ctx, f.envelopeOnly(acct, folderID, uidvalidity, meta)); err != nil {
				return stored, err
			}
			stored++
		case meta.RFC822Size > 0 && meta.RFC822Size <= f.inline:
			inline.AddNum(meta.UID)
		default:
			// Includes a size the server did not report: the spool path is the one
			// that is safe whatever the message turns out to weigh.
			spooled = append(spooled, meta)
		}
	}

	n, err := f.fetchInline(ctx, c.Client, acct, folderID, uidvalidity, inline, byUID)
	stored += n
	if err != nil {
		return stored, err
	}
	for _, meta := range spooled {
		if err := ctx.Err(); err != nil {
			return stored, err
		}
		ok, err := f.fetchSpooled(ctx, c.Client, acct, folderID, uidvalidity, meta)
		if err != nil {
			return stored, err
		}
		if ok {
			stored++
		}
	}
	return stored, nil
}

// fetchInline fetches the bodies of small messages in one command, collecting
// each into memory, and stores the raw bytes in the row.
func (f *Fetcher) fetchInline(ctx context.Context, c *imapclient.Client, acct Account, folderID string, uidvalidity uint32, set imap.UIDSet, byUID map[imap.UID]*imapclient.FetchMessageBuffer) (int, error) {
	if len(set) == 0 {
		return 0, nil
	}
	section := &imap.FetchItemBodySection{Peek: true}
	cmd := c.Fetch(set, &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}})
	stored := 0
	for {
		data := cmd.Next()
		if data == nil {
			break
		}
		buf, err := data.Collect()
		if err != nil {
			_ = cmd.Close()
			return stored, fmt.Errorf("fetch body: %w", err)
		}
		meta, ok := byUID[buf.UID]
		if !ok {
			continue // the server sent a message we did not ask about
		}
		if err := f.storeInline(ctx, acct, folderID, uidvalidity, meta, buf.FindBodySection(section)); err != nil {
			_ = cmd.Close()
			return stored, err
		}
		stored++
	}
	if err := cmd.Close(); err != nil {
		return stored, fmt.Errorf("fetch: %w", err)
	}
	return stored, nil
}

// storeInline writes a message whose raw bytes are already in memory (up to
// InlineMessageBytes) into its row.
func (f *Fetcher) storeInline(ctx context.Context, acct Account, folderID string, uidvalidity uint32, meta *imapclient.FetchMessageBuffer, raw []byte) error {
	m := f.baseMessage(acct, folderID, uidvalidity, meta)
	m.RawBlob = raw
	m.ContentKey = store.ContentKey(m.MessageID, headerBlock(raw))
	// An in-memory message goes through the same bounded walk as a spooled one,
	// so its attachment metadata and hashes are enumerated in one pass.
	parsed := mailmime.ParseStream(bytes.NewReader(raw), acct.TrustedAuthservIDs...)
	applyParsed(&m, parsed)
	if parsed.BodySkipped {
		m.BodyStatus = store.BodyUnparsed
	}
	if err := f.dbs.UpsertMessage(ctx, m); err != nil {
		return err
	}
	return f.dbs.SetMessageDerived(ctx, m.ID, derivedFrom(m.ID, parsed))
}

// StoreRaw mirrors one message whose bytes the caller already holds, applying
// the same three size tiers as a fetch. The fast dev seeder uses it in place of
// IMAP; sharing storeInline, storeSpooled and envelopeOnly with the fetch is
// what lets a test say the two modes agree.
func (f *Fetcher) StoreRaw(ctx context.Context, acct Account, folderID string, uidvalidity uint32, meta *imapclient.FetchMessageBuffer, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The tiers are chosen on the size FETCH reported, as fetchBatch does.
	switch {
	case meta.RFC822Size > f.max:
		return f.dbs.UpsertMessage(ctx, f.envelopeOnly(acct, folderID, uidvalidity, meta))
	case meta.RFC822Size > 0 && meta.RFC822Size <= f.inline:
		return f.storeInline(ctx, acct, folderID, uidvalidity, meta, raw)
	}
	rel := spoolRel(folderID, uidvalidity, uint32(meta.UID))
	if _, err := writeSpool(filepath.Join(f.dbs.Dir, filepath.FromSlash(rel)), bytes.NewReader(raw), f.max); err != nil {
		return err
	}
	return f.storeSpooled(ctx, acct, folderID, uidvalidity, meta, rel)
}

// spoolRel is where a message's spool file lives, relative to the data dir. The
// UIDVALIDITY is part of the path so a rebuilt folder's new message at an old
// UID cannot overwrite the disabled message's file.
func spoolRel(folderID string, uidvalidity uint32, uid uint32) string {
	return path.Join("spool", folderID, strconv.FormatUint(uint64(uidvalidity), 10),
		strconv.FormatUint(uint64(uid), 10)+".eml")
}

// fetchSpooled streams one message's body to a file under the data directory
// and parses it from there, so the message and its attachments are never held
// whole in memory. It reports whether a row was stored; a message that expunged
// itself between the two commands, or that delivered more than the limit, is not
// an error for the run.
func (f *Fetcher) fetchSpooled(ctx context.Context, c *imapclient.Client, acct Account, folderID string, uidvalidity uint32, meta *imapclient.FetchMessageBuffer) (bool, error) {
	section := &imap.FetchItemBodySection{Peek: true}
	cmd := c.Fetch(imap.UIDSetNum(meta.UID), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}})

	rel := spoolRel(folderID, uidvalidity, uint32(meta.UID))
	var spoolErr error
	got := false
	for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
		for item := msg.Next(); item != nil; item = msg.Next() {
			body, ok := item.(imapclient.FetchItemDataBodySection)
			if !ok || got || spoolErr != nil {
				continue // the client discards what we do not read
			}
			if _, err := writeSpool(filepath.Join(f.dbs.Dir, filepath.FromSlash(rel)), body.Literal, f.max); err != nil {
				spoolErr = err
				continue
			}
			got = true
		}
	}
	if err := cmd.Close(); err != nil {
		return false, fmt.Errorf("fetch body %d: %w", meta.UID, err)
	}
	switch {
	case errors.Is(spoolErr, errSpoolTooLarge):
		// The server announced a size within the limit and sent more. Treat it as
		// over the limit rather than trust either number.
		return true, f.dbs.UpsertMessage(ctx, f.envelopeOnly(acct, folderID, uidvalidity, meta))
	case spoolErr != nil:
		return false, spoolErr
	case !got:
		return false, nil
	}

	if err := f.storeSpooled(ctx, acct, folderID, uidvalidity, meta, rel); err != nil {
		return false, err
	}
	return true, nil
}

// storeSpooled parses a message already written to the spool and stores its row.
func (f *Fetcher) storeSpooled(ctx context.Context, acct Account, folderID string, uidvalidity uint32, meta *imapclient.FetchMessageBuffer, rel string) error {
	m := f.baseMessage(acct, folderID, uidvalidity, meta)
	m.RawPath = rel
	parsed, header, err := parseSpooled(filepath.Join(f.dbs.Dir, filepath.FromSlash(rel)), acct.TrustedAuthservIDs)
	if err != nil {
		return err
	}
	m.ContentKey = store.ContentKey(m.MessageID, headerBlock(header))
	applyParsed(&m, parsed)
	if parsed.BodySkipped {
		m.BodyStatus = store.BodyUnparsed
	}
	if err := f.dbs.UpsertMessage(ctx, m); err != nil {
		return err
	}
	return f.dbs.SetMessageDerived(ctx, m.ID, derivedFrom(m.ID, parsed))
}

// DerivedVersion names the pipeline that turns a raw message into its derived
// data: the parser's text and part walk, the sanitizer's policy and the
// attachment rows. **Bump it whenever the output of mime.Parse, render or the
// part walk changes**; every row behind it is then re-derived from its raw
// message by Rederive (N10/N14 in papercuts.md, decided in round 32b). Rows
// mirrored before versions existed are at 0, so they heal on the next run.
const DerivedVersion = 1

// rederiveBatch bounds how many messages one sync run re-derives, so a large
// backlog heals across runs instead of stalling a fetch.
const rederiveBatch = 200

// derivedFrom turns one parse into the whole derived record. It is pure, and
// the same function serves a fresh message and a re-derived one. The default
// policy blocks remote images and strips tracking pixels; an operator allow-list
// is served by re-rendering from the raw message, so the cached copy never
// carries live remote content.
func derivedFrom(id string, parsed mailmime.Parsed) store.Derived {
	d := store.Derived{
		Version:        DerivedVersion,
		BodyText:       parsed.Text,
		Snippet:        parsed.Snippet,
		HasAttachments: len(parsed.Parts) > 0,
		ParseErrors:    parsed.Errors,
		BodyStatus:     store.BodyOK,
	}
	if parsed.BodySkipped {
		d.BodyStatus = store.BodyUnparsed
	}
	if parsed.HTML != "" {
		d.BodyHTML = render.Body(parsed, render.Options{MessageID: id}).HTML
	}
	for _, p := range parsed.Parts {
		d.Attachments = append(d.Attachments, store.Attachment{
			MessageID:   id,
			Filename:    p.Filename,
			MIMEType:    p.ContentType,
			Size:        p.Size,
			ContentHash: p.Hash,
			CID:         p.CID,
			StoragePath: p.Path,
		})
	}
	return d
}

// Rederive brings up to limit of an account's messages up to DerivedVersion,
// newest first, by parsing each one's raw bytes again (the row's blob or its
// spool file, streamed, never held whole) and writing the result in one
// transaction. It returns how many it healed.
//
// A message whose raw bytes are gone (the spool file is missing) can never be
// derived, so it is marked failed at this version and left out of later passes;
// otherwise a few of them at the top of the newest-first list would fill every
// slot of every pass (N16). The pass then carries on, so a broken message does
// not cost the healthy ones behind it their turn. A failure that may clear (a
// permission error, a busy disk) is logged and skipped without a mark, so the
// next pass tries again. An error from the database or a cancelled context ends
// the pass.
func (f *Fetcher) Rederive(ctx context.Context, accountID string, limit int) (int, error) {
	healed := 0
	for healed < limit {
		ids, err := f.dbs.MessageIDsBehind(ctx, accountID, DerivedVersion, limit-healed)
		if err != nil {
			return healed, fmt.Errorf("rederive %s: %w", accountID, err)
		}
		progressed := false // a heal or a mark; skipped transients alone must end the loop
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return healed, fmt.Errorf("rederive %s: %w", accountID, err)
			}
			m, err := f.dbs.GetMessage(ctx, id)
			if errors.Is(err, store.ErrNotFound) {
				continue // disabled since the list was taken
			}
			if err != nil {
				return healed, fmt.Errorf("rederive %s: %w", accountID, err)
			}
			parsed, err := f.parseRaw(m)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				slog.WarnContext(ctx, "sync: raw message is gone, not re-deriving it", "message", id, "error", err)
				if err := f.dbs.MarkDeriveFailed(ctx, id, DerivedVersion); err != nil {
					return healed, fmt.Errorf("rederive %s: %w", accountID, err)
				}
				progressed = true
				continue
			case err != nil:
				slog.WarnContext(ctx, "sync: cannot re-derive message yet", "message", id, "error", err)
				continue
			}
			if err := f.dbs.SetMessageDerived(ctx, id, derivedFrom(id, parsed)); err != nil {
				return healed, fmt.Errorf("rederive %s: %w", accountID, err)
			}
			healed++
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return healed, nil
}

// parseRaw parses a mirrored message from wherever its raw bytes live.
func (f *Fetcher) parseRaw(m store.Message) (mailmime.Parsed, error) {
	if m.RawPath != "" {
		parsed, _, err := parseSpooled(filepath.Join(f.dbs.Dir, filepath.FromSlash(m.RawPath)), nil)
		return parsed, err
	}
	return mailmime.ParseStream(bytes.NewReader(m.RawBlob)), nil
}

// parseSpooled reads a spooled message from disk in a single pass, returning the
// parsed fields and the header block (the content key needs it when a message has
// no Message-ID).
func parseSpooled(file string, trustedAuthservIDs []string) (mailmime.Parsed, []byte, error) {
	fh, err := os.Open(file) //nolint:gosec // G304: a path this package built from a folder id and a UID
	if err != nil {
		return mailmime.Parsed{}, nil, fmt.Errorf("open spooled message: %w", err)
	}
	defer func() { _ = fh.Close() }()
	header, err := mailmime.HeaderBlock(fh)
	if err != nil {
		header = nil // an oversize header block is reported by ParseStream below
	}
	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		return mailmime.Parsed{}, nil, fmt.Errorf("rewind spooled message: %w", err)
	}
	return mailmime.ParseStream(fh, trustedAuthservIDs...), header, nil
}

// envelopeOnly builds the row for a message that is not downloaded: the
// envelope fields the first FETCH returned, an explicit status, and a content
// key made from the envelope since there is no header block to hash.
func (f *Fetcher) envelopeOnly(acct Account, folderID string, uidvalidity uint32, meta *imapclient.FetchMessageBuffer) store.Message {
	m := f.baseMessage(acct, folderID, uidvalidity, meta)
	m.BodyStatus = store.BodyTooLarge
	// No UID in the fallback: the key must survive a move to another folder.
	m.ContentKey = store.ContentKey(m.MessageID, fmt.Appendf(nil, "%d|%s|%s|%s",
		meta.RFC822Size, meta.InternalDate.UTC().Format(time.RFC3339), m.From.Address, m.Subject))
	return m
}

// baseMessage projects the metadata FETCH (envelope, flags, size, internal
// date) into a mirror row. Every tier starts here; the body fields are filled in
// afterwards by applyParsed, or left empty for a message that is not downloaded.
// Sanitising the HTML is render/'s job (chunk 2d), so body_html is left for it.
func (f *Fetcher) baseMessage(acct Account, folderID string, uidvalidity uint32, buf *imapclient.FetchMessageBuffer) store.Message {
	uid := uint32(buf.UID)
	m := store.Message{
		ID:           messageRowID(folderID, uidvalidity, uid),
		AccountID:    acct.ID,
		FolderID:     folderID,
		UID:          uid,
		UIDValidity:  uidvalidity,
		Size:         buf.RFC822Size,
		InternalDate: buf.InternalDate,
		Flags:        flagStrings(buf.Flags),
	}
	if env := buf.Envelope; env != nil {
		m.MessageID = wrapMessageID(env.MessageID)
		m.Subject = env.Subject
		m.InReplyTo = joinMessageIDs(env.InReplyTo)
		m.From = firstAddress(env.From)
		m.To = addresses(env.To)
		m.CC = addresses(env.Cc)
		m.Date = env.Date
	}
	if m.Date.IsZero() || m.Date.After(f.now().Add(maxDateSkew)) {
		// A missing, unparsable or future-dated Date header sorts by internal
		// date so a clock-skewed message does not pin itself to the top of the
		// inbox (ARCHITECTURE.md 9a).
		m.Date = buf.InternalDate
	}
	return m
}

// applyParsed copies what the parser read from the body into the row.
func applyParsed(m *store.Message, parsed mailmime.Parsed) {
	m.BodyText = parsed.Text
	m.Snippet = parsed.Snippet
	m.HasAttachments = len(parsed.Parts) > 0
	m.References = strings.Join(parsed.References, " ")
	if len(parsed.InReplyTo) > 0 {
		// Prefer the raw header; ENVELOPE is the fallback when it is absent.
		m.InReplyTo = strings.Join(parsed.InReplyTo, " ")
	}
	m.ReplyTo = parsedAddresses(parsed.ReplyTo)
	m.DeliveredTo = parsedAddresses(parsed.DeliveredTo)
	m.AuthResults = store.AuthResults{
		AuthservID: parsed.Auth.AuthservID,
		SPF:        parsed.Auth.SPF, DKIM: parsed.Auth.DKIM, DMARC: parsed.Auth.DMARC, Raw: parsed.Auth.Raw,
	}
	m.ParseErrors = parsed.Errors
}

// parsedAddresses converts the parser's own address type to the mirror's.
func parsedAddresses(in []mailmime.Address) []store.Address {
	out := make([]store.Address, 0, len(in))
	for _, a := range in {
		out = append(out, store.Address{Name: a.Name, Address: a.Address})
	}
	return out
}

// selectable reports whether a LIST entry is a real mailbox we can SELECT.
func selectable(mb *imap.ListData) bool {
	if mb.Mailbox == "" {
		return false
	}
	for _, attr := range mb.Attrs {
		if attr == imap.MailboxAttrNoSelect || attr == imap.MailboxAttrNonExistent {
			return false
		}
	}
	return true
}

// dialTimeout bounds connecting and the TLS handshake to a host that does not
// answer; the context can end it sooner.
const dialTimeout = 15 * time.Second

// dial opens the IMAP connection under ctx. It does not use imapclient.Dial*,
// which take no context, so cancellation also reaches the connect and handshake.
func (f *Fetcher) dial(ctx context.Context, acct Account) (*session, error) {
	return f.dialWith(ctx, acct, nil)
}

// dialWith is dial with extra imapclient options, so the IDLE connection can
// install a unilateral-data handler and be woken by a server notification. The
// connection carries the stall guard; callers arm it around their commands.
func (f *Fetcher) dialWith(ctx context.Context, acct Account, options *imapclient.Options) (*session, error) {
	addr := net.JoinHostPort(acct.IMAPHost, strconv.Itoa(acct.IMAPPort))
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	wire := net.Conn(conn)
	if !acct.Insecure {
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: acct.IMAPHost,
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"imap"},
		})
		hsCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		defer cancel()
		if err := tlsConn.HandshakeContext(hsCtx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		wire = tlsConn
	}
	guarded := &stallConn{Conn: wire, timeout: f.stall}
	return &session{Client: imapclient.New(guarded, options), stall: guarded}, nil
}

// folderRowID is the stable mirror id for a mailbox, derived from its account
// and name, so re-running sync reuses the same row.
func folderRowID(accountID, name string) string {
	sum := sha256.Sum256([]byte(accountID + "\x00" + name))
	return hex.EncodeToString(sum[:16])
}

// messageRowID is the stable id for a message in a folder. It includes the
// folder's UIDVALIDITY so a folder rebuild that reuses UID numbers gives the new
// row a distinct id from the disabled old one. UpsertMessage keys on
// (folder, uidvalidity, uid) and keeps the original id, so this is only the value
// used on first insert.
func messageRowID(folderID string, uidvalidity uint32, uid uint32) string {
	sum := sha256.Sum256([]byte(folderID + "\x00" + strconv.FormatUint(uint64(uidvalidity), 10) + "\x00" + strconv.FormatUint(uint64(uid), 10)))
	return hex.EncodeToString(sum[:16])
}

func flagStrings(flags []imap.Flag) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, string(f))
	}
	return out
}

func firstAddress(addrs []imap.Address) store.Address {
	for _, a := range addrs {
		if addr := addressOf(a); addr.Address != "" {
			return addr
		}
	}
	return store.Address{}
}

func addresses(addrs []imap.Address) []store.Address {
	out := make([]store.Address, 0, len(addrs))
	for _, a := range addrs {
		if addr := addressOf(a); addr.Address != "" {
			out = append(out, addr)
		}
	}
	return out
}

func addressOf(a imap.Address) store.Address {
	if a.IsGroupStart() || a.IsGroupEnd() {
		return store.Address{}
	}
	return store.Address{Name: a.Name, Address: a.Addr()}
}

// wrapMessageID restores the angle brackets ENVELOPE strips, so the stored
// header matches what the raw message carries.
func wrapMessageID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) >= 2 && id[0] == '<' && id[len(id)-1] == '>' {
		return id
	}
	return "<" + id + ">"
}

func joinMessageIDs(ids []string) string {
	wrapped := make([]string, 0, len(ids))
	for _, id := range ids {
		if w := wrapMessageID(id); w != "" {
			wrapped = append(wrapped, w)
		}
	}
	return strings.Join(wrapped, " ")
}

// headerBlock returns the raw header bytes ContentKey falls back to when a
// message has no Message-ID.
func headerBlock(raw []byte) []byte {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[:i]
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[:i]
	}
	return raw
}
