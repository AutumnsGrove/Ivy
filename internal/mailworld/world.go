// Package mailworld is Ivy's fake outside world: a real in-process IMAP server,
// an SMTP server, fake LLM providers and a controllable clock, all driven by a
// scenario API. It is used by Go tests, Playwright and the ivy-dev CLI so all
// three exercise the same thing (STANDARDS.md section 3).
//
// It fakes only the network boundary; our own packages are never mocked.
package mailworld

import (
	"io"
	"log"
	"net"
	"sync"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// World owns the fake servers, the accounts and the injected faults.
type World struct {
	imapAddr string
	ln       net.Listener
	srv      *imapserver.Server
	mem      *imapmemserver.Server
	clock    *Clock

	mu       sync.Mutex
	faults   []Fault
	accounts map[string]*Account
}

// New starts the fake IMAP server on a random loopback port. Close releases it.
func New(opts ...Option) (*World, error) {
	w := &World{
		mem:      imapmemserver.New(),
		clock:    newClock(),
		accounts: make(map[string]*Account),
	}
	for _, opt := range opts {
		opt(w)
	}

	w.srv = imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			sess := w.mem.NewSession()
			if w.hasFault(AuthFail{}) {
				sess = authFailSession{Session: sess}
			}
			return sess, nil, nil
		},
		Caps:         imapCapabilities(),
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
	return w, nil
}

// Option customises a World.
type Option func(*World)

// IMAPAddr is the host:port the fake IMAP server listens on.
func (w *World) IMAPAddr() string { return w.imapAddr }

// Close stops every listener and connection.
func (w *World) Close() error { return w.srv.Close() }

// Clock is the injected clock every fake uses.
func (w *World) Clock() *Clock { return w.clock }

// Account returns the account for address, creating it (with an INBOX) if it
// does not exist yet. Password is used for IMAP and SMTP auth.
func (w *World) Account(address, password string) *Account {
	w.mu.Lock()
	defer w.mu.Unlock()
	if a, ok := w.accounts[address]; ok {
		return a
	}
	user := imapmemserver.NewUser(address, password)
	w.mem.AddUser(user)
	a := &Account{world: w, user: user, address: address, password: password}
	_ = a.CreateMailbox("INBOX")
	w.accounts[address] = a
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

// Fault is a scenario-armed failure the fake servers inject.
type Fault interface{ isFault() }

// DropConnection closes a connection after it has read After client commands.
type DropConnection struct{ After int }

// AuthFail makes every login fail.
type AuthFail struct{}

func (DropConnection) isFault() {}
func (AuthFail) isFault()       {}

// authFailSession forces Login to fail while leaving the memory session intact.
type authFailSession struct{ imapserver.Session }

func (authFailSession) Login(string, string) error { return imapserver.ErrAuthFailed }

func imapCapabilities() imap.CapSet {
	return imap.CapSet{
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
}
