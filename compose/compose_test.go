package compose_test

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/jhillyerd/enmime"
	"golang.org/x/net/html"

	"github.com/AutumnsGrove/Ivy/compose"
)

// base is a valid message every corpus case mutates. The id and the date are
// injected exactly as the send queue will inject them (CHUNK4-BRIEF section 3).
func base() compose.Message {
	return compose.Message{
		From:      compose.Address{Name: "Autumn", Address: "autumn@example.test"},
		To:        []compose.Address{{Name: "Mara", Address: "mara@example.test"}},
		Subject:   "hello",
		Text:      "hi there",
		MessageID: "<ivy-2026-10-06-1@example.test>",
		Date:      time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
	}
}

// allowedHeaders is every header the builder is permitted to emit. Anything
// else in an output is an injected header, which is the whole point of the
// corpus.
var allowedHeaders = map[string]bool{
	"Bcc": true, "Cc": true, "Content-Type": true, "Date": true, "From": true,
	"In-Reply-To": true, "Message-Id": true, "Mime-Version": true,
	"References": true, "Reply-To": true, "Subject": true, "To": true,
}

// assertSafeHeaders is the oracle for a successfully built message: it must
// parse, use CRLF throughout, carry only known headers once each, and no header
// value may still contain a CR, LF or NUL that a peer's parser would unfold
// into a new header.
func assertSafeHeaders(t *testing.T, raw []byte) *mail.Message {
	t.Helper()
	for i, b := range raw {
		if b == '\n' && (i == 0 || raw[i-1] != '\r') {
			t.Fatalf("output has a bare LF at byte %d, so a peer may not see the intended header boundary", i)
		}
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("built message does not parse: %v", err)
	}
	for name, vals := range msg.Header {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if !allowedHeaders[canonical] {
			t.Errorf("built message carries an unexpected header %q; a hostile value forged it", name)
		}
		if len(vals) != 1 {
			t.Errorf("header %q appears %d times, want exactly once", name, len(vals))
		}
		for _, v := range vals {
			if strings.ContainsAny(v, "\r\n\x00") {
				t.Errorf("header %q value still carries CR, LF or NUL: %q", name, v)
			}
		}
	}
	if !bytes.Contains(raw, []byte("\r\n\r\n")) {
		t.Errorf("built message has no CRLF header/body separator")
	}
	return msg
}

func decodedHeader(t *testing.T, v string) string {
	t.Helper()
	out, err := new(mime.WordDecoder).DecodeHeader(v)
	if err != nil {
		t.Fatalf("decode header %q: %v", v, err)
	}
	return out
}

// TestBuildRejectsInjectedHeaders is the corpus's red half: a CR, LF or NUL in
// any header-bound value is refused with the field named, never stripped, and a
// subject or id over its bound is refused rather than silently truncated.
func TestBuildRejectsInjectedHeaders(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		field string
		apply func(m *compose.Message)
	}{
		{"subject CRLF", "subject", func(m *compose.Message) { m.Subject = "ok\r\nBcc: evil@example.test" }},
		{"subject LF", "subject", func(m *compose.Message) { m.Subject = "ok\nX-Injected: yes" }},
		{"subject CR", "subject", func(m *compose.Message) { m.Subject = "ok\rX-Injected: yes" }},
		{"subject NUL", "subject", func(m *compose.Message) { m.Subject = "ok\x00x" }},
		{"subject header fence", "subject", func(m *compose.Message) { m.Subject = "ok\r\n\r\nbody" }},
		{"subject too long", "subject", func(m *compose.Message) { m.Subject = strings.Repeat("a", compose.MaxSubjectBytes+1) }},
		{"display name CRLF", "from", func(m *compose.Message) { m.From.Name = "Bob\r\nBcc: evil@example.test" }},
		{"display name LF", "from", func(m *compose.Message) { m.From.Name = "Bob\nX: y" }},
		{"display name NUL", "from", func(m *compose.Message) { m.From.Name = "Bob\x00" }},
		{"to display name CRLF", "to", func(m *compose.Message) { m.To[0].Name = "Mara\r\nBcc: evil@example.test" }},
		{"from non-ASCII local part", "from", func(m *compose.Message) { m.From.Address = "mü@example.test" }},
		{"from non-ASCII domain", "from", func(m *compose.Message) { m.From.Address = "a@exämple.test" }},
		{"from with a space", "from", func(m *compose.Message) { m.From.Address = "a b@example.test" }},
		{"in-reply-to CRLF", "in_reply_to", func(m *compose.Message) { m.InReplyTo = "<a@b>\r\nX: y" }},
		{"in-reply-to not an id", "in_reply_to", func(m *compose.Message) { m.InReplyTo = "a@b" }},
		{"references CRLF", "references", func(m *compose.Message) { m.References = []string{"<a@b>\r\nX: y"} }},
		{"references not an id", "references", func(m *compose.Message) { m.References = []string{"a@b"} }},
		{"message-id CRLF", "message_id", func(m *compose.Message) { m.MessageID = "<a@b>\r\nX: y" }},
		{"message-id not an id", "message_id", func(m *compose.Message) { m.MessageID = "a@b" }},
		{"message-id too long", "message_id", func(m *compose.Message) {
			m.MessageID = "<" + strings.Repeat("a", compose.MaxMessageIDBytes) + "@example.test>"
		}},
		{"too many recipients", "recipients", func(m *compose.Message) {
			m.To = m.To[:0]
			for i := range compose.MaxRecipients + 1 {
				m.To = append(m.To, compose.Address{Address: fmt.Sprintf("u%d@example.test", i)})
			}
		}},
		{"body too large", "body", func(m *compose.Message) { m.Text = strings.Repeat("x", compose.MaxBodyBytes+1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := base()
			tc.apply(&m)
			raw, _, err := compose.Build(m)
			if err == nil {
				t.Fatalf("Build accepted a hostile value and produced %d bytes (field %s should refuse)", len(raw), tc.field)
			}
			var ve *compose.ValidationError
			if !asValidationError(err, &ve) {
				t.Fatalf("Build error = %v, want a *compose.ValidationError naming %q", err, tc.field)
			}
			if ve.Field != tc.field {
				t.Errorf("ValidationError.Field = %q, want %q", ve.Field, tc.field)
			}
		})
	}
}

// TestBuildAcceptsAndSafelyEncodes is the corpus's green half: values that are
// legitimate, if awkward, must build into a message whose headers parse back to
// exactly the intended values, with hostile names left as names rather than
// becoming addresses or headers.
func TestBuildAcceptsAndSafelyEncodes(t *testing.T) {
	t.Parallel()
	t.Run("subject that looks like an encoded word", func(t *testing.T) {
		t.Parallel()
		m := base()
		m.Subject = "=?utf-8?Q?hi?="
		raw, _, err := compose.Build(m)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		msg := assertSafeHeaders(t, raw)
		if got := decodedHeader(t, msg.Header.Get("Subject")); got != m.Subject {
			t.Errorf("decoded subject = %q, want the literal %q", got, m.Subject)
		}
	})
	t.Run("unicode subject and display name", func(t *testing.T) {
		t.Parallel()
		m := base()
		m.Subject = "Réunion 😀"
		m.From.Name = "Müller"
		raw, _, err := compose.Build(m)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		msg := assertSafeHeaders(t, raw)
		if got := decodedHeader(t, msg.Header.Get("Subject")); got != m.Subject {
			t.Errorf("decoded subject = %q, want %q", got, m.Subject)
		}
		addr := parseAddress(t, msg.Header.Get("From"))
		if addr.Address != m.From.Address {
			t.Errorf("From address = %q, want %q", addr.Address, m.From.Address)
		}
		if addr.Name != "Müller" {
			t.Errorf("From name = %q, want Müller", addr.Name)
		}
	})
	t.Run("display name that contains an address stays a name", func(t *testing.T) {
		t.Parallel()
		m := base()
		m.From.Name = "Bob <bob@evil.example.test>"
		raw, _, err := compose.Build(m)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		msg := assertSafeHeaders(t, raw)
		addr := parseAddress(t, msg.Header.Get("From"))
		if addr.Address != m.From.Address {
			t.Errorf("From address = %q, want the real %q", addr.Address, m.From.Address)
		}
		if !strings.Contains(addr.Name, "bob@evil.example.test") {
			t.Errorf("From name = %q, want the hostile text kept as a name", addr.Name)
		}
	})
	t.Run("display name that names a header stays a name", func(t *testing.T) {
		t.Parallel()
		m := base()
		m.To[0].Name = "To: evil@example.test"
		raw, _, err := compose.Build(m)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		msg := assertSafeHeaders(t, raw) // the oracle fails if a second To appeared
		addr := parseAddress(t, msg.Header.Get("To"))
		if addr.Address != "mara@example.test" {
			t.Errorf("To address = %q, want mara@example.test", addr.Address)
		}
	})
	t.Run("duplicate recipients collapse in the envelope", func(t *testing.T) {
		t.Parallel()
		m := base()
		m.To = append(m.To, compose.Address{Address: "MARA@example.test"})
		m.Cc = []compose.Address{{Address: "mara@example.test"}}
		raw, env, err := compose.Build(m)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		assertSafeHeaders(t, raw)
		if len(env.To) != 1 || env.To[0] != "mara@example.test" {
			t.Errorf("envelope recipients = %v, want mara@example.test once", env.To)
		}
	})
}

// TestBuildBccStaysOnTheEnvelopeOnly pins invariant 7: the transmitted message
// never carries a Bcc header, the envelope still has the recipient, and only
// the Sent copy (KeepBcc) carries the header.
func TestBuildBccStaysOnTheEnvelopeOnly(t *testing.T) {
	t.Parallel()
	m := base()
	m.Bcc = []compose.Address{{Name: "Quiet", Address: "quiet@example.test"}}

	raw, env, err := compose.Build(m)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	msg := assertSafeHeaders(t, raw)
	if got := msg.Header.Get("Bcc"); got != "" {
		t.Errorf("wire message has a Bcc header %q, want none", got)
	}
	if len(env.To) != 2 || env.To[1] != "quiet@example.test" {
		t.Errorf("envelope recipients = %v, want mara then quiet", env.To)
	}

	sentRaw, _, err := compose.Build(func() compose.Message { m.KeepBcc = true; return m }())
	if err != nil {
		t.Fatalf("Build (Sent): %v", err)
	}
	sent := assertSafeHeaders(t, sentRaw)
	if got := sent.Header.Get("Bcc"); got == "" {
		t.Errorf("Sent copy has no Bcc header, want quiet@example.test")
	}
}

// TestBuildMarkdownRendersSafeHTML is the goldmark contract: the text/plain part
// is the operator's markdown exactly as typed (round 61), the HTML part is a
// multipart/alternative rendering, raw HTML never passes through, and a link
// whose scheme is not http, https or mailto is never emitted as a link.
func TestBuildMarkdownRendersSafeHTML(t *testing.T) {
	t.Parallel()
	m := base()
	m.Format = compose.BodyMarkdown
	m.Text = "**Hi** [docs](https://example.test)\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))"
	raw, _, err := compose.Build(m)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read built message: %v", err)
	}
	// Line breaks go out as CRLF; the markdown source is otherwise untouched.
	if got := strings.ReplaceAll(env.Text, "\r\n", "\n"); got != m.Text {
		t.Errorf("text/plain part = %q, want the raw markdown %q", got, m.Text)
	}
	if !strings.Contains(env.HTML, "<strong>Hi</strong>") {
		t.Errorf("text/html part did not render bold: %q", env.HTML)
	}
	if strings.Contains(strings.ToLower(env.HTML), "javascript:") {
		t.Errorf("text/html part emitted a javascript: link: %q", env.HTML)
	}
	if nodes := htmlNodes(t, env.HTML); len(nodes) == 0 {
		t.Errorf("text/html part is empty")
	} else {
		for _, n := range nodes {
			if n.Data == "script" {
				t.Errorf("raw HTML <script> passed through into outgoing mail")
			}
			if n.Data == "a" {
				for _, attr := range n.Attr {
					if strings.EqualFold(attr.Key, "href") && !allowedScheme(attr.Val) {
						t.Errorf("link destination %q is not http, https or mailto", attr.Val)
					}
				}
			}
		}
	}
}

// TestBuildPlainTextHasNoHTMLPart keeps the plain case plain: markdown off means
// one text/plain part, not an accidental HTML rendering.
func TestBuildPlainTextHasNoHTMLPart(t *testing.T) {
	t.Parallel()
	m := base()
	raw, _, err := compose.Build(m)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("read built message: %v", err)
	}
	if env.HTML != "" {
		t.Errorf("plain message has an HTML part %q, want none", env.HTML)
	}
	if env.Text != m.Text {
		t.Errorf("text/plain part = %q, want %q", env.Text, m.Text)
	}
}

// TestValidateRejectsBadAddresses covers the pre-check a caller makes before it
// renders anything: Validate refuses what Build would refuse, without building.
func TestValidateRejectsBadAddresses(t *testing.T) {
	t.Parallel()
	m := base()
	m.To = []compose.Address{{Address: "not-an-address"}}
	if err := compose.Validate(m); err == nil {
		t.Fatalf("Validate accepted an invalid address")
	}
}

func asValidationError(err error, target **compose.ValidationError) bool {
	return errors.As(err, target)
}

func parseAddress(t *testing.T, header string) *mail.Address {
	t.Helper()
	addr, err := mail.ParseAddress(header)
	if err != nil {
		t.Fatalf("parse address %q: %v", header, err)
	}
	// net/mail does not decode an RFC 2047 display name for us here, so decode
	// it the way a recipient's client would.
	if addr.Name != "" {
		if name := decodedHeader(t, addr.Name); name != addr.Name {
			addr.Name = name
		}
	}
	return addr
}

func htmlNodes(t *testing.T, doc string) []*html.Node {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parse rendered HTML: %v", err)
	}
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		out = append(out, n)
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func allowedScheme(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:")
}

// TestBuildEncodedDisplayNamesAreNotQuoted: RFC 2047 section 5 forbids an
// encoded word inside a quoted-string, so a client shows `"=?utf-8?B?...?="`
// verbatim. The oracle here is net/mail's own phrase parser with no extra
// decoding, unlike parseAddress, which decodes a quoted name itself and so let
// this through.
func TestBuildEncodedDisplayNamesAreNotQuoted(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"non-ASCII":                 "Zoë Müller",
		"long non-ASCII":            strings.Repeat("é", 60),
		"ASCII that looks like one": "=?utf-8?q?hi?=",
	}
	for name, display := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := base()
			m.From.Name = display
			m.To[0].Name = display
			m.Bcc = []compose.Address{{Name: display, Address: "hidden@example.test"}}
			m.KeepBcc = true
			raw, _, err := compose.Build(m)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			msg := assertSafeHeaders(t, raw)
			for _, h := range []string{"From", "To", "Bcc"} {
				got := msg.Header.Get(h)
				if strings.Contains(got, `"=?`) {
					t.Errorf("%s header quotes an encoded word: %q", h, got)
				}
				addr, err := new(mail.AddressParser).Parse(got)
				if err != nil {
					t.Fatalf("parse %s %q: %v", h, got, err)
				}
				// The parser decodes encoded words in an unquoted phrase itself.
				if addr.Name != display {
					t.Errorf("%s display name = %q, want %q", h, addr.Name, display)
				}
			}
		})
	}
}

// TestBuildBodyLinesAreWireSafe: an all-ASCII body is sent as 7bit with no
// re-encoding, so the operator's own line endings and line lengths reached the
// wire. A paragraph typed on a phone is one very long line, and RFC 5322 caps a
// line at 998 bytes; a lone LF or CR is not a line break SMTP accepts.
func TestBuildBodyLinesAreWireSafe(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("word ", 400) // one 2000-byte line
	for _, tc := range []struct {
		name   string
		format compose.BodyFormat
	}{{"plain", compose.BodyPlain}, {"markdown", compose.BodyMarkdown}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := base()
			m.Format = tc.format
			m.Text = "first\nsecond\rthird\r\n" + long + "\nlast"
			raw, _, err := compose.Build(m)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			assertSafeHeaders(t, raw) // fails on any bare LF
			if bytes.Contains(bytes.ReplaceAll(raw, []byte("\r\n"), nil), []byte("\r")) {
				t.Errorf("output carries a lone CR")
			}
			for i, line := range bytes.Split(raw, []byte("\r\n")) {
				if len(line) > 998 {
					t.Errorf("line %d is %d bytes, over the 998 limit of RFC 5322", i, len(line))
				}
			}
			env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if got, want := strings.Fields(env.Text), strings.Fields(m.Text); strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("words changed in transit:\n got %q\nwant %q", env.Text, m.Text)
			}
		})
	}
}

// TestNoRecipientsIsAValidationError: a message with nobody to send to must be
// the same typed refusal as any other bad field, from Validate and from Build,
// not a bare builder error the send API cannot tell from a server fault.
func TestNoRecipientsIsAValidationError(t *testing.T) {
	t.Parallel()
	m := base()
	m.To = nil
	for name, err := range map[string]error{
		"Validate": compose.Validate(m),
		"Build":    func() error { _, _, err := compose.Build(m); return err }(),
	} {
		var ve *compose.ValidationError
		if !errors.As(err, &ve) || ve.Field != "recipients" {
			t.Errorf("%s with no recipients = %v, want a *ValidationError for recipients", name, err)
		}
	}
}
