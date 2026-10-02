package compress

import (
	"mime"
	"strconv"
	"strings"
)

// Encoding is a supported content coding. The zero value, Identity, means no
// compression.
type Encoding string

// The codings Ivy speaks. Preference order for equal quality is zstd, brotli,
// then gzip (S7: zstd is the cheapest per request and Safari decodes it).
const (
	Identity Encoding = ""
	Gzip     Encoding = "gzip"
	Brotli   Encoding = "br"
	Zstd     Encoding = "zstd"
)

var preference = []Encoding{Zstd, Brotli, Gzip}

// Negotiate returns the best encoding for an Accept-Encoding header, driven
// strictly by that header. When nothing acceptable is offered it returns
// Identity.
//
// Quality values are honoured (a lower q can beat our preference order, an
// explicit q=0 refuses a coding, and * stands in for uncoded names). A missing
// or empty header means no compression: we do not spend CPU on a client that
// did not ask.
func Negotiate(header string) Encoding {
	if strings.TrimSpace(header) == "" {
		return Identity
	}
	q := parseAcceptEncoding(header)
	best := Identity
	bestQ := 0.0
	for _, enc := range preference {
		value, ok := q[string(enc)]
		if !ok {
			continue
		}
		if value > bestQ {
			best, bestQ = enc, value
		}
	}
	return best
}

// parseAcceptEncoding maps a coding name to its quality, applying the *
// wildcard to codings that were not named explicitly. Unnamed codings with no
// wildcard are absent.
func parseAcceptEncoding(header string) map[string]float64 {
	named := make(map[string]float64)
	wildcard := -1.0
	for part := range strings.SplitSeq(header, ",") {
		name, value, ok := parseCoding(part)
		if !ok {
			continue
		}
		if name == "*" {
			wildcard = value
			continue
		}
		named[name] = value
	}
	if wildcard < 0 {
		return named
	}
	for _, enc := range preference {
		if _, ok := named[string(enc)]; !ok {
			named[string(enc)] = wildcard
		}
	}
	return named
}

func parseCoding(part string) (name string, q float64, ok bool) {
	fields := strings.Split(part, ";")
	name = strings.ToLower(strings.TrimSpace(fields[0]))
	if name == "" {
		return "", 0, false
	}
	q = 1
	for _, param := range fields[1:] {
		key, value, found := strings.Cut(strings.TrimSpace(param), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "q") {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || parsed < 0 || parsed > 1 {
			// An unparseable quality is not acceptable.
			return name, 0, true
		}
		q = parsed
	}
	return name, q, true
}

// compressibleContentType reports whether a response of this Content-Type is
// worth compressing. Already-compressed media (images, fonts, archives) are
// left alone.
func compressibleContentType(contentType string) bool {
	mediaType := contentType
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
		mediaType = parsed
	} else if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "" {
		return false
	}
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	if mediaType == "application/json" || strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json") {
		return true
	}
	switch mediaType {
	case "application/javascript",
		"application/xml",
		"application/xhtml+xml",
		"application/wasm",
		"application/x-ndjson",
		"image/svg+xml":
		return true
	}
	return strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+xml")
}
