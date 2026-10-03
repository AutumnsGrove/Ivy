package mime

import (
	"bufio"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"
)

// ListParts walks a raw message and returns every leaf part with the path
// CopyPart and CopyCID need to serve it, its decoded size, its file name and
// its content id. It streams each body to nothing, so a message with a huge
// attachment is listed without holding it in memory; the nesting, count and
// header limits are the same as Parse.
//
// The message body parts are returned too (they are leaves like any other), so
// the caller decides what it will show as an attachment.
func ListParts(r io.Reader) ([]PartInfo, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	head, err := readHeaderBlock(br)
	if err != nil {
		return nil, err
	}
	l := &partLister{}
	if err := l.walk(parseHeader(head), br, "", 0); err != nil {
		return nil, err
	}
	return l.parts, nil
}

type partLister struct {
	parts []PartInfo
	count int
}

func (l *partLister) walk(hdr textproto.MIMEHeader, body io.Reader, path string, depth int) error {
	mediaType, params := contentType(hdr)
	if boundary := params["boundary"]; strings.HasPrefix(mediaType, "multipart/") && boundary != "" {
		if depth+1 > MaxMultipartDepth {
			return ErrTooDeep
		}
		mr := multipart.NewReader(body, boundary)
		for i := 1; ; i++ {
			part, err := mr.NextRawPart()
			if err != nil {
				// A malformed boundary ends the container, as it does in
				// BuildSkeleton: what was read is still usable.
				return nil
			}
			if l.count++; l.count > MaxParts {
				return ErrTooManyParts
			}
			if err := l.walk(part.Header, part, joinPath(path, i), depth+1); err != nil {
				return err
			}
		}
	}
	if path == "" {
		path = "1"
		if l.count++; l.count > MaxParts {
			return ErrTooManyParts
		}
	}
	n, err := io.Copy(io.Discard, decoder(hdr.Get("Content-Transfer-Encoding"), body))
	if err != nil {
		return err
	}
	l.parts = append(l.parts, describePart(hdr, params, mediaType, path, n))
	return nil
}

// describePart projects a leaf's headers into a PartInfo. A part counts as an
// attachment when it is not a text body; an inline part is one with a content
// id that is not a file.
func describePart(hdr textproto.MIMEHeader, params map[string]string, mediaType, path string, size int64) PartInfo {
	name, attachment := partName(hdr, params)
	cid := normalizeCID(hdr.Get("Content-ID"))
	return PartInfo{
		Path:        path,
		Filename:    name,
		ContentType: mediaType,
		Size:        size,
		Attachment:  attachment || !strings.HasPrefix(mediaType, "text/"),
		CID:         cid,
		Inline:      cid != "" && !attachment,
	}
}

// normalizeCID strips the angle brackets and whitespace a Content-ID header
// carries, so "cid:<logo@x>" and the header value compare equal.
func normalizeCID(v string) string {
	return strings.Trim(strings.TrimSpace(v), "<>")
}
