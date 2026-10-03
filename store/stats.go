package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AccountStat is the per-account roll-up the read API needs: how much inbox
// mail is unread and how much of the mailbox has synced. It is one query per
// concern, not one per account row.
type AccountStat struct {
	Unread        int
	Folders       int
	SyncedFolders int
	LastSync      time.Time
}

// AccountStats returns a stat per account. An account with no folders is
// present with the zero value, so the caller never has to guess.
func (d *DBs) AccountStats(ctx context.Context) (map[string]AccountStat, error) {
	stats := make(map[string]AccountStat)

	rows, err := d.Mirror.Read.QueryContext(ctx, `
		SELECT m.account_id, count(*)
		FROM messages m
		JOIN folders f ON f.id = m.folder_id
		WHERE f.role = ? AND m.disabled_at IS NULL AND m.seen = 0
		GROUP BY m.account_id`, RoleInbox)
	if err != nil {
		return nil, fmt.Errorf("account stats: unread: %w", err)
	}
	if err := scanAccountStats(rows, stats, func(s *AccountStat, n int64) { s.Unread = int(n) }); err != nil {
		return nil, err
	}

	rows, err = d.Mirror.Read.QueryContext(ctx, `
		SELECT account_id, count(*),
		       COALESCE(sum(CASE WHEN last_sync_at IS NOT NULL THEN 1 ELSE 0 END), 0),
		       max(last_sync_at)
		FROM folders
		GROUP BY account_id`)
	if err != nil {
		return nil, fmt.Errorf("account stats: folders: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			accountID       string
			folders, synced int64
			lastSync        sql.NullString
		)
		if err := rows.Scan(&accountID, &folders, &synced, &lastSync); err != nil {
			return nil, fmt.Errorf("account stats: folders: %w", err)
		}
		s := stats[accountID]
		s.Folders = int(folders)
		s.SyncedFolders = int(synced)
		if lastSync.Valid {
			t, err := parseTime(lastSync.String)
			if err != nil {
				return nil, fmt.Errorf("account stats: last sync: %w", err)
			}
			s.LastSync = t
		}
		stats[accountID] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("account stats: folders: %w", err)
	}
	return stats, nil
}

// scanAccountStats folds a (account_id, count) result into the map with set.
func scanAccountStats(rows *sql.Rows, stats map[string]AccountStat, set func(*AccountStat, int64)) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			accountID string
			n         int64
		)
		if err := rows.Scan(&accountID, &n); err != nil {
			return fmt.Errorf("account stats: %w", err)
		}
		s := stats[accountID]
		set(&s, n)
		stats[accountID] = s
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("account stats: %w", err)
	}
	return nil
}

// MessageNeeds reports whether triage has flagged this message as needing the
// operator. A missing row is not an error: most mail has not been looked at.
func (d *DBs) MessageNeeds(ctx context.Context, accountID, contentKey string) (bool, error) {
	var one int
	err := d.Mirror.Read.QueryRowContext(ctx, `
		SELECT 1 FROM needs_me
		WHERE account_id = ? AND content_key = ? AND verdict = 'needs'
		LIMIT 1`, accountID, contentKey).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("message needs %s: %w", contentKey, err)
	}
	return true, nil
}

// MirrorBytes is the mirror file's size, for mirror health. It reads the
// allocation the database itself reports, so it does not need the path.
func (d *DBs) MirrorBytes(ctx context.Context) (int64, error) {
	var pageCount, pageSize int64
	if err := d.Mirror.Read.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("mirror bytes: page count: %w", err)
	}
	if err := d.Mirror.Read.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("mirror bytes: page size: %w", err)
	}
	return pageCount * pageSize, nil
}
