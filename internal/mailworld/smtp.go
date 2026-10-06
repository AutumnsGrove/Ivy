package mailworld

import (
	"io"
	"net"
	"sync"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// DefaultSMTPSize is the message size the fake SMTP server advertises in its
// EHLO SIZE line and enforces on DATA. It sits near Purelymail's live limit
// (about 48.8 MiB, spike S1) so tests inherit a real-sized server rather than
// an unlimited one; a test that wants the too-large path passes a small value
// with WithSMTPSize.
const DefaultSMTPSize = 48 << 20

// WithSMTPSize sets the SIZE the fake SMTP server advertises and enforces.
func WithSMTPSize(bytes int64) Option {
	return func(w *World) {
		if bytes > 0 {
			w.smtpMaxBytes = bytes
		}
	}
}

// SentCopy models whether the provider files a copy of an outgoing message in
// the sender's Sent mailbox itself (auto) or leaves it to the client (client),
// which is what Purelymail does today (spike S1).
type SentCopy int

const (
	// SentCopyClient means the client must APPEND its own copy.
	SentCopyClient SentCopy = iota
	// SentCopyAuto means the server files the copy.
	SentCopyAuto
)

// SentMessage is one message the fake SMTP server accepted, for tests and the
// ivy-dev CLI to assert on.
type SentMessage struct {
	From string
	To   []string
	Raw  []byte
}

// SMTPReject makes the next SMTP transaction fail with an SMTP error of the
// given code: 4xx is transient, 5xx is a permanent rejection (552 too large).
type SMTPReject struct {
	Code    int
	Message string
}

func (SMTPReject) isFault() {}

// SMTPRejectRcpt refuses one named recipient at RCPT with the given code, so a
// test can model one bad address among several. It is spent once, when that
// address is tried.
type SMTPRejectRcpt struct {
	Address string
	Code    int
	Message string
}

func (SMTPRejectRcpt) isFault() {}

// SMTPAuthFail makes every SMTP AUTH fail.
type SMTPAuthFail struct{}

func (SMTPAuthFail) isFault() {}

// SMTPAddr is the host:port the fake SMTP server listens on.
func (w *World) SMTPAddr() string { return w.smtpAddr }

// SMTPMaxBytes is the SIZE the fake SMTP server advertises and enforces.
func (w *World) SMTPMaxBytes() int64 { return w.smtpMaxBytes }

// SMTPStallPhase is where an SMTPStall fault parks the server's side of the
// conversation.
type SMTPStallPhase int

const (
	// SMTPStallGreeting accepts the connection and then never sends the
	// greeting, so every command deadline fires.
	SMTPStallGreeting SMTPStallPhase = iota
	// SMTPStallData answers DATA with 354 and then never reads the body or sends
	// the final reply, so the submission deadline fires.
	SMTPStallData
)

// SMTPStall models a peer that accepts the TCP connection and then stops
// responding. It lasts until the faults are cleared, like Unreachable; the
// parked server goroutine is released when the world closes.
type SMTPStall struct{ Phase SMTPStallPhase }

func (SMTPStall) isFault() {}

// smtpFaultListener wraps the SMTP listener so an armed greeting stall parks a
// connection before the server writes anything.
type smtpFaultListener struct {
	net.Listener
	w *World
}

func (l *smtpFaultListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if l.w.hasFault(SMTPStall{Phase: SMTPStallGreeting}) {
		return &stallConn{Conn: c, released: make(chan struct{})}, nil
	}
	return c, nil
}

// stallConn blocks every write until it is closed, so the peer sees a
// connection that was accepted and then went silent. Close releases the blocked
// writer, which is what go-smtp's Server.Close does for every live connection.
type stallConn struct {
	net.Conn

	once     sync.Once
	released chan struct{}
}

func (c *stallConn) Write([]byte) (int, error) {
	<-c.released
	return 0, net.ErrClosed
}

func (c *stallConn) Close() error {
	c.once.Do(func() { close(c.released) })
	return c.Conn.Close()
}

// Sent returns every message the SMTP server accepted, newest last. It is a
// copy, so callers cannot mutate the world's record.
func (w *World) Sent() []SentMessage {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]SentMessage, len(w.sent))
	copy(out, w.sent)
	return out
}

func (w *World) recordSent(m SentMessage) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sent = append(w.sent, m)
}

// SetSentCopy chooses how this account's provider handles the Sent copy.
func (a *Account) SetSentCopy(mode SentCopy) { a.sentCopy = mode }

// smtpBackend is the go-smtp backend; each connection gets a session.
type smtpBackend struct{ w *World }

func (b *smtpBackend) NewSession(*smtp.Conn) (smtp.Session, error) {
	return &smtpSession{w: b.w}, nil
}

// smtpSession is one SMTP connection. Local recipients are delivered into
// their account's INBOX through the memory store, which fires IDLE/EXISTS the
// same way a real provider does.
type smtpSession struct {
	w       *World
	account *Account
	from    string
	rcpts   []string
}

func (s *smtpSession) Reset() {
	s.from = ""
	s.rcpts = nil
}

func (s *smtpSession) Logout() error { return nil }

func (s *smtpSession) AuthMechanisms() []string { return []string{sasl.Plain} }

func (s *smtpSession) Auth(mech string) (sasl.Server, error) {
	if mech != sasl.Plain {
		return nil, smtp.ErrAuthUnknownMechanism
	}
	return sasl.NewPlainServer(func(_, user, pass string) error {
		if s.w.hasFault(SMTPAuthFail{}) {
			return smtp.ErrAuthFailed
		}
		a := s.w.accountByAddress(user)
		if a == nil || a.password != pass {
			return smtp.ErrAuthFailed
		}
		s.account = a
		return nil
	}), nil
}

func (s *smtpSession) Mail(from string, _ *smtp.MailOptions) error {
	if s.account == nil {
		return smtp.ErrAuthRequired
	}
	if rej, ok := s.w.takeSMTPReject(); ok {
		return &smtp.SMTPError{Code: rej.Code, Message: rej.Message}
	}
	s.from = from
	return nil
}

func (s *smtpSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	if rej, ok := s.w.takeSMTPRejectRcpt(to); ok {
		return &smtp.SMTPError{Code: rej.Code, Message: rej.Message}
	}
	s.rcpts = append(s.rcpts, to)
	return nil
}

func (s *smtpSession) Data(r io.Reader) error {
	// A data stall parks the server after 354: the client's body write completes
	// into the socket buffer, its submission deadline fires, and no message is
	// recorded. The goroutine ends when the world closes.
	if s.w.hasFault(SMTPStall{Phase: SMTPStallData}) {
		<-s.w.done
		return &smtp.SMTPError{Code: 421, Message: "mailworld closed"}
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.w.recordSent(SentMessage{From: s.from, To: append([]string(nil), s.rcpts...), Raw: raw})

	for _, to := range s.rcpts {
		if dest := s.w.accountByAddress(to); dest != nil {
			if _, err := dest.Append("INBOX", raw); err != nil {
				return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "local delivery failed: " + err.Error()}
			}
		}
	}
	if s.account.sentCopy == SentCopyAuto {
		_ = s.account.CreateMailbox("Sent")
		if _, err := s.account.Append("Sent", raw); err != nil {
			return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "sent copy failed: " + err.Error()}
		}
	}
	s.Reset()
	return nil
}
