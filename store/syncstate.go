package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SyncStatus is where an account's sync stands. The names match the API's
// SyncState enum, with underscores where the contract uses hyphens, and one more
// (error) for a failure that is neither auth nor reachability.
type SyncStatus string

const (
	SyncOK          SyncStatus = "ok"
	SyncSyncing     SyncStatus = "syncing"
	SyncAuthFailed  SyncStatus = "auth_failed"
	SyncUnreachable SyncStatus = "unreachable"
	SyncError       SyncStatus = "error"
)

// MaxSyncErrorDetail bounds the error text kept per account (STANDARDS.md 4a).
// The caller shortens it; a longer one is refused rather than silently cut.
const MaxSyncErrorDetail = 1024

// SyncState is one account's sync status row. LastOKAt is the zero time until a
// sync has succeeded once.
type SyncState struct {
	AccountID       string
	Status          SyncStatus
	LastOKAt        time.Time
	LastErrorCode   string
	LastErrorDetail string
	BackfillDone    int
	BackfillTotal   int
	UpdatedAt       time.Time
}

func (s SyncState) validate() error {
	switch s.Status {
	case SyncOK, SyncSyncing, SyncAuthFailed, SyncUnreachable, SyncError:
	default:
		return fmt.Errorf("unknown sync status %q", s.Status)
	}
	if s.BackfillDone < 0 || s.BackfillTotal < 0 || s.BackfillDone > s.BackfillTotal {
		return fmt.Errorf("backfill progress %d of %d is not valid", s.BackfillDone, s.BackfillTotal)
	}
	if len(s.LastErrorDetail) > MaxSyncErrorDetail {
		return fmt.Errorf("error detail is %d bytes, the limit is %d", len(s.LastErrorDetail), MaxSyncErrorDetail)
	}
	return nil
}

// SetSyncState writes an account's status in one statement. A zero LastOKAt
// keeps the stored one, so recording a failure never erases when the account
// last worked; the error fields are replaced as given, so recovery clears them.
func (d *DBs) SetSyncState(ctx context.Context, s SyncState) error {
	if err := s.validate(); err != nil {
		return fmt.Errorf("set sync state %s: %w", s.AccountID, err)
	}
	_, err := d.Mirror.Write.ExecContext(ctx, `
		INSERT INTO sync_state (account_id, status, last_ok_at, last_error_code,
			last_error_detail, backfill_done, backfill_total, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET
			status=excluded.status,
			last_ok_at=COALESCE(excluded.last_ok_at, sync_state.last_ok_at),
			last_error_code=excluded.last_error_code,
			last_error_detail=excluded.last_error_detail,
			backfill_done=excluded.backfill_done, backfill_total=excluded.backfill_total,
			updated_at=excluded.updated_at`,
		s.AccountID, string(s.Status), nullableTime(s.LastOKAt), s.LastErrorCode,
		s.LastErrorDetail, s.BackfillDone, s.BackfillTotal, nullableTime(s.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("set sync state %s: %w", s.AccountID, err)
	}
	return nil
}

// GetSyncState returns an account's status, or ErrNotFound if it has never
// been recorded: "never synced" and "synced fine" must stay distinguishable.
func (d *DBs) GetSyncState(ctx context.Context, accountID string) (SyncState, error) {
	var (
		s            SyncState
		status       string
		lastOK, upAt sql.NullString
	)
	err := d.Mirror.Read.QueryRowContext(ctx, `
		SELECT account_id, status, last_ok_at, last_error_code, last_error_detail,
		       backfill_done, backfill_total, updated_at
		FROM sync_state WHERE account_id = ?`, accountID).Scan(
		&s.AccountID, &status, &lastOK, &s.LastErrorCode, &s.LastErrorDetail,
		&s.BackfillDone, &s.BackfillTotal, &upAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncState{}, ErrNotFound
	}
	if err != nil {
		return SyncState{}, fmt.Errorf("get sync state %s: %w", accountID, err)
	}
	s.Status = SyncStatus(status)
	for _, tc := range []struct {
		raw  sql.NullString
		dest *time.Time
		name string
	}{{lastOK, &s.LastOKAt, "last_ok_at"}, {upAt, &s.UpdatedAt, "updated_at"}} {
		if !tc.raw.Valid {
			continue
		}
		t, err := parseTime(tc.raw.String)
		if err != nil {
			return SyncState{}, fmt.Errorf("get sync state %s: %s: %w", accountID, tc.name, err)
		}
		*tc.dest = t
	}
	return s, nil
}
