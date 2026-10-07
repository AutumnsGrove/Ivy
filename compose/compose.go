// Package compose builds outgoing RFC 5322 messages. It is a pure builder: it
// validates every value that reaches a header and renders bytes, and it does no
// I/O. Sending those bytes is the smtp package's job, and holding a message
// across a retry is the send queue's (chunk 4b).
//
// Email headers are hostile input in both directions (STANDARDS.md section 10).
// A CR, LF or NUL in any operator- or sender-supplied value is rejected with a
// *ValidationError, never stripped, so a hostile subject or display name can
// never forge a header (CHUNK4-BRIEF invariants 8 and 9).
package compose

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/jhillyerd/enmime"
)

// Address is one mailbox with an optional display name. The name may be
// non-ASCII and is RFC 2047 encoded; the addr-spec must be ASCII, because
// Purelymail does not accept SMTPUTF8 (spike S1).
type Address struct {
	Name    string
	Address string
}

// Attachment is one file or inline part of an outgoing message. The bytes are
// bounded by MaxAttachmentBytes before the builder is called; the name, type
// and CID are header-bound and validated like every other header value, never
// concatenated into a header.
type Attachment struct {
	Filename string
	MIMEType string
	Content  []byte
	// Inline marks a part placed in the body, referenced by CID. A non-inline
	// part is a normal attachment.
	Inline bool
	// CID is the content id without angle brackets, required when Inline. The
	// builder wraps it as the Content-ID header.
	CID string
}

// Message is everything needed to build one outgoing message. The two instants
// and the id are injected, never generated here, so a retry and the Sent copy
// reuse them (CHUNK4-BRIEF section 3).
type Message struct {
	From    Address
	To      []Address
	Cc      []Address
	Bcc     []Address
	ReplyTo []Address
	Subject string

	// Text is the operator's message as typed, bar CRLF line breaks and a break
	// in any line over 900 bytes (wireText). It is always the text/plain part
	// (round 61). When Markdown is set, goldmark also renders
	// it to the text/html part of a multipart/alternative.
	Text     string
	Markdown bool

	// InReplyTo and References are transmission headers only; the reply logic
	// that computes them lives in reply.go. References preserves order.
	InReplyTo  string
	References []string

	// MessageID and Date are required and must be injected.
	MessageID string
	Date      time.Time

	// Attachments are file parts and inline images. Each is validated before
	// the message is built, and the total is bounded by
	// MaxTotalAttachmentsBytes.
	Attachments []Attachment

	// KeepBcc adds a Bcc header to the output. The copy handed to SMTP leaves it
	// false, so no Bcc recipient ever appears in a transmitted header; only the
	// copy Ivy APPENDs to Sent sets it (invariant 7).
	KeepBcc bool
}

// Envelope is what SMTP needs: the reverse-path and every recipient, Bcc
// included, in To, then Cc, then Bcc order, de-duplicated case-insensitively.
type Envelope struct {
	From string
	To   []string
}

// Compose limits (STANDARDS.md section 4a). A message over any of these is
// refused before anything is built, with a defined outcome rather than a
// silently truncated header.
const (
	// MaxRecipients bounds To + Cc + Bcc after de-duplication.
	MaxRecipients = 100
	// MaxSubjectBytes bounds the subject after any RFC 2047 encoding would
	// apply; a subject over it is refused.
	MaxSubjectBytes = 998
	// MaxBodyBytes bounds the operator's text held for the plain-text part.
	MaxBodyBytes = 1 << 20
	// MaxReferences bounds the References chain; deeper threading belongs in
	// the message's own Message-ID chain, not the header.
	MaxReferences = 20
	// MaxMessageIDBytes bounds one message-id, angle brackets included.
	MaxMessageIDBytes = 320
	// MaxAttachments bounds the number of file and inline parts on one message.
	MaxAttachments = 20
	// MaxAttachmentBytes bounds one attachment's raw bytes before encoding. The
	// base64 inflation keeps the built message well under a provider's 48 MiB.
	MaxAttachmentBytes = 25 << 20
	// MaxTotalAttachmentsBytes bounds every attachment together, so one message
	// cannot grow past what the provider will accept (round 65).
	MaxTotalAttachmentsBytes = 25 << 20
	// MaxFilenameBytes bounds one attachment's file name.
	MaxFilenameBytes = 255
	// MaxMIMEBytes bounds one attachment's content type.
	MaxMIMEBytes = 255
)

// ValidationError is a refused field. Field is a stable lowercase name ("from",
// "subject", "references", "recipients", "body", ...) that the send API maps to
// copy; Reason is for the logs. A CR, LF or NUL is a rejection, never a strip.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Reason }

// Build validates m and renders it to RFC 5322 bytes for transmission, plus the
// SMTP envelope. It returns a *ValidationError for the first bad field and
// never writes a partial message.
func Build(m Message) ([]byte, Envelope, error) {
	if err := validate(m); err != nil {
		return nil, Envelope{}, err
	}
	env := Envelope{From: m.From.Address, To: recipientList(m)}

	b := enmime.Builder().
		From(m.From.Name, m.From.Address).
		ToAddrs(mailAddresses(m.To)).
		CCAddrs(mailAddresses(m.Cc)).
		BCCAddrs(mailAddresses(m.Bcc)).
		ReplyToAddrs(mailAddresses(m.ReplyTo)).
		Subject(encodeWord(m.Subject)).
		Date(m.Date).
		Header("Message-ID", m.MessageID).
		Text([]byte(wireText(m.Text, true)))
	if m.InReplyTo != "" {
		b = b.Header("In-Reply-To", m.InReplyTo)
	}
	if len(m.References) > 0 {
		b = b.Header("References", strings.Join(m.References, " "))
	}
	if m.Markdown && m.Text != "" {
		html, err := renderMarkdown(m.Text)
		if err != nil {
			return nil, Envelope{}, fmt.Errorf("compose: render markdown: %w", err)
		}
		b = b.HTML([]byte(wireText(string(html), false)))
	}
	// Invariant 7: the wire copy never carries Bcc, so it is added to the
	// builder's header block only for the Sent copy. BCCAddrs above is what makes
	// the builder accept a Bcc-only message.
	if m.KeepBcc && len(m.Bcc) > 0 {
		b = b.Header("Bcc", joinAddresses(m.Bcc))
	}
	// Attachment and inline parts are added after the body so the tree reads
	// text/html, then inlines (multipart/related), then files (multipart/mixed).
	for _, a := range m.Attachments {
		if a.Inline {
			b = b.AddInline(a.Content, a.MIMEType, a.Filename, a.CID)
		} else {
			b = b.AddAttachment(a.Content, a.MIMEType, a.Filename)
		}
	}

	part, err := b.Build()
	if err != nil {
		return nil, Envelope{}, fmt.Errorf("compose: build message: %w", err)
	}
	// The builder quotes an encoded display name, which RFC 2047 forbids, so the
	// address headers are rewritten with compose's own formatting.
	part.Header.Set("From", formatAddress(m.From))
	for name, list := range map[string][]Address{"To": m.To, "Cc": m.Cc, "Reply-To": m.ReplyTo} {
		if len(list) > 0 {
			part.Header.Set(name, joinAddresses(list))
		}
	}
	var buf bytes.Buffer
	if err := part.Encode(&buf); err != nil {
		return nil, Envelope{}, fmt.Errorf("compose: encode message: %w", err)
	}
	return buf.Bytes(), env, nil
}

// Validate checks m's addresses and header-bound values without rendering it,
// so a caller can pre-check before it stores or builds anything. It returns the
// first problem as a *ValidationError.
func Validate(m Message) error {
	return validate(m)
}
