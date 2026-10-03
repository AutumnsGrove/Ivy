// Package store owns the two SQLite files and their schema.
//
// mirror.db is a rebuildable mirror of the mailboxes; state.db holds the small
// locally owned state that is backed up. Both refer to mail by the content key,
// never by a mirror row id (ARCHITECTURE.md section 3).
//
// Each file is opened for the single-writer model (STANDARDS.md section 4): a
// pool of read connections that never block the writer (WAL), and exactly one
// write connection. Writers queue inside Go on that one connection instead of
// racing for SQLite's write lock, so a read-then-write transaction can never
// fail with SQLITE_BUSY.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // the pure-Go driver, registered as "sqlite"
)

// readConns bounds the read pool: enough for concurrent API reads and a sync
// worker on a 4-core board, small enough to stay inside its memory budget.
const readConns = 4

// DB is one SQLite file opened for the single-writer model.
//
// Read serves every query; it is opened query_only, so a stray write through it
// fails loudly. Write is the only way to change the file. It holds one
// connection and begins transactions with BEGIN IMMEDIATE, so use it for
// ExecContext and short transactions and never keep a Rows open on it: a second
// statement would wait for the connection that Rows is holding.
type DB struct {
	Read  *sql.DB
	Write *sql.DB
}

// Close closes both handles; the write connection goes last so SQLite can
// checkpoint and remove the WAL once no reader is left.
func (d *DB) Close() error {
	return errors.Join(d.Read.Close(), d.Write.Close())
}

// Ping checks that the file is readable. It deliberately does not touch the
// write connection: a long batch holds it, and a health check must not queue
// behind a sync.
func (d *DB) Ping(ctx context.Context) error {
	return d.Read.PingContext(ctx)
}

// DBs is the pair of databases Ivy runs on.
type DBs struct {
	Mirror *DB
	State  *DB
	// Dir is the data directory holding both files, so other packages can place
	// their own files (the message spool) beside them.
	Dir string
}

// Open opens both databases under dir, applying the per-connection pragmas and
// any pending migrations. Opening an already-current directory is a no-op.
func Open(ctx context.Context, dir string) (*DBs, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	mirror, err := openDB(ctx, filepath.Join(dir, "mirror.db"), mirrorMigrations)
	if err != nil {
		return nil, fmt.Errorf("mirror: %w", err)
	}
	state, err := openDB(ctx, filepath.Join(dir, "state.db"), stateMigrations)
	if err != nil {
		_ = mirror.Close()
		return nil, fmt.Errorf("state: %w", err)
	}
	dbs := &DBs{Mirror: mirror, State: state, Dir: dir}
	if err := dbs.moveLegacyProfiles(ctx); err != nil {
		_ = dbs.Close()
		return nil, err
	}
	return dbs, nil
}

// Close closes both databases, returning the first error.
func (d *DBs) Close() error {
	return errors.Join(d.Mirror.Close(), d.State.Close())
}

// openDB opens one file: the writer first, so the file exists and is migrated
// before any reader connects to it.
func openDB(ctx context.Context, path string, migrations []migration) (*DB, error) {
	write, err := openPool(ctx, path, "&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	if err := migrate(ctx, write, migrations); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	read, err := openPool(ctx, path, "&_pragma=query_only(1)")
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}
	read.SetMaxOpenConns(readConns)
	return &DB{Read: read, Write: write}, nil
}

func openPool(ctx context.Context, path, extra string) (*sql.DB, error) {
	// Pragmas are set per connection through the DSN so that every pooled
	// connection gets them, not just the one that ran a PRAGMA statement. The
	// path is escaped because SQLite parses the DSN as a URI: a '?', '#' or '%'
	// in the operator's data dir would otherwise truncate or rewrite it.
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)" +
		extra
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
