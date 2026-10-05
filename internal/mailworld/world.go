// Package mailworld is Ivy's fake outside world: a real in-process IMAP server,
// an SMTP server, fake LLM providers and a controllable clock, all driven by a
// scenario API. It is used by Go tests, Playwright and the ivy-dev CLI so all
// three exercise the same thing (STANDARDS.md section 3).
//
// It fakes only the network boundary; our own packages are never mocked.
package mailworld

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-smtp"
)

// World owns the fake servers, the accounts and the injected faults.
type World struct {
	imapAddr   string
	ln         net.Listener
	srv        *imapserver.Server
	smtpAddr   string
	smtpLn     net.Listener
	smtpSrv    *smtp.Server
	openRouter *httptest.Server
	ollama     *httptest.Server
	mem        *imapmemserver.Server
	clock      *Clock
	condStore  bool
	noKeywords bool

	mu        sync.Mutex
	faults    []Fault
	accounts  map[string]*Account
	sent      []SentMessage
	calls     []LLMCall
	chatQueue []string
	jevQueue  []map[string]JevAnswer
	embedDims int
}

// New starts the fake IMAP, SMTP and LLM provider servers on random loopback
// ports. Close releases them.
func New(opts ...Option) (*World, error) {
	w := &World{
		mem:       imapmemserver.New(),
		clock:     newClock(),
		condStore: true,
		accounts:  make(map[string]*Account),
	}
	for _, opt := range opts {
		opt(w)
	}

	w.srv = imapserver.New(&imapserver.Options{
		NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			sess := w.mem.NewSession()
			if w.noKeywords || w.hasFault(AuthFail{}) || w.hasFault(FailFetch{}) || w.hasFault(AckThenDrop{}) {
				sess = faultSession{
					Session:    sess,
					conn:       conn.NetConn(),
					authFail:   w.hasFault(AuthFail{}),
					fetchFail:  w.hasFault(FailFetch{}),
					ackDrop:    w.hasFault(AckThenDrop{}),
					noKeywords: w.noKeywords,
				}
			}
			return sess, nil, nil
		},
		Caps:         imapCapabilities(w.condStore),
		InsecureAuth: true,
		Logger:       log.New(io.Discard, "", 0),
	})

	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	w.ln = &faultListener{Listener: ln, w: w}
	w.imapAddr = ln.Addr().String()
	go func() { _ = w.srv.Serve(w.ln) }()

	w.smtpSrv = smtp.NewServer(&smtpBackend{w: w})
	w.smtpSrv.Domain = "mailworld"
	w.smtpSrv.AllowInsecureAuth = true
	w.smtpSrv.ErrorLog = log.New(io.Discard, "", 0)
	smtpLn, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		_ = w.srv.Close()
		return nil, err
	}
	w.smtpAddr = smtpLn.Addr().String()
	w.smtpLn = smtpLn
	go func() { _ = w.smtpSrv.Serve(smtpLn) }()

	w.embedDims = DefaultEmbeddingDims
	w.openRouter = httptest.NewServer(w.openRouterMux())
	w.ollama = httptest.NewServer(w.ollamaMux())
	return w, nil
}

// Option customises a World.
type Option func(*World)

// WithoutCondStore removes the CONDSTORE/QRESYNC capabilities and behaviour,
// so tests can exercise Ivy's fallback to UID/flags comparison.
func WithoutCondStore() Option {
	return func(w *World) { w.condStore = false }
}

// WithoutKeywords models a server that keeps only the system flags: SELECT
// leaves \* out of PERMANENTFLAGS and a STORE of a custom keyword is refused,
// so tests can exercise the local-only fallback for tags (ARCHITECTURE.md 3).
func WithoutKeywords() Option {
	return func(w *World) { w.noKeywords = true }
}

// IMAPAddr is the host:port the fake IMAP server listens on.
func (w *World) IMAPAddr() string { return w.imapAddr }

// Close stops every listener and connection.
func (w *World) Close() error {
	w.openRouter.Close()
	w.ollama.Close()
	_ = w.smtpSrv.Close()
	// Serve may not have registered the listener with the server yet, so close
	// it directly too: otherwise its Accept loop leaks (caught by goleak).
	_ = w.smtpLn.Close()
	return w.srv.Close()
}

// Clock is the injected clock every fake uses.
func (w *World) Clock() *Clock { return w.clock }

// Account returns the account for address, creating it (with an INBOX) if it
// does not exist yet. Password is used for IMAP and SMTP auth.
func (w *World) Account(address, password string) *Account {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := strings.ToLower(address)
	if a, ok := w.accounts[key]; ok {
		return a
	}
	user := imapmemserver.NewUser(address, password)
	w.mem.AddUser(user)
	a := &Account{world: w, user: user, address: address, password: password}
	_ = a.CreateMailbox("INBOX")
	w.accounts[key] = a
	return a
}

// Fault arms a fault for future connections.
func (w *World) Fault(f Fault) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.faults = append(w.faults, f)
}

// ClearFaults removes every armed fault.
func (w *World) ClearFaults() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.faults = nil
}

func (w *World) hasFault(f Fault) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, have := range w.faults {
		if have == f {
			return true
		}
	}
	return false
}

// takeDropFault removes and returns the first DropConnection fault, if any.
func (w *World) takeDropFault() (DropConnection, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, f := range w.faults {
		if drop, ok := f.(DropConnection); ok {
			w.faults = append(w.faults[:i], w.faults[i+1:]...)
			return drop, true
		}
	}
	return DropConnection{}, false
}

// takeSMTPReject removes and returns the first SMTPReject fault, if any, so a
// single armed rejection models a one-off transient failure.
func (w *World) takeSMTPReject() (SMTPReject, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, f := range w.faults {
		if rej, ok := f.(SMTPReject); ok {
			w.faults = append(w.faults[:i], w.faults[i+1:]...)
			return rej, true
		}
	}
	return SMTPReject{}, false
}

// accountByAddress returns the account for a local address, or nil when the
// address is outside the fake world.
func (w *World) accountByAddress(address string) *Account {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.accounts[strings.ToLower(address)]
}

// Fault is a scenario-armed failure the fake servers inject.
type Fault interface{ isFault() }

// DropConnection closes a connection after it has read After client commands.
type DropConnection struct{ After int }

// Unreachable closes every IMAP connection as it is accepted and keeps doing so
// until the faults are cleared, unlike DropConnection which is spent by one
// connection. It models a host that is down or off the network.
type Unreachable struct{}

// AuthFail makes every login fail.
type AuthFail struct{}

// FailFetch makes every IMAP FETCH fail, for DEV.md's fetch-failed state.
type FailFetch struct{}

// Latency delays every IMAP response, so a sync-in-progress screen has time to
// show progress (DEV.md section 4's backfilling).
type Latency struct{ Delay time.Duration }

// LLMDown makes the fake LLM providers return 503.
type LLMDown struct{}

// LLMCapReached makes the fake providers return 429, the answer a real
// provider gives when an account's spend cap is hit.
type LLMCapReached struct{}

// AckThenDrop sends a successful response to a STORE, EXPUNGE or MOVE and then
// closes the connection a moment later, modelling the ack that races a dropped
// link. A client that reads the ack proceeds; one that does not sees a drop.
// It lasts until the faults are cleared, which a test does before recovery.
type AckThenDrop struct{}

func (DropConnection) isFault() {}
func (Unreachable) isFault()    {}
func (AuthFail) isFault()       {}
func (FailFetch) isFault()      {}
func (Latency) isFault()        {}
func (LLMDown) isFault()        {}
func (LLMCapReached) isFault()  {}
func (AckThenDrop) isFault()    {}

// faultSession injects per-connection faults while preserving the optional
// interfaces the server requires for the capabilities we advertise (it panics
// if NAMESPACE or MOVE is advertised but absent).
type faultSession struct {
	imapserver.Session
	conn      net.Conn
	authFail  bool
	fetchFail bool
	ackDrop   bool
	// noKeywords refuses custom keywords (WithoutKeywords).
	noKeywords bool
}

func (s faultSession) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	data, err := s.Session.Select(mailbox, options)
	if err != nil || !s.noKeywords {
		return data, err
	}
	// Copy before editing: the in-memory server's slice is its own.
	kept := make([]imap.Flag, 0, len(data.PermanentFlags))
	for _, f := range data.PermanentFlags {
		if f != imap.FlagWildcard {
			kept = append(kept, f)
		}
	}
	out := *data
	out.PermanentFlags = kept
	return &out, nil
}

func (s faultSession) Login(username, password string) error {
	if s.authFail {
		return imapserver.ErrAuthFailed
	}
	return s.Session.Login(username, password)
}

func (s faultSession) Fetch(w *imapserver.FetchWriter, set imap.NumSet, options *imap.FetchOptions) error {
	if s.fetchFail {
		return errors.New("mailworld: FETCH failed (fault)")
	}
	return s.Session.Fetch(w, set, options)
}

func (s faultSession) Namespace() (*imap.NamespaceData, error) {
	return s.Session.(imapserver.SessionNamespace).Namespace()
}

func (s faultSession) Move(w *imapserver.MoveWriter, set imap.NumSet, dest string) error {
	err := s.Session.(imapserver.SessionMove).Move(w, set, dest)
	s.ackThenDrop()
	return err
}

func (s faultSession) Store(w *imapserver.FetchWriter, set imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	if s.noKeywords && flags != nil && hasKeyword(flags.Flags) {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeCannot,
			Text: "this mailbox does not keep custom keywords",
		}
	}
	err := s.Session.Store(w, set, flags, options)
	s.ackThenDrop()
	return err
}

func (s faultSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	err := s.Session.Expunge(w, uids)
	s.ackThenDrop()
	return err
}

// hasKeyword reports whether any flag is a custom keyword, which is any flag
// that is not a backslash system flag.
func hasKeyword(flags []imap.Flag) bool {
	for _, f := range flags {
		if !strings.HasPrefix(string(f), `\`) {
			return true
		}
	}
	return false
}

// ackThenDrop closes the connection shortly after the response is written, so
// the client usually reads the ack first and then sees the link die.
func (s faultSession) ackThenDrop() {
	if !s.ackDrop || s.conn == nil {
		return
	}
	go func() {
		time.Sleep(2 * time.Millisecond)
		_ = s.conn.Close()
	}()
}

// latency returns the delay an armed Latency fault asks for, if any. Unlike
// DropConnection it is not consumed: it lasts until the faults are cleared.
func (w *World) latency() (time.Duration, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, f := range w.faults {
		if lat, ok := f.(Latency); ok {
			return lat.Delay, true
		}
	}
	return 0, false
}

func imapCapabilities(condStore bool) imap.CapSet {
	caps := imap.CapSet{
		imap.CapIMAP4rev1:   {},
		imap.CapIdle:        {},
		imap.CapUIDPlus:     {},
		imap.CapUnselect:    {},
		imap.CapEnable:      {},
		imap.CapSASLIR:      {},
		imap.CapUTF8Accept:  {},
		imap.CapESearch:     {},
		imap.CapLiteralPlus: {},
		imap.CapNamespace:   {},
		imap.CapMove:        {},
		imap.CapChildren:    {},
	}
	if condStore {
		caps[imap.CapCondStore] = struct{}{}
		caps[imap.CapQResync] = struct{}{}
	}
	return caps
}
