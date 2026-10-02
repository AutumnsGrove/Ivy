package asset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/compress"
)

// FileServer serves the embedded frontend from fsys. For each file it prefers
// a precompressed sibling matching the request's Accept-Encoding, keeps
// fingerprint-named files immutable in caches, and falls back to index.html for
// client-side routes.
func FileServer(fsys fs.FS) http.Handler {
	return &fileServer{fsys: fsys}
}

type fileServer struct {
	fsys fs.FS
}

func (s *fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	enc := compress.Negotiate(r.Header.Get("Accept-Encoding"))
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}

	data, coding, ok := s.read(name, enc)
	if !ok {
		// A request for a directory-less route is a client-side path: the
		// static adapter's fallback is index.html. A missing file that has an
		// extension is a genuine 404.
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
		data, coding, ok = s.read(name, enc)
		if !ok {
			http.NotFound(w, r)
			return
		}
	}

	h := w.Header()
	h.Set("Vary", "Accept-Encoding")
	if coding != compress.Identity {
		h.Set("Content-Encoding", string(coding))
	}
	if immutable(name) {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("ETag", etag(data))
	// ServeContent adds Content-Type, Content-Length, Range and HEAD handling.
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// read returns the named file, preferring a precompressed sibling for enc. The
// returned coding is Identity when the original was used.
func (s *fileServer) read(name string, enc compress.Encoding) ([]byte, compress.Encoding, bool) {
	if suffix := suffixFor(enc); suffix != "" {
		if data, err := fs.ReadFile(s.fsys, name+suffix); err == nil {
			return data, enc, true
		}
	}
	data, err := fs.ReadFile(s.fsys, name)
	if err != nil {
		return nil, compress.Identity, false
	}
	return data, compress.Identity, true
}

func suffixFor(enc compress.Encoding) string {
	switch enc {
	case compress.Brotli:
		return brSuffix
	case compress.Zstd:
		return zstSuffix
	case compress.Gzip:
		return gzSuffix
	default:
		return ""
	}
}

// immutable reports whether a path is content-hashed and so can be cached
// forever. SvelteKit emits those under _app/immutable/.
func immutable(name string) bool {
	return strings.HasPrefix(name, "_app/immutable/")
}

func etag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}
