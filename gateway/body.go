package gateway

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	stdmime "mime"
	"net/http"
	"strings"

	mailmime "github.com/AutumnsGrove/Ivy/mime"
	"github.com/AutumnsGrove/Ivy/render"
	"github.com/AutumnsGrove/Ivy/store"
)

// handleMessageBody serves the stored, already-sanitised body as its own
// same-origin document with the reader's policy as a response header. A <meta>
// CSP inside a frame is enforced by WebKit but ignored by Chromium, and a
// frame's srcdoc subresources are invisible to Playwright, so the frame points
// here and the browser enforces a real header (the 2d finding).
func (s *Server) handleMessageBody(w http.ResponseWriter, r *http.Request) {
	m, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if err != nil {
		s.messageError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", render.ContentSecurityPolicy(render.Options{}))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(bodyDocument(m)))
}

// bodyDocument wraps the stored sanitised HTML, or the plain text, in a small
// document. The HTML is not re-parsed or re-sanitised: sync stored the result
// of render/, and the response policy is the second layer.
func bodyDocument(m store.Message) string {
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\">")
	b.WriteString("<style>body{margin:0;font-family:system-ui,-apple-system,sans-serif;line-height:1.6;word-wrap:break-word}img{max-width:100%;height:auto}table{max-width:100%}a{color:inherit}</style>")
	b.WriteString("</head><body>")
	switch {
	case m.BodyHTML != "":
		b.WriteString(m.BodyHTML)
	case m.BodyStatus == store.BodyTooLarge:
		b.WriteString("<p>This message is too large to display here. Open it in a mail app to read it.</p>")
	case strings.TrimSpace(m.BodyText) != "":
		for _, p := range paragraphs(m.BodyText) {
			fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(p))
		}
	case m.BodyStatus == store.BodyUnparsed:
		b.WriteString("<p>This message could not be read. Its header is shown above.</p>")
	default:
		b.WriteString("<p>This message has no readable body.</p>")
	}
	b.WriteString("</body></html>")
	return b.String()
}

// handleInline streams one inline cid: part. The sanitizer rewrote every cid:
// source in the stored HTML to this path, so an image inside the body document
// loads only from here.
func (s *Server) handleInline(w http.ResponseWriter, r *http.Request) {
	want := trimCID(r.PathValue("cid"))
	s.servePart(w, r, "inline",
		func(ctx context.Context) (store.Attachment, error) {
			return s.dbs.GetAttachmentByCID(ctx, r.PathValue("id"), want)
		},
		func(p mailmime.PartInfo) bool {
			return p.CID != "" && trimCID(p.CID) == want
		},
	)
}

// handleAttachment streams one file attachment by its part path, the id the
// message view reported. The bytes come from the spooled file or the row blob
// through mime.CopyPart, never held whole.
func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	want := r.PathValue("part")
	s.servePart(w, r, "attachment",
		func(ctx context.Context) (store.Attachment, error) {
			return s.dbs.GetAttachmentByPath(ctx, r.PathValue("id"), want)
		},
		func(p mailmime.PartInfo) bool {
			return p.Path == want && (p.Attachment || p.CID != "")
		},
	)
}

// servePart resolves a part from the stored attachment rows and streams it.
// Only a message mirrored before the table was populated falls back to walking
// the raw message; either way the bytes are streamed, never held whole.
func (s *Server) servePart(w http.ResponseWriter, r *http.Request, kind string, lookup func(context.Context) (store.Attachment, error), match func(mailmime.PartInfo) bool) {
	m, err := s.dbs.GetMessage(r.Context(), r.PathValue("id"))
	if err != nil {
		s.messageError(w, r, err)
		return
	}
	att, err := lookup(r.Context())
	switch {
	case err == nil:
		s.streamStored(w, r, m, att, kind)
	case errors.Is(err, store.ErrNotFound):
		s.streamFromRaw(w, r, m, match, kind)
	default:
		s.serverError(w, r, err)
	}
}

// streamStored serves a part by the path the table recorded, rewinding the raw
// message and copying the decoded part.
func (s *Server) streamStored(w http.ResponseWriter, r *http.Request, m store.Message, att store.Attachment, kind string) {
	rc, ok := s.rawReader(m)
	if !ok {
		s.notFound(w, r, kind)
		return
	}
	defer func() { _ = rc.Close() }()
	if _, err := rc.Seek(0, 0); err != nil {
		s.serverError(w, r, err)
		return
	}
	setPartHeaders(w, att.MIMEType, att.Filename, kind)
	if err := mailmime.CopyPart(rc, att.StoragePath, w); err != nil {
		slog.WarnContext(r.Context(), "gateway: serve message part", "message", m.ID, "part", att.StoragePath, "error", err)
	}
}

// streamFromRaw is the compatibility path: walk the raw message to find the
// part, then rewind and copy it.
func (s *Server) streamFromRaw(w http.ResponseWriter, r *http.Request, m store.Message, match func(mailmime.PartInfo) bool, kind string) {
	rc, ok := s.rawReader(m)
	if !ok {
		s.notFound(w, r, kind)
		return
	}
	defer func() { _ = rc.Close() }()

	parts, err := mailmime.ListParts(rc)
	if err != nil {
		s.notFound(w, r, kind)
		return
	}
	for _, p := range parts {
		if !match(p) {
			continue
		}
		if _, err := rc.Seek(0, 0); err != nil {
			s.serverError(w, r, err)
			return
		}
		setPartHeaders(w, p.ContentType, p.Filename, kind)
		if err := mailmime.CopyPart(rc, p.Path, w); err != nil {
			slog.WarnContext(r.Context(), "gateway: serve message part", "message", m.ID, "part", p.Path, "error", err)
		}
		return
	}
	s.notFound(w, r, kind)
}

// setPartHeaders writes the response headers before any body byte. A mid-stream
// decode failure can then only be logged; the client sees a truncated body.
func setPartHeaders(w http.ResponseWriter, contentType, filename, kind string) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "no-store")
	if kind == "attachment" {
		h.Set("Content-Disposition", stdmime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	} else {
		h.Set("Content-Disposition", "inline")
	}
}

// messageError maps a load failure onto the error envelope: a hidden or absent
// message is a 404, anything else is the server's problem.
func (s *Server) messageError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "message")
		return
	}
	s.serverError(w, r, err)
}

func trimCID(v string) string {
	return strings.Trim(strings.TrimSpace(v), "<>")
}
