// Package mime parses raw RFC 822 messages into the pieces Ivy mirrors and
// renders: decoded plain text and HTML, attachments and inline cid: parts,
// threading headers, Authentication-Results and a one-line snippet.
//
// It is enmime glue with no database, network or sanitising: render/ owns the
// security pass (ARCHITECTURE.md 5). Email is hostile input, so parsing never
// fails: problems are collected in Parsed.Errors and the caller still gets the
// raw-derived fields it could read.
package mime

import (
	"bytes"
	"fmt"
	"regexp"

	"github.com/jhillyerd/enmime"
)

// maxPartErrors bounds the non-fatal errors kept per message, so a crafted
// message cannot grow the mirror row without limit.
const maxPartErrors = 20

// messageIDRe pulls the angle-bracketed ids out of References and In-Reply-To.
// Message-IDs cannot contain angle brackets, so this cannot backtrack.
var messageIDRe = regexp.MustCompile(`<[^<>]+>`)

// Address is one mail header address as it arrived.
type Address struct {
	Name    string
	Address string
}

// Part is one attachment or inline part.
type Part struct {
	Filename    string
	ContentType string
	ContentID   string // without angle brackets; empty for attachments
	Inline      bool
	Content     []byte
}

// AuthResults is the SPF/DKIM/DMARC verdict set parsed from the
// Authentication-Results header(s), plus the raw header values.
type AuthResults struct {
	// AuthservID is the trusted authentication service that supplied the
	// verdicts (RFC 8601), so the operator can see which server attested them.
	AuthservID string
	SPF        string
	DKIM       string
	DMARC      string
	Raw        []string
}

// Parsed is the result of parsing one raw message.
type Parsed struct {
	Text        string
	HTML        string
	Snippet     string
	References  []string
	InReplyTo   []string
	ReplyTo     []Address
	DeliveredTo []Address
	Attachments []Part
	Inlines     []Part
	// Large lists the parts ParseStream left on disk (their bodies are not in
	// Attachments or Inlines). Empty for Parse, which keeps everything.
	Large []PartInfo
	// BodySkipped is true when the body could not be read at all (too deep, too
	// many parts, an unreadable header) and only the headers were parsed.
	BodySkipped bool
	Auth        AuthResults
	Errors      []string
}

// Parse decodes one raw message. It never returns an error: a message that
// enmime cannot read yields the fields we could recover plus an entry in
// Parsed.Errors, because a single hostile message must not stop a sync.
//
// trustedAuthservIDs are the Authentication-Results authserv-ids whose verdicts
// may be believed; the empty list trusts none (N9, ParseAuthResults).
func Parse(raw []byte, trustedAuthservIDs ...string) (p Parsed) {
	defer func() {
		// enmime is fuzzed upstream, but a parser panic on hostile mail would
		// kill a sync worker, so it is caught and recorded like any other error.
		if r := recover(); r != nil {
			p.Errors = append(p.Errors, fmt.Sprintf("parser panic: %v", r))
		}
	}()

	if exceedsMultipartDepth(raw) {
		// Keep the headers (threading, addresses, auth) and skip the body rather
		// than let enmime spend exponential time on it.
		raw = headerOnly(raw)
		p.Errors = append(p.Errors, fmt.Sprintf("multipart nesting deeper than %d: body not parsed", MaxMultipartDepth))
	}

	env, err := enmime.NewParser(enmime.MaxStoredPartErrors(maxPartErrors)).ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		p.Errors = append(p.Errors, err.Error())
		return p
	}

	p.Text = env.Text
	p.HTML = env.HTML
	p.Snippet = Snippet(env.Text)
	p.References = messageIDs(env.GetHeaderValues("References"))
	p.InReplyTo = messageIDs(env.GetHeaderValues("In-Reply-To"))
	p.ReplyTo = addresses(env, "Reply-To", &p.Errors)
	p.DeliveredTo = addresses(env, "Delivered-To", &p.Errors)
	p.Attachments = parts(env.Attachments, false)
	p.Inlines = parts(env.Inlines, true)
	p.Auth = ParseAuthResults(env.GetHeaderValues("Authentication-Results"), trustedAuthservIDs)
	p.Errors = append(p.Errors, partErrors(env.Errors)...)
	return p
}

// messageIDs extracts every id from the given header values, in order.
func messageIDs(values []string) []string {
	var out []string
	for _, v := range values {
		out = append(out, messageIDRe.FindAllString(v, -1)...)
	}
	return out
}

// addresses decodes one address header. A missing header is not an error; a
// malformed one is recorded so the message still lands in the mirror.
func addresses(env *enmime.Envelope, key string, errs *[]string) []Address {
	if env.GetHeader(key) == "" {
		return nil
	}
	list, err := env.AddressList(key)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: %v", key, err))
		return nil
	}
	out := make([]Address, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		out = append(out, Address{Name: a.Name, Address: a.Address})
	}
	return out
}

func parts(in []*enmime.Part, inline bool) []Part {
	out := make([]Part, 0, len(in))
	for _, p := range in {
		if p == nil {
			continue
		}
		out = append(out, Part{
			Filename:    p.FileName,
			ContentType: p.ContentType,
			ContentID:   p.ContentID,
			Inline:      inline,
			Content:     p.Content,
		})
	}
	return out
}

// partErrors flattens enmime's collected problems. Deriving plain text from an
// HTML-only message is routine, not a fault, so that specific warning is
// dropped; a real conversion failure (Severe) is kept.
func partErrors(in []*enmime.Error) []string {
	var out []string
	for _, e := range in {
		if e == nil {
			continue
		}
		if e.Name == enmime.ErrorPlainTextFromHTML && !e.Severe {
			continue
		}
		out = append(out, e.Error())
	}
	return out
}

// MaxMultipartDepth is the deepest multipart nesting Parse will hand to enmime.
// Real mail rarely nests past eight levels. It is a hard cap because enmime's
// work on an unterminated nest doubles with every level (measured: 20 ms at 16,
// 5 s at 24, effectively never at 40), so a few KB of hostile input would pin
// the sync worker.
const MaxMultipartDepth = 16
