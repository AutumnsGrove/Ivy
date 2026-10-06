// Package smtp is Ivy's thin wrapper over go-smtp for outgoing mail. It owns
// dialing, implicit TLS, AUTH PLAIN, the EHLO SIZE read, RCPT and DATA, and
// every deadline; it does no queueing and never retries. The send queue (chunk
// 4b) calls Submit and decides what a failure means.
//
// SMTP cannot be asked what happened after the last DATA byte, so the error
// taxonomy is the whole contract: a *SendError says whether a retry can help
// and, for a refused recipient, which address (CHUNK4-BRIEF invariants 1-2).
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
)

// Account is the connection descriptor for one mailbox's SMTP submission. The
// password is never persisted or logged by this package.
type Account struct {
	Host     string
	Port     int
	Username string
	Password string
	// Insecure dials without TLS. It exists only for the loopback mail world;
	// the zero value is implicit TLS on a dedicated port, so a forgotten field
	// can never put a password on the wire unencrypted (STANDARDS.md 4a.7).
	Insecure bool
}

// Envelope is what SMTP transmits: the reverse-path and every recipient,
// including Bcc, which appears in no header.
type Envelope struct {
	From string
	To   []string
}

// Kind is a stable, machine-readable reason a submission failed. The 4b send
// queue branches on SendError.Transient; the UI maps Kind to copy.
type Kind string

const (
	// KindUnreachable is a dial, TLS or connection failure before any command
	// was acknowledged. Transient.
	KindUnreachable Kind = "unreachable"
	// KindTimeout is a command or DATA that exceeded its deadline. Transient.
	KindTimeout Kind = "timeout"
	// KindCanceled is the caller's context ending. The caller decides whether to
	// retry; it is not a server verdict.
	KindCanceled Kind = "canceled"
	// KindAuthFailed is AUTH rejected. Permanent until the password changes.
	KindAuthFailed Kind = "auth_failed"
	// KindRecipientRefused is one RCPT refused with a 5xx. Permanent; Recipient
	// names it. A 4xx at RCPT is KindTransient with Recipient set.
	KindRecipientRefused Kind = "recipient_refused"
	// KindTooLarge is the message over the provider's advertised SIZE. Permanent.
	KindTooLarge Kind = "too_large"
	// KindRejected is any other permanent 5xx. Permanent.
	KindRejected Kind = "rejected"
	// KindTransient is a 4xx reply. Transient.
	KindTransient Kind = "transient"
	// KindLocal is a local failure before DATA (the queue could not commit its
	// may-have-been-sent point). Nothing was sent. Transient.
	KindLocal Kind = "local"
)

// SendError is a failed submission. Transient reports whether an automatic
// retry may help; a retry is only ever allowed from a state that proves nothing
// was accepted (a 4xx or a failure before DATA ended). Ambiguous means the
// message may have been accepted and must not be retried automatically: the
// send queue turns it into `unconfirmed` (round 60). Code is the SMTP reply
// code when there was one, otherwise zero.
type SendError struct {
	Kind      Kind
	Transient bool
	Ambiguous bool
	Recipient string
	Code      int
	Err       error
}

func (e *SendError) Error() string {
	switch {
	case e.Recipient != "":
		return fmt.Sprintf("smtp: %s (%s): %v", e.Kind, e.Recipient, e.Err)
	case e.Code != 0:
		return fmt.Sprintf("smtp: %s (%d): %v", e.Kind, e.Code, e.Err)
	default:
		return fmt.Sprintf("smtp: %s: %v", e.Kind, e.Err)
	}
}

func (e *SendError) Unwrap() error { return e.Err }

// Submission deadlines (STANDARDS.md 4a). Each is a documented maximum with a
// defined outcome: a dial or handshake past its bound is unreachable, a command
// past its bound is a timeout, and DATA past its bound is a timeout. A stalled
// peer therefore cannot hang a caller.
const (
	DefaultDialTimeout    = 15 * time.Second
	DefaultCommandTimeout = 30 * time.Second
	DefaultDataTimeout    = 2 * time.Minute
)

// Submitter sends one message per call. It holds no connection between calls,
// so a retry opens a fresh one and a failure can never leak into the next send.
type Submitter struct {
	dial    time.Duration
	command time.Duration
	data    time.Duration
}

// Option customises a Submitter.
type Option func(*Submitter)

// WithTimeout overrides the dial/handshake, per-command and DATA deadlines.
// Tests pass short values so a stalled peer is proved quickly.
func WithTimeout(dial, command, data time.Duration) Option {
	return func(s *Submitter) {
		if dial > 0 {
			s.dial = dial
		}
		if command > 0 {
			s.command = command
		}
		if data > 0 {
			s.data = data
		}
	}
}

// New builds a Submitter with the production deadlines.
func New(opts ...Option) *Submitter {
	s := &Submitter{dial: DefaultDialTimeout, command: DefaultCommandTimeout, data: DefaultDataTimeout}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Submit authenticates, checks the message against the provider's advertised
// SIZE, sends it, and returns nil once the server has accepted the whole
// transaction. size is the body's length in bytes, or negative when unknown.
// On failure it returns a *SendError. Submission to one recipient is the unit:
// a RCPT refusal aborts the whole message, so that send never goes out
// partially (round 61).
func (s *Submitter) Submit(ctx context.Context, acct Account, env Envelope, body io.Reader, size int64, options ...SubmitOption) error {
	cfg := submitConfig{}
	for _, opt := range options {
		opt(&cfg)
	}
	conn, err := s.dialConn(ctx, acct)
	if err != nil {
		return &SendError{Kind: KindUnreachable, Transient: true, Err: err}
	}
	// go-smtp takes no context, so closing the connection is what unblocks a
	// command parked on a stalled peer. A caller that gives up is never held.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()

	c := gosmtp.NewClient(conn)
	c.CommandTimeout = s.command
	c.SubmissionTimeout = s.data

	if err := c.Hello("localhost"); err != nil {
		return classify(ctx, err)
	}
	if err := c.Auth(sasl.NewPlainClient("", acct.Username, acct.Password)); err != nil {
		// The auth step is where the password is judged: a 535 (or any non-4xx
		// refusal here) is permanent until the password changes. A timeout or a
		// 4xx stays a transport verdict.
		if ctx.Err() != nil {
			return classify(ctx, err)
		}
		var netErr net.Error
		if (errors.As(err, &netErr) && netErr.Timeout()) || errors.Is(err, os.ErrDeadlineExceeded) {
			return classify(ctx, err)
		}
		var smtpErr *gosmtp.SMTPError
		if errors.As(err, &smtpErr) && smtpErr.Code >= 400 && smtpErr.Code < 500 {
			return classify(ctx, err)
		}
		return &SendError{Kind: KindAuthFailed, Err: err}
	}
	if maxSize, ok := c.MaxMessageSize(); ok && size >= 0 && int64(size) > int64(maxSize) {
		return &SendError{
			Kind: KindTooLarge, Code: 552,
			Err: fmt.Errorf("message is %d bytes, the provider accepts %d", size, maxSize),
		}
	}
	mailOpts := &gosmtp.MailOptions{}
	if size >= 0 {
		mailOpts.Size = size
	}
	if err := c.Mail(env.From, mailOpts); err != nil {
		return classify(ctx, err)
	}
	for _, rcpt := range env.To {
		if err := c.Rcpt(rcpt, nil); err != nil {
			// One refused recipient aborts the whole transaction: RSET, then
			// report the address. Nobody gets a partial send.
			_ = c.Reset()
			if ctx.Err() != nil {
				return classify(ctx, err)
			}
			var smtpErr *gosmtp.SMTPError
			if errors.As(err, &smtpErr) && smtpErr.Code >= 500 {
				return &SendError{Kind: KindRecipientRefused, Recipient: rcpt, Code: smtpErr.Code, Err: err}
			}
			// A 4xx, a timeout or a dropped connection is not a verdict on the
			// address: nothing was accepted, so it is the ordinary transient failure.
			se := classify(ctx, err)
			se.Recipient = rcpt
			return se
		}
	}
	// The durable point before DATA. Everything before it is safe to retry.
	if cfg.beforeData != nil {
		if err := cfg.beforeData(); err != nil {
			_ = c.Reset()
			return &SendError{Kind: KindLocal, Transient: true, Err: err}
		}
	}

	w, err := c.Data()
	if err != nil {
		return classify(ctx, err)
	}
	// Bound the whole DATA phase on the connection: the body write and the final
	// reply share the sender's data deadline.
	_ = conn.SetDeadline(time.Now().Add(s.data))
	if _, err := io.Copy(w, body); err != nil {
		_ = conn.SetDeadline(time.Time{})
		return classify(ctx, err)
	}
	if err := w.Close(); err != nil {
		_ = conn.SetDeadline(time.Time{})
		// The terminating dot may already be on the wire, so a network failure here
		// is ambiguous; an explicit server reply is not.
		return classifyAmbiguous(ctx, err)
	}
	_ = conn.SetDeadline(time.Time{})
	_ = c.Quit() // the message is accepted; a failed goodbye changes nothing
	return nil
}

// dialConn opens the connection: implicit TLS for a real provider, plaintext
// only for the loopback fake (Insecure), with the dial and the handshake both
// bounded by the dial deadline.
func (s *Submitter) dialConn(ctx context.Context, acct Account) (net.Conn, error) {
	d := &net.Dialer{Timeout: s.dial}
	addr := net.JoinHostPort(acct.Host, strconv.Itoa(acct.Port))
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if acct.Insecure {
		return raw, nil
	}
	tlsConn := tls.Client(raw, &tls.Config{ServerName: acct.Host, MinVersion: tls.VersionTLS12})
	hsCtx, cancel := context.WithTimeout(ctx, s.dial)
	defer cancel()
	if err := tlsConn.HandshakeContext(hsCtx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return tlsConn, nil
}

// SubmitOption customises one submission.
type SubmitOption func(*submitConfig)

type submitConfig struct {
	beforeData func() error
}

// WithBeforeData runs fn after the last RCPT and immediately before DATA. The
// send queue commits its "may have been sent" point there, so a crash before
// this point is safely retried and a crash after it is unconfirmed. A non-nil
// error from fn aborts before DATA and nothing is ambiguous.
func WithBeforeData(fn func() error) SubmitOption {
	return func(c *submitConfig) { c.beforeData = fn }
}

// classifyAmbiguous maps a failure at the end of DATA. An explicit server reply
// is definitive; a network, timeout or cancellation failure means the dot may
// already have been written, so the outcome is unknown.
func classifyAmbiguous(ctx context.Context, err error) *SendError {
	se := classify(ctx, err)
	var smtpErr *gosmtp.SMTPError
	if errors.As(err, &smtpErr) {
		return se
	}
	se.Ambiguous = true
	return se
}

// classify maps a go-smtp or network error to the queue's stable verdict. A
// cancelled context wins: no server verdict stands when the caller left. A
// timeout is transient; a 4xx is transient; a 5xx (552 included) is permanent.
func classify(ctx context.Context, err error) *SendError {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return &SendError{Kind: KindCanceled, Err: ctx.Err()}
	}
	var smtpErr *gosmtp.SMTPError
	if errors.As(err, &smtpErr) {
		switch {
		case smtpErr.Code == 552:
			return &SendError{Kind: KindTooLarge, Code: 552, Err: err}
		case smtpErr.Code >= 500:
			return &SendError{Kind: KindRejected, Code: smtpErr.Code, Err: err}
		case smtpErr.Code >= 400:
			return &SendError{Kind: KindTransient, Transient: true, Code: smtpErr.Code, Err: err}
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &SendError{Kind: KindTimeout, Transient: true, Err: err}
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return &SendError{Kind: KindTimeout, Transient: true, Err: err}
	}
	return &SendError{Kind: KindUnreachable, Transient: true, Err: err}
}
