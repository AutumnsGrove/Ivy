// Package lockfile is the advisory lock that keeps one Ivy server on a data
// directory and lets `ivy restore` refuse while it is in use.
//
// It is a flock, so the lock dies with the process: a crashed server never
// leaves a stale lock that blocks the next start (unlike a PID file). The
// implementation is Unix-only, matching Ivy's targets; other platforms get a
// no-op lock (lock_other.go).
package lockfile

import "errors"

// ErrLocked reports that another process (or this one) holds the lock.
var ErrLocked = errors.New("lockfile: already locked")
