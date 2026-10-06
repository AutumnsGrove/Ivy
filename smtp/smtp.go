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
	"errors"
	"fmt"
	"io"
	"time"
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
	// KindRecipientRefused is one RCPT refused. Permanent; Recipient names it.
	KindRecipientRefused Kind = "recipient_refused"
	// KindTooLarge is the message over the provider's advertised SIZE. Permanent.
	KindTooLarge Kind = "too_large"
	// KindRejected is any other permanent 5xx. Permanent.
	KindRejected Kind = "rejected"
	// KindTransient is a 4xx reply. Transient.
	KindTransient Kind = "transient"
)

// SendError is a failed submission. Transient reports whether an automatic
// retry may help; a retry is only ever allowed from a state that proves nothing
// was accepted (a 4xx or a failure before DATA ended). Code is the SMTP reply
// code when there was one, otherwise zero.
type SendError struct {
	Kind      Kind
	Transient bool
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

// errNotImplemented marks the 4a transport as unbuilt. It is removed when the
// gate G1 tests pass; it exists so those tests compile and fail on behaviour
// rather than on a compile error (STANDARDS.md section 1).
var errNotImplemented = errors.New("smtp: not implemented")

// Submit authenticates, checks the message against the provider's advertised
// SIZE, sends it, and returns nil once the server has accepted the whole
// transaction. size is the body's length in bytes, or negative when unknown.
// On failure it returns a *SendError. Submission to one recipient is the unit:
// a RCPT refusal aborts the whole message, so that send never goes out
// partially (round 61).
func (s *Submitter) Submit(ctx context.Context, acct Account, env Envelope, body io.Reader, size int64) error {
	return errNotImplemented
}
