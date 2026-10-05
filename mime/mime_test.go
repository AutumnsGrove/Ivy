package mime_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ivymime "github.com/AutumnsGrove/Ivy/mime"
)

// raw builds a CRLF message so every case reads like wire input, not Go strings.
func raw(headers, body string) []byte {
	h := strings.ReplaceAll(headers, "\n", "\r\n")
	b := strings.ReplaceAll(body, "\n", "\r\n")
	return []byte(h + "\r\n\r\n" + b)
}

func TestParseTextOnly(t *testing.T) {
	t.Parallel()
	p := ivymime.Parse(raw(
		`From: Alice <alice@example.com>
To: me@grove.test
Subject: Hello
Message-ID: <m1@example.com>
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8`,
		"Hello, world!\n",
	))
	if !strings.Contains(p.Text, "Hello, world!") {
		t.Errorf("Text = %q, want it to contain the body", p.Text)
	}
	if p.HTML != "" {
		t.Errorf("HTML = %q, want empty for a text-only message", p.HTML)
	}
	if p.Snippet != "Hello, world!" {
		t.Errorf("Snippet = %q, want %q", p.Snippet, "Hello, world!")
	}
	if len(p.Errors) != 0 {
		t.Errorf("Errors = %v, want none", p.Errors)
	}
	if len(p.Attachments) != 0 || len(p.Inlines) != 0 {
		t.Errorf("parts = %d attachments / %d inlines, want none", len(p.Attachments), len(p.Inlines))
	}
}

func TestParseMultipartAlternative(t *testing.T) {
	t.Parallel()
	p := ivymime.Parse(raw(
		`Message-ID: <alt@example.com>
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="ALT"`,
		`--ALT
Content-Type: text/plain; charset=utf-8

plain body
--ALT
Content-Type: text/html; charset=utf-8

<p>html <b>body</b></p>
--ALT--`,
	))
	if strings.TrimSpace(p.Text) != "plain body" {
		t.Errorf("Text = %q, want %q", p.Text, "plain body")
	}
	if !strings.Contains(p.HTML, "<b>body</b>") {
		t.Errorf("HTML = %q, want the html part", p.HTML)
	}
	if len(p.Errors) != 0 {
		t.Errorf("Errors = %v, want none", p.Errors)
	}
}

func TestParseAttachment(t *testing.T) {
	t.Parallel()
	p := ivymime.Parse(raw(
		`Message-ID: <att@example.com>
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="MIX"`,
		`--MIX
Content-Type: text/plain; charset=utf-8

see attached
--MIX
Content-Type: application/pdf; name="report.pdf"
Content-Transfer-Encoding: base64
Content-Disposition: attachment; filename="report.pdf"

JVBERi0xLjQK
--MIX--`,
	))
	if len(p.Attachments) != 1 {
		t.Fatalf("Attachments = %d, want 1", len(p.Attachments))
	}
	a := p.Attachments[0]
	if a.Filename != "report.pdf" || a.ContentType != "application/pdf" {
		t.Errorf("attachment = %+v, want report.pdf application/pdf", a)
	}
	if string(a.Content) != "%PDF-1.4\n" {
		t.Errorf("attachment content = %q, want the decoded PDF magic", a.Content)
	}
	if a.Inline {
		t.Error("attachment Inline = true, want false")
	}
	if len(p.Inlines) != 0 {
		t.Errorf("Inlines = %d, want 0", len(p.Inlines))
	}
}

func TestParseInlineCID(t *testing.T) {
	t.Parallel()
	p := ivymime.Parse(raw(
		`Message-ID: <inline@example.com>
MIME-Version: 1.0
Content-Type: multipart/related; boundary="REL"`,
		`--REL
Content-Type: text/html; charset=utf-8

<p><img src="cid:chart.png" alt="chart"></p>
--REL
Content-Type: image/png; name="chart.png"
Content-Transfer-Encoding: base64
Content-ID: <chart.png>
Content-Disposition: inline; filename="chart.png"

aGVsbG8=
--REL--`,
	))
	if len(p.Inlines) != 1 {
		t.Fatalf("Inlines = %d, want 1", len(p.Inlines))
	}
	img := p.Inlines[0]
	if img.ContentID != "chart.png" {
		t.Errorf("ContentID = %q, want chart.png without angle brackets", img.ContentID)
	}
	if !img.Inline || img.ContentType != "image/png" {
		t.Errorf("inline part = %+v, want an inline png", img)
	}
	if string(img.Content) != "hello" {
		t.Errorf("inline content = %q, want hello", img.Content)
	}
	if len(p.Attachments) != 0 {
		t.Errorf("Attachments = %d, want 0 (an inline is not an attachment)", len(p.Attachments))
	}
}

func TestParseReferencesAndAddressHeaders(t *testing.T) {
	t.Parallel()
	p := ivymime.Parse(raw(
		`From: website@example.com
To: me@grove.test
Reply-To: Visitor Name <visitor@example.com>
Delivered-To: me@grove.test
In-Reply-To: <parent@example.com>
References: <root@example.com> <parent@example.com>
Message-ID: <child@example.com>
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8`,
		"body",
	))
	wantRefs := []string{"<root@example.com>", "<parent@example.com>"}
	if strings.Join(p.References, " ") != strings.Join(wantRefs, " ") {
		t.Errorf("References = %v, want %v", p.References, wantRefs)
	}
	if strings.Join(p.InReplyTo, " ") != "<parent@example.com>" {
		t.Errorf("InReplyTo = %v, want [<parent@example.com>]", p.InReplyTo)
	}
	if len(p.ReplyTo) != 1 || p.ReplyTo[0].Address != "visitor@example.com" {
		t.Errorf("ReplyTo = %+v, want visitor@example.com", p.ReplyTo)
	} else if p.ReplyTo[0].Name != "Visitor Name" {
		t.Errorf("ReplyTo name = %q, want Visitor Name", p.ReplyTo[0].Name)
	}
	if len(p.DeliveredTo) != 1 || p.DeliveredTo[0].Address != "me@grove.test" {
		t.Errorf("DeliveredTo = %+v, want me@grove.test", p.DeliveredTo)
	}
}

func TestParseAuthResultsHeader(t *testing.T) {
	t.Parallel()
	p := ivymime.Parse(raw(
		`Authentication-Results: example.com; spf=pass smtp.mailfrom=example.com; dkim=pass; dmarc=pass
Message-ID: <auth@example.com>
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8`,
		"body",
	), "example.com")
	if p.Auth.SPF != "pass" || p.Auth.DKIM != "pass" || p.Auth.DMARC != "pass" {
		t.Errorf("Auth = %+v, want all pass", p.Auth)
	}
	if p.Auth.AuthservID != "example.com" {
		t.Errorf("Auth.AuthservID = %q, want example.com", p.Auth.AuthservID)
	}
	if len(p.Auth.Raw) != 1 {
		t.Errorf("Auth.Raw = %v, want one raw value", p.Auth.Raw)
	}
}

func TestParseAuthResults(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		values  []string
		trusted []string
		want    ivymime.AuthResults
	}{
		{
			name:   "no headers",
			values: nil,
			want:   ivymime.AuthResults{},
		},
		{
			name:    "purelymail adds only an unknown auth method",
			values:  []string{"mail.purelymail.com; auth=pass"},
			trusted: []string{"mail.purelymail.com"},
			want:    ivymime.AuthResults{AuthservID: "mail.purelymail.com", Raw: []string{"mail.purelymail.com; auth=pass"}},
		},
		{
			name:    "topmost trusted header wins",
			values:  []string{"mx.example.net; dmarc=fail", "other.example; spf=pass; dkim=fail"},
			trusted: []string{"mx.example.net", "other.example"},
			want:    ivymime.AuthResults{AuthservID: "mx.example.net", DMARC: "fail", Raw: []string{"mx.example.net; dmarc=fail", "other.example; spf=pass; dkim=fail"}},
		},
		{
			name:    "any dkim pass wins within a header",
			values:  []string{"mx.example.net; dkim=fail reason=bad; dkim=pass header.d=example.com"},
			trusted: []string{"mx.example.net"},
			want:    ivymime.AuthResults{AuthservID: "mx.example.net", DKIM: "pass", Raw: []string{"mx.example.net; dkim=fail reason=bad; dkim=pass header.d=example.com"}},
		},
		{
			name:    "case and spacing are ignored",
			values:  []string{"MX.Example.NET ; SPF=Pass ; DKIM=Pass ; DMARC=FAIL"},
			trusted: []string{"mx.example.net"},
			want:    ivymime.AuthResults{AuthservID: "mx.example.net", SPF: "pass", DKIM: "pass", DMARC: "fail", Raw: []string{"MX.Example.NET ; SPF=Pass ; DKIM=Pass ; DMARC=FAIL"}},
		},
		{
			name:    "unknown methods are ignored",
			values:  []string{"mx.example.net; iprev=pass; auth=pass; dmarc=pass"},
			trusted: []string{"mx.example.net"},
			want:    ivymime.AuthResults{AuthservID: "mx.example.net", DMARC: "pass", Raw: []string{"mx.example.net; iprev=pass; auth=pass; dmarc=pass"}},
		},
		{
			name:   "malformed input yields no verdicts",
			values: []string{";;; = = =", ""},
			want:   ivymime.AuthResults{Raw: []string{";;; = = =", ""}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ivymime.ParseAuthResults(tc.values, tc.trusted)
			if got.SPF != tc.want.SPF || got.DKIM != tc.want.DKIM || got.DMARC != tc.want.DMARC || got.AuthservID != tc.want.AuthservID {
				t.Errorf("verdicts = %+v, want %+v", got, tc.want)
			}
			if strings.Join(got.Raw, "\x00") != strings.Join(tc.want.Raw, "\x00") {
				t.Errorf("Raw = %q, want %q", got.Raw, tc.want.Raw)
			}
		})
	}
}

func TestParseFoldedAuthResults(t *testing.T) {
	t.Parallel()
	p := ivymime.Parse(raw(
		`Authentication-Results: mx.example.net;
	spf=pass smtp.mailfrom=example.com;
	dkim=pass;
	dmarc=pass
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8`,
		"body",
	), "mx.example.net")
	if p.Auth.SPF != "pass" || p.Auth.DKIM != "pass" || p.Auth.DMARC != "pass" {
		t.Errorf("Auth = %+v, want folded header unfolded and parsed", p.Auth)
	}
}

func TestSnippet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"collapses whitespace", "Hello,\n\n  world!   This\tis  a body.", "Hello, world! This is a body."},
		{"empty", "   \n  ", ""},
		{"keeps unicode runes intact", strings.Repeat("é", 250), strings.Repeat("é", 200)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ivymime.Snippet(tc.in); got != tc.want {
				t.Errorf("Snippet = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseHTMLOnlyDerivesTextWithoutNoise(t *testing.T) {
	t.Parallel()
	p := ivymime.Parse(raw(
		`Message-ID: <html@example.com>
MIME-Version: 1.0
Content-Type: text/html; charset=utf-8`,
		"<html><body><p>Only HTML here.</p></body></html>",
	))
	if !strings.Contains(p.Text, "Only HTML here.") {
		t.Errorf("Text = %q, want the down-converted HTML", p.Text)
	}
	// Switching HTML to text is normal for HTML-only mail, not an error worth
	// storing on every such message.
	for _, e := range p.Errors {
		if strings.Contains(e, "Plain Text from HTML") {
			t.Errorf("Errors = %v, want the routine HTML-to-text note filtered out", p.Errors)
		}
	}
}

func TestParseMalformedStillReturnsWhatItCould(t *testing.T) {
	t.Parallel()
	p := ivymime.Parse([]byte("not a message at all \x00\x01\x02"))
	if p.Snippet != "" && len(p.Errors) == 0 {
		t.Errorf("Parsed = %+v, want either a snippet or a recorded error", p)
	}
}

func TestParseCorpus(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "internal", "mailworld", "testdata", "corpus")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("corpus is empty")
	}
	for _, entry := range entries {
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			p := ivymime.Parse(data)
			switch name {
			case "charset-latin1.eml":
				if !strings.Contains(p.Text, "café") {
					t.Errorf("Text = %q, want the ISO-8859-1 body decoded", p.Text)
				}
			case "rtl-emoji.eml":
				if !strings.Contains(p.Text, "🌿") {
					t.Errorf("Text = %q, want the emoji preserved", p.Text)
				}
			case "xss.html.eml":
				// Parsing must not sanitise; render/ owns that pass.
				if !strings.Contains(p.HTML, "<script>") {
					t.Errorf("HTML = %q, want raw markup kept for render to sanitise", p.HTML)
				}
			}
		})
	}
}

// TestParseNastyCorpus walks the parser's own hand-built corpus: the shapes
// TESTING.md section 3 names that a real mailbox still has to survive.
func TestParseNastyCorpus(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("testdata", "corpus")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("corpus is empty")
	}
	for _, entry := range entries {
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			p := ivymime.Parse(data)
			switch name {
			case "nested-rfc822.eml":
				if !strings.Contains(p.Text, "Forwarding the original") {
					t.Errorf("Text = %q, want the outer text part", p.Text)
				}
			case "broken-boundary.eml":
				if !strings.Contains(p.Text, "unterminated") {
					t.Errorf("Text = %q, want the part before the missing boundary", p.Text)
				}
			}
		})
	}
}

// TestParseHostileShapes feeds the parser generated edge cases that are awkward
// to keep as files: non-UTF-8 bytes under a utf-8 declaration and a header far
// larger than a normal one.
func TestParseHostileShapes(t *testing.T) {
	t.Parallel()

	mismatched := append(
		[]byte("Subject: caf\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nCaf"),
		0xE9, '\n',
	)
	if p := ivymime.Parse(mismatched); !strings.Contains(p.Text, "Caf") {
		t.Errorf("mismatched charset Text = %q, want the bytes decoded loosely", p.Text)
	}

	huge := raw("Subject: "+strings.Repeat("a", 50_000)+"\nMIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8", "body")
	if p := ivymime.Parse(huge); !strings.Contains(p.Text, "body") {
		t.Errorf("huge header Text = %q, want the body", p.Text)
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"From: a@example.com\r\nSubject: hi\r\n\r\nbody",
		"MIME-Version: 1.0\r\nContent-Type: text/html\r\n\r\n<p>hi</p>",
		"From: a@example.com\r\nSubject: 日本語\n\nこんにちは",
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n--b\r\n\r\nx",
		"Authentication-Results: mx; spf=pass\r\n\r\n",
	}
	dir := filepath.Join("..", "internal", "mailworld", "testdata", "corpus")
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if data, err := os.ReadFile(filepath.Join(dir, entry.Name())); err == nil {
				seeds = append(seeds, string(data))
			}
		}
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	// Unterminated nesting at and past the limit: the shape that once ran for minutes.
	f.Add(nestedMultipart(ivymime.MaxMultipartDepth, false))
	f.Add(nestedMultipart(ivymime.MaxMultipartDepth+4, false))
	f.Fuzz(func(t *testing.T, data []byte) {
		start := time.Now()
		p := ivymime.Parse(data)
		// Parse runs on the sync path for every message, so a slow input is a
		// denial of service even when it returns the right answer. The bound is for a parser that
		// runs away (the nesting seeds once took minutes): a 980-byte seed takes 0.02s normally but
		// 2s under -race on a loaded CI runner, so a tight bound only measures the runner.
		if elapsed := time.Since(start); elapsed > 30*time.Second {
			t.Errorf("Parse took %v on a %d-byte input", elapsed, len(data))
		}
		if n := len([]rune(p.Snippet)); n > 200 {
			t.Errorf("Snippet is %d runes, want at most 200", n)
		}
	})
}

func FuzzParseAuthResults(f *testing.F) {
	f.Add("mx; spf=pass; dkim=pass; dmarc=fail")
	f.Add("")
	f.Add(";;; = = =")
	f.Add("mx; dkim=fail; dkim=pass")
	f.Fuzz(func(t *testing.T, value string) {
		got := ivymime.ParseAuthResults([]string{value}, []string{"mx"})
		// The result token is whatever follows "method=", so the parser's
		// contract is only that a recorded verdict is a trimmed lowercase token
		// with no trailing properties (extension verdicts are allowed).
		for _, verdict := range []string{got.SPF, got.DKIM, got.DMARC} {
			if verdict == "" {
				continue
			}
			if verdict != strings.ToLower(verdict) || strings.ContainsAny(verdict, "; \t\r\n") {
				t.Errorf("verdict %q from %q is not a clean result token", verdict, value)
			}
		}
	})
}

// nestedMultipart builds depth multipart/mixed layers around one text part. An
// unclosed message models a truncated or hostile one; enmime's cost on those
// doubles with every level, so depth 40 would run for minutes.
func nestedMultipart(depth int, closed bool) []byte {
	var b strings.Builder
	b.WriteString("From: a@example.com\r\nReferences: <root@example.com>\r\nMIME-Version: 1.0\r\n")
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=\"b%d\"\r\n\r\n--b%d\r\n", i, i)
	}
	b.WriteString("Content-Type: text/plain\r\n\r\nhi\r\n")
	if closed {
		for i := depth - 1; i >= 0; i-- {
			fmt.Fprintf(&b, "--b%d--\r\n", i)
		}
	}
	return []byte(b.String())
}

func TestParseBoundsUnterminatedNesting(t *testing.T) {
	t.Parallel()
	done := make(chan ivymime.Parsed, 1)
	go func() { done <- ivymime.Parse(nestedMultipart(40, false)) }()

	select {
	case p := <-done:
		if len(p.References) != 1 {
			t.Errorf("headers were lost: References = %v", p.References)
		}
		if len(p.Errors) == 0 {
			t.Error("a message too deep to parse should record why its body was skipped")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Parse did not return for 40 unterminated multipart levels")
	}
}

// Legitimate deep nesting (a message forwarded several times) still parses.
func TestParseKeepsRealisticNesting(t *testing.T) {
	t.Parallel()
	p := ivymime.Parse(nestedMultipart(ivymime.MaxMultipartDepth, true))
	if p.Text != "hi" || len(p.Errors) != 0 {
		t.Errorf("depth %d closed: text=%q errors=%v", ivymime.MaxMultipartDepth, p.Text, p.Errors)
	}
}

// Mailers routinely fold the boundary parameter onto a continuation line and
// leave it unquoted; the depth guard must see those too.
func TestParseBoundsFoldedUnquotedNesting(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("From: a@example.com\r\nMIME-Version: 1.0\r\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "Content-Type: multipart/mixed;\r\n boundary=b%d\r\n\r\n--b%d\r\n", i, i)
	}
	b.WriteString("Content-Type: text/plain\r\n\r\nhi\r\n")

	done := make(chan ivymime.Parsed, 1)
	go func() { done <- ivymime.Parse([]byte(b.String())) }()
	select {
	case p := <-done:
		if len(p.Errors) == 0 {
			t.Error("expected the depth error to be recorded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Parse did not return for folded, unquoted unterminated nesting")
	}
}
