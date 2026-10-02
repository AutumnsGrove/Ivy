package asset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/compress"
)

// FileServer serves the embedded frontend from fsys. For each file it prefers
// a precompressed sibling matching the request's Accept-Encoding, keeps
// fingerprint-named files immutable in caches, and falls back to index.html for
// client-side routes.
//
// fsys is expected to be immutable for the life of the process (the embedded
// build is): the entity tags are hashed once per file and cached, and files are
// served straight from the fs.FS without copying the whole body per request.
func FileServer(fsys fs.FS) http.Handler {
	return &fileServer{fsys: fsys}
}

type fileServer struct {
	fsys fs.FS
	// etags caches the hash of each served file. The set cannot change while the
	// binary runs, so a request re-reads no more than the file it serves (N4).
	etags sync.Map // served path -> ETag
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

	f, served, coding, ok := s.open(name, enc)
	if !ok {
		// A request for a directory-less route is a client-side path: the
		// static adapter's fallback is index.html. A missing file that has an
		// extension is a genuine 404.
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
		f, served, coding, ok = s.open(name, enc)
		if !ok {
			http.NotFound(w, r)
			return
		}
	}
	defer func() { _ = f.Close() }()

	// ServeContent wants a ReadSeeker; every embedded file is one. A non-seekable
	// fs.FS (only a test fake, in practice) is buffered once for the request.
	body, seekable := f.(io.ReadSeeker)
	if !seekable {
		data, err := io.ReadAll(f)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		body = bytes.NewReader(data)
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
	h.Set("ETag", s.etag(served, body))
	// ServeContent adds Content-Type, Content-Length, Range and HEAD handling.
	http.ServeContent(w, r, name, time.Time{}, body)
}

// open returns the file to serve: the precompressed sibling for enc when one
// exists, else the original. served is the path the bytes came from and keys the
// ETag cache; name's extension is what ServeContent sniffs, so the logical name
// is kept separate.
func (s *fileServer) open(name string, enc compress.Encoding) (f fs.File, served string, coding compress.Encoding, ok bool) {
	if suffix := suffixFor(enc); suffix != "" {
		if file, err := s.fsys.Open(name + suffix); err == nil {
			return file, name + suffix, enc, true
		}
	}
	file, err := s.fsys.Open(name)
	if err != nil {
		return nil, "", compress.Identity, false
	}
	return file, name, compress.Identity, true
}

// etag hashes a served file once and caches the tag, rewinding the reader so the
// same handle can serve the body. Email cannot reach these bytes, and the
// embedded set is fixed at build time, so the tag never goes stale.
func (s *fileServer) etag(served string, body io.ReadSeeker) string {
	if tag, ok := s.etags.Load(served); ok {
		return tag.(string)
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, body); err != nil {
		return ""
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	tag := `"` + hex.EncodeToString(sum.Sum(nil)[:16]) + `"`
	s.etags.Store(served, tag)
	return tag
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
