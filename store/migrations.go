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
				_ = tx.Rollback()
				return fmt.Errorf("migration %d: %w", m.version, err)
			}
		}
		// PRAGMA user_version cannot be parameterised; m.version is a literal
		// from our own code, never user input.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			_ = tx.Rollback()
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
	{
		version: 2,
		statements: []string{
			// Account customization (PLAN.md 3): display name, icon and optional photo.
			`ALTER TABLE accounts ADD COLUMN icon TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE accounts ADD COLUMN photo_blob BLOB`,

			// Conversations are keyed by the content key of their root message so a
			// move or UID change never re-derives them (ARCHITECTURE.md 3).
			`CREATE TABLE threads (
				id              TEXT PRIMARY KEY,
				account_id      TEXT NOT NULL REFERENCES accounts(id),
				root_message_id TEXT,
				subject_norm    TEXT,
				last_date       TEXT,
				message_count   INTEGER NOT NULL DEFAULT 0
			)`,
			`CREATE INDEX idx_threads_account_last ON threads(account_id, last_date DESC)`,

			`CREATE TABLE attachments (
				id           TEXT PRIMARY KEY,
				message_id   TEXT NOT NULL REFERENCES messages(id),
				filename     TEXT,
				mime         TEXT,
				size         INTEGER NOT NULL DEFAULT 0,
				content_hash TEXT,
				cid          TEXT,
				storage_path TEXT
			)`,
			`CREATE INDEX idx_attachments_message ON attachments(message_id)`,

			// Derived, regenerable triage verdicts (ARCHITECTURE.md 3). Nothing
			// writes this until the triage milestone; the read query left-joins it.
			`CREATE TABLE needs_me (
				account_id   TEXT NOT NULL,
				content_key  TEXT NOT NULL,
				verdict      TEXT,
				reason       TEXT,
				stage2_model TEXT,
				state        TEXT NOT NULL DEFAULT '',
				PRIMARY KEY (account_id, content_key)
			)`,

			// Denormalised from flags_json so the unread count is an indexed query
			// instead of a JSON scan over every message.
			`ALTER TABLE messages ADD COLUMN seen INTEGER NOT NULL DEFAULT 0`,
			`CREATE INDEX idx_messages_inbox ON messages(folder_id, date DESC, id DESC)`,
			`CREATE INDEX idx_folders_role ON folders(role)`,
		},
	},
	{
		version: 3,
		statements: []string{
			// Fields parsed from the raw header block (2c) so reply handling and
			// the auth trust signal never need to re-parse the raw message.
			`ALTER TABLE messages ADD COLUMN reply_to_json TEXT`,
			`ALTER TABLE messages ADD COLUMN delivered_to_json TEXT`,
			`ALTER TABLE messages ADD COLUMN auth_results TEXT`,
			`ALTER TABLE messages ADD COLUMN parse_errors TEXT`,
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
