package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	stdmime "mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/compose"
	mailmime "github.com/AutumnsGrove/Ivy/mime"
	"github.com/AutumnsGrove/Ivy/store"
)

// Upload limits (STANDARDS.md 4a). The per-file cap is the builder's, so the
// staging stop and the send-time validation agree.
const (
	defaultMailAttachmentListLimit = 20
	maxMailAttachmentListLimit     = 50
	uploadSniffBytes               = 512
)

// dangerousAttachmentExtensions and dangerousAttachmentTypes are the deny list:
// files a mail client could auto-run or render as scripted content. The operator
// can send anything else, including source files.
var dangerousAttachmentExtensions = map[string]bool{
	".exe": true, ".msi": true, ".bat": true, ".cmd": true, ".com": true,
	".scr": true, ".pif": true, ".vbs": true, ".vbe": true, ".js": true,
	".jse": true, ".wsf": true, ".wsh": true, ".ps1": true, ".jar": true,
	".app": true, ".dmg": true, ".sh": true, ".hta": true, ".lnk": true,
	".iso": true, ".img": true, ".svg": true, ".html": true, ".htm": true,
	".xhtml": true,
}

var dangerousAttachmentTypes = map[string]bool{
	"application/x-msdownload":    true,
	"application/x-msdos-program": true,
	"application/x-executable":    true,
	"application/x-sharedlib":     true,
	"application/x-sh":            true,
	"application/x-shellscript":   true,
	"application/x-javascript":    true,
	"application/javascript":      true,
	"text/javascript":             true,
	"text/html":                   true,
	"application/xhtml+xml":       true,
	"image/svg+xml":               true,
	"application/x-httpd-php":     true,
}

// handleUploadAttachment stages an outgoing attachment. The raw body is streamed
// to disk and never read whole into memory; the declared type is sniffed and
// checked, and the name is refused if it could forge a header (the builder would
// refuse it later anyway, but the operator should hear about it now).
func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := r.PathValue("id")
	if _, err := s.dbs.GetAccount(ctx, accountID); errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "account")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "An attachment needs a name")
		return
	}
	if err := validateUploadName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_message", err.Error())
		return
	}

	// Sniff the head before streaming the rest, so a declared image that is
	// really markup is caught without buffering the file.
	body := http.MaxBytesReader(w, r.Body, compose.MaxAttachmentBytes)
	head := make([]byte, uploadSniffBytes)
	n, readErr := io.ReadFull(body, head)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		if isTooLarge(readErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That file is over the 25 MiB limit")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "That file could not be read")
		return
	}
	head = head[:n]
	mimeType := resolveAttachmentType(r.Header.Get("Content-Type"), head)
	if err := checkAttachmentType(name, mimeType); err != nil {
		writeError(w, http.StatusBadRequest, "bad_type", err.Error())
		return
	}

	staged, err := s.dbs.StageUpload(ctx, store.Upload{
		ID: s.newID(), AccountID: accountID, Name: name, MIMEType: mimeType, CreatedAt: s.now().UTC(),
	}, io.MultiReader(bytes.NewReader(head), body), compose.MaxAttachmentBytes)
	switch {
	case errors.Is(err, store.ErrUploadTooLarge) || isTooLarge(err):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That file is over the 25 MiB limit")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, uploadView(staged))
}

// handleGetUpload streams a staged attachment's bytes for a thumbnail or a
// resumed draft. Only a raster image is served inline: anything else is a
// download, because a same-origin page could otherwise carry script.
func (s *Server) handleGetUpload(w http.ResponseWriter, r *http.Request) {
	up, err := s.dbs.GetUpload(r.Context(), r.PathValue("id"), r.PathValue("uploadId"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "upload")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	rc, err := s.dbs.OpenUpload(up)
	if err != nil {
		s.notFound(w, r, "upload")
		return
	}
	defer func() { _ = rc.Close() }()

	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if rasterImage(up.MIMEType) {
		h.Set("Content-Type", up.MIMEType)
		h.Set("Content-Disposition", "inline")
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", stdmime.FormatMediaType("attachment", map[string]string{"filename": up.Name}))
	}
	if _, err := io.Copy(w, rc); err != nil {
		slog.WarnContext(r.Context(), "gateway: stream upload", "upload", up.ID, "error", err)
	}
}

// handleDeleteUpload frees a staged attachment the screen removed.
func (s *Server) handleDeleteUpload(w http.ResponseWriter, r *http.Request) {
	err := s.dbs.DeleteUpload(r.Context(), r.PathValue("id"), r.PathValue("uploadId"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "upload")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUploadFromMail copies a mirrored attachment into staging server-side,
// with no browser upload. The message must belong to the account and still be
// visible.
func (s *Server) handleUploadFromMail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := r.PathValue("id")
	var in api.UploadFromMailInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That attachment request is not valid")
		return
	}
	m, err := s.dbs.GetMessage(ctx, in.MessageId)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "message")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	if m.AccountID != accountID {
		s.notFound(w, r, "message")
		return
	}
	att, err := s.dbs.GetAttachmentByPath(ctx, in.MessageId, in.Path)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r, "attachment")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	rc, ok := s.rawReader(m)
	if !ok {
		writeError(w, http.StatusConflict, "message_gone", "That attachment is no longer stored")
		return
	}
	defer func() { _ = rc.Close() }()
	if _, err := rc.Seek(0, 0); err != nil {
		s.serverError(w, r, err)
		return
	}

	// CopyPart writes decoded bytes; StageUpload pulls them through a pipe, so
	// even a large attachment is never held whole.
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := mailmime.CopyPart(rc, att.StoragePath, pw)
		_ = pw.CloseWithError(err)
		done <- err
	}()
	name := safeAttachmentName(att.Filename)
	staged, stageErr := s.dbs.StageUpload(ctx, store.Upload{
		ID: s.newID(), AccountID: accountID, Name: name, MIMEType: att.MIMEType, CreatedAt: s.now().UTC(),
	}, pr, compose.MaxAttachmentBytes)
	_ = pr.Close()
	copyErr := <-done
	switch {
	case errors.Is(stageErr, store.ErrUploadTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That attachment is over the 25 MiB limit")
		return
	case stageErr != nil:
		s.serverError(w, r, stageErr)
		return
	case copyErr != nil:
		s.serverError(w, r, copyErr)
		return
	}
	writeJSON(w, http.StatusCreated, uploadView(staged))
}

// handleListMailAttachments serves "From your mail": recent attachments already
// in the mirror, newest first.
func (s *Server) handleListMailAttachments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := r.PathValue("id")
	if _, err := s.dbs.GetAccount(ctx, accountID); errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r, "account")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = defaultMailAttachmentListLimit
	}
	if limit > maxMailAttachmentListLimit {
		limit = maxMailAttachmentListLimit
	}
	rows, err := s.dbs.ListRecentMailAttachments(ctx, accountID, r.URL.Query().Get("q"), limit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := api.MailAttachmentList{Attachments: make([]api.MailAttachment, 0, len(rows))}
	for _, a := range rows {
		inline := a.Inline
		out.Attachments = append(out.Attachments, api.MailAttachment{
			MessageId: a.MessageID, Path: a.Path, Name: a.Name, Mime: a.MIMEType, Size: a.Size, Inline: &inline,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func uploadView(u store.Upload) api.Upload {
	return api.Upload{Id: u.ID, Name: u.Name, Mime: u.MIMEType, Size: u.Size}
}

// uploadError is a refusal the operator should read; the message is written to
// the error envelope as-is, so it is capitalized (ST1005 allows a custom type).
type uploadError struct{ msg string }

func (e *uploadError) Error() string { return e.msg }

// validateUploadName refuses a name that could forge a header or is over the
// builder's bound; it is the same rule the send-time validation applies.
func validateUploadName(name string) error {
	if hasBadHeaderBytes(name) {
		return &uploadError{"That file name is not valid"}
	}
	if len(name) > compose.MaxFilenameBytes {
		return &uploadError{"That file name is too long"}
	}
	return nil
}

func hasBadHeaderBytes(s string) bool { return strings.ContainsAny(s, "\r\n\x00") }

// resolveAttachmentType decides the stored content type. A declared raster image
// is kept unless the bytes are actually markup; everything else keeps its
// declared type, or the sniff when nothing specific was declared.
func resolveAttachmentType(declared string, head []byte) string {
	declared = strings.TrimSpace(declared)
	if declared == "" {
		declared = "application/octet-stream"
	}
	if len(head) == 0 {
		return declared
	}
	sniff := http.DetectContentType(head)
	if strings.HasPrefix(strings.ToLower(declared), "image/") {
		if strings.HasPrefix(sniff, "text/") || sniff == "application/xhtml+xml" {
			return sniff
		}
		return declared
	}
	if declared == "application/octet-stream" && !strings.HasPrefix(sniff, "text/plain") {
		return sniff
	}
	return declared
}

// checkAttachmentType applies the deny list by extension and by MIME type.
func checkAttachmentType(name, mimeType string) error {
	if ext := strings.ToLower(path.Ext(name)); dangerousAttachmentExtensions[ext] {
		return &uploadError{fmt.Sprintf("Ivy will not send %s files", ext)}
	}
	mt, _, err := stdmime.ParseMediaType(mimeType)
	if err == nil && dangerousAttachmentTypes[strings.ToLower(mt)] {
		return &uploadError{fmt.Sprintf("Ivy will not send %s files", mt)}
	}
	return nil
}

// safeAttachmentName makes a mirrored attachment's name header-safe for reuse as
// a staging name: control bytes and path separators are dropped, and a blank or
// empty name becomes "attachment".
func safeAttachmentName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return -1
		case r == '/' || r == '\\':
			return '-'
		default:
			return r
		}
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "attachment"
	}
	if len(name) > compose.MaxFilenameBytes {
		name = name[:compose.MaxFilenameBytes]
	}
	return name
}

// isTooLarge reports the HTTP body cap (MaxBytesReader) anywhere in the error
// chain, so an over-large upload is a 413 whatever layer wraps it.
func isTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}
