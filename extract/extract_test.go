package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestExtractCalendarInvite(t *testing.T) {
	ics := strings.Join([]string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"BEGIN:VEVENT",
		"UID:abc-123@example.com",
		"SUMMARY:Quarterly review",
		"LOCATION:Room 4",
		"DTSTART;TZID=Europe/London:20261006T140000",
		"DESCRIPTION:Agenda\\n- budget\\n- roadmap",
		"END:VEVENT",
		"END:VCALENDAR",
	}, "\r\n")

	got := Extract(context.Background(), "invite.ics", "text/calendar", []byte(ics))
	if got.Status != StatusOK || got.Tier != TierBody {
		t.Fatalf("status/tier = %q/%d, want %q/%d", got.Status, got.Tier, StatusOK, TierBody)
	}
	for _, want := range []string{"Quarterly review", "Room 4", "20261006T140000", "budget"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text %q is missing %q", got.Text, want)
		}
	}
}

func TestExtractCalendarUnfoldsLongLines(t *testing.T) {
	// A folded SUMMARY: the continuation's leading space is the fold indicator
	// (RFC 5545) and is dropped; a meaningful space is kept before the fold.
	ics := "BEGIN:VEVENT\r\nSUMMARY:Domain renewal for \r\n example.com\r\nEND:VEVENT\r\n"
	got := Extract(context.Background(), "", "text/calendar", []byte(ics))
	if !strings.Contains(got.Text, "Domain renewal for example.com") {
		t.Fatalf("folded line not joined: %q", got.Text)
	}
}

func TestExtractPDF(t *testing.T) {
	data := buildPDF("Hello Ivy search")
	got := Extract(context.Background(), "note.pdf", "application/pdf", data)
	if got.Status != StatusOK || got.Tier != TierDocument {
		t.Fatalf("status/tier = %q/%d, want %q/%d (%q)", got.Status, got.Tier, StatusOK, TierDocument, got.Text)
	}
	if !strings.Contains(got.Text, "Hello Ivy search") {
		t.Errorf("text %q is missing the PDF's words", got.Text)
	}
}

func TestExtractCorruptPDFDoesNotPanic(t *testing.T) {
	// Every prefix of a valid PDF must return cleanly: a hostile truncated file
	// is the common case, and one panic would kill a sync worker.
	full := buildPDF("Truncation test")
	for n := 0; n < len(full); n += 7 {
		got := Extract(context.Background(), "x.pdf", "application/pdf", full[:n])
		switch got.Status {
		case StatusFailed, StatusEmpty, StatusOK, StatusUnsupported, StatusTooLarge:
		default:
			t.Fatalf("prefix %d: unknown status %q", n, got.Status)
		}
	}
}

func TestExtractDocx(t *testing.T) {
	// Two runs in one paragraph must not glue; a second paragraph must be
	// separated as well.
	data := buildZip(t, map[string]string{
		"word/document.xml": `<w:document><w:body>` +
			`<w:p><w:r><w:t>Quarterly</w:t></w:r><w:r><w:t>report</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>Revenue up</w:t></w:r></w:p>` +
			`</w:body></w:document>`,
	})
	got := Extract(context.Background(), "report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data)
	if got.Status != StatusOK || got.Tier != TierDocument {
		t.Fatalf("status/tier = %q/%d, want %q/%d", got.Status, got.Tier, StatusOK, TierDocument)
	}
	for _, want := range []string{"Quarterly", "report", "Revenue up"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text %q is missing %q", got.Text, want)
		}
	}
}

func TestExtractXLSXFromSharedStrings(t *testing.T) {
	data := buildZip(t, map[string]string{
		"xl/sharedStrings.xml": `<sst><si><t>Invoice</t></si><si><t>Acme Ltd</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row><c><v>0</v></c></row>` +
			`<row><c><t>inline note</t></c></row></sheetData></worksheet>`,
	})
	got := Extract(context.Background(), "book.xlsx", "", data)
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want %q", got.Status, StatusOK)
	}
	for _, want := range []string{"Invoice", "Acme Ltd", "inline note"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text %q is missing %q", got.Text, want)
		}
	}
}

func TestExtractUnsupported(t *testing.T) {
	got := Extract(context.Background(), "photo.heic", "image/heic", []byte{0, 1, 2, 3})
	if got.Status != StatusUnsupported || got.Text != "" {
		t.Fatalf("got %+v, want unsupported with no text", got)
	}
}

func TestExtractEmpty(t *testing.T) {
	if got := Extract(context.Background(), "x.pdf", "application/pdf", nil); got.Status != StatusEmpty {
		t.Fatalf("status = %q, want %q", got.Status, StatusEmpty)
	}
}

func TestExtractTooLarge(t *testing.T) {
	big := make([]byte, MaxInputBytes+1)
	if got := Extract(context.Background(), "x.pdf", "application/pdf", big); got.Status != StatusTooLarge {
		t.Fatalf("status = %q, want %q", got.Status, StatusTooLarge)
	}
}

func TestExtractCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := Extract(ctx, "x.pdf", "application/pdf", buildPDF("hello"))
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, StatusFailed)
	}
}

func TestExtractOutputIsBounded(t *testing.T) {
	// A calendar with a huge description must be cut to MaxOutputBytes, on a
	// rune boundary so the text stays valid UTF-8.
	var b strings.Builder
	b.WriteString("BEGIN:VEVENT\r\nDESCRIPTION:")
	for b.Len() < MaxOutputBytes+64 {
		b.WriteString("é") // two bytes, so a naive byte cut would split it
	}
	b.WriteString("\r\nEND:VEVENT\r\n")
	got := Extract(context.Background(), "big.ics", "text/calendar", []byte(b.String()))
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want %q", got.Status, StatusOK)
	}
	if len(got.Text) > MaxOutputBytes {
		t.Fatalf("text is %d bytes, over the %d cap", len(got.Text), MaxOutputBytes)
	}
	if !utf8Valid(got.Text) {
		t.Fatal("truncated text is not valid UTF-8")
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// buildPDF assembles a one-page PDF with a text layer, computing the xref
// offsets so the real reader can open it.
func buildPDF(text string) []byte {
	var b bytes.Buffer
	var offsets []int
	writeObj := func(n int, body string) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	b.WriteString("%PDF-1.4\n")
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	content := fmt.Sprintf("BT /F1 24 Tf 72 720 Td (%s) Tj ET", text)
	writeObj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	writeObj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	xref := b.Len()
	b.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return b.Bytes()
}

// buildZip packs named text parts into an in-memory OOXML-shaped zip.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}
