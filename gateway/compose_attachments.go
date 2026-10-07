package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/compose"
	mailmime "github.com/AutumnsGrove/Ivy/mime"
	"github.com/AutumnsGrove/Ivy/store"
)

// attachmentError is a refused attachment a handler maps to 400
// `invalid_message`. Every other error from the helpers below is the server's.
type attachmentError struct{ msg string }

func (e *attachmentError) Error() string { return e.msg }

func composeAttachments(refs *[]api.ComposeAttachment) []api.ComposeAttachment {
	if refs == nil {
		return nil
	}
	return *refs
}

// resolveComposeAttachments loads staged uploads by id and turns them into
// builder attachments. The account scopes every lookup, the total is bounded
// before any file is read, and an inline part's Content-ID is derived from the
// upload id, so the body's cid: reference matches the part the builder emits.
func (s *Server) resolveComposeAttachments(ctx context.Context, accountID string, refs []api.ComposeAttachment) ([]compose.Attachment, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if len(refs) > compose.MaxAttachments {
		return nil, &attachmentError{fmt.Sprintf("That message has %d attachments; %d is the most Ivy will send", len(refs), compose.MaxAttachments)}
	}
	uploads := make([]store.Upload, 0, len(refs))
	var total int64
	for _, ref := range refs {
		up, err := s.dbs.GetUpload(ctx, accountID, ref.Id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, &attachmentError{"An attachment is no longer available; add it again"}
		case err != nil:
			return nil, err
		}
		total += up.Size
		if total > compose.MaxTotalAttachmentsBytes {
			return nil, &attachmentError{"Those attachments are too large together; the most Ivy will send is 25 MiB"}
		}
		uploads = append(uploads, up)
	}
	out := make([]compose.Attachment, 0, len(uploads))
	for i, up := range uploads {
		content, err := s.readStagedUpload(up)
		if err != nil {
			return nil, err
		}
		out = append(out, compose.Attachment{
			Filename: up.Name,
			MIMEType: up.MIMEType,
			Content:  content,
			Inline:   refs[i].Inline != nil && *refs[i].Inline,
			CID:      up.ID + "@ivy",
		})
	}
	return out, nil
}

func (s *Server) readStagedUpload(up store.Upload) ([]byte, error) {
	rc, err := s.dbs.OpenUpload(up)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	// The row's size was bounded at staging; the limit is the belt to its braces.
	return io.ReadAll(io.LimitReader(rc, compose.MaxAttachmentBytes+1))
}

// materializeAttachments re-stages the file and inline parts of a stored MIME
// body, so a resume gets fresh ids even when the original staging was swept.
// The body is the source of truth: it is what was filed and what the send would
// rebuild from.
func (s *Server) materializeAttachments(ctx context.Context, accountID string, raw []byte) ([]api.AttachmentInfo, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	parts, err := mailmime.ListParts(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var out []api.AttachmentInfo
	for _, p := range parts {
		if !p.Attachment && p.CID == "" {
			continue
		}
		id, err := s.stagePart(ctx, accountID, raw, p)
		if err != nil {
			return nil, err
		}
		name := p.Filename
		if name == "" {
			name = "attachment"
		}
		out = append(out, api.AttachmentInfo{
			Id: &id, Name: name, Size: p.Size, Inline: p.CID != "" && !p.Attachment,
		})
	}
	return out, nil
}

// stagePart streams one part of a stored body into staging through a pipe, so
// even a large attachment is never held whole.
func (s *Server) stagePart(ctx context.Context, accountID string, raw []byte, p mailmime.PartInfo) (string, error) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := mailmime.CopyPart(bytes.NewReader(raw), p.Path, pw)
		_ = pw.CloseWithError(err)
		done <- err
	}()
	name := p.Filename
	if name == "" {
		name = "attachment"
	}
	up, stageErr := s.dbs.StageUpload(ctx, store.Upload{
		ID: s.newID(), AccountID: accountID, Name: safeAttachmentName(name),
		MIMEType: p.ContentType, CreatedAt: s.now().UTC(),
	}, pr, compose.MaxAttachmentBytes)
	_ = pr.Close()
	copyErr := <-done
	switch {
	case errors.Is(stageErr, store.ErrUploadTooLarge):
		return "", &attachmentError{"An attachment is too large to reopen; it was kept in Drafts"}
	case stageErr != nil:
		return "", stageErr
	case copyErr != nil:
		return "", copyErr
	}
	return up.ID, nil
}
