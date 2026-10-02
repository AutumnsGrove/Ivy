package asset

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const immutablePath = "_app/immutable/chunk.abcdef123456.js"

func testFS(t *testing.T) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{
		"index.html":              &fstest.MapFile{Data: []byte("<!doctype html><title>Ivy</title>")},
		"robots.txt":              &fstest.MapFile{Data: []byte("User-agent: *\n")},
		"garden/vines.svg":        &fstest.MapFile{Data: []byte("<svg>vines</svg>")},
		immutablePath:             &fstest.MapFile{Data: bigJS},
		"logo.png":                &fstest.MapFile{Data: []byte("\x89PNGfake")},
		"only-gzip.js":            &fstest.MapFile{Data: bigJS},
		"only-gzip.js" + gzSuffix: &fstest.MapFile{Data: mustEncode(t, gzSuffix, bigJS)},
	}
	for _, v := range variants {
		m[immutablePath+v.suffix] = &fstest.MapFile{Data: mustEncode(t, v.suffix, bigJS)}
	}
	return m
}

func mustEncode(t testing.TB, suffix string, data []byte) []byte {
	t.Helper()
	for _, v := range variants {
		if v.suffix == suffix {
			enc, err := v.encode(data)
			if err != nil {
				t.Fatalf("encode %s: %v", suffix, err)
			}
			return enc
		}
	}
	t.Fatalf("unknown suffix %s", suffix)
	return nil
}

func serve(t *testing.T, fsys fs.FS, method, target, accept string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if accept != "" {
		req.Header.Set("Accept-Encoding", accept)
	} else {
		req.Header["Accept-Encoding"] = nil
	}
	rec := httptest.NewRecorder()
	FileServer(fsys).ServeHTTP(rec, req)
	return rec.Result()
}

func decodeResponse(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	switch resp.Header.Get("Content-Encoding") {
	case "":
		return raw
	case "br":
		return mustDecode(t, "br", raw)
	case "zstd":
		return mustDecode(t, "zstd", raw)
	case "gzip":
		return mustDecode(t, "gzip", raw)
	}
	t.Fatalf("unknown coding %q", resp.Header.Get("Content-Encoding"))
	return nil
}

func TestFileServerServesPrecompressedVariant(t *testing.T) {
	t.Parallel()
	fsys := testFS(t)
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
			resp := serve(t, fsys, http.MethodGet, "/"+immutablePath, tt.accept)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Encoding"); got != tt.want {
				t.Fatalf("Content-Encoding = %q, want %q", got, tt.want)
			}
			if !contains(resp.Header.Get("Vary"), "Accept-Encoding") {
				t.Errorf("Vary = %q", resp.Header.Get("Vary"))
			}
			if body := decodeResponse(t, resp); !bytes.Equal(body, bigJS) {
				t.Errorf("decoded body differs from the original")
			}
		})
	}
}

func TestFileServerFallsBackToIdentity(t *testing.T) {
	t.Parallel()
	resp := serve(t, testFS(t), http.MethodGet, "/"+immutablePath, "")
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none", got)
	}
	if body := decodeResponse(t, resp); !bytes.Equal(body, bigJS) {
		t.Errorf("body differs from the original")
	}
}

func TestFileServerFallsBackWhenVariantMissing(t *testing.T) {
	t.Parallel()
	resp := serve(t, testFS(t), http.MethodGet, "/only-gzip.js", "zstd")
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want identity when the zstd variant is absent", got)
	}
	if body := decodeResponse(t, resp); !bytes.Equal(body, bigJS) {
		t.Errorf("body differs from the original")
	}
	resp = serve(t, testFS(t), http.MethodGet, "/only-gzip.js", "gzip")
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if body := decodeResponse(t, resp); !bytes.Equal(body, bigJS) {
		t.Errorf("gzip body differs from the original")
	}
}

func TestFileServerCacheHeaders(t *testing.T) {
	t.Parallel()
	fsys := testFS(t)

	immutable := serve(t, fsys, http.MethodGet, "/"+immutablePath, "")
	if got := immutable.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("immutable Cache-Control = %q", got)
	}
	if immutable.Header.Get("ETag") == "" {
		t.Errorf("immutable response has no ETag")
	}

	index := serve(t, fsys, http.MethodGet, "/", "")
	if got := index.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", got)
	}
	etag := index.Header.Get("ETag")
	if etag == "" {
		t.Fatalf("index has no ETag")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	FileServer(fsys).ServeHTTP(rec, req)
	if code := rec.Result().StatusCode; code != http.StatusNotModified {
		t.Errorf("conditional request status = %d, want 304", code)
	}
}

// TestFileServerServesRanges proves the server hands ServeContent a seekable
// body: N4 serves the embedded file directly instead of copying it, and a range
// client (video, resumable download) must still work.
func TestFileServerServesRanges(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/"+immutablePath, nil)
	req.Header["Accept-Encoding"] = nil
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()
	FileServer(testFS(t)).ServeHTTP(rec, req)
	resp := rec.Result()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Content-Range"), "bytes 0-9/"+strconv.Itoa(len(bigJS)); got != want {
		t.Errorf("Content-Range = %q, want %q", got, want)
	}
	if body, _ := io.ReadAll(resp.Body); !bytes.Equal(body, bigJS[:10]) {
		t.Errorf("range body = %q, want the first 10 bytes", body)
	}
}

func TestFileServerSPAFallback(t *testing.T) {
	t.Parallel()
	resp := serve(t, testFS(t), http.MethodGet, "/inbox/thread/12345", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body := decodeResponse(t, resp); !bytes.Contains(body, []byte("Ivy")) {
		t.Errorf("SPA fallback served %q, want index.html", body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("SPA fallback Cache-Control = %q, want no-cache", got)
	}
}

func TestFileServerMissingAssetIs404(t *testing.T) {
	t.Parallel()
	resp := serve(t, testFS(t), http.MethodGet, "/missing.js", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestFileServerContentTypes(t *testing.T) {
	t.Parallel()
	fsys := testFS(t)
	tests := map[string]string{
		"/garden/vines.svg": "image/svg+xml",
		"/robots.txt":       "text/plain",
	}
	for target, wantPrefix := range tests {
		resp := serve(t, fsys, http.MethodGet, target, "")
		if got := resp.Header.Get("Content-Type"); !contains(got, wantPrefix) {
			t.Errorf("%s Content-Type = %q, want prefix %q", target, got, wantPrefix)
		}
	}
}

func TestFileServerMethodAndHead(t *testing.T) {
	t.Parallel()
	resp := serve(t, testFS(t), http.MethodPost, "/index.html", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", resp.StatusCode)
	}
	resp = serve(t, testFS(t), http.MethodHead, "/"+immutablePath, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Errorf("HEAD body has %d bytes, want none", len(body))
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}

func mustDecode(t *testing.T, coding string, raw []byte) []byte {
	t.Helper()
	var r io.Reader
	switch coding {
	case "br":
		r = brotli.NewReader(bytes.NewReader(raw))
	case "zstd":
		zr, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("zstd reader: %v", err)
		}
		defer zr.Close()
		r = zr
	case "gzip":
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		defer gz.Close()
		r = gz
	default:
		t.Fatalf("unknown coding %q", coding)
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("decode %s: %v", coding, err)
	}
	return decoded
}
