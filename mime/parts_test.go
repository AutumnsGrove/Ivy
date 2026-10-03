package mime_test

import (
	"bytes"
	"errors"
	"io"
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

func TestCopyCIDStreamsTheInlinePart(t *testing.T) {
	t.Parallel()
	var got bytes.Buffer
	info, err := ivymime.CopyCID(bytes.NewReader(partMessage()), "logo@example", &got)
	if err != nil {
		t.Fatalf("CopyCID: %v", err)
	}
	if got.String() != "tiny png" {
		t.Errorf("cid body = %q, want the decoded image", got.String())
	}
	if info.ContentType != "image/png" || info.Filename != "logo.png" {
		t.Errorf("CopyCID info = %+v", info)
	}

	// Angle brackets are how the header carries the id; the caller may pass
	// either form, matching how the sanitizer rewrites cid: sources.
	got.Reset()
	if _, err := ivymime.CopyCID(bytes.NewReader(partMessage()), "<logo@example>", &got); err != nil {
		t.Fatalf("CopyCID with brackets: %v", err)
	}
	if got.String() != "tiny png" {
		t.Errorf("bracketed cid body = %q", got.String())
	}

	if _, err := ivymime.CopyCID(bytes.NewReader(partMessage()), "missing", io.Discard); !errors.Is(err, ivymime.ErrPartNotFound) {
		t.Errorf("missing cid = %v, want ErrPartNotFound", err)
	}
	if _, err := ivymime.CopyCID(bytes.NewReader(partMessage()), "", io.Discard); !errors.Is(err, ivymime.ErrPartNotFound) {
		t.Errorf("empty cid = %v, want ErrPartNotFound", err)
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
		_, _ = ivymime.CopyCID(bytes.NewReader(data), "x", &sink)
	})
}
