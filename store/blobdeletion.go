package store

import (
	"context"
	"fmt"
)

// RecordPendingBlobDeletion notes a purged blob for the backup to erase from
// every target. A deleted row can no longer say which bytes to remove, and a
// target may be offline at purge time, so the erasure is recorded in the backed
// up state and retried until every target is clean (N24). An empty hash (a
// message that was never downloaded) records nothing, and recording twice is a
// no-op.
func (d *DBs) RecordPendingBlobDeletion(ctx context.Context, hash string) error {
	if hash == "" {
		return nil
	}
	if _, err := d.State.Write.ExecContext(ctx,
		`INSERT OR IGNORE INTO pending_blob_deletions (content_hash) VALUES (?)`, hash); err != nil {
		return fmt.Errorf("record pending blob deletion %s: %w", hash, err)
	}
	return nil
}

// PendingBlobDeletions returns the hashes a purge still has to erase from the
// backup targets, oldest recorded first.
func (d *DBs) PendingBlobDeletions(ctx context.Context) ([]string, error) {
	rows, err := d.State.Read.QueryContext(ctx,
		`SELECT content_hash FROM pending_blob_deletions ORDER BY recorded_at, content_hash`)
	if err != nil {
		return nil, fmt.Errorf("pending blob deletions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, fmt.Errorf("pending blob deletions: %w", err)
		}
		hashes = append(hashes, hash)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pending blob deletions: %w", err)
	}
	return hashes, nil
}

// ClearPendingBlobDeletion forgets a hash the backup has erased everywhere.
// Clearing an unknown hash is a no-op.
func (d *DBs) ClearPendingBlobDeletion(ctx context.Context, hash string) error {
	if _, err := d.State.Write.ExecContext(ctx,
		`DELETE FROM pending_blob_deletions WHERE content_hash = ?`, hash); err != nil {
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
