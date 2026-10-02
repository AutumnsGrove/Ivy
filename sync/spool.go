package sync

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// errSpoolTooLarge reports a message that delivered more bytes than the
// download limit, whatever size the server announced.
var errSpoolTooLarge = errors.New("sync: message exceeds the download limit")

// writeSpool copies r to dst through a temporary file in the same directory and
// renames it into place, so a reader never sees a half-written message and a
// failed download leaves nothing behind. The limit is checked against the bytes
// that actually arrive, not the size the server announced.
//
// The file is not fsynced: it is a cache of mail the server still holds, and a
// torn file after power loss is refetched, so flash writes are saved.
func writeSpool(dst string, r io.Reader, limit int64) (int64, error) {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, fmt.Errorf("create spool dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".spool-*") // 0600 and a unique name
	if err != nil {
		return 0, fmt.Errorf("create spool file: %w", err)
	}
	n, copyErr := io.Copy(tmp, io.LimitReader(r, limit+1))
	closeErr := tmp.Close()
	switch {
	case copyErr != nil:
		_ = os.Remove(tmp.Name())
		return n, fmt.Errorf("spool message: %w", copyErr)
	case closeErr != nil:
		_ = os.Remove(tmp.Name())
		return n, fmt.Errorf("close spool file: %w", closeErr)
	case n > limit:
		_ = os.Remove(tmp.Name())
		return n, errSpoolTooLarge
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		_ = os.Remove(tmp.Name())
		return n, fmt.Errorf("place spool file: %w", err)
	}
	return n, nil
}
