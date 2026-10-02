// Package mailworld is Ivy's fake outside world: a real in-process IMAP server,
// an SMTP server, fake LLM providers and a controllable clock, all driven by a
// scenario API. It is used by Go tests, Playwright and the ivy-dev CLI so all
// three exercise the same thing (STANDARDS.md section 3).
//
// It fakes only the network boundary; our own packages are never mocked.
package mailworld

import (
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
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			sess := w.mem.NewSession()
			if w.hasFault(AuthFail{}) || w.hasFault(FailFetch{}) {
				sess = faultSession{
					Session:   sess,
					authFail:  w.hasFault(AuthFail{}),
					fetchFail: w.hasFault(FailFetch{}),
				}
			}
			return sess, nil, nil
		},
		Caps:         imapCapabilities(w.condStore),
		InsecureAuth: true,
		Logger:       log.New(io.Discard, "", 0),
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
	smtpLn, err := net.Listen("tcp", "127.0.0.1:0")
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

func (DropConnection) isFault() {}
func (AuthFail) isFault()       {}
func (FailFetch) isFault()      {}
func (Latency) isFault()        {}
func (LLMDown) isFault()        {}
func (LLMCapReached) isFault()  {}

// faultSession injects per-connection faults while preserving the optional
// interfaces the server requires for the capabilities we advertise (it panics
// if NAMESPACE or MOVE is advertised but absent).
type faultSession struct {
	imapserver.Session
	authFail  bool
	fetchFail bool
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
	return s.Session.(imapserver.SessionMove).Move(w, set, dest)
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
