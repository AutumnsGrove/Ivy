package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesBothDatabases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	dbs, err := Open(dir)
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
	dbs, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbs.Close()

	if got := userVersion(t, dbs.Mirror); got != len(mirrorMigrations) {
		t.Errorf("mirror user_version = %d, want %d", got, len(mirrorMigrations))
	}
	if got := userVersion(t, dbs.State); got != len(stateMigrations) {
		t.Errorf("state user_version = %d, want %d", got, len(stateMigrations))
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	first, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	first.Close()

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()

	if got := userVersion(t, second.Mirror); got != len(mirrorMigrations) {
		t.Errorf("mirror user_version after reopen = %d, want %d", got, len(mirrorMigrations))
	}
	if got := userVersion(t, second.State); got != len(stateMigrations) {
		t.Errorf("state user_version after reopen = %d, want %d", got, len(stateMigrations))
	}
}

func TestPragmasApplied(t *testing.T) {
	t.Parallel()
	dbs, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbs.Close()

	for name, db := range map[string]*sql.DB{"mirror": dbs.Mirror, "state": dbs.State} {
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
	dbs, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbs.Close()

	mirror := []string{"accounts", "folders", "messages"}
	for _, table := range mirror {
		if !tableExists(t, dbs.Mirror, table) {
			t.Errorf("mirror table %s missing", table)
		}
	}
	state := []string{"settings", "tags", "message_tags"}
	for _, table := range state {
		if !tableExists(t, dbs.State, table) {
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
