package mime_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	ivymime "github.com/AutumnsGrove/Ivy/mime"
)

// b64Line is one full base64 line. Its sizes are derived from it, not typed in:
// every 4 characters decode to 3 bytes, and the CRLF is not part of the data.
const (
	b64Line        = "QUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJDQUJD\r\n"
	b64LineWire    = len(b64Line)
	b64LineDecoded = (b64LineWire - 2) / 4 * 3
	// aboutHugeLines is enough lines for roughly 100 MiB on the wire: three times
	// the allocation the tests allow, so only a streaming parser passes.
	aboutHugeLines = int64((100 << 20) / b64LineWire)
)

// repeatLines yields b64Line count times without ever holding them all.
type repeatLines struct {
	left int64
	off  int
}

func (r *repeatLines) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && r.left > 0 {
		c := copy(p[n:], b64Line[r.off:])
		n += c
		r.off += c
		if r.off == len(b64Line) {
			r.off = 0
			r.left--
		}
	}
	return n, nil
}

// hugeAttachment is a text-plus-attachment message whose attachment is
// lines*b64LineWire bytes of base64, produced on demand: it never exists in memory.
func hugeAttachment(lines int64) io.Reader {
	head := "From: a@example.com\r\nReferences: <root@example.com>\r\nSubject: big\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
		"--outer\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello there\r\n" +
		"--outer\r\nContent-Type: application/octet-stream; name=\"big.bin\"\r\n" +
		"Content-Disposition: attachment; filename=\"big.bin\"\r\nContent-Transfer-Encoding: base64\r\n\r\n"
	return io.MultiReader(strings.NewReader(head), &repeatLines{left: lines}, strings.NewReader("\r\n--outer--\r\n"))
}

// totalAlloc runs f and reports how many bytes it allocated, which for a
// streaming parser is bounded by buffers, not by the input.
func totalAlloc(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func smallMultipart() []byte {
	att := base64.StdEncoding.EncodeToString([]byte("attachment bytes"))
	return []byte("From: Alice <a@example.com>\r\nReferences: <root@example.com>\r\nSubject: s\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/alternative; boundary=\"alt\"\r\n\r\n" +
		"--alt\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain body\r\n" +
		"--alt\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>html body</p>\r\n--alt--\r\n" +
		"--outer\r\nContent-Type: text/plain; name=\"note.txt\"\r\nContent-Disposition: attachment; filename=\"note.txt\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + att + "\r\n--outer--\r\n")
}

// Not parallel: it measures allocation, which other running tests would disturb.
func TestParseStreamNeverHoldsLargeAttachment(t *testing.T) {
	const lines = aboutHugeLines
	var p ivymime.Parsed
	var elapsed time.Duration
	alloc := totalAlloc(func() {
		start := time.Now()
		p = ivymime.ParseStream(hugeAttachment(lines))
		elapsed = time.Since(start)
	})

	if alloc > 32<<20 {
		t.Errorf("parsing a 100 MiB message allocated %d MiB; it must stream", alloc>>20)
	}
	// A wall-clock bound measures the machine as much as the parser: under -race on a shared
	// two-core runner, with other packages' tests running, 100 MiB took 4s on a laptop and over
	// 20s in CI (36s on one starved core). The memory bound above is the real streaming check; this
	// only has to catch a parser that stops making progress, so it is far past any loaded run.
	if elapsed > 2*time.Minute {
		t.Errorf("parsing took %v", elapsed)
	}
	if !strings.Contains(p.Text, "hello there") {
		t.Errorf("Text = %q, want the body kept beside the huge attachment", p.Text)
	}
	if len(p.References) != 1 {
		t.Errorf("References = %v, want the header parsed", p.References)
	}
	if len(p.Large) != 1 || p.Large[0].Filename != "big.bin" || !p.Large[0].Attachment {
		t.Fatalf("Large = %+v, want the one big.bin attachment", p.Large)
	}
	if got, want := p.Large[0].Size, lines*int64(b64LineDecoded); got != want {
		t.Errorf("Large[0].Size = %d, want the decoded %d", got, want)
	}
	if p.Large[0].Path != "2" {
		t.Errorf("Large[0].Path = %q, want 2", p.Large[0].Path)
	}
	for _, a := range p.Attachments {
		t.Errorf("attachment %q kept in memory, want it left on disk", a.Filename)
	}
	if p.BodySkipped || len(p.Errors) != 0 {
		t.Errorf("BodySkipped = %v, Errors = %v; want a clean parse", p.BodySkipped, p.Errors)
	}
}

func TestParseStreamMatchesParseOnSmallMessages(t *testing.T) {
	t.Parallel()
	msg := smallMultipart()
	want := ivymime.Parse(msg)
	got := ivymime.ParseStream(bytes.NewReader(msg))

	if got.Text != want.Text || got.HTML != want.HTML || got.Snippet != want.Snippet {
		t.Errorf("text/html/snippet differ:\n got %q %q %q\nwant %q %q %q",
			got.Text, got.HTML, got.Snippet, want.Text, want.HTML, want.Snippet)
	}
	if len(got.Attachments) != 1 || len(got.Large) != 0 {
		t.Fatalf("attachments = %d, large = %d; a small attachment stays in memory", len(got.Attachments), len(got.Large))
	}
	if string(got.Attachments[0].Content) != "attachment bytes" {
		t.Errorf("attachment content = %q", got.Attachments[0].Content)
	}
	if len(got.References) != 1 || got.References[0] != "<root@example.com>" {
		t.Errorf("References = %v", got.References)
	}
}

func TestParseStreamBoundsEverythingItKeeps(t *testing.T) {
	t.Parallel()
	// 200 attachments of 60 KiB each are individually small enough to keep (12
	// MiB together), but the message as a whole may not exceed MaxSkeletonBytes.
	var b strings.Builder
	b.WriteString("From: a@example.com\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n")
	b.WriteString("--b\r\nContent-Type: text/plain\r\n\r\nbody\r\n")
	chunk := strings.Repeat("x", 60<<10)
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "--b\r\nContent-Type: application/octet-stream; name=\"f%d\"\r\nContent-Disposition: attachment; filename=\"f%d\"\r\n\r\n%s\r\n", i, i, chunk)
	}
	b.WriteString("--b--\r\n")

	sk, err := ivymime.BuildSkeleton(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("BuildSkeleton: %v", err)
	}
	if len(sk.Raw) > ivymime.MaxSkeletonBytes+(1<<20) {
		t.Errorf("skeleton is %d bytes, want it bounded near %d", len(sk.Raw), ivymime.MaxSkeletonBytes)
	}
	if len(sk.Large) == 0 {
		t.Error("nothing was left on disk although the parts total 12 MiB")
	}
}

func TestParseStreamKeepsInlineTextBetweenLimits(t *testing.T) {
	t.Parallel()
	// A 1 MiB newsletter body is bigger than an attachment may be but is the
	// message, so it is kept.
	body := strings.Repeat("newsletter line\r\n", 60000)
	msg := "From: a@example.com\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body
	p := ivymime.ParseStream(strings.NewReader(msg))
	if len(p.Large) != 0 || !strings.Contains(p.Text, "newsletter line") {
		t.Errorf("a %d byte inline text body was dropped: large=%v textLen=%d", len(body), p.Large, len(p.Text))
	}
}

func TestParseStreamSurvivesHostileStructure(t *testing.T) {
	t.Parallel()
	cases := map[string][]byte{
		"unterminated nesting": nestedMultipart(60, false),
		"closed deep nesting":  nestedMultipart(ivymime.MaxMultipartDepth+8, true),
		"too many parts":       manyParts(ivymime.MaxParts + 50),
		"huge header block":    []byte("From: a@example.com\r\nX-Big: " + strings.Repeat("a", ivymime.MaxHeaderBytes+10) + "\r\n\r\nbody"),
		"no blank line":        []byte("From: a@example.com"),
		"empty":                {},
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			done := make(chan ivymime.Parsed, 1)
			go func() { done <- ivymime.ParseStream(bytes.NewReader(msg)) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("ParseStream did not return")
			}
		})
	}

	p := ivymime.ParseStream(bytes.NewReader(nestedMultipart(60, false)))
	if !p.BodySkipped || len(p.Errors) == 0 {
		t.Errorf("BodySkipped = %v, Errors = %v; a too-deep body must be reported", p.BodySkipped, p.Errors)
	}
	if len(p.References) != 1 {
		t.Errorf("References = %v; the headers must survive a skipped body", p.References)
	}
}

func manyParts(n int) []byte {
	var b strings.Builder
	b.WriteString("From: a@example.com\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n")
	for i := 0; i < n; i++ {
		b.WriteString("--b\r\nContent-Type: text/plain\r\n\r\nx\r\n")
	}
	b.WriteString("--b--\r\n")
	return []byte(b.String())
}

func TestCopyPartDecodesFromTheStream(t *testing.T) {
	t.Parallel()
	msg := smallMultipart()

	var got bytes.Buffer
	if err := ivymime.CopyPart(bytes.NewReader(msg), "2", &got); err != nil {
		t.Fatalf("CopyPart: %v", err)
	}
	if got.String() != "attachment bytes" {
		t.Errorf("part 2 = %q, want the decoded attachment", got.String())
	}

	got.Reset()
	if err := ivymime.CopyPart(bytes.NewReader(msg), "1.2", &got); err != nil {
		t.Fatalf("CopyPart 1.2: %v", err)
	}
	if got.String() != "<p>html body</p>" {
		t.Errorf("part 1.2 = %q", got.String())
	}

	if err := ivymime.CopyPart(bytes.NewReader(msg), "9", io.Discard); !errors.Is(err, ivymime.ErrPartNotFound) {
		t.Errorf("CopyPart of a missing part = %v, want ErrPartNotFound", err)
	}
}

func TestCopyPartDecodesQuotedPrintable(t *testing.T) {
	t.Parallel()
	msg := "From: a@example.com\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\ncaf=C3=A9 =\r\nau lait\r\n"
	var got bytes.Buffer
	if err := ivymime.CopyPart(strings.NewReader(msg), "1", &got); err != nil {
		t.Fatalf("CopyPart: %v", err)
	}
	if !strings.HasPrefix(got.String(), "café au lait") {
		t.Errorf("decoded = %q", got.String())
	}
}

// Not parallel: it measures allocation.
func TestCopyPartStreamsAHugeAttachment(t *testing.T) {
	const lines = aboutHugeLines
	var n int64
	alloc := totalAlloc(func() {
		var err error
		n, err = copyCount(hugeAttachment(lines), "2")
		if err != nil {
			t.Errorf("CopyPart: %v", err)
		}
	})
	if alloc > 32<<20 {
		t.Errorf("serving a 100 MiB attachment allocated %d MiB; it must stream", alloc>>20)
	}
	if want := lines * int64(b64LineDecoded); n != want {
		t.Errorf("decoded %d bytes, want %d", n, want)
	}
}

func copyCount(r io.Reader, path string) (int64, error) {
	var c countWriter
	err := ivymime.CopyPart(r, path, &c)
	return c.n, err
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

func FuzzParseStream(f *testing.F) {
	f.Add(smallMultipart())
	f.Add(nestedMultipart(ivymime.MaxMultipartDepth, false))
	f.Add(nestedMultipart(ivymime.MaxMultipartDepth+4, true))
	f.Add(manyParts(20))
	f.Add([]byte("From: a@example.com\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\n\r\nx"))
	f.Fuzz(func(t *testing.T, data []byte) {
		start := time.Now()
		p := ivymime.ParseStream(bytes.NewReader(data))
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("ParseStream took %v on a %d-byte input", elapsed, len(data))
		}
		if n := len([]rune(p.Snippet)); n > 200 {
			t.Errorf("Snippet is %d runes, want at most 200", n)
		}
		var sink countWriter
		_ = ivymime.CopyPart(bytes.NewReader(data), "1", &sink)
	})
}
