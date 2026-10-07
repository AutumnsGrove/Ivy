package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/blobstore"
)

// MaxUploadAge is how long a staged outgoing attachment survives with no
// terminal owner. The bytes are only staging: a built draft or send body embeds
// them, and resume re-materialises new staging from that body, so an aged-out
// file is invisible to the operator (docs/handoffs/2026-10-06-4g-attachments-G4.md).
const MaxUploadAge = 7 * 24 * time.Hour

// ErrUploadTooLarge reports a staged file over the caller's per-upload cap.
// Nothing is kept: the partially copied blob is removed.
var ErrUploadTooLarge = errors.New("upload too large")

// Upload is one staged outgoing attachment. The bytes are content-addressed on
// disk under data/uploads/<hash>; this row carries what the builder needs. It is
// locally owned state (CLAUDE.md rule 5), scoped to one account.
type Upload struct {
	ID        string
	AccountID string
	Hash      string
	Name      string
	MIMEType  string
	Size      int64
	CreatedAt time.Time
}

const uploadSelect = `SELECT id, account_id, hash, name, mime, size, created_at FROM uploads`

func scanUpload(s scanner) (Upload, error) {
	var (
		u       Upload
		created string
	)
	if err := s.Scan(&u.ID, &u.AccountID, &u.Hash, &u.Name, &u.MIMEType, &u.Size, &created); err != nil {
		return Upload{}, err
	}
	t, err := parseTime(created)
	if err != nil {
		return Upload{}, fmt.Errorf("upload %s created_at: %w", u.ID, err)
	}
	u.CreatedAt = t
	return u, nil
}

// StageUpload streams r into the upload store and records the row. At most
// maxBytes are read; anything more is ErrUploadTooLarge with nothing kept. It is
// idempotent on (account, id): a retried upload returns the stored row and never
// rewrites the bytes.
func (d *DBs) StageUpload(ctx context.Context, up Upload, r io.Reader, maxBytes int64) (Upload, error) {
	switch {
	case up.ID == "", up.AccountID == "", up.Name == "":
		return Upload{}, errors.New("stage upload: id, account and name are required")
	case up.CreatedAt.IsZero():
		return Upload{}, errors.New("stage upload: created_at is required")
	case maxBytes <= 0:
		return Upload{}, errors.New("stage upload: max must be positive")
	}
	d.uploadMu.RLock()
	defer d.uploadMu.RUnlock()
	existing, err := d.GetUpload(ctx, up.AccountID, up.ID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Upload{}, err
	}

	hash, size, err := d.Uploads.Put(ctx, io.LimitReader(r, maxBytes+1))
	if err != nil {
		return Upload{}, err
	}
	if size > maxBytes {
		if err := d.removeUploadHash(ctx, hash); err != nil {
			return Upload{}, err
		}
		return Upload{}, ErrUploadTooLarge
	}
	up.Hash, up.Size = hash, size
	if _, err := d.State.Write.ExecContext(ctx, `
		INSERT INTO uploads (id, account_id, hash, name, mime, size, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		up.ID, up.AccountID, up.Hash, up.Name, up.MIMEType, up.Size, formatTime(up.CreatedAt)); err != nil {
		return Upload{}, fmt.Errorf("stage upload %s: %w", up.ID, err)
	}
	return up, nil
}

// GetUpload returns one account's staged upload, or ErrNotFound.
func (d *DBs) GetUpload(ctx context.Context, accountID, id string) (Upload, error) {
	row := d.State.Read.QueryRowContext(ctx, uploadSelect+` WHERE account_id = ? AND id = ?`, accountID, id)
	u, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, fmt.Errorf("get upload %s: %w", id, err)
	}
	return u, nil
}

// OpenUpload returns the staged bytes for a row. The caller closes it.
func (d *DBs) OpenUpload(u Upload) (io.ReadCloser, error) {
	return d.Uploads.Open(u.Hash)
}

// DeleteUpload removes one account's staged upload and its file once no row
// shares the hash. A missing row is ErrNotFound.
func (d *DBs) DeleteUpload(ctx context.Context, accountID, id string) error {
	hash, err := d.deleteUploadRow(ctx, accountID, id)
	if err != nil {
		return err
	}
	return d.releaseUploadBlob(ctx, hash)
}

// releaseUploadBlob removes a blob nothing references. It never waits: if a stage
// is in flight the file may be about to gain a row, so it is left for the orphan
// sweep. The row count is taken under the lock, after any stage has finished.
func (d *DBs) releaseUploadBlob(ctx context.Context, hash string) error {
	if !d.uploadMu.TryLock() {
		return nil
	}
	defer d.uploadMu.Unlock()
	return d.removeUploadHash(ctx, hash)
}

// deleteUploadRow deletes the row and returns its hash.
func (d *DBs) deleteUploadRow(ctx context.Context, accountID, id string) (string, error) {
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("delete upload %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	var hash string
	err = tx.QueryRowContext(ctx, `SELECT hash FROM uploads WHERE account_id = ? AND id = ?`, accountID, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("delete upload %s: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE account_id = ? AND id = ?`, accountID, id); err != nil {
		return "", fmt.Errorf("delete upload %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("delete upload %s: %w", id, err)
	}
	return hash, nil
}

// SweepUploads deletes uploads staged before the cutoff and removes each file
// once no row shares its hash. It returns how many rows went.
func (d *DBs) SweepUploads(ctx context.Context, before time.Time) (int, error) {
	cutoff := formatTime(before)
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sweep uploads: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT hash FROM uploads WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sweep uploads: %w", err)
	}
	var candidates []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("sweep uploads: %w", err)
		}
		candidates = append(candidates, hash)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("sweep uploads: %w", err)
	}
	_ = rows.Close()

	res, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sweep uploads: %w", err)
	}
	deleted, _ := res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sweep uploads: %w", err)
	}
	for _, hash := range candidates {
		if err := d.releaseUploadBlob(ctx, hash); err != nil {
			return int(deleted), err
		}
	}
	return int(deleted), nil
}

// SweepOrphanUploads removes staging files no row references: a blob left by a
// crash or a failed insert after the write, and a temp file from an interrupted
// write. It returns how many files went. A stage holds the read lock from before
// it writes until its row is inserted, so with the write lock in hand nothing is
// mid-flight and any unreferenced file is stale; if a stage is running it stands
// down and the next pass tries again.
func (d *DBs) SweepOrphanUploads(ctx context.Context) (int, error) {
	return sweepOrphanBlobs(ctx, &d.uploadMu, d.Uploads, d.State.Read, `SELECT DISTINCT hash FROM uploads`)
}

// sweepOrphanBlobs removes the temp files and the blobs whose hash the query does
// not return. It holds the write lock, taken without waiting: the writers of this
// store hold the read lock from before they write a file until its row is in, so
// under the write lock nothing is mid-flight and every unreferenced file is stale.
// If a writer is running it stands down and the next pass tries again.
func sweepOrphanBlobs(ctx context.Context, mu *sync.RWMutex, blobs *blobstore.Store, db *sql.DB, referencedQuery string) (int, error) {
	if !mu.TryLock() {
		return 0, nil
	}
	defer mu.Unlock()

	removed, err := blobs.RemoveTemps()
	if err != nil {
		return removed, err
	}
	hashes, err := blobs.Hashes()
	if err != nil {
		return removed, err
	}
	rows, err := db.QueryContext(ctx, referencedQuery)
	if err != nil {
		return removed, fmt.Errorf("sweep orphan blobs: %w", err)
	}
	referenced := map[string]bool{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			_ = rows.Close()
			return removed, fmt.Errorf("sweep orphan blobs: %w", err)
		}
		referenced[hash] = true
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return removed, fmt.Errorf("sweep orphan blobs: %w", err)
	}
	for _, hash := range hashes {
		if referenced[hash] {
			continue
		}
		if err := blobs.Remove(hash); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// removeUploadHash drops a blob only when no row references it, so a shared file
// is never removed out from under a live row.
func (d *DBs) removeUploadHash(ctx context.Context, hash string) error {
	var refs int
	if err := d.State.Read.QueryRowContext(ctx, `SELECT count(*) FROM uploads WHERE hash = ?`, hash).Scan(&refs); err != nil {
		return fmt.Errorf("remove upload blob: %w", err)
	}
	if refs > 0 {
		return nil
	}
	return d.Uploads.Remove(hash)
}
