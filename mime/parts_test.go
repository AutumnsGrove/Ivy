package mime_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	ivymime "github.com/AutumnsGrove/Ivy/mime"
)

// partMessage is a message with a plain body, an HTML body, a file attachment
// and an inline cid: image, so every kind of leaf is present exactly once.
func partMessage() []byte {
	att := "attachment bytes"
	img := "tiny png"
	return []byte("From: Alice <alice@example.com>\r\nSubject: parts\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/alternative; boundary=\"alt\"\r\n\r\n" +
		"--alt\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain body\r\n" +
		"--alt\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>html <img src=\"cid:logo@example\"></p>\r\n--alt--\r\n" +
		"--outer\r\nContent-Type: text/plain; name=\"note.txt\"\r\nContent-Disposition: attachment; filename=\"note.txt\"\r\n\r\n" + att + "\r\n" +
		"--outer\r\nContent-Type: image/png; name=\"logo.png\"\r\nContent-Disposition: inline; filename=\"logo.png\"\r\nContent-ID: <logo@example>\r\n\r\n" + img + "\r\n" +
		"--outer--\r\n")
}

func TestListPartsFindsEveryLeaf(t *testing.T) {
	t.Parallel()
	parts, err := ivymime.ListParts(bytes.NewReader(partMessage()))
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(parts) != 4 {
		t.Fatalf("got %d parts, want 4: %+v", len(parts), parts)
	}

	byPath := map[string]ivymime.PartInfo{}
	for _, p := range parts {
		byPath[p.Path] = p
	}

	// The walker records each leaf with the path CopyPart needs, the decoded
	// size and, for a cid part, the content id (angle brackets stripped).
	att, ok := byPath["2"]
	if !ok {
		t.Fatalf("no part at path 2: %+v", parts)
	}
	if att.Filename != "note.txt" || att.ContentType != "text/plain" || att.Size != int64(len("attachment bytes")) {
		t.Errorf("attachment = %+v", att)
	}
	if !att.Attachment || att.CID != "" {
		t.Errorf("attachment flags = attachment:%v cid:%q", att.Attachment, att.CID)
	}

	img, ok := byPath["3"]
	if !ok {
		t.Fatalf("no part at path 3: %+v", parts)
	}
	if img.Filename != "logo.png" || img.ContentType != "image/png" || img.Size != int64(len("tiny png")) {
		t.Errorf("inline = %+v", img)
	}
	if img.CID != "logo@example" || !img.Inline {
		t.Errorf("inline cid = %q, inline:%v", img.CID, img.Inline)
	}

	// The message body parts are listed too, so the caller decides what is an
	// attachment; neither is one.
	body, ok := byPath["1.1"]
	if !ok {
		t.Fatalf("no body part at 1.1: %+v", parts)
	}
	if body.Attachment || body.CID != "" {
		t.Errorf("body part should not be an attachment: %+v", body)
	}
}

// Not parallel: it measures allocation.
func TestListPartsStreamsAHugeAttachment(t *testing.T) {
	const lines = aboutHugeLines / 2
	var parts []ivymime.PartInfo
	alloc := totalAlloc(func() {
		var err error
		parts, err = ivymime.ListParts(hugeAttachment(lines))
		if err != nil {
			t.Fatalf("ListParts: %v", err)
		}
	})
	if alloc > 32<<20 {
		t.Errorf("listing a 50 MiB attachment allocated %d MiB; it must stream", alloc>>20)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(parts))
	}
	if want := lines * int64(b64LineDecoded); parts[1].Size != want {
		t.Errorf("attachment size = %d, want decoded %d", parts[1].Size, want)
	}
}

func TestListPartsBoundsHostileStructure(t *testing.T) {
	t.Parallel()
	_, err := ivymime.ListParts(bytes.NewReader(manyParts(ivymime.MaxParts + 4)))
	if !errors.Is(err, ivymime.ErrTooManyParts) {
		t.Errorf("many parts = %v, want ErrTooManyParts", err)
	}
	_, err = ivymime.ListParts(bytes.NewReader(nestedMultipart(ivymime.MaxMultipartDepth+2, false)))
	if !errors.Is(err, ivymime.ErrTooDeep) {
		t.Errorf("deep nesting = %v, want ErrTooDeep", err)
	}
}

// ParseStream fills Parsed.Parts from the skeleton walk it already does, so a
// spooled message's attachment metadata (including a content hash) costs no
// extra pass.
func TestParseStreamEnumeratesListedParts(t *testing.T) {
	t.Parallel()
	p := ivymime.ParseStream(bytes.NewReader(partMessage()))
	if len(p.Parts) != 2 {
		t.Fatalf("parts = %+v, want the file and the inline image", p.Parts)
	}
	byPath := map[string]ivymime.PartInfo{}
	for _, part := range p.Parts {
		byPath[part.Path] = part
	}

	file := byPath["2"]
	if file.Filename != "note.txt" || file.Size != int64(len("attachment bytes")) || !file.Attachment {
		t.Errorf("file part = %+v", file)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte("attachment bytes"))); file.Hash != want {
		t.Errorf("file hash = %q, want %q", file.Hash, want)
	}

	inline := byPath["3"]
	if inline.CID != "logo@example" || !inline.Inline || inline.Size != int64(len("tiny png")) {
		t.Errorf("inline part = %+v", inline)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte("tiny png"))); inline.Hash != want {
		t.Errorf("inline hash = %q, want %q", inline.Hash, want)
	}
}

// The hash of a part too big to keep is computed while the walk streams it past.
func TestParseStreamHashesALargePartWhileWalking(t *testing.T) {
	const lines = 64
	p := ivymime.ParseStream(hugeAttachment(lines))
	if len(p.Parts) != 1 {
		t.Fatalf("parts = %+v, want the one attachment", p.Parts)
	}
	if want := lines * int64(b64LineDecoded); p.Parts[0].Size != want {
		t.Errorf("size = %d, want decoded %d", p.Parts[0].Size, want)
	}
	if p.Parts[0].Hash == "" {
		t.Error("the large part has no content hash")
	}
}

func FuzzListParts(f *testing.F) {
	f.Add(partMessage())
	f.Add(smallMultipart())
	f.Add([]byte("From: a@example.com\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\n\r\nx"))
	f.Fuzz(func(t *testing.T, data []byte) {
		start := time.Now()
		parts, err := ivymime.ListParts(bytes.NewReader(data))
		if err == nil {
			for _, p := range parts {
				if strings.ContainsRune(p.Path, '/') {
					t.Fatalf("part path %q is not a clean path", p.Path)
				}
			}
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("ListParts took %v on a %d-byte input", elapsed, len(data))
		}
		var sink countWriter
		_ = ivymime.CopyPart(bytes.NewReader(data), "1", &sink)
	})
}

// badBase64Message has a good body, an attachment whose base64 is malformed (a
// routine thing in real mail: stray characters, a mangled gateway) and a good
// attachment after it.
func badBase64Message() []byte {
	return []byte("From: a@example.com\r\nSubject: hi\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nthe real body text\r\n" +
		"--b\r\nContent-Type: application/pdf; name=\"a.pdf\"\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\nQUJD*#$%^&REVGR0hJ\r\nzzz!!\r\n" +
		"--b\r\nContent-Type: text/plain; name=\"n.txt\"\r\nContent-Disposition: attachment; filename=\"n.txt\"\r\n\r\nafter\r\n" +
		"--b--\r\n")
}

// One undecodable attachment must cost the message nothing but that part's hash:
// before the fix ParseStream abandoned the whole walk and the body was lost.
func TestParseStreamSurvivesAMalformedAttachment(t *testing.T) {
	t.Parallel()
	p := ivymime.ParseStream(bytes.NewReader(badBase64Message()))
	if p.BodySkipped {
		t.Fatalf("body skipped for one bad attachment: %v", p.Errors)
	}
	if !strings.Contains(p.Text, "the real body text") {
		t.Errorf("Text = %q, want the body", p.Text)
	}
	if len(p.Parts) != 2 {
		t.Fatalf("Parts = %+v, want the bad attachment and the good one", p.Parts)
	}
	if p.Parts[0].Hash != "" {
		t.Errorf("undecodable part has hash %q, want none", p.Parts[0].Hash)
	}
	if p.Parts[1].Hash == "" || p.Parts[1].Size != int64(len("after")) {
		t.Errorf("part after the bad one = %+v, want it hashed and sized", p.Parts[1])
	}
}

func TestListPartsSurvivesAMalformedAttachment(t *testing.T) {
	t.Parallel()
	parts, err := ivymime.ListParts(bytes.NewReader(badBase64Message()))
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %+v, want body, bad attachment and good attachment", parts)
	}
}
