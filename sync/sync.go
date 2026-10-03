// Package sync is Ivy's read path: it fetches folders and messages from an IMAP
// server into the mirror, keyed by (folder, uid) and the stable content key.
//
// Chunk 2b is deliberately a small one-shot read fetch. There is no IDLE, no
// QRESYNC, no write path and no idle timetable: the steady-state engine in
// chunk 3 generalizes this boundary rather than replacing it. Nothing here
// deletes: a message that vanishes from the server is handled by chunk 3's
// disabled-not-deleted sweep.
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
	"net"
	"os"
	"path"
	"path/filepath"
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
}

// Fetcher mirrors accounts into the store. It is stateless between runs, so a
// crashed run resumes by asking the store which UIDs it already holds.
type Fetcher struct {
	dbs    *store.DBs
	now    func() time.Time
	batch  int
	inline int64 // largest message fetched into memory
	max    int64 // largest message downloaded at all
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

// NewFetcher builds a Fetcher over the mirror.
func NewFetcher(dbs *store.DBs, opts ...Option) *Fetcher {
	f := &Fetcher{dbs: dbs, now: time.Now, batch: defaultBatchSize, inline: InlineMessageBytes, max: MaxMessageBytes}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Fetch connects to one account and mirrors every selectable mailbox, newest
// message first. A message the mirror already holds is skipped, so a resumed
// run never re-downloads and never duplicates.
func (f *Fetcher) Fetch(ctx context.Context, acct Account) (Result, error) {
	// Housekeeping first, so leftovers from a crashed run never outlive the next.
	if _, err := SweepSpool(ctx, f.dbs, f.now()); err != nil {
		return Result{}, fmt.Errorf("sync account %s: %w", acct.ID, err)
	}
	if err := f.ensureAccount(ctx, acct); err != nil {
		return Result{}, fmt.Errorf("sync account %s: %w", acct.ID, err)
	}
	c, err := dial(ctx, acct)
	if err != nil {
		return Result{}, fmt.Errorf("sync account %s: dial: %w", acct.ID, err)
	}
	defer func() { _ = c.Close() }()
	// The IMAP client's commands take no context, so a server that goes quiet
	// would block them forever. Closing the connection is what unblocks them.
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()

	res, ferr := f.fetchAll(ctx, c, acct)
	// Re-thread even after a partial fetch: the messages already mirrored are
	// usable, and the next run rebuilds the assignment from scratch anyway. A
	// cancelled run skips it, since the connection is gone and nothing more can
	// be read.
	if ctx.Err() == nil {
		if terr := f.threadAccount(ctx, acct.ID); terr != nil && ferr == nil {
			ferr = terr
		}
	}
	if ferr != nil && ctx.Err() != nil {
		// The connection error is a symptom; report the cancellation that caused it.
		return res, fmt.Errorf("sync account %s: %w", acct.ID, ctx.Err())
	}
	return res, ferr
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

// fetchAll logs in on an open connection and mirrors every selectable mailbox.
func (f *Fetcher) fetchAll(ctx context.Context, c *imapclient.Client, acct Account) (Result, error) {
	if err := c.Login(acct.Username, acct.Password).Wait(); err != nil {
		return Result{}, fmt.Errorf("sync account %s: login: %w", acct.ID, err)
	}
	mailboxes, err := c.List("", "*", nil).Collect()
	if err != nil {
		return Result{}, fmt.Errorf("sync account %s: list: %w", acct.ID, err)
	}

	var res Result
	for _, mb := range mailboxes {
		if !selectable(mb) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("sync account %s: %w", acct.ID, err)
		}
		stored, skipped, err := f.fetchFolder(ctx, c, acct, mb)
		if err != nil {
			return res, fmt.Errorf("sync account %s: %w", acct.ID, err)
		}
		res.Folders++
		res.Stored += stored
		res.Skipped += skipped
	}
	return res, nil
}

// ensureAccount creates the mirror account row the first time sync runs. An
// existing row is left alone: display name, icon, colour and photo belong to
// the settings UI (2g), not to the read path.
func (f *Fetcher) ensureAccount(ctx context.Context, acct Account) error {
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

// fetchFolder selects one mailbox, records it, and fetches the messages the
// mirror does not already hold, newest UID first. It returns how many messages
// it stored and how many it skipped because the mirror already had them.
func (f *Fetcher) fetchFolder(ctx context.Context, c *imapclient.Client, acct Account, mb *imap.ListData) (int, int, error) {
	// Read-only (EXAMINE): this path never writes to the server, and chunk 3's
	// writes will select read-write on their own.
	data, err := c.Select(mb.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return 0, 0, fmt.Errorf("select %s: %w", mb.Mailbox, err)
	}
	folder := store.Folder{
		ID:            folderRowID(acct.ID, mb.Mailbox),
		AccountID:     acct.ID,
		Name:          mb.Mailbox,
		Role:          RoleFor(mb.Mailbox, mb.Attrs),
		UIDValidity:   data.UIDValidity,
		HighestModSeq: data.HighestModSeq,
		LastSyncAt:    f.now(),
	}
	// Reuse the row id an earlier sync assigned and, when UIDVALIDITY still
	// matches, treat the stored UIDs as the checkpoint. A UIDVALIDITY change
	// invalidates UIDs, so the whole folder is re-read; chunk 3 owns the
	// disable-and-rebuild handling for the rows the new validity no longer
	// covers.
	have := map[uint32]bool{}
	existing, err := f.dbs.GetFolderByName(ctx, acct.ID, mb.Mailbox)
	switch {
	case err == nil:
		folder.ID = existing.ID
		if existing.UIDValidity == data.UIDValidity {
			uids, err := f.dbs.MessageUIDs(ctx, existing.ID)
			if err != nil {
				return 0, 0, err
			}
			for _, uid := range uids {
				have[uid] = true
			}
		}
	case !errors.Is(err, store.ErrNotFound):
		return 0, 0, fmt.Errorf("look up folder %s: %w", mb.Mailbox, err)
	}
	if err := f.dbs.UpsertFolder(ctx, folder); err != nil {
		return 0, 0, err
	}

	highest := uint32(data.UIDNext)
	if highest > 0 {
		highest--
	}
	stored := 0
	for _, r := range uidBatches(highest, f.batch) {
		if err := ctx.Err(); err != nil {
			return stored, len(have), err
		}
		set := imap.UIDSet{}
		missing := 0
		for u := r.lo; u <= r.hi; u++ {
			if !have[u] {
				set.AddNum(imap.UID(u))
				missing++
			}
		}
		if missing == 0 {
			continue
		}
		n, err := f.fetchBatch(ctx, c, acct, folder.ID, set)
		stored += n
		if err != nil {
			return stored, len(have), err
		}
	}
	return stored, len(have), nil
}

// fetchBatch mirrors one UID set. It first asks for every message's envelope,
// flags and size, which is small whatever the messages weigh, then fetches each
// body by size tier (see InlineMessageBytes). Each message is upserted as soon as
// it is complete, so a dropped connection keeps the progress it made rather than
// losing the whole batch.
func (f *Fetcher) fetchBatch(ctx context.Context, c *imapclient.Client, acct Account, folderID string, set imap.UIDSet) (int, error) {
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
			if err := f.dbs.UpsertMessage(ctx, f.envelopeOnly(acct, folderID, meta)); err != nil {
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

	n, err := f.fetchInline(ctx, c, acct, folderID, inline, byUID)
	stored += n
	if err != nil {
		return stored, err
	}
	for _, meta := range spooled {
		if err := ctx.Err(); err != nil {
			return stored, err
		}
		ok, err := f.fetchSpooled(ctx, c, acct, folderID, meta)
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
func (f *Fetcher) fetchInline(ctx context.Context, c *imapclient.Client, acct Account, folderID string, set imap.UIDSet, byUID map[imap.UID]*imapclient.FetchMessageBuffer) (int, error) {
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
		raw := buf.FindBodySection(section)
		m := f.baseMessage(acct, folderID, meta)
		m.RawBlob = raw
		m.ContentKey = store.ContentKey(m.MessageID, headerBlock(raw))
		parsed := mailmime.Parse(raw, acct.TrustedAuthservIDs...)
		applyParsed(&m, parsed)
		if err := f.dbs.UpsertMessage(ctx, m); err != nil {
			_ = cmd.Close()
			return stored, err
		}
		if err := f.storeRendered(ctx, m, parsed); err != nil {
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

// fetchSpooled streams one message's body to a file under the data directory
// and parses it from there, so the message and its attachments are never held
// whole in memory. It reports whether a row was stored; a message that expunged
// itself between the two commands, or that delivered more than the limit, is not
// an error for the run.
func (f *Fetcher) fetchSpooled(ctx context.Context, c *imapclient.Client, acct Account, folderID string, meta *imapclient.FetchMessageBuffer) (bool, error) {
	section := &imap.FetchItemBodySection{Peek: true}
	cmd := c.Fetch(imap.UIDSetNum(meta.UID), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}})

	rel := path.Join("spool", folderID, strconv.FormatUint(uint64(meta.UID), 10)+".eml")
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
		return true, f.dbs.UpsertMessage(ctx, f.envelopeOnly(acct, folderID, meta))
	case spoolErr != nil:
		return false, spoolErr
	case !got:
		return false, nil
	}

	m := f.baseMessage(acct, folderID, meta)
	m.RawPath = rel
	parsed, header, err := parseSpooled(filepath.Join(f.dbs.Dir, filepath.FromSlash(rel)), acct.TrustedAuthservIDs)
	if err != nil {
		return false, err
	}
	m.ContentKey = store.ContentKey(m.MessageID, headerBlock(header))
	applyParsed(&m, parsed)
	if parsed.BodySkipped {
		m.BodyStatus = store.BodyUnparsed
	}
	if err := f.dbs.UpsertMessage(ctx, m); err != nil {
		return true, err
	}
	return true, f.storeRendered(ctx, m, parsed)
}

// storeRendered sanitises a parsed body and writes the derived column through
// its own setter, which UpsertMessage deliberately never touches
// (ARCHITECTURE.md section 3). The default policy blocks remote images and
// strips tracking pixels; an operator allow-list is served by re-rendering from
// the raw message, so the cached copy never carries live remote content.
func (f *Fetcher) storeRendered(ctx context.Context, m store.Message, parsed mailmime.Parsed) error {
	if parsed.HTML == "" {
		return nil
	}
	res := render.Body(parsed, render.Options{MessageID: m.ID})
	if res.HTML == "" {
		return nil
	}
	return f.dbs.SetMessageBodyHTML(ctx, m.ID, res.HTML)
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
func (f *Fetcher) envelopeOnly(acct Account, folderID string, meta *imapclient.FetchMessageBuffer) store.Message {
	m := f.baseMessage(acct, folderID, meta)
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
func (f *Fetcher) baseMessage(acct Account, folderID string, buf *imapclient.FetchMessageBuffer) store.Message {
	uid := uint32(buf.UID)
	m := store.Message{
		ID:           messageRowID(folderID, uid),
		AccountID:    acct.ID,
		FolderID:     folderID,
		UID:          uid,
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
	m.HasAttachments = len(parsed.Attachments) > 0 || hasLargeAttachment(parsed.Large)
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

// hasLargeAttachment reports whether any part left on disk is a file, as
// opposed to a very large inline text body.
func hasLargeAttachment(large []mailmime.PartInfo) bool {
	for _, l := range large {
		if l.Attachment {
			return true
		}
	}
	return false
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
func dial(ctx context.Context, acct Account) (*imapclient.Client, error) {
	addr := net.JoinHostPort(acct.IMAPHost, strconv.Itoa(acct.IMAPPort))
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if acct.Insecure {
		return imapclient.New(conn, nil), nil
	}
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
	return imapclient.New(tlsConn, nil), nil
}

// folderRowID is the stable mirror id for a mailbox, derived from its account
// and name, so re-running sync reuses the same row.
func folderRowID(accountID, name string) string {
	sum := sha256.Sum256([]byte(accountID + "\x00" + name))
	return hex.EncodeToString(sum[:16])
}

// messageRowID is the stable id for a message in a folder. UpsertMessage keys
// on (folder, uid) and keeps the original id, so this is only the value used on
// first insert.
func messageRowID(folderID string, uid uint32) string {
	sum := sha256.Sum256([]byte(folderID + "\x00" + strconv.FormatUint(uint64(uid), 10)))
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
