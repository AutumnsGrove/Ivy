package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// MaxStoppedWaits bounds how many "stop waiting" decisions are kept, so the list the
// Waiting on query excludes stays small (STANDARDS.md 4a). Past it the oldest are
// forgotten, which at worst shows a stale wait again.
const MaxStoppedWaits = 2000

// StopWaiting records that a sent message needs no answer. Doing it again changes
// nothing. It is local state and never touches IMAP.
func (d *DBs) StopWaiting(ctx context.Context, accountID, contentKey string, now time.Time) error {
	if accountID == "" || contentKey == "" {
		return errors.New("stop waiting: an account and a content key are required")
	}
	tx, err := d.State.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("stop waiting: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO waiting_stopped (account_id, content_key, created_at) VALUES (?, ?, ?)
		 ON CONFLICT (account_id, content_key) DO NOTHING`,
		accountID, contentKey, formatTime(now)); err != nil {
		return fmt.Errorf("stop waiting: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM waiting_stopped WHERE (account_id, content_key) IN (
			SELECT account_id, content_key FROM waiting_stopped
			ORDER BY created_at DESC, content_key DESC LIMIT -1 OFFSET ?)`,
		MaxStoppedWaits); err != nil {
		return fmt.Errorf("stop waiting: prune: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("stop waiting: %w", err)
	}
	return nil
}

// StoppedWaitingRefs returns the sent messages the operator has stopped waiting on,
// optionally for one account. They are (account, content key) pairs because a key
// alone is shared by every account that received the same post.
func (d *DBs) StoppedWaitingRefs(ctx context.Context, accountID string) ([]ContentRef, error) {
	rows, err := d.State.Read.QueryContext(ctx,
		`SELECT account_id, content_key FROM waiting_stopped
		 WHERE (? = '' OR account_id = ?) ORDER BY created_at, content_key LIMIT ?`,
		accountID, accountID, MaxStoppedWaits)
	if err != nil {
		return nil, fmt.Errorf("stopped waiting refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ContentRef
	for rows.Next() {
		var ref ContentRef
		if err := rows.Scan(&ref.AccountID, &ref.ContentKey); err != nil {
			return nil, fmt.Errorf("stopped waiting refs: %w", err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stopped waiting refs: %w", err)
	}
	return out, nil
}
