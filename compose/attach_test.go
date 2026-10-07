package compose_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime"

	"github.com/AutumnsGrove/Ivy/compose"
)

// withAttachments returns a valid message carrying the given parts.
func withAttachments(atts ...compose.Attachment) compose.Message {
	m := base()
	m.Attachments = atts
	return m
}

// A file attachment is emitted as its own part with the operator's name and
// type, and survives a parse with its bytes intact.
func TestBuildEmitsFileAttachment(t *testing.T) {
	t.Parallel()
	raw, _, err := compose.Build(withAttachments(compose.Attachment{
		Filename: "notes.txt", MIMEType: "text/plain", Content: []byte("hello attachment"),
	}))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	assertSafeHeaders(t, raw)
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(env.Attachments))
	}
	att := env.Attachments[0]
	if att.FileName != "notes.txt" || att.ContentType != "text/plain" {
		t.Errorf("attachment = %q %q, want notes.txt text/plain", att.FileName, att.ContentType)
	}
	if att.Disposition != "attachment" {
		t.Errorf("disposition = %q, want attachment", att.Disposition)
	}
	if string(att.Content) != "hello attachment" {
		t.Errorf("content = %q, want the attachment bytes", att.Content)
	}
}

// An inline image carries a Content-ID and the HTML part points at it with a
// cid: URL, which the outgoing policy must allow.
func TestBuildEmitsInlineImageWithCID(t *testing.T) {
	t.Parallel()
	m := withAttachments(compose.Attachment{
		Filename: "chart.png", MIMEType: "image/png", Content: []byte("\x89PNG fake"),
		Inline: true, CID: "chart@ivy",
	})
	m.Markdown = true
	m.Text = "Look ![chart](cid:chart@ivy) here"
	raw, _, err := compose.Build(m)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	assertSafeHeaders(t, raw)
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env.Inlines) != 1 {
		t.Fatalf("inlines = %d, want 1", len(env.Inlines))
	}
	inline := env.Inlines[0]
	if got := strings.Trim(inline.ContentID, "<>"); got != "chart@ivy" {
		t.Errorf("content id = %q, want chart@ivy", got)
	}
	if inline.Disposition != "inline" {
		t.Errorf("disposition = %q, want inline", inline.Disposition)
	}
	if !strings.Contains(env.HTML, `src="cid:chart@ivy"`) {
		t.Errorf("html does not reference the cid: %s", env.HTML)
	}
}

// A message with both kinds nests a related inside a mixed, so text, html,
// inline and attachment all arrive.
func TestBuildAttachmentStructure(t *testing.T) {
	t.Parallel()
	m := withAttachments(
		compose.Attachment{Filename: "a.txt", MIMEType: "text/plain", Content: []byte("a")},
		compose.Attachment{Filename: "i.png", MIMEType: "image/png", Content: []byte("i"), Inline: true, CID: "i@ivy"},
	)
	m.Markdown = true
	raw, _, err := compose.Build(m)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	top := string(raw)
	if !strings.Contains(top, "multipart/mixed") || !strings.Contains(top, "multipart/related") {
		t.Errorf("built message lacks nested mixed/related structure")
	}
}

// Every hostile or out-of-range attachment field is refused with the field
// named, never stripped or silently truncated.
func TestBuildRejectsHostileAttachments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		field  string
		attach compose.Attachment
	}{
		{"crlf in name", "attachment", compose.Attachment{Filename: "a\r\nBcc: evil@x", MIMEType: "text/plain"}},
		{"nul in name", "attachment", compose.Attachment{Filename: "a\x00b", MIMEType: "text/plain"}},
		{"empty name", "attachment", compose.Attachment{MIMEType: "text/plain"}},
		{"long name", "attachment", compose.Attachment{Filename: strings.Repeat("n", compose.MaxFilenameBytes+1), MIMEType: "text/plain"}},
		{"crlf in mime", "attachment", compose.Attachment{Filename: "a.txt", MIMEType: "text/plain\r\nX: y"}},
		{"long mime", "attachment", compose.Attachment{Filename: "a.txt", MIMEType: strings.Repeat("t", compose.MaxMIMEBytes+1)}},
		{"inline without cid", "attachment", compose.Attachment{Filename: "a.png", MIMEType: "image/png", Inline: true}},
		{"inline with bad cid", "attachment", compose.Attachment{Filename: "a.png", MIMEType: "image/png", Inline: true, CID: "bad\r\ncid"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := compose.Build(withAttachments(tc.attach))
			var ve *compose.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
			if ve.Field != tc.field {
				t.Errorf("field = %q, want %q", ve.Field, tc.field)
			}
		})
	}
}

// The count and total-size caps are refused before anything is built.
func TestBuildRejectsAttachmentLimits(t *testing.T) {
	t.Parallel()

	many := make([]compose.Attachment, compose.MaxAttachments+1)
	for i := range many {
		many[i] = compose.Attachment{Filename: "a.txt", MIMEType: "text/plain", Content: []byte("x")}
	}
	if _, _, err := compose.Build(withAttachments(many...)); !fieldIs(err, "attachments") {
		t.Errorf("too many: err = %v, want an attachments ValidationError", err)
	}

	big := []compose.Attachment{
		{Filename: "a.bin", MIMEType: "application/octet-stream", Content: bytes.Repeat([]byte("x"), compose.MaxTotalAttachmentsBytes/2+1)},
		{Filename: "b.bin", MIMEType: "application/octet-stream", Content: bytes.Repeat([]byte("x"), compose.MaxTotalAttachmentsBytes/2+1)},
	}
	if _, _, err := compose.Build(withAttachments(big...)); !fieldIs(err, "attachments") {
		t.Errorf("too large: err = %v, want an attachments ValidationError", err)
	}
}

func fieldIs(err error, field string) bool {
	var ve *compose.ValidationError
	return errors.As(err, &ve) && ve.Field == field
}

// A message with no attachments is unchanged: the builder stays a plain
// text/plain or multipart/alternative, never a degenerate multipart.
func TestBuildWithoutAttachmentsHasNoMixedPart(t *testing.T) {
	t.Parallel()
	raw, _, err := compose.Build(base())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if bytes.Contains(raw, []byte("multipart/mixed")) {
		t.Errorf("a message with no attachments was wrapped in multipart/mixed")
	}
}

// FuzzBuildAttachment proves arbitrary attachment bytes and names either build a
// parseable, header-safe message or are refused; nothing else.
func FuzzBuildAttachment(f *testing.F) {
	f.Add("a.txt", "text/plain", "content", false, "")
	f.Add("a\r\nb", "text/plain", "x", true, "c@ivy")
	f.Add("photo.jpg", "image/jpeg", "\xff\xd8\xff", true, "id@ivy")
	f.Fuzz(func(t *testing.T, name, mime, content string, inline bool, cid string) {
		m := withAttachments(compose.Attachment{
			Filename: name, MIMEType: mime, Content: []byte(content), Inline: inline, CID: cid,
		})
		raw, _, err := compose.Build(m)
		if err != nil {
			var ve *compose.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("unexpected error kind: %v", err)
			}
			return
		}
		assertSafeHeaders(t, raw)
	})
}
