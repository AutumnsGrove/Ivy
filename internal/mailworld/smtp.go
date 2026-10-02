package mailworld

import (
	"io"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// SentCopy models whether the provider files a copy of an outgoing message in
// the sender's Sent mailbox itself (auto) or leaves it to the client (client),
// which is what Purelymail does today (spike S1).
type SentCopy int

const (
	SentCopyClient SentCopy = iota // the client must APPEND its own copy
	SentCopyAuto                   // the server files the copy
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

// SMTPAuthFail makes every SMTP AUTH fail.
type SMTPAuthFail struct{}

func (SMTPAuthFail) isFault() {}

// SMTPAddr is the host:port the fake SMTP server listens on.
func (w *World) SMTPAddr() string { return w.smtpAddr }

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
	s.rcpts = append(s.rcpts, to)
	return nil
}

func (s *smtpSession) Data(r io.Reader) error {
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
