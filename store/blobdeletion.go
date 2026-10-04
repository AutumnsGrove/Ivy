package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// pendingDeletionsDir holds one empty marker file per blob a purge still owes an
// erasure, named by the blob's hash. It is a directory in the data dir rather
// than a table in state.db on purpose: restoring an older state.db snapshot
// would roll a table back and forget a debt an offline backup target is still
// owed, leaving a purged message in the target for good (N29). A marker file is
// also created and removed atomically by whichever process (the server or `ivy
// backup`) gets there, with no locking.
const pendingDeletionsDir = "pending-blob-deletions"

func (d *DBs) pendingDeletionsPath() string {
	return filepath.Join(d.Dir, pendingDeletionsDir)
}

// RecordPendingBlobDeletion notes a purged blob for the backup to erase from
// every target. A deleted row can no longer say which bytes to remove, and a
// target may be offline at purge time, so the erasure is recorded durably and
// retried until every target is clean (N24). The marker is synced before this
// returns, because the caller deletes the row next. An empty hash (a message that
// was never downloaded) records nothing, a malformed hash is an error, and
// recording twice is a no-op.
func (d *DBs) RecordPendingBlobDeletion(ctx context.Context, hash string) error {
	if hash == "" {
		return nil
	}
	if _, err := d.Blobs.RelPath(hash); err != nil {
		return fmt.Errorf("record pending blob deletion: %w", err)
	}
	dir := d.pendingDeletionsPath()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("record pending blob deletion %s: %w", hash, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, hash), os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: the hash was validated as 64 hex characters above
	if err != nil {
		return fmt.Errorf("record pending blob deletion %s: %w", hash, err)
	}
	syncErr := f.Sync()
	if err := errors.Join(syncErr, f.Close()); err != nil {
		return fmt.Errorf("record pending blob deletion %s: %w", hash, err)
	}
	// fsync the directory so a power loss cannot lose the entry the purge relies on.
	if dd, err := os.Open(dir); err == nil { //nolint:gosec // G304: our own data directory
		_ = dd.Sync()
		_ = dd.Close()
	}
	return ctx.Err()
}

// PendingBlobDeletions returns the hashes a purge still has to erase from the
// backup targets, oldest recorded first.
func (d *DBs) PendingBlobDeletions(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(d.pendingDeletionsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pending blob deletions: %w", err)
	}
	type marker struct {
		hash string
		at   int64
	}
	var markers []marker
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		// A stray file that is not a blob hash is not a debt; skipping it keeps one
		// odd name from stopping every erasure after it.
		if _, err := d.Blobs.RelPath(e.Name()); err != nil {
			continue
		}
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // cleared since the listing
		}
		if err != nil {
			return nil, fmt.Errorf("pending blob deletions: %w", err)
		}
		markers = append(markers, marker{hash: e.Name(), at: info.ModTime().UnixNano()})
	}
	sort.Slice(markers, func(i, j int) bool {
		if markers[i].at != markers[j].at {
			return markers[i].at < markers[j].at
		}
		return markers[i].hash < markers[j].hash
	})
	var hashes []string
	for _, m := range markers {
		hashes = append(hashes, m.hash)
	}
	return hashes, ctx.Err()
}

// ClearPendingBlobDeletion forgets a hash the backup has erased everywhere.
// Clearing an unknown hash is a no-op, and so is a malformed one: only a valid
// hash can have been recorded, and the name must never reach the filesystem.
func (d *DBs) ClearPendingBlobDeletion(_ context.Context, hash string) error {
	if _, err := d.Blobs.RelPath(hash); err != nil {
		return nil
	}
	if err := os.Remove(filepath.Join(d.pendingDeletionsPath(), hash)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear pending blob deletion %s: %w", hash, err)
	}
	return nil
}

// BlobReferenced reports whether any row still points at a blob hash, so a purge
// keeps the bytes when another hidden message shares them (N8). An empty hash is
// never referenced. It counts a row the purge is about to delete too; callers
// purge a row at a time and re-check afterwards through PendingBlobDeletions.
func (d *DBs) BlobReferenced(ctx context.Context, hash string) (bool, error) {
	if hash == "" {
		return false, nil
	}
	var n int
	if err := d.Mirror.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM messages WHERE disabled_blob = ?`, hash).Scan(&n); err != nil {
		return false, fmt.Errorf("blob references %s: %w", hash, err)
	}
	return n > 0, nil
}
