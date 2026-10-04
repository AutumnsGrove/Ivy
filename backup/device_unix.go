//go:build unix

package backup

import (
	"fmt"
	"os"
	"syscall"
)

// SameDevice reports whether two paths live on the same filesystem, so doctor
// can warn when every backup target shares the DB's disk (ARCHITECTURE.md 9).
// A path that does not exist is an error; the caller resolves the nearest
// existing ancestor first.
func SameDevice(a, b string) (bool, error) {
	da, err := deviceOf(a)
	if err != nil {
		return false, err
	}
	db, err := deviceOf(b)
	if err != nil {
		return false, err
	}
	return da == db, nil
}

func deviceOf(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("backup: %s has no device information", path)
	}
	// The device id is a small positive number on every platform Ivy targets;
	// the type differs (int32 on darwin, uint64 on linux), so it is widened here.
	return uint64(stat.Dev), nil //nolint:gosec // G115: see above
}
