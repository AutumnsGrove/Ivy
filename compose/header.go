package compose

import (
	"encoding/base64"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
)

// msgIDPattern matches a canonical RFC 5322 msg-id: angle brackets, one @, and
// no spaces, angle brackets or control characters inside.
var msgIDPattern = regexp.MustCompile(`^<[^<>@\s\x00-\x1f]+@[^<>@\s\x00-\x1f]+>$`)

func invalid(field, reason string) *ValidationError {
	return &ValidationError{Field: field, Reason: reason}
}

// hasBadBytes reports a CR, LF or NUL: the three bytes that could end a header
// line early or split a field. They are rejected, never stripped.
func hasBadBytes(s string) bool { return strings.ContainsAny(s, "\r\n\x00") }

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// validate checks every field in a fixed order and returns the first problem.
func validate(m Message) error {
	if err := validateAddress("from", m.From); err != nil {
		return err
	}
	for _, a := range m.To {
		if err := validateAddress("to", a); err != nil {
			return err
		}
	}
	for _, a := range m.Cc {
		if err := validateAddress("cc", a); err != nil {
			return err
		}
	}
	for _, a := range m.Bcc {
		if err := validateAddress("bcc", a); err != nil {
			return err
		}
	}
	for _, a := range m.ReplyTo {
		if err := validateAddress("reply_to", a); err != nil {
			return err
		}
	}
	if n := len(recipientList(m)); n > MaxRecipients {
		return invalid("recipients", fmt.Sprintf("%d recipients after de-duplication, the maximum is %d", n, MaxRecipients))
	}
	if hasBadBytes(m.Subject) {
		return invalid("subject", "contains CR, LF or NUL")
	}
	if len(m.Subject) > MaxSubjectBytes {
		return invalid("subject", fmt.Sprintf("%d bytes, the maximum is %d", len(m.Subject), MaxSubjectBytes))
	}
	if len(m.Text) > MaxBodyBytes {
		return invalid("body", fmt.Sprintf("%d bytes, the maximum is %d", len(m.Text), MaxBodyBytes))
	}
	if err := validateMessageID("message_id", m.MessageID, true); err != nil {
		return err
	}
	if m.Date.IsZero() {
		return invalid("date", "must be set; compose never invents an instant")
	}
	if err := validateMessageID("in_reply_to", m.InReplyTo, false); err != nil {
		return err
	}
	if len(m.References) > MaxReferences {
		return invalid("references", fmt.Sprintf("%d ids, the maximum is %d", len(m.References), MaxReferences))
	}
	for _, id := range m.References {
		if err := validateMessageID("references", id, true); err != nil {
			return err
		}
	}
	return nil
}

func validateAddress(field string, a Address) error {
	if strings.TrimSpace(a.Address) == "" {
		return invalid(field, "address is empty")
	}
	if hasBadBytes(a.Name) || hasBadBytes(a.Address) {
		return invalid(field, "contains CR, LF or NUL")
	}
	// Purelymail does not accept SMTPUTF8 (spike S1), so a non-ASCII local part
	// or domain is refused before anything is queued. A display name may be
	// non-ASCII; it is RFC 2047 encoded.
	if !isASCII(a.Address) {
		return invalid(field, "address is not ASCII; this provider does not accept SMTPUTF8")
	}
	if strings.ContainsAny(a.Address, " \t") {
		return invalid(field, "address contains whitespace")
	}
	parsed, err := mail.ParseAddress(a.Address)
	if err != nil || parsed.Address != a.Address || parsed.Name != "" {
		return invalid(field, fmt.Sprintf("%q is not a bare address", a.Address))
	}
	return nil
}

func validateMessageID(field, id string, required bool) error {
	if id == "" {
		if required {
			return invalid(field, "is empty")
		}
		return nil
	}
	if hasBadBytes(id) {
		return invalid(field, "contains CR, LF or NUL")
	}
	if len(id) > MaxMessageIDBytes {
		return invalid(field, fmt.Sprintf("%d bytes, the maximum is %d", len(id), MaxMessageIDBytes))
	}
	if !isASCII(id) || !msgIDPattern.MatchString(id) {
		return invalid(field, fmt.Sprintf("%q is not a canonical <id@domain>", id))
	}
	return nil
}

// recipientList de-duplicates To, then Cc, then Bcc, case-insensitively, and
// keeps the first spelling of each address.
func recipientList(m Message) []string {
	seen := make(map[string]bool, len(m.To)+len(m.Cc)+len(m.Bcc))
	out := make([]string, 0, len(m.To)+len(m.Cc)+len(m.Bcc))
	add := func(list []Address) {
		for _, a := range list {
			key := strings.ToLower(a.Address)
			if !seen[key] {
				seen[key] = true
				out = append(out, a.Address)
			}
		}
	}
	add(m.To)
	add(m.Cc)
	add(m.Bcc)
	return out
}

// encodeWord turns a header value into a safe ASCII form: ASCII without an
// encoded-word marker is left alone; anything else becomes one or more RFC 2047
// base64 encoded words, each under the 75-byte limit, so a value that merely
// looks like an encoded word cannot be read as one and a long value folds.
func encodeWord(s string) string {
	if s == "" || (isASCII(s) && !strings.Contains(s, "=?")) {
		return s
	}
	const maxChunk = 45 // base64 of 45 bytes is 60 + "=?utf-8?B?" + "?=" = 72
	var words []string
	var buf []byte
	flush := func() {
		if len(buf) > 0 {
			words = append(words, encodedWord(buf))
			buf = buf[:0]
		}
	}
	for _, r := range s {
		b := []byte(string(r))
		if len(buf)+len(b) > maxChunk {
			flush()
		}
		buf = append(buf, b...)
	}
	flush()
	return strings.Join(words, " ")
}

// encodedWord is one RFC 2047 base64 encoded word. mime.BEncoding.Encode is
// deliberately not used here: it returns a pure-ASCII string unchanged, which
// would leave a subject that merely looks like an encoded word readable as one.
func encodedWord(chunk []byte) string {
	return "=?utf-8?B?" + base64.StdEncoding.EncodeToString(chunk) + "?="
}

// mailAddresses hands the builder the raw addresses; it only needs them to
// accept the message. The header text itself is written by formatAddress, below.
func mailAddresses(list []Address) []mail.Address {
	out := make([]mail.Address, len(list))
	for i, a := range list {
		out[i] = mail.Address{Name: a.Name, Address: a.Address}
	}
	return out
}

// formatAddress renders one mailbox for a header. A plain ASCII name goes
// through net/mail, which quotes it as needed. Anything else becomes RFC 2047
// encoded words written bare: net/mail would wrap them in a quoted-string, where
// RFC 2047 section 5 forbids encoded words and a client shows them verbatim.
func formatAddress(a Address) string {
	if isASCII(a.Name) && !strings.Contains(a.Name, "=?") {
		return (&mail.Address{Name: a.Name, Address: a.Address}).String()
	}
	return encodeWord(a.Name) + " <" + a.Address + ">"
}

// joinAddresses renders an address list as one header value. enmime's own
// joiner is unexported and quotes encoded words, so compose formats every
// address header itself and overwrites the builder's.
func joinAddresses(list []Address) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = formatAddress(a)
	}
	return strings.Join(parts, ", ")
}
