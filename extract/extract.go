// Package extract turns attachments and documents into plain text for search
// (ARCHITECTURE.md section 6). It is the tier 0 and 1 half of the extraction
// ladder: tier 0 is the message body and calendar invites, tier 1 is digital
// documents (PDF, OOXML). Vision (tier 2) stays in chunk 5.
//
// It holds no state and never returns an error for a document: hostile or
// unreadable input yields a Result with a status and no text, so one bad
// attachment never costs a message its place in the mirror. Every path is
// bounded by MaxInputBytes of input, MaxOutputBytes of text and, for a PDF, a
// page cap; a caller may tighten the time bound through ctx (STANDARDS.md 4a).
package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// Extraction tiers (ARCHITECTURE.md section 6).
const (
	TierBody     = 0 // the message body, a calendar invite
	TierDocument = 1 // a digital PDF or an OOXML document
)

// Result.Status values. A caller records them in extracted_text.status so
// "there was no text here" and "I could not read this" stay distinguishable
// (STANDARDS.md 4a.3).
const (
	StatusOK          = "ok"
	StatusUnsupported = "unsupported"
	StatusEmpty       = "empty"
	StatusTooLarge    = "too_large"
	StatusFailed      = "failed"
)

// Limits (STANDARDS.md 4a). A document larger than MaxInputBytes is not read at
// all; text stops after MaxOutputBytes; a PDF is read to MaxPages.
const (
	MaxInputBytes  = 32 << 20
	MaxOutputBytes = 1 << 20
	MaxPages       = 500
)

// maxOOXMLInflate bounds the markup read from one OOXML archive in total. Each
// part is capped at MaxInputBytes, but an archive of a few KiB can hold
// thousands of parts that each inflate to that, so the sum needs its own ceiling.
// A var only so a test can lower it.
var maxOOXMLInflate int64 = 64 << 20

// Result is the plain text of one document. Text is empty unless Status is
// StatusOK.
type Result struct {
	Text   string
	Tier   int
	Status string
}

type kind int

const (
	kindOther kind = iota
	kindICS
	kindPDF
	kindOOXML
)

// Extract returns the plain text of one attachment's decoded bytes. filename
// and contentType are hints; either may be empty. It never panics: the PDF
// library is unexported-recovered internally, and the OOXML walk is bounded.
func Extract(ctx context.Context, filename, contentType string, data []byte) Result {
	if len(data) == 0 {
		return Result{Status: StatusEmpty}
	}
	if int64(len(data)) > MaxInputBytes {
		return Result{Status: StatusTooLarge}
	}
	if ctx.Err() != nil {
		return Result{Status: StatusFailed}
	}
	switch classify(filename, contentType) {
	case kindICS:
		return finish(TierBody, calendarText(string(data)))
	case kindPDF:
		return extractPDF(ctx, data)
	case kindOOXML:
		return extractOOXML(ctx, data)
	default:
		return Result{Status: StatusUnsupported}
	}
}

// classify decides the reader from the content type first and the filename
// second, because a sender controls both and the content type is the more
// specific claim.
func classify(filename, contentType string) kind {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "text/calendar", "application/ics":
		return kindICS
	case "application/pdf":
		return kindPDF
	}
	if strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.") {
		return kindOOXML
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".ics", ".ical", ".ifb":
		return kindICS
	case ".pdf":
		return kindPDF
	case ".docx", ".xlsx", ".pptx":
		return kindOOXML
	}
	return kindOther
}

// finish cleans the text, applies the output cap and decides the status.
func finish(tier int, text string) Result {
	text = clean(text)
	if text == "" {
		return Result{Tier: tier, Status: StatusEmpty}
	}
	return Result{Text: truncate(text, MaxOutputBytes), Tier: tier, Status: StatusOK}
}

// clean collapses whitespace: runs of spaces and tabs become one space, blank
// lines are dropped, and the result is trimmed. The extracted text is fed to
// both a tokenizer and an embedding model, neither of which wants the layout.
func clean(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true // start trimmed
	newlines := 0
	for _, r := range s {
		if r == '\r' {
			continue
		}
		if r == '\n' {
			if newlines >= 1 {
				continue // at most one newline in a row
			}
			newlines++
			space = false
			b.WriteByte('\n')
			continue
		}
		if unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		newlines = 0
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// truncate cuts s to at most n bytes on a rune boundary, so the text stays
// valid UTF-8 for SQLite.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// extractPDF reads the text layer of a digital PDF. A scanned PDF has no text
// layer, so it yields StatusEmpty here; routing those to vision is chunk 5. The
// library is wrapped because it is unexported-recovered internally but a
// malformed file can still return odd values.
func extractPDF(ctx context.Context, data []byte) (res Result) {
	defer func() {
		if recover() != nil {
			res = Result{Tier: TierDocument, Status: StatusFailed}
		}
	}()

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Result{Tier: TierDocument, Status: StatusFailed}
	}
	pages := reader.NumPage()
	if pages > MaxPages {
		pages = MaxPages
	}
	var out strings.Builder
	for i := 1; i <= pages; i++ {
		if ctx.Err() != nil {
			return Result{Tier: TierDocument, Status: StatusFailed}
		}
		text, err := reader.Page(i).GetPlainText(nil)
		if err != nil {
			// One unreadable page does not lose the pages that read: keep what
			// was found and carry on.
			continue
		}
		out.WriteString(text)
		out.WriteByte('\n')
		if out.Len() >= MaxOutputBytes {
			break
		}
	}
	return finish(TierDocument, out.String())
}

// extractOOXML reads the text of a Word, Excel or PowerPoint file. It walks
// only the parts that carry text and ignores everything else, so an embedded
// object or a macro never reaches the caller.
func extractOOXML(ctx context.Context, data []byte) Result {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Result{Tier: TierDocument, Status: StatusFailed}
	}
	var out strings.Builder
	budget := maxOOXMLInflate
	for _, f := range zr.File {
		if !isTextPart(f.Name) {
			continue
		}
		if budget <= 0 {
			break
		}
		read, err := appendXMLText(ctx, &out, f, MaxOutputBytes-out.Len(), budget)
		budget -= read
		if ctx.Err() != nil {
			return Result{Tier: TierDocument, Status: StatusFailed}
		}
		if err != nil {
			continue // one unreadable part does not lose the others
		}
		if out.Len() >= MaxOutputBytes {
			break
		}
	}
	return finish(TierDocument, out.String())
}

// countingReader tells appendXMLText how much of the archive's inflation budget
// a part used, whether or not the part parsed.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// isTextPart selects the document parts that hold user text, by prefix so a
// zip-slip path or a multi-part workbook still matches.
func isTextPart(name string) bool {
	switch {
	case name == "word/document.xml":
		return true
	case strings.HasPrefix(name, "word/header"), strings.HasPrefix(name, "word/footer"):
		return true
	case name == "xl/sharedStrings.xml":
		return true
	case strings.HasPrefix(name, "xl/worksheets/"):
		return true
	case strings.HasPrefix(name, "ppt/slides/slide"):
		return true
	case strings.HasPrefix(name, "ppt/notesSlides/"):
		return true
	}
	return false
}

// appendXMLText streams one XML part and appends its character data, inserting
// a space at each text run so adjacent runs do not glue together. xml.Decoder
// does not resolve external entities, so a crafted file cannot make it fetch
// anything. It reads at most budget bytes of markup (and never more than
// MaxInputBytes), returns how many it read, and stops when ctx is done: a part
// of nothing but tags yields no text, so the output limit never stops it.
func appendXMLText(ctx context.Context, out *strings.Builder, f *zip.File, limit int, budget int64) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = rc.Close() }()

	// limit is the room left for this part; out already holds earlier parts' text.
	ceiling := out.Len() + limit
	src := &countingReader{r: io.LimitReader(rc, min(budget, MaxInputBytes))}
	dec := xml.NewDecoder(src)
	for tokens := 1; ; tokens++ {
		if tokens%1024 == 0 && ctx.Err() != nil {
			return src.n, ctx.Err()
		}
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return src.n, nil
		}
		if err != nil {
			return src.n, err
		}
		if cd, ok := tok.(xml.CharData); ok {
			out.Write(cd)
			out.WriteByte(' ')
		}
		if out.Len() >= ceiling {
			return src.n, nil
		}
	}
}

// calendarText pulls the human fields out of an iCalendar body. It reads the
// unfolded property lines and ignores everything it does not name, so a huge
// or hostile calendar contributes only its summary and description.
func calendarText(s string) string {
	lines := unfold(s)
	var b strings.Builder
	for _, line := range lines {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.ToUpper(strings.TrimSpace(name))
		if i := strings.IndexByte(name, ';'); i >= 0 {
			name = name[:i] // drop parameters such as DTSTART;TZID=...
		}
		switch name {
		case "SUMMARY", "DESCRIPTION", "LOCATION", "ORGANIZER", "DTSTART", "DTEND", "UID":
			b.WriteString(unescapeICS(value))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// unfold joins RFC 5545 folded lines: a line that begins with a space or tab
// continues the previous one. The line being built is a Builder, not a string
// appended to in place: a sender can fold one property hundreds of thousands of
// times, and re-copying the growing line on every fold is quadratic.
func unfold(s string) []string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	var cur strings.Builder
	open := false
	flush := func() {
		if open {
			out = append(out, cur.String())
			cur.Reset()
			open = false
		}
	}
	for _, line := range raw {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if (line[0] == ' ' || line[0] == '\t') && open {
			cur.WriteString(line[1:])
			continue
		}
		flush()
		cur.WriteString(line)
		open = true
	}
	flush()
	return out
}

// unescapeICS reverses the RFC 5545 text escapes in one property value.
func unescapeICS(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		default:
			b.WriteByte(s[i]) // \\ \, \; and anything else lose the backslash
		}
	}
	return b.String()
}
