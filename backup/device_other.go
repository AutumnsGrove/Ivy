//go:build !unix

package backup

// SameDevice cannot tell devices apart on platforms without a Unix stat, so it
// conservatively reports the same device and lets doctor warn.
func SameDevice(_, _ string) (bool, error) { return true, nil }
