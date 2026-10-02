package asset

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

var (
	bigJS    = bytes.Repeat([]byte("export const answer = () => 42; // ivy\n"), 200)
	bigCSS   = bytes.Repeat([]byte(".inbox { color: #d8b4fe; }\n"), 200)
	woffData = []byte("\x00\x01\x00\x00fake font bytes")
	brOnly   = []byte("pretend this is already a brotli stream")
)

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// decodeVariant reads a precompressed sibling and returns the original bytes.
func decodeVariant(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var r io.Reader
	switch {
	case bytes.HasSuffix([]byte(path), []byte(brSuffix)):
		r = brotli.NewReader(bytes.NewReader(raw))
	case bytes.HasSuffix([]byte(path), []byte(zstSuffix)):
		zr, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("zstd reader %s: %v", path, err)
		}
		defer zr.Close()
		r = zr
	case bytes.HasSuffix([]byte(path), []byte(gzSuffix)):
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("gzip reader %s: %v", path, err)
		}
		defer gz.Close()
		r = gz
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return decoded
}

func TestPrecompressWritesSmallerVariants(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "app.js", bigJS)
	writeFile(t, dir, "style.css", bigCSS)
	writeFile(t, dir, "logo.woff2", woffData)
	writeFile(t, dir, "fonts/x.png", []byte("not really a png"))
	writeFile(t, dir, "precompressed.js"+brSuffix, brOnly)
	writeFile(t, dir, "index.html", []byte("<html></html>"))

	stats, err := Precompress(dir)
	if err != nil {
		t.Fatalf("Precompress: %v", err)
	}

	for _, name := range []string{"app.js", "style.css"} {
		original := []byte(bigJS)
		if name == "style.css" {
			original = bigCSS
		}
		for _, suffix := range []string{brSuffix, zstSuffix, gzSuffix} {
			path := filepath.Join(dir, name+suffix)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("expected variant %s: %v", name+suffix, err)
			}
			if info.Size() >= int64(len(original)) {
				t.Errorf("%s is %d bytes, not smaller than %d", name+suffix, info.Size(), len(original))
			}
			if got := decodeVariant(t, path); !bytes.Equal(got, original) {
				t.Errorf("%s does not decode to the original", name+suffix)
			}
		}
	}

	for _, untouched := range []string{
		"logo.woff2" + brSuffix,
		"fonts/x.png" + zstSuffix,
		"precompressed.js" + brSuffix + brSuffix,
	} {
		if _, err := os.Stat(filepath.Join(dir, untouched)); err == nil {
			t.Errorf("%s should not have been written", untouched)
		}
	}

	if stats.Compressed != 2 {
		t.Errorf("Compressed = %d, want 2", stats.Compressed)
	}
}

func TestPrecompressIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "app.js", bigJS)

	if _, err := Precompress(dir); err != nil {
		t.Fatalf("first Precompress: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "app.js"+zstSuffix))
	if err != nil {
		t.Fatalf("read variant: %v", err)
	}
	if _, err := Precompress(dir); err != nil {
		t.Fatalf("second Precompress: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(dir, "app.js"+zstSuffix))
	if err != nil {
		t.Fatalf("read variant again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("re-running Precompress changed an existing variant")
	}
	if _, err := os.Stat(filepath.Join(dir, "app.js"+zstSuffix+zstSuffix)); err == nil {
		t.Errorf("Precompress recursed onto its own output")
	}
}
