package store

import (
	"context"
	"database/sql"
	"fmt"
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

	v1 := rawPool(t, filepath.Join(dir, "mirror.db"))
	if err := migrate(context.Background(), v1, mirrorMigrations[:1]); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	_, err := v1.ExecContext(context.Background(),
		`INSERT INTO accounts (id, address, imap_host, imap_port, smtp_host, smtp_port, username, created_at)
		 VALUES ('acct-1', 'me@example.test', 'imap.test', 993, 'smtp.test', 465, 'me', '2026-10-02T00:00:00Z')`,
	)
	if err != nil {
		t.Fatalf("insert v1 account: %v", err)
	}
	if err := v1.Close(); err != nil {
		t.Fatalf("close v1: %v", err)
	}

	dbs, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open upgrade: %v", err)
	}
	defer dbs.Close()

	if got := userVersion(t, dbs.Mirror.Read); got != len(mirrorMigrations) {
		t.Errorf("mirror user_version = %d, want %d", got, len(mirrorMigrations))
	}
	if !tableExists(t, dbs.Mirror.Read, "threads") {
		t.Error("threads table missing after upgrade")
	}

	var address string
	if err := dbs.Mirror.Read.QueryRow(`SELECT address FROM accounts WHERE id='acct-1'`).Scan(&address); err != nil {
		t.Fatalf("read upgraded account: %v", err)
	}
	if address != "me@example.test" {
		t.Errorf("address = %q, want me@example.test", address)
	}
}

// TestMirrorUpgradeFromEveryPriorVersion applies each historical schema, seeds
// a message, then opens normally and checks the upgrade reached current and the
// row survived. This is the "upgrade from every prior version" rule in
// TESTING.md section 6.
func TestMirrorUpgradeFromEveryPriorVersion(t *testing.T) {
	t.Parallel()
	for version := 1; version < len(mirrorMigrations); version++ {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			db := rawPool(t, filepath.Join(dir, "mirror.db"))
			if err := migrate(context.Background(), db, mirrorMigrations[:version]); err != nil {
				t.Fatalf("apply v%d: %v", version, err)
			}
			seedSQL := []string{
				`INSERT INTO accounts (id, address, imap_host, imap_port, smtp_host, smtp_port, username, created_at)
				 VALUES ('acct-1', 'me@example.test', 'imap.test', 993, 'smtp.test', 465, 'me', '2026-10-02T00:00:00Z')`,
				`INSERT INTO folders (id, account_id, name, role) VALUES ('folder-1', 'acct-1', 'INBOX', 'inbox')`,
				`INSERT INTO messages (id, account_id, folder_id, uid, content_key, subject)
				 VALUES ('msg-1', 'acct-1', 'folder-1', 1, 'ck', 'survives')`,
			}
			for _, stmt := range seedSQL {
				if _, err := db.ExecContext(context.Background(), stmt); err != nil {
					t.Fatalf("seed v%d: %v", version, err)
				}
			}
			// From v2 on, attachments reference the message by id. The v9 rebuild
			// drops and renames messages, so an actual child row proves
			// defer_foreign_keys carries the parent across without losing the link.
			if version >= 2 {
				if _, err := db.ExecContext(context.Background(),
					`INSERT INTO attachments (id, message_id, filename, mime, size)
					 VALUES ('att-1', 'msg-1', 'x.txt', 'text/plain', 1)`); err != nil {
					t.Fatalf("seed v%d attachment: %v", version, err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close v%d: %v", version, err)
			}

			dbs, err := Open(context.Background(), dir)
			if err != nil {
				t.Fatalf("Open upgrade from v%d: %v", version, err)
			}
			defer dbs.Close()

			if got := userVersion(t, dbs.Mirror.Read); got != len(mirrorMigrations) {
				t.Errorf("user_version = %d, want %d", got, len(mirrorMigrations))
			}
			var subject string
			if err := dbs.Mirror.Read.QueryRow(`SELECT subject FROM messages WHERE id='msg-1'`).Scan(&subject); err != nil {
				t.Fatalf("read message after v%d upgrade: %v", version, err)
			}
			if subject != "survives" {
				t.Errorf("subject = %q, want survives", subject)
			}
		})
	}
}

// TestMirrorReadSchema covers the tables and columns the read path queries.
func TestMirrorReadSchema(t *testing.T) {
	t.Parallel()
	dbs, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbs.Close()

	for _, table := range []string{"threads", "attachments", "needs_me"} {
		if !tableExists(t, dbs.Mirror.Read, table) {
			t.Errorf("mirror table %s missing", table)
		}
	}
	wantColumns := map[string][]string{
		"accounts": {"icon", "photo_blob"},
		"messages": {
			"seen", "reply_to_json", "delivered_to_json", "auth_results", "parse_errors",
			"raw_path", "body_status", "derived_version", "disabled_blob",
		},
	}
	for table, columns := range wantColumns {
		for _, column := range columns {
			if !columnExists(t, dbs.Mirror.Read, table, column) {
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
	defer func() { _ = rows.Close() }()
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

// rawPool opens a plain read-write handle on path, for tests that build a
// database at an old schema version before Open upgrades it.
func rawPool(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := openPool(context.Background(), path, "")
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return db
}

// The flagged backfill must key on the \Flagged system flag, not on any flag
// that merely contains the word: a keyword such as $notflagged is not a star.
func TestFlaggedBackfillMatchesOnlyTheFlaggedFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := rawPool(t, filepath.Join(dir, "mirror.db"))
	if err := migrate(context.Background(), db, mirrorMigrations[:10]); err != nil {
		t.Fatalf("apply v10: %v", err)
	}
	seed := []string{
		`INSERT INTO accounts (id, address, imap_host, imap_port, smtp_host, smtp_port, username, created_at)
		 VALUES ('acct-1', 'me@example.test', 'imap.test', 993, 'smtp.test', 465, 'me', '2026-10-02T00:00:00Z')`,
		`INSERT INTO folders (id, account_id, name, role) VALUES ('folder-1', 'acct-1', 'INBOX', 'inbox')`,
		`INSERT INTO messages (id, account_id, folder_id, uid, content_key, flags_json)
		 VALUES ('starred', 'acct-1', 'folder-1', 1, 'ck1', '["\\Seen","\\Flagged"]')`,
		`INSERT INTO messages (id, account_id, folder_id, uid, content_key, flags_json)
		 VALUES ('lowercase', 'acct-1', 'folder-1', 2, 'ck2', '["\\flagged"]')`,
		`INSERT INTO messages (id, account_id, folder_id, uid, content_key, flags_json)
		 VALUES ('keyword', 'acct-1', 'folder-1', 3, 'ck3', '["$notflagged","\\Seen"]')`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	dbs, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open upgrade: %v", err)
	}
	defer dbs.Close()

	want := map[string]bool{"starred": true, "lowercase": true, "keyword": false}
	for id, flagged := range want {
		var got bool
		if err := dbs.Mirror.Read.QueryRow(`SELECT flagged FROM messages WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if got != flagged {
			t.Errorf("%s flagged = %v, want %v", id, got, flagged)
		}
	}
}
