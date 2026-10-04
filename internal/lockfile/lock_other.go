//go:build !unix

package lockfile

// Lock is a no-op on platforms without flock. Ivy only ships Unix targets
// (linux/arm64, linux/amd64, darwin); this exists so a cross-platform build
// does not break, and it deliberately does not pretend to exclude a writer.
type Lock struct{}

// Acquire always succeeds on a platform without an advisory lock.
func Acquire(string) (*Lock, error) { return &Lock{}, nil }

// Release is a no-op.
func (l *Lock) Release() error { return nil }
