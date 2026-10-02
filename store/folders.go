package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Folder roles map to the IMAP special-use boxes, resolved from LIST
// attributes or name heuristics and overridable in settings.
const (
	RoleInbox   = "inbox"
	RoleSent    = "sent"
	RoleDrafts  = "drafts"
	RoleTrash   = "trash"
	RoleArchive = "archive"
	RoleJunk    = "junk"
	RoleOther   = "other"
)

// Folder is one mailbox on the server. UIDValidity and HighestModSeq drive
// QRESYNC; LastSyncAt is the zero time until the folder has synced once.
type Folder struct {
	ID            string
	AccountID     string
	Name          string
	Role          string
	UIDValidity   uint32
	HighestModSeq uint32
	LastSyncAt    time.Time
}

// UpsertFolder writes a folder keyed by (account, name), so a reconnect never
// duplicates it; UIDValidity and HighestModSeq are refreshed in place.
func (d *DBs) UpsertFolder(ctx context.Context, f Folder) error {
	_, err := d.Mirror.ExecContext(ctx, `
		INSERT INTO folders (id, account_id, name, role, uidvalidity, highestmodseq, last_sync_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, name) DO UPDATE SET
			role=excluded.role, uidvalidity=excluded.uidvalidity,
			highestmodseq=excluded.highestmodseq, last_sync_at=excluded.last_sync_at`,
		f.ID, f.AccountID, f.Name, f.Role, f.UIDValidity, f.HighestModSeq, nullableTime(f.LastSyncAt),
	)
	if err != nil {
		return fmt.Errorf("upsert folder %s/%s: %w", f.AccountID, f.Name, err)
	}
	return nil
}

// GetFolderByName returns one folder or ErrNotFound.
func (d *DBs) GetFolderByName(ctx context.Context, accountID, name string) (Folder, error) {
	row := d.Mirror.QueryRowContext(ctx, folderSelect+` WHERE account_id=? AND name=?`, accountID, name)
	f, err := scanFolder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, ErrNotFound
	}
	if err != nil {
		return Folder{}, fmt.Errorf("get folder %s/%s: %w", accountID, name, err)
	}
	return f, nil
}

// ListFolders returns one account's folders, ordered by name.
func (d *DBs) ListFolders(ctx context.Context, accountID string) ([]Folder, error) {
	rows, err := d.Mirror.QueryContext(ctx, folderSelect+` WHERE account_id=? ORDER BY name`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list folders for %s: %w", accountID, err)
	}
	defer rows.Close()

	var out []Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, fmt.Errorf("list folders for %s: %w", accountID, err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list folders for %s: %w", accountID, err)
	}
	return out, nil
}

const folderSelect = `
	SELECT id, account_id, name, role, uidvalidity, highestmodseq, last_sync_at
	FROM folders`

func scanFolder(s scanner) (Folder, error) {
	var (
		f      Folder
		synced sql.NullString
	)
	if err := s.Scan(&f.ID, &f.AccountID, &f.Name, &f.Role, &f.UIDValidity, &f.HighestModSeq, &synced); err != nil {
		return Folder{}, err
	}
	if synced.Valid {
		t, err := parseTime(synced.String)
		if err != nil {
			return Folder{}, fmt.Errorf("last_sync_at: %w", err)
		}
		f.LastSyncAt = t
	}
	return f, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}
