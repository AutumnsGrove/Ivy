package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// DisabledStat is the per-account inventory of mail the mirror keeps but does
// not show. Pending is the subset a completed pass has not classified yet, so it
// is neither restorable nor part of a mass-disable count (N22 in papercuts.md).
type DisabledStat struct {
	Hidden  int
	Moved   int
	Removed int
	Pending int
}

// DisabledStats returns a stat per account that has hidden mail. Hidden includes
// everything; Moved, Removed and Pending break it down so Mirror health can word
// the count and a caller can tell settled from unclassified rows. An account
// with no hidden mail is absent.
func (d *DBs) DisabledStats(ctx context.Context) (map[string]DisabledStat, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT account_id, count(*),
		       sum(CASE WHEN disabled_reason = ? THEN 1 ELSE 0 END),
		       sum(CASE WHEN disabled_reason = ? THEN 1 ELSE 0 END),
		       sum(CASE WHEN disabled_reason = ? THEN 1 ELSE 0 END)
		FROM messages
		WHERE disabled_at IS NOT NULL
		GROUP BY account_id`,
		DisabledMoved, DisabledRemoved, DisabledPending)
	if err != nil {
		return nil, fmt.Errorf("disabled stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	stats := make(map[string]DisabledStat)
	for rows.Next() {
		var (
			accountID                 string
			hidden, moved, removed, p int64
		)
		if err := rows.Scan(&accountID, &hidden, &moved, &removed, &p); err != nil {
			return nil, fmt.Errorf("disabled stats: %w", err)
		}
		stats[accountID] = DisabledStat{
			Hidden: int(hidden), Moved: int(moved), Removed: int(removed), Pending: int(p),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("disabled stats: %w", err)
	}
	return stats, nil
}

// RestoreMessage makes one hidden row visible again. The bytes, derived data and
// tags are untouched; only the disabled flag is cleared, so restoring a live row
// is a harmless no-op. An unknown id is ErrNotFound.
func (d *DBs) RestoreMessage(ctx context.Context, id string) error {
	n, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE messages SET disabled_at = NULL, disabled_reason = NULL
		 WHERE id = ? AND disabled_at IS NOT NULL`, id)
	if err != nil {
		return fmt.Errorf("restore message %s: %w", id, err)
	}
	affected, err := n.RowsAffected()
	if err != nil {
		return fmt.Errorf("restore message %s: %w", id, err)
	}
	if affected == 0 {
		// The update matched no hidden row: distinguish an already-visible row from
		// a missing one so the caller can answer 404 honestly.
		var exists int
		if err := d.Mirror.Read.QueryRowContext(ctx,
			`SELECT count(*) FROM messages WHERE id = ?`, id).Scan(&exists); err != nil {
			return fmt.Errorf("restore message %s: %w", id, err)
		}
		if exists == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// RestoreAccountDisabled makes every settled hidden row of an account visible and
// returns how many it restored. Pending rows are left alone: a pass has not yet
// decided whether they moved or were removed (N22).
func (d *DBs) RestoreAccountDisabled(ctx context.Context, accountID string) (int, error) {
	res, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE messages SET disabled_at = NULL, disabled_reason = NULL
		 WHERE account_id = ? AND disabled_at IS NOT NULL AND COALESCE(disabled_reason, '') <> ?`,
		accountID, DisabledPending)
	if err != nil {
		return 0, fmt.Errorf("restore hidden mail of %s: %w", accountID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("restore hidden mail of %s: %w", accountID, err)
	}
	return int(n), nil
}

// PurgedMessage is the outcome of a purge. RawPath is the spool file to unlink
// (empty when the message had none). BlobHash is the blob store copy; when
// BlobUnreferenced is true no other hidden row shares those bytes, so the caller
// erases the blob locally and from every backup target (N24).
type PurgedMessage struct {
	RawPath          string
	BlobHash         string
	BlobUnreferenced bool
}

// PurgeMessage is the only erasure Ivy has: it deletes one hidden message's row
// and attachment rows and reports what else the caller must erase. A live row is
// refused with ErrNotDisabled, so the reader's ordinary delete can never be
// wired to it. Deleting the row before the file means a crash leaves an orphan
// the spool sweep collects, never a row pointing at a file that is gone. The
// pending blob deletion is recorded before the row goes, so a crash in between
// still leaves the erasure to be retried (N24).
func (d *DBs) PurgeMessage(ctx context.Context, id string) (PurgedMessage, error) {
	var (
		rawPath, blobHash string
		disabledAt        sql.NullString
	)
	err := d.Mirror.Read.QueryRowContext(ctx,
		`SELECT COALESCE(raw_path, ''), COALESCE(disabled_blob, ''), disabled_at FROM messages WHERE id = ?`,
		id).Scan(&rawPath, &blobHash, &disabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PurgedMessage{}, ErrNotFound
	}
	if err != nil {
		return PurgedMessage{}, fmt.Errorf("purge message %s: %w", id, err)
	}
	if !disabledAt.Valid {
		return PurgedMessage{}, ErrNotDisabled
	}

	// Decide whether the blob outlives this row, and record the deletion before
	// the row is removed. If the delete then fails the record is cleared again.
	unreferenced := false
	if blobHash != "" {
		var refs int
		if err := d.Mirror.Read.QueryRowContext(ctx,
			`SELECT count(*) FROM messages WHERE disabled_blob = ? AND id <> ?`, blobHash, id).Scan(&refs); err != nil {
			return PurgedMessage{}, fmt.Errorf("purge message %s: count blob refs: %w", id, err)
		}
		unreferenced = refs == 0
	}
	if unreferenced {
		if err := d.RecordPendingBlobDeletion(ctx, blobHash); err != nil {
			return PurgedMessage{}, err
		}
	}

	if err := d.deleteMessageRows(ctx, id); err != nil {
		if unreferenced {
			_ = d.ClearPendingBlobDeletion(ctx, blobHash)
		}
		return PurgedMessage{}, err
	}
	return PurgedMessage{RawPath: rawPath, BlobHash: blobHash, BlobUnreferenced: unreferenced}, nil
}

// deleteMessageRows removes a message's attachment rows and its row in one
// transaction.
func (d *DBs) deleteMessageRows(ctx context.Context, id string) error {
	tx, err := d.Mirror.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("purge message %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM attachments WHERE message_id = ?`, id); err != nil {
		return fmt.Errorf("purge message %s: delete attachments: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, id); err != nil {
		return fmt.Errorf("purge message %s: delete row: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("purge message %s: %w", id, err)
	}
	return nil
}
