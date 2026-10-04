package devstack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// CacheDir holds built copies of the dev databases, one directory per key. A
// restore is a file copy, which is what makes a reset quick however large the
// profile is (DEV.md section 3).
func CacheDir(root string) string { return filepath.Join(DevDir(root), "cache") }

// cacheKey names a build by everything that changes what it contains: the
// profile and seed, which accounts are configured, and the three versions that
// shape the rows. The mode is not part of it, because fast and full are tested
// to agree. Hosts and ports are not either; they are refreshed on restore.
func cacheKey(stack *Stack, opts Options) string {
	mirror, state := store.SchemaVersions()
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%d|%d|%d", opts.Profile, opts.Seed, mirror, state, ivysync.DerivedVersion)
	for _, a := range stack.Config.Accounts {
		fmt.Fprintf(h, "|%s", a.ID)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// hasData reports whether the data directory already holds a mirror. Only an
// empty one is restored into, and only an empty one's build is cached, so the
// cache can never contain (or overwrite) what the operator did since.
func hasData(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "mirror.db"))
	return err == nil
}

// restoreCache copies a cached build into the data directory. It reports false,
// with the reason when there was a cache that had to be discarded, so the caller
// builds instead. A restore that fails halfway leaves nothing behind.
func restoreCache(ctx context.Context, dir string, stack *Stack) (Summary, bool, string) {
	if _, err := os.Stat(dir); err != nil {
		return Summary{}, false, ""
	}
	dataDir := stack.Config.DataDir
	fail := func(err error) (Summary, bool, string) {
		_ = os.RemoveAll(dataDir)
		return Summary{}, false, fmt.Sprintf("cached build discarded: %v", err)
	}
	if err := copyTree(dir, dataDir); err != nil {
		return fail(err)
	}
	dbs, err := store.Open(ctx, dataDir)
	if err != nil {
		return fail(err)
	}
	defer dbs.Close()

	// The cached account rows name the mailworld ports of the run that built
	// them; point them at this run's.
	for _, a := range stack.Config.Accounts {
		row, err := dbs.GetAccount(ctx, a.ID)
		if err != nil {
			return fail(fmt.Errorf("account %s: %w", a.ID, err))
		}
		row.IMAPHost, row.IMAPPort, row.SMTPHost, row.SMTPPort = a.IMAPHost, a.IMAPPort, a.SMTPHost, a.SMTPPort
		if err := dbs.UpsertAccount(ctx, row); err != nil {
			return fail(err)
		}
	}
	var n int
	if err := dbs.Mirror.Read.QueryRowContext(ctx, `SELECT count(*) FROM messages`).Scan(&n); err != nil {
		return fail(err)
	}
	return Summary{Accounts: len(stack.Config.Accounts), Messages: n, Restored: true}, true, ""
}

// saveCache copies a finished build into the cache, through a temporary
// directory so an interrupted copy is never mistaken for a complete one. The
// databases must be closed first so their write-ahead logs are folded in.
func saveCache(dir, dataDir string) error {
	tmp := dir + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := copyTree(dataDir, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("devstack: cache the build: %w", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("devstack: cache the build: %w", err)
	}
	return nil
}

// copyTree copies every regular file under src to dst. It copies rather than
// links: a hard link would let a write to the restored database change the
// cached one. SQLite's shared-memory files are skipped, since they only describe
// a live connection.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case !d.Type().IsRegular() || strings.HasSuffix(d.Name(), "-shm"):
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src) //nolint:gosec // G304: paths come from walking the harness's own directories
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // G304: as above
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	_, err = io.Copy(out, in)
	return err
}
