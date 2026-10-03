package mime

import (
	"bufio"
	"fmt"
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

// CopyCID streams the decoded body of the part whose Content-ID is cid to w and
// returns its metadata. A cid: source in the sanitised HTML points at this
// endpoint, so the match is by content id, never by a caller-chosen path. The
// id may carry the angle brackets the header does.
func CopyCID(r io.Reader, cid string, w io.Writer) (PartInfo, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	head, err := readHeaderBlock(br)
	if err != nil {
		return PartInfo{}, err
	}
	want := normalizeCID(cid)
	if want == "" {
		return PartInfo{}, fmt.Errorf("%w: empty content id", ErrPartNotFound)
	}
	c := &cidCopier{want: want, w: w}
	found, err := c.walk(parseHeader(head), br, "", 0)
	if err != nil {
		return PartInfo{}, err
	}
	if !found {
		return PartInfo{}, fmt.Errorf("%w: cid:%s", ErrPartNotFound, cid)
	}
	return c.info, nil
}

type cidCopier struct {
	want  string
	w     io.Writer
	info  PartInfo
	count int
}

func (c *cidCopier) walk(hdr textproto.MIMEHeader, body io.Reader, path string, depth int) (bool, error) {
	mediaType, params := contentType(hdr)
	if boundary := params["boundary"]; strings.HasPrefix(mediaType, "multipart/") && boundary != "" {
		if depth+1 > MaxMultipartDepth {
			return false, ErrTooDeep
		}
		mr := multipart.NewReader(body, boundary)
		for i := 1; ; i++ {
			part, err := mr.NextRawPart()
			if err != nil {
				return false, nil
			}
			if c.count++; c.count > MaxParts {
				return false, ErrTooManyParts
			}
			found, err := c.walk(part.Header, part, joinPath(path, i), depth+1)
			if found || err != nil {
				return found, err
			}
		}
	}
	if path == "" {
		path = "1"
		if c.count++; c.count > MaxParts {
			return false, ErrTooManyParts
		}
	}
	if normalizeCID(hdr.Get("Content-ID")) != c.want {
		return false, nil
	}
	n, err := io.Copy(c.w, decoder(hdr.Get("Content-Transfer-Encoding"), body))
	if err != nil {
		return false, err
	}
	c.info = describePart(hdr, params, mediaType, path, n)
	return true, nil
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
