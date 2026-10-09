package jev

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// MinStateBytes is the smallest budget worth a call. Below it the headers alone would
// crowd out the body, so BuildState reports nothing to read instead.
const MinStateBytes = 1024

// Caps on each header field, so a hostile sender can shape the state's wording but
// never its size or its line structure.
const (
	maxSubjectBytes     = 300
	maxFromBytes        = 200
	maxListIDBytes      = 200
	maxNameBytes        = 80
	maxNamesPerLine     = 10
	maxAttachmentsShown = 20
	maxAttachmentBytes  = 100
	maxAttachmentsLine  = 600
	maxHeaderBytes      = 4 << 10

	// workFactor bounds the body work to a multiple of the budget, so a 60 MiB
	// message costs what a 100 KiB one does. Text beyond it would be clipped anyway.
	workFactor = 4
	workSlack  = 4 << 10

	// A signature divider counts only near the end; one in the middle is prose.
	signatureWindowLines = 12
)

// Party is a sender or recipient. Only a sender's address reaches the model: the
// domain is a strong signal for classification, while recipient addresses are not.
type Party struct {
	Name    string
	Address string
}

// Attachment is named by filename and type only; its text is not sent.
type Attachment struct {
	Name string
	Type string
}

// Auth is the provider-attested SPF, DKIM and DMARC verdicts. The caller passes
// only verdicts from a trusted authserv-id; an empty field is simply absent.
type Auth struct {
	SPF, DKIM, DMARC string
}

// StateInput is the part of a stored message a question may see.
type StateInput struct {
	From        Party
	To, CC      []Party
	Subject     string
	Date        time.Time
	ListID      string
	Auth        Auth
	Body        string
	Attachments []Attachment
}

// State is the text sent to Jev for one message.
type State struct {
	Text string
	// Clipped is true when anything was cut to fit the budget.
	Clipped bool
}

var (
	looksLikeHTML = regexp.MustCompile(`(?i)<(html|body|head|div|p|br|span|table|tr|td|style|script|a|b|i|font|ul|li|h[1-6])[\s/>]`)
	htmlComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlBlocks    = regexp.MustCompile(`(?is)<(style|script|head)\b[^>]*>.*?</(style|script|head)\s*>`)
	htmlBreaks    = regexp.MustCompile(`(?i)</?(p|div|br|tr|li|h[1-6]|table|ul|ol)\b[^>]*>`)
	htmlTags      = regexp.MustCompile(`</?[a-zA-Z!][^>]*>`)
	attribution   = regexp.MustCompile(`(?i)^(on|le|am|el)\s.{3,250}(wrote|a écrit|schrieb|escribió):$`)
	originalMsg   = regexp.MustCompile(`(?i)^-{2,}\s*(original message|ursprüngliche nachricht|message d'origine)\s*-{2,}$`)
	multiBlank    = regexp.MustCompile(`\n{3,}`)
)

// BuildState reduces a message to the text a question sees, within budget bytes:
// the few headers that matter, then the body with quoted replies and the
// signature removed. It never includes markup, control or directional characters,
// or a header line a sender could forge, and it reports false when there is
// nothing worth a paid call. The result is a pure function of the input, so the
// cache key does not have to cover it.
func BuildState(in StateInput, budget int) (State, bool) {
	if budget < MinStateBytes {
		return State{}, false
	}
	subject := field(in.Subject, maxSubjectBytes)
	body, workClipped := cleanBody(in.Body, budget*workFactor+workSlack)
	if subject == "" && body == "" {
		return State{}, false
	}

	headerBudget := budget / 2
	if headerBudget > maxHeaderBytes {
		headerBudget = maxHeaderBytes
	}
	headers, dropped := headerBlock(in, subject, headerBudget)

	bodyBudget := budget - len(headers) - len("\n\n")
	clipped := workClipped || dropped
	if len(body) > bodyBudget {
		body = clipUTF8(body, bodyBudget)
		clipped = true
	}
	return State{Text: headers + "\n\n" + body, Clipped: clipped}, true
}

// headerBlock lays out the headers in priority order and drops, whole, any line
// that no longer fits, so a clipped header is never half a line.
func headerBlock(in StateInput, subject string, budget int) (string, bool) {
	var lines []string
	if from := fromLine(in.From); from != "" {
		lines = append(lines, "From: "+from)
	}
	if to := namesLine(in.To); to != "" {
		lines = append(lines, "To: "+to)
	}
	if cc := namesLine(in.CC); cc != "" {
		lines = append(lines, "Cc: "+cc)
	}
	if subject != "" {
		lines = append(lines, "Subject: "+subject)
	}
	if !in.Date.IsZero() {
		lines = append(lines, "Date: "+in.Date.UTC().Format(time.RFC3339))
	}
	if id := field(in.ListID, maxListIDBytes); id != "" {
		lines = append(lines, "List-Id: "+id)
	}
	if auth := authLine(in.Auth); auth != "" {
		lines = append(lines, "Auth: "+auth)
	}
	if att := attachmentsLine(in.Attachments); att != "" {
		lines = append(lines, "Attachments: "+att)
	}

	var b strings.Builder
	dropped := false
	for _, l := range lines {
		add := len(l)
		if b.Len() > 0 {
			add++
		}
		if b.Len()+add > budget {
			dropped = true
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l)
	}
	return b.String(), dropped
}

func fromLine(p Party) string {
	name, addr := field(p.Name, maxFromBytes), field(p.Address, maxFromBytes)
	switch {
	case name != "" && addr != "":
		return name + " <" + addr + ">"
	case addr != "":
		return addr
	}
	return name
}

// namesLine lists display names only. A recipient without one is counted rather
// than shown, so no recipient address is sent.
func namesLine(ps []Party) string {
	var names []string
	unnamed, more := 0, 0
	for _, p := range ps {
		name := field(p.Name, maxNameBytes)
		switch {
		case name == "":
			unnamed++
		case len(names) < maxNamesPerLine:
			names = append(names, name)
		default:
			more++
		}
	}
	if unnamed > 0 {
		names = append(names, fmt.Sprintf("%d unnamed", unnamed))
	}
	if more > 0 {
		names = append(names, fmt.Sprintf("%d more", more))
	}
	return strings.Join(names, ", ")
}

// verdicts is the closed vocabulary of RFC 8601 results; anything else collapses to
// "other" so the line can carry no sender-chosen text.
var verdicts = map[string]bool{
	"pass": true, "fail": true, "softfail": true, "neutral": true,
	"none": true, "temperror": true, "permerror": true, "policy": true,
}

func authLine(a Auth) string {
	if a.SPF == "" && a.DKIM == "" && a.DMARC == "" {
		return ""
	}
	v := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		switch {
		case s == "":
			return "none"
		case verdicts[s]:
			return s
		}
		return "other"
	}
	return fmt.Sprintf("spf=%s dkim=%s dmarc=%s", v(a.SPF), v(a.DKIM), v(a.DMARC))
}

func attachmentsLine(as []Attachment) string {
	var parts []string
	for i, a := range as {
		if i == maxAttachmentsShown {
			parts = append(parts, fmt.Sprintf("%d more", len(as)-i))
			break
		}
		name, typ := field(a.Name, maxAttachmentBytes), field(a.Type, 60)
		if name == "" && typ == "" {
			continue
		}
		if typ != "" {
			name = strings.TrimSpace(name + " (" + typ + ")")
		}
		parts = append(parts, name)
	}
	return clipUTF8(strings.Join(parts, ", "), maxAttachmentsLine)
}

// cleanBody returns the readable new text of a body, and whether the input was cut
// to bound the work.
func cleanBody(raw string, work int) (string, bool) {
	clipped := false
	if len(raw) > work {
		raw = clipUTF8(raw, work)
		clipped = true
	}
	if looksLikeHTML.MatchString(raw) {
		raw = stripMarkup(raw)
	}
	text := sanitize(raw)
	text = dropQuotes(text)
	text = multiBlank.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text), clipped
}

func stripMarkup(s string) string {
	s = htmlComment.ReplaceAllString(s, "")
	s = htmlBlocks.ReplaceAllString(s, "")
	s = htmlBreaks.ReplaceAllString(s, "\n")
	s = htmlTags.ReplaceAllString(s, "")
	return html.UnescapeString(s)
}

// dropQuotes removes quoted lines, the attribution above them, everything after an
// "Original Message" marker and a signature near the end. Text written between
// quoted blocks is kept, since inline replies are the new content. A forwarded
// message has no such markers and is kept whole: it is the content, not a quote.
func dropQuotes(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if originalMsg.MatchString(t) {
			break
		}
		if strings.HasPrefix(t, ">") || attribution.MatchString(t) {
			continue
		}
		kept = append(kept, strings.TrimRight(l, " \t"))
	}
	for i := len(kept) - 1; i >= 0 && i >= len(kept)-signatureWindowLines; i-- {
		if kept[i] == "--" {
			kept = kept[:i]
			break
		}
	}
	return strings.Join(kept, "\n")
}

// sanitize makes text safe to put in a prompt: valid UTF-8, no control characters,
// and none of the invisible or directional characters that let text read one way
// to a person and another to a model.
func sanitize(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0xad || r == 0xfeff:
			return -1
		case r >= 0x200b && r <= 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x2060 && r <= 0x206f:
			return -1
		}
		return r
	}, s)
}

// field is sanitize for a one-line header value: newlines become spaces, so a
// sender cannot start a forged header line, and the result is clipped.
func field(s string, max int) string {
	s = clipUTF8(s, max*2) // bound the work before mapping a huge value
	s = sanitize(s)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	return clipUTF8(s, max)
}

// clipUTF8 cuts s to at most max bytes without splitting a multi-byte sequence.
func clipUTF8(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
