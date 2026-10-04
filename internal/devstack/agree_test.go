package devstack_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/store"
)

// volatile columns hold what two runs can never share: the wall-clock time a
// row was written and the ephemeral ports each mailworld listened on. Every
// other column must match.
var volatile = map[string]bool{
	"last_sync_at": true, "created_at": true, "updated_at": true,
	"imap_port": true, "smtp_port": true,
}

// dumpTable renders every row of a table as text, ordered by its first column
// (the id), with blobs reduced to a digest so a mismatch stays readable.
func dumpTable(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY 1`)
	if err != nil {
		t.Fatalf("dump %s: %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		var parts []string
		for i, v := range vals {
			switch {
			case volatile[cols[i]]:
				v = "-"
			default:
				if b, ok := v.([]byte); ok {
					sum := sha256.Sum256(b)
					v = "blob:" + hex.EncodeToString(sum[:6])
				}
			}
			parts = append(parts, fmt.Sprintf("%s=%v", cols[i], v))
		}
		out = append(out, strings.Join(parts, " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func tableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '%_fts%' AND name <> 'schema_migrations' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	return names
}

func spoolFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	root := filepath.Join(dir, "spool")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func populated(t *testing.T, mode devstack.Mode, profile string) string {
	t.Helper()
	opts := prepareOpts(t)
	opts.Profile = profile
	opts.Mode = mode
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare %s: %v", mode, err)
	}
	t.Cleanup(func() { _ = stack.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := devstack.Populate(ctx, stack, opts); err != nil {
		t.Fatalf("Populate %s: %v", mode, err)
	}
	return stack.Config.DataDir
}

// The fast seeder exists only because it is quick, so it must never become a
// second opinion about what the mailbox looks like (DEV.md section 2).
func TestFastAndFullAgreeForDemo(t *testing.T) {
	t.Parallel()
	full := populated(t, devstack.ModeFull, "demo")
	fast := populated(t, devstack.ModeFast, "demo")

	fullDBs, err := store.Open(context.Background(), full)
	if err != nil {
		t.Fatal(err)
	}
	defer fullDBs.Close()
	fastDBs, err := store.Open(context.Background(), fast)
	if err != nil {
		t.Fatal(err)
	}
	defer fastDBs.Close()

	for _, side := range []struct {
		name       string
		full, fast *sql.DB
	}{
		{"mirror", fullDBs.Mirror.Read, fastDBs.Mirror.Read},
		{"state", fullDBs.State.Read, fastDBs.State.Read},
	} {
		tables := tableNames(t, side.full)
		if len(tables) == 0 {
			t.Fatalf("%s: no tables found", side.name)
		}
		for _, table := range tables {
			want := dumpTable(t, side.full, table)
			got := dumpTable(t, side.fast, table)
			if len(want) != len(got) {
				t.Errorf("%s.%s: full has %d rows, fast has %d", side.name, table, len(want), len(got))
				continue
			}
			for i := range want {
				if want[i] != got[i] {
					t.Errorf("%s.%s row %d differs\n full: %.600s\n fast: %.600s", side.name, table, i, want[i], got[i])
					break
				}
			}
		}
	}

	if want, got := spoolFiles(t, full), spoolFiles(t, fast); strings.Join(want, ",") != strings.Join(got, ",") {
		t.Errorf("spool files differ\n full: %v\n fast: %v", want, got)
	}
	if n := len(dumpTable(t, fullDBs.Mirror.Read, "messages")); n < 300 {
		t.Errorf("only %d messages compared, the demo profile should hold 300+", n)
	}
}
