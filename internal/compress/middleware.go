package compress

import (
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// DefaultMinSize is the body size above which a response is worth compressing.
// S7 measured that everything above about 1 KB lands at 35-40% of its size,
// while tiny bodies gain nothing and cost a header.
const DefaultMinSize = 1024

// Hot-path levels from S7: zstd default, brotli 5, gzip 6. Never brotli 9+.
const (
	gzipLevel   = 6
	brotliLevel = 5
)

// Middleware compresses responses with the best coding the request accepts.
// Responses below a minimum size, of an already-compressed media type, or in a
// handler that set its own Content-Encoding are sent as-is. Vary:
// Accept-Encoding is always set so caches keep the two forms apart.
func Middleware(next http.Handler) http.Handler {
	return Handler(next, DefaultMinSize)
}

// Handler is Middleware with an explicit size threshold (mostly for tests).
func Handler(next http.Handler, minSize int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := &responseWriter{
			inner:   w,
			enc:     Negotiate(r.Header.Get("Accept-Encoding")),
			minSize: minSize,
			head:    r.Method == http.MethodHead,
		}
		defer func() { _ = cw.Close() }()
		next.ServeHTTP(cw, r)
	})
}

// responseWriter buffers until it knows whether compressing pays, then streams
// either through a compressor or straight to the client. The original header
// is not sent until that decision is made, so Content-Encoding, Content-Length
// and the Content-Type sniff all land in the same header block.
type responseWriter struct {
	inner   http.ResponseWriter
	enc     Encoding
	minSize int
	head    bool

	status      int
	wroteHeader bool
	headerSent  bool

	decided     bool
	compressing bool
	encWriter   io.WriteCloser
	buf         []byte
}

func (w *responseWriter) Header() http.Header { return w.inner.Header() }

func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.decided {
		if w.enc == Identity {
			w.decide()
			return w.writeDirect(b)
		}
		w.buf = append(w.buf, b...)
		if len(w.buf) < w.minSize {
			return len(b), nil
		}
		w.decide()
		if _, err := w.drain(); err != nil {
			return 0, err
		}
		return len(b), nil
	}
	return w.writeDirect(b)
}

// decide is called exactly once, when the response is either large enough or
// being flushed. It fixes the response's final shape.
func (w *responseWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	if w.head || !bodyAllowed(w.status) || w.enc == Identity {
		return
	}
	h := w.inner.Header()
	if h.Get("Content-Encoding") != "" || mustNotTransform(w.status, h) {
		return
	}
	if h.Get("Content-Type") == "" && len(w.buf) > 0 {
		h.Set("Content-Type", http.DetectContentType(sample(w.buf)))
	}
	if !compressibleContentType(h.Get("Content-Type")) {
		return
	}
	w.compressing = true
	h.Set("Content-Encoding", string(w.enc))
	h.Del("Content-Length")
	suffixETag(h, string(w.enc))
	w.encWriter = acquire(w.enc, w.inner)
}

func (w *responseWriter) writeHeader() {
	if w.headerSent {
		return
	}
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	addVary(w.inner.Header())
	w.inner.WriteHeader(w.status)
	w.headerSent = true
}

// drain writes the buffered body (starting compression first if needed).
func (w *responseWriter) drain() (int, error) {
	if !w.decided {
		w.decide()
	}
	if len(w.buf) == 0 {
		return 0, nil
	}
	w.writeHeader()
	n := len(w.buf)
	var err error
	if w.compressing {
		_, err = w.encWriter.Write(w.buf)
	} else {
		_, err = w.inner.Write(w.buf)
	}
	w.buf = w.buf[:0]
	return n, err
}

func (w *responseWriter) writeDirect(b []byte) (int, error) {
	w.writeHeader()
	if w.compressing {
		return w.encWriter.Write(b)
	}
	return w.inner.Write(b)
}

// Flush forces the decision (so a stream starts before the threshold) and
// pushes the encoder and the connection so heartbeats arrive promptly.
func (w *responseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	w.decide()
	// The header must be sent before the encoder touches the underlying writer,
	// which would otherwise flush it implicitly and trigger a second
	// WriteHeader when the handler returns.
	w.writeHeader()
	if _, err := w.drain(); err != nil {
		return
	}
	if f, ok := w.encWriter.(interface{ Flush() error }); ok {
		_ = f.Flush()
	}
	if f, ok := w.inner.(http.Flusher); ok {
		f.Flush()
	}
}

// Close finishes the compressor and flushes any body left under the
// threshold. It is idempotent.
func (w *responseWriter) Close() error {
	if !w.decided {
		if len(w.buf) >= w.minSize {
			w.decide()
		} else {
			// Below the threshold: never compress.
			w.decided = true
		}
	}
	_, drainErr := w.drain()
	if w.compressing && w.encWriter != nil {
		w.writeHeader()
		closeErr := w.encWriter.Close()
		release(w.enc, w.encWriter)
		w.encWriter = nil
		if drainErr == nil {
			drainErr = closeErr
		}
	}
	w.writeHeader()
	return drainErr
}

// Unwrap lets http.ResponseController reach the real writer (Flush, Hijack).
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.inner }

var _ interface {
	http.ResponseWriter
	http.Flusher
	Unwrap() http.ResponseWriter
} = (*responseWriter)(nil)

func bodyAllowed(status int) bool {
	return status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified
}

// mustNotTransform reports responses whose bytes must reach the client as the
// handler wrote them: a byte range (Content-Range offsets refer to the
// identity representation) and anything marked Cache-Control: no-transform.
func mustNotTransform(status int, h http.Header) bool {
	if status == http.StatusPartialContent || h.Get("Content-Range") != "" {
		return true
	}
	for _, value := range h.Values("Cache-Control") {
		for directive := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(directive), "no-transform") {
				return true
			}
		}
	}
	return false
}

func sample(b []byte) []byte {
	const sniffLen = 512 // what http.DetectContentType reads
	if len(b) > sniffLen {
		return b[:sniffLen]
	}
	return b
}

// addVary appends Accept-Encoding to the Vary header without duplicating it or
// clobbering a handler's own Vary value.
func addVary(h http.Header) {
	vary := h.Get("Vary")
	if vary == "" {
		h.Set("Vary", "Accept-Encoding")
		return
	}
	for _, name := range strings.Split(vary, ",") {
		if strings.EqualFold(strings.TrimSpace(name), "Accept-Encoding") {
			return
		}
	}
	h.Set("Vary", vary+", Accept-Encoding")
}

// suffixETag marks the compressed representation so a cache keyed by ETag does
// not confuse it with the identity one.
func suffixETag(h http.Header, suffix string) {
	etag := h.Get("ETag")
	if etag == "" {
		return
	}
	if strings.HasSuffix(etag, `"`) {
		h.Set("ETag", etag[:len(etag)-1]+"-"+suffix+`"`)
		return
	}
	h.Set("ETag", etag+"-"+suffix)
}

var (
	gzipPool = sync.Pool{New: func() any {
		zw, err := gzip.NewWriterLevel(io.Discard, gzipLevel)
		if err != nil {
			panic(err)
		}
		return zw
	}}
	brotliPool = sync.Pool{New: func() any {
		return brotli.NewWriterLevel(io.Discard, brotliLevel)
	}}
	zstdPool = sync.Pool{New: func() any {
		zw, err := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			panic(err)
		}
		return zw
	}}
)

func acquire(enc Encoding, dst io.Writer) io.WriteCloser {
	switch enc {
	case Gzip:
		zw := gzipPool.Get().(*gzip.Writer)
		zw.Reset(dst)
		return zw
	case Brotli:
		bw := brotliPool.Get().(*brotli.Writer)
		bw.Reset(dst)
		return bw
	case Zstd:
		zw := zstdPool.Get().(*zstd.Encoder)
		zw.Reset(dst)
		return zw
	default:
		return nil
	}
}

func release(enc Encoding, w io.WriteCloser) {
	switch enc {
	case Gzip:
		gzipPool.Put(w)
	case Brotli:
		brotliPool.Put(w)
	case Zstd:
		zstdPool.Put(w)
	case Identity:
		// Nothing was acquired for an uncompressed response.
	}
}
