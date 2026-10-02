package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMirrorMigrationsAreAppendOnly pins the positional contract: versions run
// 1..N with no gaps or reordering, so an upgrade always applies the next step
// and a released migration can never be inserted mid-list (ARCHITECTURE.md 3).
func TestMirrorMigrationsAreAppendOnly(t *testing.T) {
	t.Parallel()
	assertPositional(t, "mirror", mirrorMigrations)
	assertPositional(t, "state", stateMigrations)
}

func assertPositional(t *testing.T, name string, migrations []migration) {
	t.Helper()
	for i, m := range migrations {
		if m.version != i+1 {
			t.Errorf("%s migration %d has version %d, want %d", name, i, m.version, i+1)
		}
	}
}

// TestMirrorUpgradeFromV1PreservesData simulates a database created by the
// previous schema and asserts Open upgrades it in place without losing rows.
func TestMirrorUpgradeFromV1PreservesData(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	v1, err := openDB(filepath.Join(dir, "mirror.db"))
	if err != nil {
		t.Fatalf("open mirror: %v", err)
	}
	if err := migrate(v1, mirrorMigrations[:1]); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	_, err = v1.Exec(
		`INSERT INTO accounts (id, address, imap_host, imap_port, smtp_host, smtp_port, username, created_at)
		 VALUES ('acct-1', 'me@example.test', 'imap.test', 993, 'smtp.test', 465, 'me', '2026-10-02T00:00:00Z')`,
	)
	if err != nil {
		t.Fatalf("insert v1 account: %v", err)
	}
	if err := v1.Close(); err != nil {
		t.Fatalf("close v1: %v", err)
	}

	dbs, err := Open(dir)
	if err != nil {
		t.Fatalf("Open upgrade: %v", err)
	}
	defer dbs.Close()

	if got := userVersion(t, dbs.Mirror); got != len(mirrorMigrations) {
		t.Errorf("mirror user_version = %d, want %d", got, len(mirrorMigrations))
	}
	if !tableExists(t, dbs.Mirror, "threads") {
		t.Error("threads table missing after upgrade")
	}

	var address string
	if err := dbs.Mirror.QueryRow(`SELECT address FROM accounts WHERE id='acct-1'`).Scan(&address); err != nil {
		t.Fatalf("read upgraded account: %v", err)
	}
	if address != "me@example.test" {
		t.Errorf("address = %q, want me@example.test", address)
	}
}

// TestMirrorReadSchema covers the tables and columns the read path queries.
func TestMirrorReadSchema(t *testing.T) {
	t.Parallel()
	dbs, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbs.Close()

	for _, table := range []string{"threads", "attachments", "needs_me"} {
		if !tableExists(t, dbs.Mirror, table) {
			t.Errorf("mirror table %s missing", table)
		}
	}
	wantColumns := map[string][]string{
		"accounts": {"icon", "photo_blob"},
		"messages": {"seen"},
	}
	for table, columns := range wantColumns {
		for _, column := range columns {
			if !columnExists(t, dbs.Mirror, table, column) {
				t.Errorf("mirror table %s missing column %s", table, column)
			}
		}
	}
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}
	return false
}
