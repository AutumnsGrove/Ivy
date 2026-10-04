package store

import (
	"context"
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
//
// A migration that rebuilds a table another table references (v9 replaces
// messages, which attachments points at) cannot run with foreign keys enforced,
// and the pragma cannot change inside a transaction. So the write connection's
// foreign keys are turned off for the whole pending run and restored afterwards,
// then foreign_key_check proves no migration left a dangling reference.
func migrate(ctx context.Context, db *sql.DB, migrations []migration) error {
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	pending := false
	for _, m := range migrations {
		if m.version > current {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable foreign keys for migrations: %w", err)
	}
	defer func() {
		// Best effort: a failed run is followed by the caller closing the connection.
		_, _ = db.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON")
	}()

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		for _, stmt := range m.statements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("migration %d: %w", m.version, err)
			}
		}
		// PRAGMA user_version cannot be parameterised; m.version is a literal
		// from our own code, never user input.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set user_version %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
		current = m.version
	}

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("re-enable foreign keys after migrations: %w", err)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign_key_check after migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		vals := make([]any, 4)
		ptrs := make([]any, 4)
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("foreign_key_check after migrations: %w", err)
		}
		return fmt.Errorf("foreign_key_check after migrations: %v violates a reference", vals[0])
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("foreign_key_check after migrations: %w", err)
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
	{
		version: 4,
		statements: []string{
			// A message above the in-memory limit is spooled to disk and referenced
			// here, so reading its row never loads the bytes (STANDARDS.md 4a).
			`ALTER TABLE messages ADD COLUMN raw_path TEXT`,
			// ok, too_large (not downloaded) or unparsed; see store.BodyOK.
			`ALTER TABLE messages ADD COLUMN body_status TEXT NOT NULL DEFAULT 'ok'`,
		},
	},
	{
		version: 5,
		statements: []string{
			// Extraction and embeddings dedupe by content, not by row: the decoded
			// bytes' hash is the durable key (ARCHITECTURE.md section 3).
			`CREATE INDEX idx_attachments_hash ON attachments(content_hash)`,
		},
	},
	{
		version: 6,
		statements: []string{
			// Which pipeline (parser, sanitizer, attachment walk) produced a row's
			// derived data. 0 is "never derived, or derived before versions existed";
			// sync re-derives rows behind its current version from the raw message.
			`ALTER TABLE messages ADD COLUMN derived_version INTEGER NOT NULL DEFAULT 0`,
			`CREATE INDEX idx_messages_derived_version ON messages(account_id, derived_version)`,
		},
	},
	{
		version: 7,
		statements: []string{
			// The derived_version a re-derive pass last gave up on, because the raw
			// bytes were gone. The pass skips a row until the version moves past it,
			// so a few unreadable messages cannot fill every pass (N16).
			`ALTER TABLE messages ADD COLUMN derive_failed_version INTEGER NOT NULL DEFAULT 0`,
		},
	},
	{
		version: 8,
		statements: []string{
			// Where each account's sync stands (ARCHITECTURE.md 9b). It describes the
			// connection to the server, so it is rebuildable and lives in the mirror,
			// not in the backed-up state. A missing row means "never synced", which
			// is not the same as healthy.
			`CREATE TABLE sync_state (
				account_id        TEXT PRIMARY KEY REFERENCES accounts(id),
				status            TEXT NOT NULL,
				last_ok_at        TEXT,
				last_error_code   TEXT NOT NULL DEFAULT '',
				last_error_detail TEXT NOT NULL DEFAULT '',
				backfill_done     INTEGER NOT NULL DEFAULT 0,
				backfill_total    INTEGER NOT NULL DEFAULT 0,
				updated_at        TEXT
			)`,
		},
	},
	{
		version: 9,
		statements: []string{
			// A folder row is never deleted: a mailbox the server no longer lists
			// is marked gone and hidden from every list, but its messages keep
			// their rows and spool files (CHUNK3-BRIEF.md 1).
			`ALTER TABLE folders ADD COLUMN gone_at TEXT`,

			// Message identity now includes the folder's UIDVALIDITY. A server that
			// rebuilds a mailbox may hand a new message the UID of an old, disabled
			// one; without this the new row would collide with the old on
			// (folder_id, uid), on the row id and on the spool path. SQLite cannot
			// drop the old UNIQUE(folder_id, uid) in place, so the table is rebuilt.
			// migrate disables foreign keys around pending migrations, so dropping
			// the table attachments references is safe; every id is copied and
			// foreign_key_check verifies the result before the connection is reused.
			`CREATE TABLE messages_new (
				id                    TEXT PRIMARY KEY,
				account_id            TEXT NOT NULL REFERENCES accounts(id),
				folder_id             TEXT NOT NULL REFERENCES folders(id),
				uid                   INTEGER NOT NULL,
				uidvalidity           INTEGER NOT NULL DEFAULT 0,
				content_key           TEXT NOT NULL,
				message_id_hdr        TEXT,
				in_reply_to           TEXT,
				refs                  TEXT,
				subject               TEXT,
				from_json             TEXT,
				to_json               TEXT,
				cc_json               TEXT,
				reply_to_json         TEXT,
				delivered_to_json     TEXT,
				date                  TEXT,
				size                  INTEGER NOT NULL DEFAULT 0,
				flags_json            TEXT,
				internaldate          TEXT,
				has_attachments       INTEGER NOT NULL DEFAULT 0,
				raw_blob              BLOB,
				body_text             TEXT,
				body_html_sanitized   TEXT,
				thread_id             TEXT,
				snippet               TEXT,
				auth_results          TEXT,
				parse_errors          TEXT,
				disabled_at           TEXT,
				disabled_reason       TEXT,
				seen                  INTEGER NOT NULL DEFAULT 0,
				raw_path              TEXT,
				body_status           TEXT NOT NULL DEFAULT 'ok',
				derived_version       INTEGER NOT NULL DEFAULT 0,
				derive_failed_version INTEGER NOT NULL DEFAULT 0,
				UNIQUE(folder_id, uidvalidity, uid)
			)`,
			`INSERT INTO messages_new (
				id, account_id, folder_id, uid, uidvalidity, content_key, message_id_hdr,
				in_reply_to, refs, subject, from_json, to_json, cc_json, reply_to_json,
				delivered_to_json, date, size, flags_json, internaldate, has_attachments,
				raw_blob, body_text, body_html_sanitized, thread_id, snippet, auth_results,
				parse_errors, disabled_at, disabled_reason, seen, raw_path, body_status,
				derived_version, derive_failed_version)
			 SELECT m.id, m.account_id, m.folder_id, m.uid, COALESCE(f.uidvalidity, 0),
				m.content_key, m.message_id_hdr, m.in_reply_to, m.refs, m.subject, m.from_json,
				m.to_json, m.cc_json, m.reply_to_json, m.delivered_to_json, m.date, m.size,
				m.flags_json, m.internaldate, m.has_attachments, m.raw_blob, m.body_text,
				m.body_html_sanitized, m.thread_id, m.snippet, m.auth_results, m.parse_errors,
				m.disabled_at, m.disabled_reason, m.seen, m.raw_path, m.body_status,
				m.derived_version, m.derive_failed_version
			 FROM messages m LEFT JOIN folders f ON f.id = m.folder_id`,
			`DROP TABLE messages`,
			`ALTER TABLE messages_new RENAME TO messages`,
			`CREATE INDEX idx_messages_account_content ON messages(account_id, content_key)`,
			`CREATE INDEX idx_messages_thread ON messages(thread_id)`,
			`CREATE INDEX idx_messages_inbox ON messages(folder_id, date DESC, id DESC)`,
			`CREATE INDEX idx_messages_derived_version ON messages(account_id, derived_version)`,
		},
	},
	{
		version: 10,
		statements: []string{
			// The content hash of a hidden message's backup blob (blobstore). It is
			// the one message the server no longer holds, so the bytes are copied
			// outside the databases at disable time and the backup mirrors the store
			// (ARCHITECTURE.md 9). A live row leaves this NULL.
			`ALTER TABLE messages ADD COLUMN disabled_blob TEXT`,
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
	{
		version: 2,
		statements: []string{
			// The operator's name, icon and photo for an account (PLAN.md 3). They are
			// locally owned, so they live here and not on the rebuildable mirror row;
			// account_id is the config id the mirror's accounts table also uses.
			`CREATE TABLE account_profiles (
				account_id   TEXT PRIMARY KEY,
				display_name TEXT NOT NULL DEFAULT '',
				icon         TEXT NOT NULL DEFAULT '',
				photo_blob   BLOB
			)`,
		},
	},
}

// SchemaVersions reports the newest migration of the mirror and of the state
// database, so a cache of built databases can tell when a schema change has
// made it stale.
func SchemaVersions() (mirror, state int) {
	return mirrorMigrations[len(mirrorMigrations)-1].version, stateMigrations[len(stateMigrations)-1].version
}
