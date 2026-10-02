// Package store owns the two SQLite files and their schema.
//
// mirror.db is a rebuildable mirror of the mailboxes; state.db holds the small
// locally owned state that is backed up. Both refer to mail by the content key,
// never by a mirror row id (ARCHITECTURE.md section 3).
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DBs is the pair of databases Ivy runs on.
type DBs struct {
	Mirror *sql.DB
	State  *sql.DB
}

// Open opens both databases under dir, applying the per-connection pragmas and
// any pending migrations. Opening an already-current directory is a no-op.
func Open(dir string) (*DBs, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	mirror, err := openDB(filepath.Join(dir, "mirror.db"))
	if err != nil {
		return nil, fmt.Errorf("open mirror: %w", err)
	}
	if err := migrate(mirror, mirrorMigrations); err != nil {
		_ = mirror.Close()
		return nil, fmt.Errorf("migrate mirror: %w", err)
	}

	state, err := openDB(filepath.Join(dir, "state.db"))
	if err != nil {
		_ = mirror.Close()
		return nil, fmt.Errorf("open state: %w", err)
	}
	if err := migrate(state, stateMigrations); err != nil {
		_ = mirror.Close()
		_ = state.Close()
		return nil, fmt.Errorf("migrate state: %w", err)
	}

	return &DBs{Mirror: mirror, State: state}, nil
}

// Close closes both databases, returning the first error.
func (d *DBs) Close() error {
	err := d.Mirror.Close()
	if e := d.State.Close(); e != nil && err == nil {
		err = e
	}
	return err
}

func openDB(path string) (*sql.DB, error) {
	// Pragmas are set per connection through the DSN so that every pooled
	// connection gets them, not just the one that ran a PRAGMA statement.
	// The path is escaped because SQLite parses the DSN as a URI: a '?', '#' or '%'
	// in the operator's data dir would otherwise truncate or rewrite it.
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
