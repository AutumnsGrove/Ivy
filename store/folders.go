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
// QRESYNC; LastSyncAt is the zero time until the folder has synced once. A
// folder the server no longer lists is kept with GoneAt set and hidden from
// every list (nothing is ever erased).
type Folder struct {
	ID            string
	AccountID     string
	Name          string
	Role          string
	UIDValidity   uint32
	HighestModSeq uint64
	LastSyncAt    time.Time
	GoneAt        time.Time
}

// UpsertFolder writes a folder keyed by (account, name), so a reconnect never
// duplicates it; UIDValidity and HighestModSeq are refreshed in place. A
// folder that reappears clears gone_at, so it is a live mailbox again.
func (d *DBs) UpsertFolder(ctx context.Context, f Folder) error {
	_, err := d.Mirror.Write.ExecContext(ctx, `
		INSERT INTO folders (id, account_id, name, role, uidvalidity, highestmodseq, last_sync_at, gone_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, name) DO UPDATE SET
			role=excluded.role, uidvalidity=excluded.uidvalidity,
			highestmodseq=excluded.highestmodseq, last_sync_at=excluded.last_sync_at,
			gone_at=NULL`,
		f.ID, f.AccountID, f.Name, f.Role, f.UIDValidity, f.HighestModSeq, nullableTime(f.LastSyncAt),
		nullableTime(f.GoneAt),
	)
	if err != nil {
		return fmt.Errorf("upsert folder %s/%s: %w", f.AccountID, f.Name, err)
	}
	return nil
}

// SetFolderGone marks a folder the server no longer lists, keeping its row and
// hiding it from lists. It is idempotent: a folder already gone keeps its first
// gone time.
func (d *DBs) SetFolderGone(ctx context.Context, folderID string, goneAt time.Time) error {
	_, err := d.Mirror.Write.ExecContext(ctx,
		`UPDATE folders SET gone_at = COALESCE(gone_at, ?) WHERE id = ?`, nullableTime(goneAt), folderID)
	if err != nil {
		return fmt.Errorf("set folder %s gone: %w", folderID, err)
	}
	return nil
}

// GetFolderByName returns one folder or ErrNotFound.
func (d *DBs) GetFolderByName(ctx context.Context, accountID, name string) (Folder, error) {
	row := d.Mirror.Read.QueryRowContext(ctx, folderSelect+` WHERE account_id=? AND name=?`, accountID, name)
	f, err := scanFolder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, ErrNotFound
	}
	if err != nil {
		return Folder{}, fmt.Errorf("get folder %s/%s: %w", accountID, name, err)
	}
	return f, nil
}

// GetFolderByID returns one folder by its stable id, or ErrNotFound. The outbox
// stores folder ids rather than names, so dispatch resolves the name here.
func (d *DBs) GetFolderByID(ctx context.Context, id string) (Folder, error) {
	row := d.Mirror.Read.QueryRowContext(ctx, folderSelect+` WHERE id=?`, id)
	f, err := scanFolder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, ErrNotFound
	}
	if err != nil {
		return Folder{}, fmt.Errorf("get folder %s: %w", id, err)
	}
	return f, nil
}

// ListFolders returns one account's live folders, ordered by name. A folder the
// server has deleted or renamed away is gone and not listed.
func (d *DBs) ListFolders(ctx context.Context, accountID string) ([]Folder, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, folderSelect+` WHERE account_id=? AND gone_at IS NULL ORDER BY name`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list folders for %s: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanFolders(rows, accountID)
}

// AllFolders returns one account's folders including gone ones, ordered by name.
// The runner uses it to hide a folder the server no longer lists.
func (d *DBs) AllFolders(ctx context.Context, accountID string) ([]Folder, error) {
	rows, err := d.Mirror.Read.QueryContext(ctx, folderSelect+` WHERE account_id=? ORDER BY name`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list all folders for %s: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanFolders(rows, accountID)
}

func scanFolders(rows *sql.Rows, accountID string) ([]Folder, error) {
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
	SELECT id, account_id, name, role, uidvalidity, highestmodseq, last_sync_at, gone_at
	FROM folders`

func scanFolder(s scanner) (Folder, error) {
	var (
		f            Folder
		synced, gone sql.NullString
	)
	if err := s.Scan(&f.ID, &f.AccountID, &f.Name, &f.Role, &f.UIDValidity, &f.HighestModSeq, &synced, &gone); err != nil {
		return Folder{}, err
	}
	for _, tc := range []struct {
		raw  sql.NullString
		dest *time.Time
		name string
	}{{synced, &f.LastSyncAt, "last_sync_at"}, {gone, &f.GoneAt, "gone_at"}} {
		if !tc.raw.Valid {
			continue
		}
		t, err := parseTime(tc.raw.String)
		if err != nil {
			return Folder{}, fmt.Errorf("%s: %w", tc.name, err)
		}
		*tc.dest = t
	}
	return f, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}
