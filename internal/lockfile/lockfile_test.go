package lockfile

import (
	"errors"
	"path/filepath"
	"testing"
)

// One writer at a time: a second acquire of the same file fails instead of
// letting a restore clobber a running server's state.db.
func TestAcquireIsExclusive(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ivy.lock")

	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Acquire = %v, want ErrLocked", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

// Releasing twice is harmless, so a deferred Release never panics on an error
// path that already released.
func TestReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	lock, err := Acquire(filepath.Join(t.TempDir(), "ivy.lock"))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}
