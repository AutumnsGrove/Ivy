package sync

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failAfter yields n bytes and then an error, like a connection dropping in the
// middle of a message literal.
type failAfter struct {
	n   int
	err error
}

func (f *failAfter) Read(p []byte) (int, error) {
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

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestWriteSpoolWritesAtomicallyAndPrivately(t *testing.T) {
	t.Parallel()
	dst := filepath.Join(t.TempDir(), "spool", "folder", "7.eml")
	n, err := writeSpool(dst, strings.NewReader("hello message"), 1<<20)
	if err != nil {
		t.Fatalf("writeSpool: %v", err)
	}
	if n != int64(len("hello message")) {
		t.Errorf("n = %d", n)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "hello message" {
		t.Fatalf("file = %q, %v", got, err)
	}
	info, _ := os.Stat(dst)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 (mail is private)", info.Mode().Perm())
	}
	if names := dirEntries(t, filepath.Dir(dst)); len(names) != 1 {
		t.Errorf("directory holds %v, want only the message", names)
	}
}

func TestWriteSpoolLeavesNothingWhenTheSourceFails(t *testing.T) {
	t.Parallel()
	dst := filepath.Join(t.TempDir(), "7.eml")
	boom := errors.New("connection reset")
	_, err := writeSpool(dst, &failAfter{n: 100_000, err: boom}, 1<<20)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the source error", err)
	}
	if names := dirEntries(t, filepath.Dir(dst)); len(names) != 0 {
		t.Errorf("a failed download left %v behind", names)
	}
}

// A server may send more than RFC822.SIZE promised, so the limit is enforced
// on the bytes actually read.
func TestWriteSpoolEnforcesTheLimitOnWhatArrives(t *testing.T) {
	t.Parallel()
	dst := filepath.Join(t.TempDir(), "7.eml")
	_, err := writeSpool(dst, bytes.NewReader(make([]byte, 5000)), 4096)
	if !errors.Is(err, errSpoolTooLarge) {
		t.Fatalf("err = %v, want errSpoolTooLarge", err)
	}
	if names := dirEntries(t, filepath.Dir(dst)); len(names) != 0 {
		t.Errorf("an oversize download left %v behind", names)
	}

	if _, err := writeSpool(dst, bytes.NewReader(make([]byte, 4096)), 4096); err != nil {
		t.Errorf("a message exactly at the limit was refused: %v", err)
	}
}

func TestWriteSpoolReplacesAnEarlierCopy(t *testing.T) {
	t.Parallel()
	dst := filepath.Join(t.TempDir(), "7.eml")
	for _, body := range []string{"first copy, longer", "second"} {
		if _, err := writeSpool(dst, strings.NewReader(body), 1<<20); err != nil {
			t.Fatalf("writeSpool: %v", err)
		}
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "second" {
		t.Errorf("file = %q, want the latest copy only", got)
	}
}

var _ io.Reader = (*failAfter)(nil)
