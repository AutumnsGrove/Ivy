package mailworld

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"time"
)

// MessageBuilder builds raw RFC 5322 messages for tests and seeding. Identical
// builder calls produce byte-identical output, so seeded data is deterministic.
type MessageBuilder struct {
	from      string
	to        string
	subject   string
	text      string
	html      string
	messageID string
	date      time.Time
	headers   [][2]string
}

// Msg starts a new message builder.
func Msg() *MessageBuilder { return &MessageBuilder{} }

// From sets the From header.
func (b *MessageBuilder) From(s string) *MessageBuilder { b.from = s; return b }

// To sets the To header.
func (b *MessageBuilder) To(s string) *MessageBuilder { b.to = s; return b }

// Subject sets the Subject header.
func (b *MessageBuilder) Subject(s string) *MessageBuilder { b.subject = s; return b }

// Text sets a text/plain body. With HTML set, the two become multipart/alternative.
func (b *MessageBuilder) Text(s string) *MessageBuilder { b.text = s; return b }

// HTML sets a text/html body.
func (b *MessageBuilder) HTML(s string) *MessageBuilder { b.html = s; return b }

// MessageID overrides the generated Message-ID.
func (b *MessageBuilder) MessageID(s string) *MessageBuilder { b.messageID = s; return b }

// Date overrides the default Date header.
func (b *MessageBuilder) Date(t time.Time) *MessageBuilder { b.date = t; return b }

// Header adds an arbitrary header.
func (b *MessageBuilder) Header(key, value string) *MessageBuilder {
	b.headers = append(b.headers, [2]string{key, value})
	return b
}

// Build renders the raw message bytes.
func (b *MessageBuilder) Build() []byte {
	sum := sha256.Sum256([]byte(b.from + "\x00" + b.to + "\x00" + b.subject + "\x00" + b.text + "\x00" + b.html))

	id := b.messageID
	if id == "" {
		id = fmt.Sprintf("<%x@mailworld>", sum[:12])
	}
	date := b.date
	if date.IsZero() {
		date = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	}

	var buf bytes.Buffer
	writeHeader(&buf, "From", b.from)
	if b.to != "" {
		writeHeader(&buf, "To", b.to)
	}
	writeHeader(&buf, "Subject", b.subject)
	writeHeader(&buf, "Date", date.Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", id)
	writeHeader(&buf, "MIME-Version", "1.0")
	for _, h := range b.headers {
		writeHeader(&buf, h[0], h[1])
	}

	switch {
	case b.html != "" && b.text != "":
		boundary := fmt.Sprintf("=_%x", sum[:8])
		writeHeader(&buf, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
		buf.WriteString("\r\n")
		fmt.Fprintf(&buf, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, b.text)
		fmt.Fprintf(&buf, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n", boundary, b.html)
		fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	case b.html != "":
		writeHeader(&buf, "Content-Type", "text/html; charset=utf-8")
		buf.WriteString("\r\n")
		buf.WriteString(b.html)
	default:
		writeHeader(&buf, "Content-Type", "text/plain; charset=utf-8")
		buf.WriteString("\r\n")
		buf.WriteString(b.text)
	}
	return buf.Bytes()
}

func writeHeader(buf *bytes.Buffer, key, value string) {
	fmt.Fprintf(buf, "%s: %s\r\n", key, value)
}
