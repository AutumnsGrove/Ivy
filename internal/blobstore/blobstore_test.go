package blobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// countFiles is every regular file under root, for the de-duplication check.
func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return n
}

// disabledBlobs is the only copy of server-deleted mail, so the store must be
// content-addressed and readable by the hash alone.
func TestPutStoresContentAndReturnsItsHash(t *testing.T) {
	t.Parallel()
	s := mustOpen(t, t.TempDir())

	got, n, err := s.Put(context.Background(), strings.NewReader("a hidden message"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if want := hashOf("a hidden message"); got != want {
		t.Errorf("hash = %s, want %s", got, want)
	}
	if n != int64(len("a hidden message")) {
		t.Errorf("n = %d, want %d", n, len("a hidden message"))
	}
	if !s.Has(got) {
		t.Errorf("Has(%s) = false after Put", got)
	}
	data, err := os.ReadFile(s.Path(got))
	if err != nil || string(data) != "a hidden message" {
		t.Fatalf("stored bytes = %q, %v", data, err)
	}
	info, err := os.Stat(s.Path(got))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
}

// Two copies of the same mail are one blob: this is what keeps a repeated
// newsletter from filling the backup target.
func TestPutDeduplicatesIdenticalContent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := mustOpen(t, root)

	first, _, err := s.Put(context.Background(), strings.NewReader("same bytes"))
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	before, err := os.Stat(s.Path(first))
	if err != nil {
		t.Fatalf("stat first: %v", err)
	}
	second, _, err := s.Put(context.Background(), strings.NewReader("same bytes"))
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if first != second {
		t.Errorf("hash changed on identical content: %s vs %s", first, second)
	}
	if n := countFiles(t, root); n != 1 {
		t.Errorf("file count = %d after two identical Puts, want 1", n)
	}
	after, err := os.Stat(s.Path(first))
	if err != nil {
		t.Fatalf("stat second: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("dedupe rewrote the existing blob (mtime changed)")
	}
}

func TestPutDistinguishesDifferentContent(t *testing.T) {
	t.Parallel()
	s := mustOpen(t, t.TempDir())
	a, _, err := s.Put(context.Background(), strings.NewReader("one"))
	if err != nil {
		t.Fatalf("Put a: %v", err)
	}
	b, _, err := s.Put(context.Background(), strings.NewReader("two"))
	if err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if a == b {
		t.Errorf("different content shared the hash %s", a)
	}
}

// A read error in the middle of a message (a dropped IMAP literal) must leave
// the store untouched, never a half-written blob under a real name.
func TestPutLeavesNothingBehindOnFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := mustOpen(t, root)

	_, _, err := s.Put(context.Background(), &failingReader{err: errors.New("connection reset")})
	if err == nil {
		t.Fatal("Put of a failing reader returned nil error")
	}
	if n := countFiles(t, root); n != 0 {
		t.Errorf("file count = %d after a failed Put, want 0", n)
	}
}

// Nothing blocks without a deadline or context (STANDARDS.md 4a): a cancelled
// Put stops reading and cleans up.
func TestPutStopsOnCancelledContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := mustOpen(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := s.Put(ctx, bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put with cancelled context = %v, want context.Canceled", err)
	}
	if n := countFiles(t, root); n != 0 {
		t.Errorf("file count = %d after a cancelled Put, want 0", n)
	}
}

// An unknown or malformed hash is refused rather than read as a path.
func TestPathRejectsMalformedHash(t *testing.T) {
	t.Parallel()
	s := mustOpen(t, t.TempDir())
	for _, bad := range []string{"", "abc", "../../etc/passwd", strings.Repeat("z", 64)} {
		if _, err := s.pathFor(bad); err == nil {
			t.Errorf("pathFor(%q) accepted a malformed hash", bad)
		}
	}
}

func mustOpen(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// failingReader yields some bytes and then fails, like a message literal cut
// off mid-flight.
type failingReader struct {
	n   int
	err error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n == 0 {
		return 0, f.err
	}
	k := min(len(p), f.n)
	for i := range k {
		p[i] = 'x'
	}
	f.n -= k
	return k, nil
}

var _ io.Reader = (*failingReader)(nil)
