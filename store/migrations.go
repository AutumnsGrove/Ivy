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
	{
		version: 11,
		statements: []string{
			// Denormalised from flags_json like `seen`, so the reader can show and
			// toggle a star without a JSON scan over every message.
			`ALTER TABLE messages ADD COLUMN flagged INTEGER NOT NULL DEFAULT 0`,
			// Backfill already-mirrored mail; flags_json keeps the server's casing.
			`UPDATE messages SET flagged = 1 WHERE flags_json LIKE '%Flagged%'`,
		},
	},
	{
		version: 12,
		statements: []string{
			// v11's LIKE also matched keywords that merely contain the word (a
			// "$notflagged" label). Recompute from the parsed flag list instead;
			// v11 stays as released because migrations are append-only.
			`UPDATE messages SET flagged = 0`,
			`UPDATE messages SET flagged = 1
			 WHERE json_valid(flags_json)
			   AND EXISTS (SELECT 1 FROM json_each(messages.flags_json) WHERE lower(value) = '\flagged')`,
		},
	},
	{
		version: 13,
		statements: []string{
			// extracted_text holds the plain text pulled out of a message body or
			// one attachment, keyed by the durable content identity (the message
			// content_key for a body, the attachment content_hash otherwise). It is
			// derived and rebuildable, so it lives in the mirror (ARCHITECTURE.md 3).
			`CREATE TABLE extracted_text (
				ref             TEXT NOT NULL,
				kind            TEXT NOT NULL,
				tier            INTEGER NOT NULL DEFAULT 0,
				status          TEXT NOT NULL DEFAULT '',
				text            TEXT NOT NULL DEFAULT '',
				derived_version INTEGER NOT NULL DEFAULT 0,
				updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
				PRIMARY KEY (ref, kind)
			)`,
			`CREATE INDEX idx_extracted_text_status ON extracted_text(kind, status)`,
			// search_docs maps a durable content key to its FTS row, so a document
			// can be updated by identity without scanning the index. One row per
			// (account, content key): a message in two folders is one document, and
			// an identical Message-ID is one document by design (N8).
			`CREATE TABLE search_docs (
				id          INTEGER PRIMARY KEY,
				account_id  TEXT NOT NULL,
				content_key TEXT NOT NULL,
				UNIQUE (account_id, content_key)
			)`,
			`CREATE INDEX idx_search_docs_content ON search_docs(content_key)`,
			// The FTS5 index; its rowid mirrors search_docs.id. unicode61 with
			// diacritics folding is the chosen tokenizer (round 55, next_steps).
			`CREATE VIRTUAL TABLE search_index USING fts5(
				subject, body, attachment,
				tokenize = 'unicode61 remove_diacritics 2'
			)`,
		},
	},
	{
		version: 14,
		statements: []string{
			// One vector per chunk of one content key, keyed by the durable
			// content identity and the model, so a move or a UIDVALIDITY reset
			// never re-embeds and a model change is detected rather than mixed
			// (ARCHITECTURE.md 3). Derived and rebuildable, so it lives in the
			// rebuildable mirror; the int8 values carry the scale and the true
			// L2 norm needed for cosine.
			`CREATE TABLE embeddings (
				account_id TEXT NOT NULL,
				ref        TEXT NOT NULL,
				kind       TEXT NOT NULL,
				chunk_ix   INTEGER NOT NULL,
				model      TEXT NOT NULL,
				dims       INTEGER NOT NULL,
				scale      REAL NOT NULL,
				norm       REAL NOT NULL,
				vector     BLOB NOT NULL,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				PRIMARY KEY (account_id, ref, kind, chunk_ix, model, dims)
			)`,
			`CREATE INDEX idx_embeddings_ref ON embeddings(account_id, ref, kind, model)`,
		},
	},
	{
		version: 15,
		statements: []string{
			// Which messages the ingest rule pass has already run every rule over, so
			// a steady-state sync evaluates only what arrived and never rescans the
			// mailbox. It is a rebuildable cache of the mirror, so it lives here and
			// joins the message rows in one database; the durable match counts are
			// rule_hits in state.db. A rebuilt mailbox re-evaluates, which only
			// re-records the same idempotent hits.
			`CREATE TABLE rule_eval (
				account_id  TEXT NOT NULL,
				content_key TEXT NOT NULL,
				PRIMARY KEY (account_id, content_key)
			)`,
		},
	},
	{
		version: 16,
		statements: []string{
			// One row per address seen in the visible mail, the derived half of
			// People (ARCHITECTURE.md 3). It is rebuilt from the mirror, so it lives
			// here; the operator's merges of several addresses into one person are
			// the locally owned half, person_links in state.db. An address in
			// several accounts is one row with a JSON list of accounts.
			`CREATE TABLE people (
				address       TEXT PRIMARY KEY,
				name          TEXT NOT NULL DEFAULT '',
				accounts_json TEXT NOT NULL DEFAULT '[]',
				message_count INTEGER NOT NULL DEFAULT 0,
				first_seen    TEXT,
				last_seen     TEXT
			)`,
			`CREATE INDEX idx_people_last_seen ON people(last_seen DESC)`,
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
	{
		version: 3,
		statements: []string{
			// Blobs a purge wants erased from every backup target. It is locally owned
			// state (backed up), so an offline target is retried after a restore rather
			// than losing the erasure (N24). The hash is primary-keyed, so purging two
			// rows that share a blob records it once; the row is cleared only once every
			// target is clean.
			`CREATE TABLE pending_blob_deletions (
				content_hash TEXT PRIMARY KEY,
				recorded_at  TEXT NOT NULL DEFAULT (datetime('now'))
			)`,
		},
	},
	{
		version: 4,
		statements: []string{
			// The debt moved to marker files in the data dir (blobdeletion.go): a
			// table here is rolled back by restoring an older snapshot, which forgets
			// an erasure an offline backup target is still owed (N29).
			`DROP TABLE pending_blob_deletions`,
		},
	},
	{
		version: 5,
		statements: []string{
			// The outbox is the one write path to IMAP (CHUNK3-BRIEF.md 1). It is
			// locally owned, unrecoverable state, so it lives in the backed-up
			// database and refers to mail by content key and the folder's stable id,
			// never by a mirror row id or UID. An op names a postcondition; the UID
			// is resolved at dispatch (docs/handoffs/2026-10-04-C3-outbox.md).
			`CREATE TABLE outbox (
				id                 TEXT PRIMARY KEY,
				account_id         TEXT NOT NULL,
				seq                INTEGER NOT NULL,
				kind               TEXT NOT NULL,
				content_key        TEXT NOT NULL,
				source_folder_id   TEXT NOT NULL,
				expect             TEXT NOT NULL,
				source_uidvalidity INTEGER NOT NULL DEFAULT 0,
				source_uid         INTEGER NOT NULL DEFAULT 0,
				state              TEXT NOT NULL,
				attempts           INTEGER NOT NULL DEFAULT 0,
				next_attempt_at    TEXT,
				last_error_code    TEXT NOT NULL DEFAULT '',
				last_error_detail  TEXT NOT NULL DEFAULT '',
				idempotency_key    TEXT NOT NULL,
				created_at         TEXT NOT NULL,
				updated_at         TEXT NOT NULL,
				completed_at       TEXT
			)`,
			// A repeat of a live action is the same action; a terminal row must not
			// block a later one (flag, unflag, flag), so the key is unique only over
			// non-terminal ops.
			`CREATE UNIQUE INDEX idx_outbox_idempotency ON outbox(idempotency_key) WHERE state IN ('pending','in_flight')`,
			`CREATE INDEX idx_outbox_account_state_seq ON outbox(account_id, state, seq)`,
		},
	},
	{
		version: 6,
		statements: []string{
			// The cost ledger: one row per remote API call and, for a batched call
			// such as embeddings, one row per message with the call's exact cost
			// allocated by token share (ARCHITECTURE.md 3). It is locally owned and
			// backed up, so only the gate writes it. A blocked or failed call is
			// recorded at zero cost, so the volume stays visible.
			`CREATE TABLE api_calls (
				id            INTEGER PRIMARY KEY,
				at            TEXT NOT NULL,
				provider      TEXT NOT NULL DEFAULT '',
				endpoint      TEXT NOT NULL DEFAULT '',
				model         TEXT NOT NULL DEFAULT '',
				feature       TEXT NOT NULL DEFAULT '',
				account_id    TEXT NOT NULL DEFAULT '',
				content_key   TEXT NOT NULL DEFAULT '',
				input_tokens  INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				cost_usd      REAL NOT NULL DEFAULT 0,
				cost_estimated INTEGER NOT NULL DEFAULT 0,
				latency_ms    INTEGER NOT NULL DEFAULT 0,
				outcome       TEXT NOT NULL DEFAULT '',
				call_id       TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX idx_api_calls_at ON api_calls(at)`,
			`CREATE INDEX idx_api_calls_account_at ON api_calls(account_id, at)`,
			// The monthly counters the gate checks before a call: spend and call count
			// per account, period (YYYY-MM) and endpoint.
			`CREATE TABLE api_caps (
				account_id TEXT NOT NULL DEFAULT '',
				period     TEXT NOT NULL,
				endpoint   TEXT NOT NULL,
				spent_usd  REAL NOT NULL DEFAULT 0,
				calls      INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (account_id, period, endpoint)
			)`,
		},
	},
	{
		version: 7,
		statements: []string{
			// Rules are data (round 20): conditions and actions are JSON validated
			// against a closed vocabulary in Go, so running a rule never calls a
			// model. An empty account_id means every account; the rule pass still
			// runs per account. They are locally owned, so they live in state.db.
			`CREATE TABLE rules (
				id              TEXT PRIMARY KEY,
				account_id      TEXT NOT NULL DEFAULT '',
				conditions_json TEXT NOT NULL,
				actions_json    TEXT NOT NULL,
				enabled         INTEGER NOT NULL DEFAULT 1,
				created_at      TEXT NOT NULL,
				updated_at      TEXT NOT NULL
			)`,
			// One row per (rule, account, content key) the rule matched, so "matched
			// N times" is a lifetime count and re-evaluating a message is idempotent.
			// Keyed by content key, like tags, so a move never forgets a hit.
			`CREATE TABLE rule_hits (
				rule_id     TEXT NOT NULL REFERENCES rules(id),
				account_id  TEXT NOT NULL,
				content_key TEXT NOT NULL,
				matched_at  TEXT NOT NULL,
				PRIMARY KEY (rule_id, account_id, content_key)
			)`,
		},
	},
	{
		version: 8,
		statements: []string{
			// Snooze is a local hide-until (round 10): the mail stays on the server
			// and in the mirror, and the inbox view hides it until `until`. Keyed by
			// content key, so a move never forgets a snooze.
			`CREATE TABLE snoozes (
				account_id  TEXT NOT NULL,
				content_key TEXT NOT NULL,
				until       TEXT NOT NULL,
				created_at  TEXT NOT NULL,
				PRIMARY KEY (account_id, content_key)
			)`,
			// The operator's merges of several addresses into one person. People
			// themselves are derived (mirror.db, rebuilt from seen addresses), but a
			// merge is a human decision, so it is locally owned and survives a
			// rebuild. person_id is the canonical address (or `me`).
			`CREATE TABLE person_links (
				address    TEXT PRIMARY KEY,
				person_id  TEXT NOT NULL,
				created_at TEXT NOT NULL
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
