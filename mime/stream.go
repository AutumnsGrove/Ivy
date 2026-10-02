package mime

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	stdmime "mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
)

// Limits for parsing a message that is read from disk rather than held in
// memory (STANDARDS.md 4a). Together they bound what ParseStream keeps no matter
// how large or how hostile the message is.
const (
	// MaxTextPartBytes is the largest inline text/* body kept in memory.
	MaxTextPartBytes = 2 << 20
	// MaxLeafBytes is the largest attachment or other part kept in memory.
	MaxLeafBytes = 64 << 10
	// MaxSkeletonBytes caps everything kept across all parts of one message.
	MaxSkeletonBytes = 8 << 20
	// MaxParts caps the leaf and container parts walked in one message.
	MaxParts = 1000
	// MaxHeaderBytes caps one header block.
	MaxHeaderBytes = 1 << 20
)

// Errors BuildSkeleton and CopyPart return.
var (
	// ErrPartNotFound means the message has no part at the requested path.
	ErrPartNotFound = errors.New("mime: no such part")
	// ErrTooDeep means the multipart nesting exceeds MaxMultipartDepth.
	ErrTooDeep = errors.New("mime: multipart nesting too deep")
	// ErrTooManyParts means the message has more than MaxParts parts.
	ErrTooManyParts = errors.New("mime: too many parts")
	// ErrHeaderTooLarge means a header block exceeds MaxHeaderBytes.
	ErrHeaderTooLarge = errors.New("mime: header block too large")
)

// PartInfo describes a part whose body was left on disk.
type PartInfo struct {
	Path        string // 1-based position at each level, e.g. "2.1"
	Filename    string
	ContentType string
	Size        int64 // bytes as stored in the message (still transfer-encoded)
	Attachment  bool  // a file, as opposed to a very large inline text body
}

// Skeleton is a message with every body that is too big to keep removed, in a
// form Parse can read. On an error Raw holds only the header block, so a caller
// can still parse the headers.
type Skeleton struct {
	Raw   []byte
	Large []PartInfo
}

// BuildSkeleton streams a message and returns its headers and every part small
// enough to keep, in memory bounded by the limits above. A part that is too big
// is read through and counted but never held; its headers stay so the parser
// still sees its name and type.
func BuildSkeleton(r io.Reader) (Skeleton, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	head, err := readHeaderBlock(br)
	if err != nil {
		return Skeleton{}, err
	}
	b := &skeletonBuilder{}
	b.out.Write(head)
	if err := b.body(parseHeader(head), br, "", 0); err != nil {
		return Skeleton{Raw: head}, err
	}
	return Skeleton{Raw: b.out.Bytes(), Large: b.large}, nil
}

// ParseStream parses a message read from r without holding its large parts. It
// never fails: a body that cannot be read (too deep, too many parts, a header
// block over the limit, an I/O error) yields the headers alone with BodySkipped
// set and the reason in Errors.
func ParseStream(r io.Reader) (p Parsed) {
	defer func() {
		if rec := recover(); rec != nil {
			p.Errors = append(p.Errors, fmt.Sprintf("parser panic: %v", rec))
			p.BodySkipped = true
		}
	}()
	sk, err := BuildSkeleton(r)
	if err != nil {
		p = Parse(headerOnly(sk.Raw))
		p.BodySkipped = true
		p.Errors = append(p.Errors, "body not parsed: "+err.Error())
		return p
	}
	p = Parse(sk.Raw)
	p.Large = sk.Large
	p.Attachments = withoutEmptied(p.Attachments, sk.Large)
	p.Inlines = withoutEmptied(p.Inlines, sk.Large)
	return p
}

// withoutEmptied drops the zero-length stand-ins the parser reports for parts
// that were left on disk; those are listed in Large instead.
func withoutEmptied(parts []Part, large []PartInfo) []Part {
	for _, l := range large {
		for i, p := range parts {
			if len(p.Content) == 0 && p.Filename == l.Filename && p.ContentType == l.ContentType {
				parts = append(parts[:i], parts[i+1:]...)
				break
			}
		}
	}
	return parts
}

// CopyPart streams the decoded body of the part at path ("1", "2", "2.1") to w,
// reading the message from r once, in a fixed buffer. This is how an attachment
// is served from the spooled file: it is read from disk each time and never
// held whole. A body that does not decode (corrupt base64) stops with an error
// after the bytes already written.
func CopyPart(r io.Reader, path string, w io.Writer) error {
	br := bufio.NewReaderSize(r, 64<<10)
	head, err := readHeaderBlock(br)
	if err != nil {
		return err
	}
	c := &partCopier{want: path, w: w}
	found, err := c.walk(parseHeader(head), br, "", 0)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrPartNotFound, path)
	}
	return nil
}

// skeletonBuilder accumulates the skeleton while the message streams past.
type skeletonBuilder struct {
	out      bytes.Buffer
	large    []PartInfo
	parts    int
	retained int64
}

// body handles one entity whose headers are hdr: a multipart container is walked
// part by part, anything else is a leaf.
func (b *skeletonBuilder) body(hdr textproto.MIMEHeader, body io.Reader, path string, depth int) error {
	mediaType, params := contentType(hdr)
	if boundary := params["boundary"]; strings.HasPrefix(mediaType, "multipart/") && boundary != "" {
		return b.multipart(boundary, body, path, depth)
	}
	return b.leaf(hdr, mediaType, params, body, path)
}

func (b *skeletonBuilder) multipart(boundary string, body io.Reader, path string, depth int) error {
	if depth+1 > MaxMultipartDepth {
		return ErrTooDeep
	}
	mr := multipart.NewReader(body, boundary)
	for i := 1; ; i++ {
		part, err := mr.NextRawPart()
		if err != nil {
			// io.EOF ends the container; so does a malformed boundary, because
			// the parser downstream is just as lenient and keeping what we read
			// is better than losing the message.
			break
		}
		if b.parts++; b.parts > MaxParts {
			return ErrTooManyParts
		}
		fmt.Fprintf(&b.out, "--%s\r\n", boundary)
		writeHeader(&b.out, part.Header)
		b.out.WriteString("\r\n")
		if err := b.body(part.Header, part, joinPath(path, i), depth+1); err != nil {
			return err
		}
		b.out.WriteString("\r\n")
	}
	fmt.Fprintf(&b.out, "--%s--\r\n", boundary)
	return nil
}

// leaf keeps a part's body if it fits its limit and the message's remaining
// budget, otherwise reads it through, counting, and records where it is.
func (b *skeletonBuilder) leaf(hdr textproto.MIMEHeader, mediaType string, params map[string]string, body io.Reader, path string) error {
	if path == "" {
		path = "1"
		if b.parts++; b.parts > MaxParts {
			return ErrTooManyParts
		}
	}
	name, attachment := partName(hdr, params)
	limit := int64(MaxLeafBytes)
	if strings.HasPrefix(mediaType, "text/") && !attachment {
		limit = MaxTextPartBytes
	}
	if room := MaxSkeletonBytes - b.retained; room < limit {
		limit = room
	}

	var kept bytes.Buffer
	n, err := io.CopyN(&kept, body, limit+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if n <= limit {
		b.out.Write(kept.Bytes())
		b.retained += n
		return nil
	}
	rest, err := io.Copy(io.Discard, body)
	if err != nil {
		return err
	}
	b.large = append(b.large, PartInfo{
		Path:        path,
		Filename:    name,
		ContentType: mediaType,
		Size:        n + rest,
		Attachment:  attachment || !strings.HasPrefix(mediaType, "text/"),
	})
	return nil
}

// partCopier finds one part by path and decodes it to w.
type partCopier struct {
	want  string
	w     io.Writer
	parts int
}

func (c *partCopier) walk(hdr textproto.MIMEHeader, body io.Reader, path string, depth int) (bool, error) {
	mediaType, params := contentType(hdr)
	boundary := params["boundary"]
	if !strings.HasPrefix(mediaType, "multipart/") || boundary == "" {
		if path == "" {
			path = "1"
		}
		if path != c.want {
			return false, nil
		}
		_, err := io.Copy(c.w, decoder(hdr.Get("Content-Transfer-Encoding"), body))
		return true, err
	}
	if depth+1 > MaxMultipartDepth {
		return false, ErrTooDeep
	}
	mr := multipart.NewReader(body, boundary)
	for i := 1; ; i++ {
		part, err := mr.NextRawPart()
		if err != nil {
			return false, nil
		}
		if c.parts++; c.parts > MaxParts {
			return false, ErrTooManyParts
		}
		child := joinPath(path, i)
		if child != c.want && !strings.HasPrefix(c.want, child+".") {
			continue // NextRawPart skips the rest of this part by itself
		}
		if found, err := c.walk(part.Header, part, child, depth+1); found || err != nil {
			return found, err
		}
	}
}

// decoder undoes a part's Content-Transfer-Encoding as a stream.
func decoder(encoding string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, &skipSpace{r: r})
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	default:
		return r
	}
}

// skipSpace drops the whitespace base64 bodies are wrapped with; the standard
// decoder tolerates only newlines.
type skipSpace struct{ r io.Reader }

func (s *skipSpace) Read(p []byte) (int, error) {
	for {
		n, err := s.r.Read(p)
		kept := 0
		for _, c := range p[:n] {
			if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				p[kept] = c
				kept++
			}
		}
		if kept > 0 || err != nil {
			return kept, err
		}
	}
}

// contentType returns a part's lower-cased media type (text/plain by default)
// and its parameters; a header that does not parse gives the default.
func contentType(hdr textproto.MIMEHeader) (string, map[string]string) {
	mediaType, params, err := stdmime.ParseMediaType(hdr.Get("Content-Type"))
	if err != nil && mediaType == "" {
		return "text/plain", nil
	}
	return mediaType, params
}

// partName returns a part's file name and whether it is a file attachment.
func partName(hdr textproto.MIMEHeader, ctParams map[string]string) (string, bool) {
	disposition, dParams, _ := stdmime.ParseMediaType(hdr.Get("Content-Disposition"))
	name := dParams["filename"]
	if name == "" {
		name = ctParams["name"]
	}
	if decoded, err := new(stdmime.WordDecoder).DecodeHeader(name); err == nil {
		name = decoded
	}
	return name, strings.EqualFold(disposition, "attachment") || (name != "" && !strings.EqualFold(disposition, "inline"))
}

// readHeaderBlock reads through the blank line that ends a header block,
// keeping the bytes verbatim, and fails past MaxHeaderBytes.
func readHeaderBlock(br *bufio.Reader) ([]byte, error) {
	var buf bytes.Buffer
	for {
		line, err := br.ReadSlice('\n')
		buf.Write(line)
		if buf.Len() > MaxHeaderBytes {
			return nil, ErrHeaderTooLarge
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf.Bytes(), nil
			}
			return nil, err
		}
		if len(line) == 1 || (len(line) == 2 && line[0] == '\r') {
			return buf.Bytes(), nil
		}
	}
}

// parseHeader reads a header block leniently: a malformed line stops the read
// but what came before it is still usable.
func parseHeader(head []byte) textproto.MIMEHeader {
	hdr, _ := textproto.NewReader(bufio.NewReader(bytes.NewReader(head))).ReadMIMEHeader()
	if hdr == nil {
		hdr = textproto.MIMEHeader{}
	}
	return hdr
}

// writeHeader writes a part's headers in a fixed order.
func writeHeader(w *bytes.Buffer, hdr textproto.MIMEHeader) {
	keys := make([]string, 0, len(hdr))
	for k := range hdr {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range hdr[k] {
			fmt.Fprintf(w, "%s: %s\r\n", k, v)
		}
	}
}

func joinPath(parent string, index int) string {
	if parent == "" {
		return strconv.Itoa(index)
	}
	return parent + "." + strconv.Itoa(index)
}

// HeaderBlock returns the header block of the message in r, through the blank
// line and bounded by MaxHeaderBytes. Callers use it where only the headers are
// needed, such as deriving an identity for a message without a Message-ID.
func HeaderBlock(r io.Reader) ([]byte, error) {
	return readHeaderBlock(bufio.NewReaderSize(r, 64<<10))
}
