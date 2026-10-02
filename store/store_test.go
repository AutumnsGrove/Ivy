package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestOpenCreatesBothDatabases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	dbs, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbs.Close()

	for _, name := range []string{"mirror.db", "state.db"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s not created: %v", name, err)
		}
	}
}

func TestOpenMigratesToCurrentVersion(t *testing.T) {
	t.Parallel()
	dbs, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbs.Close()

	if got := userVersion(t, dbs.Mirror.Read); got != len(mirrorMigrations) {
		t.Errorf("mirror user_version = %d, want %d", got, len(mirrorMigrations))
	}
	if got := userVersion(t, dbs.State.Read); got != len(stateMigrations) {
		t.Errorf("state user_version = %d, want %d", got, len(stateMigrations))
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	first, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	first.Close()

	second, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()

	if got := userVersion(t, second.Mirror.Read); got != len(mirrorMigrations) {
		t.Errorf("mirror user_version after reopen = %d, want %d", got, len(mirrorMigrations))
	}
	if got := userVersion(t, second.State.Read); got != len(stateMigrations) {
		t.Errorf("state user_version after reopen = %d, want %d", got, len(stateMigrations))
	}
}

func TestPragmasApplied(t *testing.T) {
	t.Parallel()
	dbs, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbs.Close()

	for name, db := range map[string]*sql.DB{"mirror": dbs.Mirror.Read, "state": dbs.State.Read} {
		if got := pragmaText(t, db, "journal_mode"); got != "wal" {
			t.Errorf("%s journal_mode = %q, want wal", name, got)
		}
		if got := pragmaInt(t, db, "foreign_keys"); got != 1 {
			t.Errorf("%s foreign_keys = %d, want 1", name, got)
		}
		if got := pragmaInt(t, db, "synchronous"); got != 1 {
			t.Errorf("%s synchronous = %d, want 1 (NORMAL)", name, got)
		}
	}
}

func TestCoreTablesExist(t *testing.T) {
	t.Parallel()
	dbs, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbs.Close()

	mirror := []string{"accounts", "folders", "messages"}
	for _, table := range mirror {
		if !tableExists(t, dbs.Mirror.Read, table) {
			t.Errorf("mirror table %s missing", table)
		}
	}
	state := []string{"settings", "tags", "message_tags"}
	for _, table := range state {
		if !tableExists(t, dbs.State.Read, table) {
			t.Errorf("state table %s missing", table)
		}
	}
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	return pragmaInt(t, db, "user_version")
}

func pragmaInt(t *testing.T, db *sql.DB, name string) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

func pragmaText(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var v string
	if err := db.QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&n)
	if err != nil {
		t.Fatalf("sqlite_master query: %v", err)
	}
	return n == 1
}

// A data dir is an operator-chosen path, so URI metacharacters in it must not
// redirect SQLite to a different file.
func TestOpenHandlesURIMetacharactersInPath(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "mail?x=1#frag%41 dir")

	dbs, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbs.Close()

	for _, name := range []string{"mirror.db", "state.db"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s not created inside the requested dir: %v", name, err)
		}
	}
}

// Many goroutines each read and then write inside one transaction, the shape of
// any read-modify-write in sync. On a plain pool with deferred transactions
// SQLite refuses to upgrade a stale reader at once with SQLITE_BUSY (7 of 8
// workers failed before the single-writer model); on Write they queue instead.
func TestConcurrentReadModifyWriteNeverBusy(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 8*25)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if err := bumpSetting(ctx, dbs.State.Write); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("worker failed: %v", err)
	}

	var got string
	if err := dbs.State.Read.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='counter'`).Scan(&got); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if got != "200" {
		t.Errorf("counter = %s, want 200: increments were lost", got)
	}
}

// bumpSetting is a read-modify-write: it reads the counter, then writes it back
// plus one, inside one transaction.
func bumpSetting(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	var cur string
	switch err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='counter'`).Scan(&cur); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		n, _ = strconv.Atoi(cur)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO settings(account_id, key, value) VALUES ('', 'counter', ?)`, strconv.Itoa(n+1)); err != nil {
		return err
	}
	return tx.Commit()
}

// A write through the read pool is a bug, so it must fail loudly instead of
// quietly taking the write lock from the one writer.
func TestReadHandleRejectsWrites(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	_, err := dbs.State.Read.ExecContext(context.Background(),
		`INSERT INTO settings(account_id, key, value) VALUES ('', 'x', 'y')`)
	if err == nil {
		t.Fatal("the read handle accepted a write")
	}
}

// WAL readers never wait for the writer: an API read must not queue behind a
// long sync transaction.
func TestReadsDoNotWaitForAnOpenWriteTransaction(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	ctx := context.Background()

	tx, err := dbs.State.Write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings(account_id, key, value) VALUES ('', 'k', 'v')`); err != nil {
		t.Fatalf("write inside tx: %v", err)
	}

	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var n int
	if err := dbs.State.Read.QueryRowContext(readCtx, `SELECT count(*) FROM settings`).Scan(&n); err != nil {
		t.Fatalf("read while a write tx is open: %v", err)
	}
	if n != 0 {
		t.Errorf("reader saw %d uncommitted rows", n)
	}
}
