package compress

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

var bigBody = bytes.Repeat([]byte("ivy is a fast self-hosted mail client. "), 200)

// newServer wraps h in the middleware and returns a running server.
func newServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(Middleware(h))
	t.Cleanup(srv.Close)
	return srv
}

// syncBuffer collects the server's error log so a test can assert that the
// net/http plumbing was happy (no superfluous WriteHeader).
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newLoggedServer(t *testing.T, h http.Handler) (*httptest.Server, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	srv := httptest.NewUnstartedServer(Middleware(h))
	srv.Config.ErrorLog = log.New(logs, "", 0)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, logs
}

// get performs a request with an explicit Accept-Encoding. Setting it (even to
// "") stops Go's transport adding its own gzip and auto-decompressing.
func get(t *testing.T, srv *httptest.Server, acceptEncoding string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept-Encoding", acceptEncoding)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// readAll decodes the body according to its Content-Encoding.
func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	var r io.Reader = resp.Body
	switch resp.Header.Get("Content-Encoding") {
	case "":
	case "gzip":
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		defer gz.Close()
		r = gz
	case "br":
		r = brotli.NewReader(resp.Body)
	case "zstd":
		zr, err := zstd.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("zstd reader: %v", err)
		}
		defer zr.Close()
		r = zr
	default:
		t.Fatalf("unknown Content-Encoding %q", resp.Header.Get("Content-Encoding"))
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}

func largeHandler(contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = w.Write(bigBody)
	})
}

func TestMiddlewareCompressesWithAcceptedEncoding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		accept string
		want   string
	}{
		{"gzip", "gzip"},
		{"br", "br"},
		{"zstd", "zstd"},
		{"gzip, deflate, br, zstd", "zstd"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			srv := newServer(t, largeHandler("application/json"))
			resp := get(t, srv, tt.accept)
			if got := resp.Header.Get("Content-Encoding"); got != tt.want {
				t.Fatalf("Content-Encoding = %q, want %q", got, tt.want)
			}
			if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
				t.Errorf("Vary = %q, want Accept-Encoding", resp.Header.Get("Vary"))
			}
			body := readAll(t, resp)
			if !bytes.Equal(body, bigBody) {
				t.Errorf("decoded body differs from original (len %d vs %d)", len(body), len(bigBody))
			}
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read raw: %v", err)
			}
			if len(raw) >= len(bigBody) {
				t.Errorf("compressed size %d not smaller than %d", len(raw), len(bigBody))
			}
		})
	}
}

func TestMiddlewareLeavesIdentityWhenNotAsked(t *testing.T) {
	t.Parallel()
	for _, accept := range []string{"", "identity", "deflate"} {
		t.Run(accept, func(t *testing.T) {
			t.Parallel()
			srv := newServer(t, largeHandler("application/json"))
			resp := get(t, srv, accept)
			if got := resp.Header.Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding = %q, want none", got)
			}
			if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
				t.Errorf("Vary = %q, want Accept-Encoding even for identity", resp.Header.Get("Vary"))
			}
			if body := readAll(t, resp); !bytes.Equal(body, bigBody) {
				t.Errorf("body differs (len %d vs %d)", len(body), len(bigBody))
			}
		})
	}
}

func TestMiddlewareLeavesSmallBodiesAlone(t *testing.T) {
	t.Parallel()
	small := []byte("hi")
	srv := newServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(small)
	}))
	resp := get(t, srv, "zstd")
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none for a body under the threshold", got)
	}
	if body := readAll(t, resp); !bytes.Equal(body, small) {
		t.Errorf("body = %q, want %q", body, small)
	}
}

func TestMiddlewareSkipsAlreadyCompressedContent(t *testing.T) {
	t.Parallel()
	srv := newServer(t, largeHandler("image/png"))
	resp := get(t, srv, "zstd")
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none for image/png", got)
	}
}

func TestMiddlewareDoesNotDoubleCompress(t *testing.T) {
	t.Parallel()
	payload := bigBody
	srv := newServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "custom")
		_, _ = w.Write(payload)
	}))
	resp := get(t, srv, "zstd")
	if got := resp.Header.Get("Content-Encoding"); got != "custom" {
		t.Fatalf("Content-Encoding = %q, want custom", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Errorf("body was altered")
	}
}

func TestMiddlewareDropsStaleContentLength(t *testing.T) {
	t.Parallel()
	srv := newServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", fmt.Sprint(len(bigBody)))
		_, _ = w.Write(bigBody)
	}))
	resp := get(t, srv, "zstd")
	if resp.ContentLength == int64(len(bigBody)) {
		t.Fatalf("Content-Length = %d, want the stale uncompressed length removed", resp.ContentLength)
	}
	if body := readAll(t, resp); !bytes.Equal(body, bigBody) {
		t.Errorf("decoded body is truncated")
	}
}

func TestMiddlewareSuffixesETag(t *testing.T) {
	t.Parallel()
	srv := newServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write(bigBody)
	}))
	resp := get(t, srv, "zstd")
	if got := resp.Header.Get("ETag"); got != `"abc123-zstd"` {
		t.Errorf("ETag = %q, want \"abc123-zstd\"", got)
	}
}

func TestMiddlewareKeepsHandlerVaryHeaders(t *testing.T) {
	t.Parallel()
	srv := newServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Vary", "Cookie")
		_, _ = w.Write(bigBody)
	}))
	resp := get(t, srv, "zstd")
	vary := resp.Header.Get("Vary")
	if !strings.Contains(vary, "Cookie") || !strings.Contains(vary, "Accept-Encoding") {
		t.Errorf("Vary = %q, want both Cookie and Accept-Encoding", vary)
	}
}

// TestMiddlewareFlushesStreams proves an SSE-shaped response reaches the client
// event by event rather than waiting for the handler to return.
func TestMiddlewareFlushesStreams(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := newServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		rc := http.NewResponseController(w)
		_, _ = io.WriteString(w, "event: one\n\n")
		if err := rc.Flush(); err != nil {
			t.Errorf("flush: %v", err)
		}
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		_, _ = io.WriteString(w, "event: two\n\n")
	}))
	resp := get(t, srv, "gzip")
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	scanner := bufio.NewScanner(gz)
	first := make(chan string, 1)
	go func() {
		if scanner.Scan() {
			first <- scanner.Text()
		}
		close(first)
	}()
	select {
	case line, ok := <-first:
		if !ok || line != "event: one" {
			t.Fatalf("first event = %q, ok=%v", line, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first event did not arrive before the handler was released")
	}
	close(release)
	rest, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read rest: %v", err)
	}
	if !strings.Contains(string(rest), "event: two") {
		t.Errorf("second event missing from %q", rest)
	}
}

// TestMiddlewareCompressesEveryTextShape covers the content types the API and
// the rendered mail surface actually produce.
// TestMiddlewareStreamsAfterHeaderFlush covers the common SSE shape: headers
// are flushed before the first event to open the stream.
func TestMiddlewareStreamsAfterHeaderFlush(t *testing.T) {
	t.Parallel()
	srv, logs := newLoggedServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		rc := http.NewResponseController(w)
		if err := rc.Flush(); err != nil {
			t.Errorf("header flush: %v", err)
		}
		_, _ = io.WriteString(w, "data: hello\n\n")
		_, _ = io.WriteString(w, "data: world\n\n")
	}))
	resp := get(t, srv, "zstd")
	if got := resp.Header.Get("Content-Encoding"); got != "zstd" {
		t.Fatalf("Content-Encoding = %q, want zstd", got)
	}
	body := readAll(t, resp)
	if !strings.Contains(string(body), "data: hello") || !strings.Contains(string(body), "data: world") {
		t.Errorf("body = %q, want both events", body)
	}
	if logs.String() != "" {
		t.Errorf("server logged a problem: %s", logs.String())
	}
}

func TestMiddlewareNoContentHasNoEncoding(t *testing.T) {
	t.Parallel()
	srv := newServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	resp := get(t, srv, "zstd")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none on a bodyless status", got)
	}
}

func TestMiddlewareCompressesEveryTextShape(t *testing.T) {
	t.Parallel()
	for _, ct := range []string{
		"text/html; charset=utf-8",
		"text/plain",
		"application/javascript",
		"image/svg+xml",
		"application/ld+json",
	} {
		t.Run(ct, func(t *testing.T) {
			t.Parallel()
			srv := newServer(t, largeHandler(ct))
			resp := get(t, srv, "zstd")
			if got := resp.Header.Get("Content-Encoding"); got != "zstd" {
				t.Fatalf("%s: Content-Encoding = %q, want zstd", ct, got)
			}
			if body := readAll(t, resp); !bytes.Equal(body, bigBody) {
				t.Errorf("%s: decoded body differs", ct)
			}
		})
	}
}

// A 206 body is a byte range of the identity representation; compressing it
// makes Content-Range describe bytes the client never receives.
func TestMiddlewareLeavesPartialContentAlone(t *testing.T) {
	t.Parallel()
	srv := newServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/100000", len(bigBody)-1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(bigBody)
	}))
	resp := get(t, srv, "gzip, br, zstd")
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("206 response was compressed with %q", got)
	}
	if !bytes.Equal(readAll(t, resp), bigBody) {
		t.Fatal("206 body was altered")
	}
}

// Cache-Control: no-transform forbids intermediaries, which includes us, from
// changing the representation.
func TestMiddlewareHonoursNoTransform(t *testing.T) {
	t.Parallel()
	srv := newServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, no-transform")
		_, _ = w.Write(bigBody)
	}))
	resp := get(t, srv, "gzip, br, zstd")
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("no-transform response was compressed with %q", got)
	}
}
