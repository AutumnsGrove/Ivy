package mailworld

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// MessageBuilder builds raw RFC 5322 messages for tests and seeding. Identical
// builder calls produce byte-identical output, so seeded data is deterministic.
type MessageBuilder struct {
	from        string
	to          string
	subject     string
	text        string
	html        string
	messageID   string
	date        time.Time
	headers     [][2]string
	attachments []part
}

// part is one MIME leaf attached to a message.
type part struct {
	filename    string
	contentType string
	data        []byte
	inline      bool
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

// Attach adds a regular attachment (Content-Disposition: attachment).
func (b *MessageBuilder) Attach(filename, contentType string, data []byte) *MessageBuilder {
	b.attachments = append(b.attachments, part{filename: filename, contentType: contentType, data: data})
	return b
}

// Inline adds an inline attachment referenced from HTML as cid:filename.
func (b *MessageBuilder) Inline(filename, contentType string, data []byte) *MessageBuilder {
	b.attachments = append(b.attachments, part{filename: filename, contentType: contentType, data: data, inline: true})
	return b
}

// Build renders the raw message bytes.
func (b *MessageBuilder) Build() []byte {
	sum := b.checksum()

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

	// Build the body entity, then wrap it in multipart/related when inline
	// parts exist and multipart/mixed when regular attachments do. This mirrors
	// the MIME shapes real senders produce, which is what the parser must eat.
	bodyCT, body := b.buildBody(sum)

	var inlines, attachments []part
	for _, p := range b.attachments {
		if p.inline {
			inlines = append(inlines, p)
		} else {
			attachments = append(attachments, p)
		}
	}

	if len(inlines) > 0 {
		relatedBoundary := fmt.Sprintf("=_rel_%x", sum[8:14])
		bodyCT, body = wrapEntity(bodyCT, body, relatedBoundary,
			fmt.Sprintf(`multipart/related; boundary="%s"; type="%s"`, relatedBoundary, contentTypeOf(bodyCT)),
			relatedParts(relatedBoundary, inlines))
	}

	if len(attachments) > 0 {
		mixedBoundary := fmt.Sprintf("=_mix_%x", sum[14:20])
		bodyCT, body = wrapEntity(bodyCT, body, mixedBoundary,
			fmt.Sprintf(`multipart/mixed; boundary="%s"`, mixedBoundary),
			attachmentParts(mixedBoundary, attachments))
	}

	buf.WriteString("Content-Type: " + bodyCT + "\r\n\r\n")
	buf.Write(body)

	return buf.Bytes()
}

// buildBody renders the text/html entity (single part or multipart/alternative).
func (b *MessageBuilder) buildBody(sum [32]byte) (string, []byte) {
	var buf bytes.Buffer
	switch {
	case b.html != "" && b.text != "":
		boundary := fmt.Sprintf("=_alt_%x", sum[:8])
		buf.WriteString("\r\n")
		fmt.Fprintf(&buf, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, b.text)
		fmt.Fprintf(&buf, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n", boundary, b.html)
		fmt.Fprintf(&buf, "--%s--\r\n", boundary)
		return `multipart/alternative; boundary="` + boundary + `"`, buf.Bytes()
	case b.html != "":
		buf.WriteString(b.html)
		return "text/html; charset=utf-8", buf.Bytes()
	default:
		buf.WriteString(b.text)
		return "text/plain; charset=utf-8", buf.Bytes()
	}
}

// wrapEntity wraps an already-rendered body entity in a multipart container
// whose first part is the body and whose remaining parts are extra.
func wrapEntity(innerCT string, inner []byte, boundary, outerCT string, extra []byte) (string, []byte) {
	var buf bytes.Buffer
	buf.WriteString("\r\n")
	fmt.Fprintf(&buf, "--%s\r\nContent-Type: %s\r\n\r\n", boundary, innerCT)
	buf.Write(inner)
	if len(inner) == 0 || inner[len(inner)-1] != '\n' {
		buf.WriteString("\r\n")
	}
	buf.Write(extra)
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return outerCT, buf.Bytes()
}

func relatedParts(boundary string, inlines []part) []byte {
	var buf bytes.Buffer
	for _, p := range inlines {
		writePart(&buf, boundary, p, "inline; filename=\""+p.filename+"\"", "<"+p.filename+">")
	}
	return buf.Bytes()
}

func attachmentParts(boundary string, attachments []part) []byte {
	var buf bytes.Buffer
	for _, p := range attachments {
		writePart(&buf, boundary, p, "attachment; filename=\""+p.filename+"\"", "")
	}
	return buf.Bytes()
}

func writePart(buf *bytes.Buffer, boundary string, p part, disposition, contentID string) {
	fmt.Fprintf(buf, "--%s\r\n", boundary)
	ct := p.contentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	if !strings.Contains(ct, "name=") {
		ct += `; name="` + p.filename + `"`
	}
	fmt.Fprintf(buf, "Content-Type: %s\r\n", ct)
	writeHeader(buf, "Content-Transfer-Encoding", "base64")
	writeHeader(buf, "Content-Disposition", disposition)
	if contentID != "" {
		writeHeader(buf, "Content-ID", contentID)
	}
	buf.WriteString("\r\n")
	writeBase64(buf, p.data)
}

func writeBase64(buf *bytes.Buffer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		buf.WriteString(enc[:76])
		buf.WriteString("\r\n")
		enc = enc[76:]
	}
	buf.WriteString(enc)
	buf.WriteString("\r\n")
}

// contentTypeOf strips parameters from a Content-Type value.
func contentTypeOf(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		return strings.TrimSpace(ct[:i])
	}
	return ct
}

func (b *MessageBuilder) checksum() [32]byte {
	h := sha256.New()
	writeField := func(s string) {
		fmt.Fprintf(h, "%d:", len(s))
		h.Write([]byte(s))
	}
	writeField(b.from)
	writeField(b.to)
	writeField(b.subject)
	writeField(b.text)
	writeField(b.html)
	for _, hdr := range b.headers {
		writeField(hdr[0])
		writeField(hdr[1])
	}
	for _, p := range b.attachments {
		writeField(p.filename)
		writeField(p.contentType)
		writeField(fmt.Sprintf("%t", p.inline))
		h.Write(p.data)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func writeHeader(buf *bytes.Buffer, key, value string) {
	fmt.Fprintf(buf, "%s: %s\r\n", key, value)
}
