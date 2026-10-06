package compose

import (
	"regexp"
	"strings"
	"time"
)

// Incoming is the parsed incoming message the reply and forward logic reads. It
// is deliberately its own type, not a store row, so the logic is pure and its
// tests are tables over hostile and odd header sets.
type Incoming struct {
	From        []Address
	To          []Address
	Cc          []Address
	ReplyTo     []Address
	DeliveredTo []Address
	Subject     string
	Date        time.Time
	MessageID   string
	References  []string
	// Text is the message's plain text, used only to build a forward's quoted
	// body. A reply does not quote (the reader already shows the thread).
	Text string
}

// IdentityRef is one address the account may send as, with its display name and
// signature. The gateway builds these from store.Identity and the account's own
// address.
type IdentityRef struct {
	Address     string
	DisplayName string
	Signature   string
}

// Prefill is a ready-to-edit compose state: the recipients, subject, body and
// threading headers a reply, reply-all or forward starts from, plus the identity
// to send as.
type Prefill struct {
	From       Address
	To         []Address
	Cc         []Address
	Subject    string
	Text       string
	InReplyTo  string
	References []string
	// ReplyTarget is the direct recipient, for the reader's "Replying to …" note.
	ReplyTarget string
	// MissingIdentity names a Delivered-To address with no configured identity,
	// so the compose screen can offer to add it. Empty when nothing is missing.
	MissingIdentity string
}

// Reply computes a direct reply or a reply-all. The direct target is Reply-To
// when set, else From; a reply-all also Ccs the original To and Cc, minus every
// one of the operator's own addresses and anyone already in To. List handling
// (`List-Post`) is out of v1 (round 60).
func Reply(orig Incoming, ids []IdentityRef, accountDefault IdentityRef, all bool) Prefill {
	from, missing := chooseIdentity(orig, ids, accountDefault)
	target := directTarget(orig)
	p := Prefill{
		From: asAddress(from), MissingIdentity: missing, ReplyTarget: target.Address,
		Subject: replySubject(orig.Subject), InReplyTo: orig.MessageID,
		References: replyReferences(orig.References, orig.MessageID),
	}
	if target.Address != "" {
		p.To = []Address{target}
	}
	if all {
		p.Cc = otherRecipients(orig, ids, accountDefault, p.To)
	}
	p.Text = AppendSignature("", from.Signature)
	return p
}

// Forward computes a forward with an attribution block and the original plain
// text. It takes no recipients: the operator fills them. A forward starts a new
// thread, so no In-Reply-To or References are set (the subject fallback treats
// Fwd: as a different subject, ARCHITECTURE.md 5).
func Forward(orig Incoming, ids []IdentityRef, accountDefault IdentityRef) Prefill {
	from, missing := chooseIdentity(orig, ids, accountDefault)
	return Prefill{
		From: asAddress(from), MissingIdentity: missing,
		Subject: forwardSubject(orig.Subject),
		Text:    AppendSignature(forwardBlock(orig), from.Signature),
	}
}

// asAddress projects a chosen identity onto the builder's mailbox type.
func asAddress(id IdentityRef) Address {
	return Address{Name: id.DisplayName, Address: id.Address}
}

// AppendSignature appends a non-empty signature behind the standard `-- ` line.
// A blank or whitespace-only signature changes nothing, and an empty body is
// just the separator and the signature. The signature is plain text; when the
// message is markdown the whole body (signature included) is rendered.
func AppendSignature(text, signature string) string {
	if strings.TrimSpace(signature) == "" {
		return text
	}
	body := strings.TrimRight(text, "\n")
	if body == "" {
		return "-- \n" + signature
	}
	return body + "\n\n-- \n" + signature
}

// hasPrefix reports whether s already carries a reply or forward prefix, so a
// reply to a reply, or a forward of a forward, is not prefixed twice. The
// variants mirror the thread fallback (Re:, RE[3]:, Fwd:, FW:).
var (
	rePrefix  = regexp.MustCompile(`(?i)^\s*re(\[\d+\])?:\s*`)
	fwdPrefix = regexp.MustCompile(`(?i)^\s*fwd?:\s*`)
)

func replySubject(subject string) string {
	if strings.TrimSpace(subject) == "" {
		return ""
	}
	if rePrefix.MatchString(subject) {
		return subject
	}
	return "Re: " + subject
}

func forwardSubject(subject string) string {
	if strings.TrimSpace(subject) == "" {
		return ""
	}
	if fwdPrefix.MatchString(subject) {
		return subject
	}
	return "Fwd: " + subject
}

// directTarget is the first mailbox of Reply-To, else of From. An empty header
// falls through, so a message with no Reply-To and a bare From still replies.
func directTarget(orig Incoming) Address {
	for _, list := range [][]Address{orig.ReplyTo, orig.From} {
		for _, a := range list {
			if a.Address != "" {
				return a
			}
		}
	}
	return Address{}
}

// otherRecipients is reply-all's Cc: the original To then Cc, de-duplicated
// case-insensitively, with the operator's own addresses and the direct recipient
// removed.
func otherRecipients(orig Incoming, ids []IdentityRef, accountDefault IdentityRef, to []Address) []Address {
	skip := map[string]bool{}
	if accountDefault.Address != "" {
		skip[strings.ToLower(accountDefault.Address)] = true
	}
	for _, id := range ids {
		if id.Address != "" {
			skip[strings.ToLower(id.Address)] = true
		}
	}
	for _, a := range to {
		skip[strings.ToLower(a.Address)] = true
	}
	seen := map[string]bool{}
	var out []Address
	for _, list := range [][]Address{orig.To, orig.Cc} {
		for _, a := range list {
			key := strings.ToLower(a.Address)
			if a.Address == "" || skip[key] || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, a)
		}
	}
	return out
}

// replyReferences is the original chain plus the original Message-ID, trimmed
// from the head to the nearest MaxReferences ancestors so the header stays
// within the builder's bound. The input is not mutated.
func replyReferences(refs []string, msgID string) []string {
	chain := refs
	if msgID != "" {
		chain = append(append([]string{}, refs...), msgID)
	}
	if len(chain) > MaxReferences {
		chain = chain[len(chain)-MaxReferences:]
	}
	return chain
}

// chooseIdentity picks the identity a reply or forward is sent as: the first
// Delivered-To, To or Cc address that matches a configured identity, else the
// account default. An unmatched Delivered-To is reported so the screen can
// offer to add it (round 63).
func chooseIdentity(orig Incoming, ids []IdentityRef, accountDefault IdentityRef) (IdentityRef, string) {
	find := func(address string) (IdentityRef, bool) {
		for _, id := range ids {
			if strings.EqualFold(id.Address, address) {
				return id, true
			}
		}
		return IdentityRef{}, false
	}
	var missing string
	for _, a := range orig.DeliveredTo {
		if a.Address == "" {
			continue
		}
		if id, ok := find(a.Address); ok {
			return id, ""
		}
		if missing == "" {
			missing = a.Address
		}
	}
	for _, list := range [][]Address{orig.To, orig.Cc} {
		for _, a := range list {
			if a.Address == "" {
				continue
			}
			if id, ok := find(a.Address); ok {
				// An unmatched Delivered-To is the routing truth: it is still worth
				// reporting even when a To/Cc address picks the identity.
				return id, missing
			}
		}
	}
	return accountDefault, missing
}

// forwardBlock is the human-readable attribution a forwarded message carries.
// It is body text, not headers, so a display name is written plainly, not
// RFC 2047 encoded.
func forwardBlock(orig Incoming) string {
	var b strings.Builder
	b.WriteString("---------- Forwarded message ----------")
	if len(orig.From) > 0 && orig.From[0].Address != "" {
		b.WriteString("\nFrom: " + displayAddress(orig.From[0]))
	}
	if !orig.Date.IsZero() {
		b.WriteString("\nDate: " + orig.Date.Format("Mon, 2 Jan 2006 15:04 -0700"))
	}
	b.WriteString("\nSubject: " + orig.Subject)
	if line := joinDisplay(orig.To); line != "" {
		b.WriteString("\nTo: " + line)
	}
	if text := strings.TrimRight(orig.Text, "\n"); text != "" {
		b.WriteString("\n\n" + text)
	}
	return b.String()
}

// displayAddress is one mailbox as body text: "Name <addr>" or the bare address.
func displayAddress(a Address) string {
	if a.Name == "" {
		return a.Address
	}
	return a.Name + " <" + a.Address + ">"
}

func joinDisplay(list []Address) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		if a.Address != "" {
			parts = append(parts, displayAddress(a))
		}
	}
	return strings.Join(parts, ", ")
}
