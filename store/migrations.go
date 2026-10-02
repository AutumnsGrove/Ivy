package store

import (
	"database/sql"
	"fmt"
)

// migration is one append-only schema step. Version numbers are positional and
// must never be reordered or edited once released (ARCHITECTURE.md section 3).
// New work appends a new entry.
type migration struct {
	version    int
	statements []string
}

// migrate applies every migration newer than the database's user_version, each
// in its own transaction. Migrations are idempotent by construction: a database
// already at the current version runs nothing.
func migrate(db *sql.DB, migrations []migration) error {
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		for _, stmt := range m.statements {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", m.version, err)
			}
		}
		// PRAGMA user_version cannot be parameterised; m.version is a literal
		// from our own code, never user input.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("set user_version %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
		current = m.version
	}
	return nil
}

// mirrorMigrations is the schema of the rebuildable mirror. It is intentionally
// minimal for now; later chunks append migrations rather than editing these.
var mirrorMigrations = []migration{
	{
		version: 1,
		statements: []string{
			`CREATE TABLE accounts (
				id             TEXT PRIMARY KEY,
				address        TEXT NOT NULL,
				imap_host      TEXT NOT NULL,
				imap_port      INTEGER NOT NULL,
				smtp_host      TEXT NOT NULL,
				smtp_port      INTEGER NOT NULL,
				username       TEXT NOT NULL,
				display_name   TEXT NOT NULL DEFAULT '',
				color          TEXT NOT NULL DEFAULT '',
				sort_order     INTEGER NOT NULL DEFAULT 0,
				llm_enabled    INTEGER NOT NULL DEFAULT 0,
				vision_enabled INTEGER NOT NULL DEFAULT 0,
				created_at     TEXT NOT NULL
			)`,
			`CREATE TABLE folders (
				id            TEXT PRIMARY KEY,
				account_id    TEXT NOT NULL REFERENCES accounts(id),
				name          TEXT NOT NULL,
				role          TEXT NOT NULL DEFAULT 'other',
				uidvalidity   INTEGER NOT NULL DEFAULT 0,
				highestmodseq INTEGER NOT NULL DEFAULT 0,
				last_sync_at  TEXT,
				UNIQUE(account_id, name)
			)`,
			`CREATE TABLE messages (
				id                  TEXT PRIMARY KEY,
				account_id          TEXT NOT NULL REFERENCES accounts(id),
				folder_id           TEXT NOT NULL REFERENCES folders(id),
				uid                 INTEGER NOT NULL,
				content_key         TEXT NOT NULL,
				message_id_hdr      TEXT,
				in_reply_to         TEXT,
				refs                TEXT,
				subject             TEXT,
				from_json           TEXT,
				to_json             TEXT,
				cc_json             TEXT,
				date                TEXT,
				size                INTEGER NOT NULL DEFAULT 0,
				flags_json          TEXT,
				internaldate        TEXT,
				has_attachments     INTEGER NOT NULL DEFAULT 0,
				raw_blob            BLOB,
				body_text           TEXT,
				body_html_sanitized TEXT,
				thread_id           TEXT,
				snippet             TEXT,
				disabled_at         TEXT,
				disabled_reason     TEXT,
				UNIQUE(folder_id, uid)
			)`,
			`CREATE INDEX idx_messages_account_content ON messages(account_id, content_key)`,
			`CREATE INDEX idx_messages_thread ON messages(thread_id)`,
		},
	},
}

// stateMigrations is the schema of the locally owned, backed-up state.
var stateMigrations = []migration{
	{
		version: 1,
		statements: []string{
			`CREATE TABLE settings (
				account_id TEXT NOT NULL DEFAULT '',
				key        TEXT NOT NULL,
				value      TEXT NOT NULL,
				PRIMARY KEY (account_id, key)
			)`,
			`CREATE TABLE tags (
				id    TEXT PRIMARY KEY,
				slug  TEXT NOT NULL UNIQUE,
				name  TEXT NOT NULL,
				color TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE TABLE message_tags (
				account_id  TEXT NOT NULL,
				content_key TEXT NOT NULL,
				tag_id      TEXT NOT NULL REFERENCES tags(id),
				source      TEXT NOT NULL,
				PRIMARY KEY (account_id, content_key, tag_id)
			)`,
		},
	},
}
