//go:build unix

package lockfile

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Lock is a held advisory lock.
type Lock struct{ f *os.File }

// Acquire takes an exclusive, non-blocking lock on path, creating the file if
// needed. A held lock returns ErrLocked immediately rather than waiting.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: the caller chose the data dir
	if err != nil {
		return nil, fmt.Errorf("lockfile: open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lockfile: lock %s: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Release drops the lock. It is safe to call more than once.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	closeErr := l.f.Close()
	l.f = nil
	return errors.Join(err, closeErr)
}
