package secrets_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/internal/secrets"
)

func TestWriteThenReadRoundTrips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := secrets.Write(dir, "purelymail", "hunter2"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := secrets.Read(dir, "purelymail")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("Read = %q, want hunter2", got)
	}
}

func TestFileIsPrivateAndLeavesNoTempFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := secrets.Write(dir, "purelymail", "hunter2"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// A rewrite must also leave only the one file behind.
	if err := secrets.Write(dir, "purelymail", "hunter3"); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (no temp files): %v", len(entries), entries)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
	if got, _ := secrets.Read(dir, "purelymail"); got != "hunter3" {
		t.Errorf("second write did not replace the first: %q", got)
	}
}

func TestReadMissingIsNotFound(t *testing.T) {
	t.Parallel()
	_, err := secrets.Read(t.TempDir(), "purelymail")
	if !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestReadRefusesAFileOthersCanRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := secrets.Write(dir, "purelymail", "hunter2"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "purelymail")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Read(dir, "purelymail"); err == nil {
		t.Fatal("Read accepted a world-readable password file")
	}
}

func TestIDsThatCouldEscapeTheDirectoryAreRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, id := range []string{"", "../etc/passwd", "a/b", ".hidden", "UPPER", strings.Repeat("a", 65), "a b"} {
		if err := secrets.Write(dir, id, "x"); err == nil {
			t.Errorf("Write accepted id %q", id)
		}
		if _, err := secrets.Read(dir, id); err == nil || errors.Is(err, secrets.ErrNotFound) {
			t.Errorf("Read(%q) = %v, want an invalid-id error", id, err)
		}
	}
}

func TestPasswordBoundsAndErrorsNeverEchoTheSecret(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := secrets.Write(dir, "purelymail", ""); err == nil {
		t.Error("Write accepted an empty password")
	}
	huge := strings.Repeat("p", secrets.MaxPasswordBytes+1)
	err := secrets.Write(dir, "purelymail", huge)
	if err == nil {
		t.Fatal("Write accepted an oversized password")
	}
	if strings.Contains(err.Error(), "ppp") {
		t.Errorf("error text echoes the password: %v", err)
	}
	if err := secrets.Write(dir, "purelymail", "line1\nline2"); err == nil {
		t.Error("Write accepted a password with a newline")
	}
}

func TestWriteCreatesTheDirectoryPrivately(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "secrets")
	if err := secrets.Write(dir, "purelymail", "hunter2"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %o, want 700", perm)
	}
}
