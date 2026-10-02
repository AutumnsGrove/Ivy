package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
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
	tmp, err := os.CreateTemp(dir, tempPrefix+"*") // 0600 and a unique name
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

// spoolGrace is how old an unowned file must be before the sweep takes it, so a
// download in progress (its temp file, or a message written an instant before
// its row) is never removed from under the code writing it.
const spoolGrace = time.Hour

// tempPrefix marks the files writeSpool creates before renaming them into place.
const tempPrefix = ".spool-"

// SweepSpool removes spool files nothing owns and returns how many it removed.
// Two things leave such files: a crash between creating a temp file and renaming
// it, and a download whose row was never written. A file that a row points at is
// always kept, a disabled message's included, because nothing is ever erased
// (CLAUDE.md 5); only unowned files older than spoolGrace go.
func SweepSpool(ctx context.Context, dbs *store.DBs, now time.Time) (int, error) {
	root := filepath.Join(dbs.Dir, "spool")
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	owned, err := dbs.SpooledPaths(ctx)
	if err != nil {
		return 0, err
	}

	removed := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // removed by someone else since the listing
			}
			return err
		}
		if now.Sub(info.ModTime()) < spoolGrace {
			return nil
		}
		rel, err := filepath.Rel(dbs.Dir, path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(d.Name(), tempPrefix) && owned[filepath.ToSlash(rel)] {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) { //nolint:gosec // G122: our own spool, files older than the grace period
			return fmt.Errorf("remove orphaned spool file: %w", err)
		}
		removed++
		return nil
	})
	if err != nil {
		return removed, fmt.Errorf("sweep spool: %w", err)
	}
	return removed, nil
}
