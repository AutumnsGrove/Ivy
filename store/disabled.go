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

// provablyMovedSQL is true for a pending row whose Message-ID is live in another
// row of the account, which is exactly what SettlePendingDisabled settles as a
// move. It reads the row as m.
const provablyMovedSQL = `(COALESCE(m.message_id_hdr, '') <> '' AND EXISTS (
	SELECT 1 FROM messages l
	WHERE l.account_id = m.account_id AND l.disabled_at IS NULL AND l.message_id_hdr = m.message_id_hdr))`

// FolderSweep is what the server dropped from one folder: the rows a pass hid
// that will not settle as a move, because mail that only changed folder is an
// edit, not a loss. Held is the folder's size before the sweep (live and pending
// rows together), which the mass-disable fraction is measured against.
type FolderSweep struct {
	Folder string
	Hidden int
	Held   int
}

// PendingDisabledByFolder reports, per folder, the pending rows of an account. It
// reads the rows rather than a pass's own tally, so a sweep a dead pass began is
// still seen by the pass that settles it.
func (d *DBs) PendingDisabledByFolder(ctx context.Context, accountID string) ([]FolderSweep, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT f.name,
		       sum(CASE WHEN m.disabled_reason = ? AND NOT `+provablyMovedSQL+` THEN 1 ELSE 0 END),
		       sum(CASE WHEN m.disabled_at IS NULL OR m.disabled_reason = ? THEN 1 ELSE 0 END)
		FROM messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.account_id = ?
		GROUP BY f.id
		HAVING sum(CASE WHEN m.disabled_reason = ? AND NOT `+provablyMovedSQL+` THEN 1 ELSE 0 END) > 0
		ORDER BY f.name`,
		DisabledPending, DisabledPending, accountID, DisabledPending)
	if err != nil {
		return nil, fmt.Errorf("pending disabled by folder: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []FolderSweep
	for rows.Next() {
		var s FolderSweep
		if err := rows.Scan(&s.Folder, &s.Hidden, &s.Held); err != nil {
			return nil, fmt.Errorf("pending disabled by folder: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pending disabled by folder: %w", err)
	}
	return out, nil
}

// RestoreMessage makes one hidden row visible again. The bytes, derived data and
// tags are untouched; only the disabled flag is cleared, so restoring a live row
// is a harmless no-op. An unknown id is ErrNotFound; a row a pass has hidden but
// not yet classified is ErrPendingClassification and stays hidden.
func (d *DBs) RestoreMessage(ctx context.Context, id string) error {
	n, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE messages SET disabled_at = NULL, disabled_reason = NULL
		 WHERE id = ? AND disabled_at IS NOT NULL AND COALESCE(disabled_reason, '') <> ?`,
		id, DisabledPending)
	if err != nil {
		return fmt.Errorf("restore message %s: %w", id, err)
	}
	affected, err := n.RowsAffected()
	if err != nil {
		return fmt.Errorf("restore message %s: %w", id, err)
	}
	if affected == 0 {
		// The update matched no restorable row: tell a missing row (404) and a
		// pending one (409) from one that is already visible.
		var pending int
		err := d.Mirror.Read.QueryRowContext(ctx,
			`SELECT disabled_at IS NOT NULL AND disabled_reason = ? FROM messages WHERE id = ?`,
			DisabledPending, id).Scan(&pending)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("restore message %s: %w", id, err)
		}
		if pending != 0 {
			return ErrPendingClassification
		}
	}
	return nil
}

// RestoreAccountDisabled makes the mail the server dropped visible again and
// returns how many rows it restored. Pending rows are left alone, because a pass
// has not yet decided whether they moved or were removed (N22); moved rows are
// left alone because their mail is live in another folder, so restoring them
// would show it twice.
func (d *DBs) RestoreAccountDisabled(ctx context.Context, accountID string) (int, error) {
	res, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE messages SET disabled_at = NULL, disabled_reason = NULL
		 WHERE account_id = ? AND disabled_at IS NOT NULL AND COALESCE(NULLIF(disabled_reason, ''), ?) = ?`,
		accountID, DisabledRemoved, DisabledRemoved)
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
	// The hidden check made before this transaction can be stale: a restore may
	// have landed since. Deleting only a still-hidden row, and rolling back
	// otherwise, keeps the one erasure from ever reaching visible mail.
	res, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ? AND disabled_at IS NOT NULL`, id)
	if err != nil {
		return fmt.Errorf("purge message %s: delete row: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("purge message %s: %w", id, err)
	} else if n == 0 {
		return ErrNotDisabled
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("purge message %s: %w", id, err)
	}
	return nil
}
