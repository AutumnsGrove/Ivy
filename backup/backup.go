// Package backup takes the daily consistent snapshot of state.db, mirrors the
// disabled-message blob store, prunes old snapshots and restores in place.
//
// state.db is the only database backed up: mirror.db is rebuilt from IMAP. The
// blob store holds the one kind of mail that rebuild cannot bring back, so it
// is copied whole and never pruned by the 15-day rule (ARCHITECTURE.md 9).
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite" // the pure-Go driver, registered as "sqlite"

	"github.com/AutumnsGrove/Ivy/internal/lockfile"
	"github.com/AutumnsGrove/Ivy/store"
)

// Settled policy (round 30): one snapshot a day, fifteen days kept, never fewer
// than the ten newest.
const (
	Keep  = 15 * 24 * time.Hour
	Floor = 10

	snapPrefix = "state-"
	snapSuffix = ".db.zst"
	// blobsDir is the disabled-blob store inside each target, beside the
	// snapshots.
	blobsDir = "blobs"
	// LockName is the data-directory lock `ivy run` holds and `ivy restore`
	// checks.
	LockName = "ivy.lock"
)

// Manager runs backups for one data directory into a set of targets.
type Manager struct {
	dbs     *store.DBs
	targets []string
	now     func() time.Time
	keep    time.Duration
	floor   int
}

// Option customises a Manager.
type Option func(*Manager)

// WithClock injects the clock, so tests can drive a month of backups.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) { m.now = now }
}

// WithPolicy overrides the keep window and floor; tests use it to shrink them.
func WithPolicy(keep time.Duration, floor int) Option {
	return func(m *Manager) {
		if keep > 0 && floor >= 0 {
			m.keep, m.floor = keep, floor
		}
	}
}

// New builds a Manager over an open store and its targets.
func New(dbs *store.DBs, targets []string, opts ...Option) *Manager {
	m := &Manager{dbs: dbs, targets: targets, now: time.Now, keep: Keep, floor: Floor}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Snapshot is one written state.db archive.
type Snapshot struct {
	Target string
	Path   string
	Time   time.Time
	Size   int64
}

// Result reports what a run wrote.
type Result struct {
	Snapshots []Snapshot
}

// Run writes and verifies a snapshot to every target, mirrors any new disabled
// blobs, and prunes each target that accepted the new snapshot. A failing target
// is reported but does not stop the others, and is never pruned.
func (m *Manager) Run(ctx context.Context) (Result, error) {
	if len(m.targets) == 0 {
		return Result{}, errors.New("backup: no targets configured")
	}
	// Reconcile first, but a hidden blob that cannot be read must not block the
	// state snapshot: it is reported at the end and retried on the next run.
	reconcileErr := m.reconcileDisabled(ctx)

	staging, err := os.MkdirTemp("", "ivy-backup-")
	if err != nil {
		return Result{}, fmt.Errorf("backup: staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	raw := filepath.Join(staging, "state.db")
	if _, err := m.dbs.State.Write.ExecContext(ctx, `VACUUM INTO ?`, raw); err != nil {
		return Result{}, fmt.Errorf("backup: vacuum into: %w", err)
	}
	// Verify the plain snapshot, then the artifact we will actually copy, so a
	// bad compression cannot masquerade as a good backup.
	if err := verifyStateFile(ctx, raw); err != nil {
		return Result{}, fmt.Errorf("backup: verify snapshot: %w", err)
	}
	now := m.now().UTC()
	name := snapshotName(now)
	packed := filepath.Join(staging, name)
	if err := compressZstd(raw, packed); err != nil {
		return Result{}, fmt.Errorf("backup: compress: %w", err)
	}
	if err := Verify(ctx, packed); err != nil {
		return Result{}, fmt.Errorf("backup: verify archive: %w", err)
	}

	var (
		res  Result
		errs []error
	)
	for _, target := range m.targets {
		snap, err := m.writeTarget(ctx, target, name, packed, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("target %s: %w", target, err))
			continue
		}
		res.Snapshots = append(res.Snapshots, snap)
		if _, err := m.pruneTarget(target, now); err != nil {
			errs = append(errs, fmt.Errorf("target %s: prune: %w", target, err))
		}
	}
	return res, errors.Join(reconcileErr, errors.Join(errs...))
}

// writeTarget copies the new snapshot and any new blobs to one target and
// verifies the copy there. Nothing is pruned by the caller unless this returns
// without error.
func (m *Manager) writeTarget(ctx context.Context, target, name, packed string, now time.Time) (Snapshot, error) {
	if err := os.MkdirAll(filepath.Join(target, blobsDir), 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("create target: %w", err)
	}
	if err := mirrorTree(ctx, m.dbs.Blobs.Dir(), filepath.Join(target, blobsDir)); err != nil {
		return Snapshot{}, fmt.Errorf("mirror blobs: %w", err)
	}

	dest := filepath.Join(target, name)
	tmp := dest + ".tmp"
	if err := copyFile(packed, tmp); err != nil {
		return Snapshot{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return Snapshot{}, fmt.Errorf("place snapshot: %w", err)
	}
	if err := Verify(ctx, dest); err != nil {
		_ = os.Remove(dest)
		return Snapshot{}, fmt.Errorf("verify copy: %w", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Target: target, Path: dest, Time: now, Size: info.Size()}, nil
}

// List returns a target's snapshots, newest first. Anything that is not a
// snapshot file (including the blob tree) is ignored.
func (m *Manager) List(target string) ([]Snapshot, error) {
	entries, err := os.ReadDir(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: list %s: %w", target, err)
	}
	var snaps []Snapshot
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		at, ok := parseSnapshotName(e.Name())
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("backup: stat %s: %w", e.Name(), err)
		}
		snaps = append(snaps, Snapshot{
			Target: target, Path: filepath.Join(target, e.Name()), Time: at, Size: info.Size(),
		})
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Time.After(snaps[j].Time) })
	return snaps, nil
}

// pruneTarget removes snapshots past the keep window, never taking the floor of
// newest ones. Only a target whose new snapshot verified reaches this.
func (m *Manager) pruneTarget(target string, now time.Time) (int, error) {
	snaps, err := m.List(target)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, s := range pruneSet(snaps, now, m.keep, m.floor) {
		if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("remove %s: %w", s.Path, err)
		}
		removed++
	}
	return removed, nil
}

// pruneSet picks the snapshots to remove from a newest-first list: everything
// past the keep window except the floor of newest ones.
func pruneSet(snaps []Snapshot, now time.Time, keep time.Duration, floor int) []Snapshot {
	var remove []Snapshot
	for i, s := range snaps {
		if i < floor {
			continue
		}
		if now.Sub(s.Time) >= keep {
			remove = append(remove, s)
		}
	}
	return remove
}

// reconcileDisabled copies every hidden message's raw bytes into the blob store
// if they are not there already, healing a row disabled before the store existed
// or one whose copy failed. It streams one message at a time.
func (m *Manager) reconcileDisabled(ctx context.Context) error {
	var errs []error
	iterErr := m.dbs.EachDisabledRaw(ctx, func(id, hash string, raw io.Reader) error {
		if hash != "" && m.dbs.Blobs.Has(hash) {
			return nil
		}
		stored, _, err := m.dbs.Blobs.Put(ctx, raw)
		if err != nil {
			// One unreadable message must not stop the others; keep going and
			// report it after the snapshot.
			errs = append(errs, fmt.Errorf("store blob for %s: %w", id, err))
			return nil
		}
		if stored == hash {
			return nil
		}
		if err := m.dbs.SetDisabledBlob(ctx, id, stored); err != nil {
			errs = append(errs, fmt.Errorf("record blob for %s: %w", id, err))
		}
		return nil
	})
	return errors.Join(append(errs, iterErr)...)
}

// Verify checks that a compressed snapshot decodes into a healthy SQLite file.
func Verify(ctx context.Context, path string) error {
	tmp, err := os.CreateTemp("", "ivy-verify-*.db")
	if err != nil {
		return fmt.Errorf("backup: verify temp: %w", err)
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpName) }()

	if err := decompressZstd(path, tmpName); err != nil {
		return err
	}
	return verifyStateFile(ctx, tmpName)
}

// verifyStateFile opens one SQLite file read-only and runs an integrity check.
func verifyStateFile(ctx context.Context, path string) error {
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?mode=ro&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()

	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity check %s: %w", path, err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity check %s: %s", path, result)
	}
	return nil
}

// Restore replaces the data directory's state.db with a verified snapshot and
// merges the blob store stored beside it. It refuses while a server holds the
// data-directory lock, and moves the replaced state.db aside rather than
// deleting it. It does not open the databases: the caller must have stopped the
// server (the lock proves it).
func (m *Manager) Restore(ctx context.Context, snapshotPath string) error {
	return Restore(ctx, m.dbs.Dir, snapshotPath)
}

// Restore is the package-level restore, usable without an open store so the CLI
// can replace state.db.
func Restore(ctx context.Context, dataDir, snapshotPath string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("backup: restore: %w", err)
	}
	lock, err := lockfile.Acquire(filepath.Join(dataDir, LockName))
	if err != nil {
		return fmt.Errorf("backup: restore: %w", err)
	}
	defer func() { _ = lock.Release() }()

	if err := Verify(ctx, snapshotPath); err != nil {
		return fmt.Errorf("backup: restore: %w", err)
	}

	stateDB := filepath.Join(dataDir, "state.db")
	tmp := filepath.Join(dataDir, ".state.db.restore")
	if err := decompressZstd(snapshotPath, tmp); err != nil {
		return fmt.Errorf("backup: restore: %w", err)
	}
	aside := stateDB + ".replaced-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(stateDB, aside); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: move current state aside: %w", err)
	}
	// The old write-ahead log belongs to the replaced file; keeping it would
	// corrupt the restored one.
	_ = os.Remove(stateDB + "-wal")
	_ = os.Remove(stateDB + "-shm")
	if err := os.Rename(tmp, stateDB); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup: place restored state: %w", err)
	}
	if err := mirrorTree(ctx, filepath.Join(filepath.Dir(snapshotPath), blobsDir), filepath.Join(dataDir, blobsDir)); err != nil {
		return fmt.Errorf("backup: restore blobs: %w", err)
	}
	return nil
}

// NextDaily returns the next local occurrence of at ("HH:MM") strictly after
// now, today if it is still ahead and tomorrow otherwise.
func NextDaily(now time.Time, at string) (time.Time, error) {
	hour, minute, err := parseClock(at)
	if err != nil {
		return time.Time{}, err
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next, nil
}

func parseClock(at string) (hour, minute int, err error) {
	parts := strings.Split(strings.TrimSpace(at), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("backup: time %q is not HH:MM", at)
	}
	hour, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("backup: time %q is not HH:MM", at)
	}
	minute, err = strconv.Atoi(parts[1])
	if err != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("backup: time %q is not HH:MM", at)
	}
	return hour, minute, nil
}

// snapshotName is the archive name for a timestamp, in UTC so it sorts and
// parses the same on any host.
func snapshotName(at time.Time) string {
	return snapPrefix + at.UTC().Format("20060102T150405Z") + snapSuffix
}

func parseSnapshotName(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, snapPrefix) || !strings.HasSuffix(name, snapSuffix) {
		return time.Time{}, false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(name, snapPrefix), snapSuffix)
	at, err := time.Parse("20060102T150405Z", mid)
	if err != nil {
		return time.Time{}, false
	}
	return at.UTC(), true
}

// compressZstd writes a zstd archive of src to dst.
func compressZstd(src, dst string) (err error) {
	in, err := os.Open(src) //nolint:gosec // G304: our own staging path
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // G304: our own staging path
	if err != nil {
		return err
	}
	zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		_ = out.Close()
		return err
	}
	if _, err := io.Copy(zw, in); err != nil {
		_ = zw.Close()
		_ = out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// maxRestoreBytes bounds a snapshot's decompressed size, so a corrupt or hostile
// archive cannot fill the disk while it is verified or restored.
const maxRestoreBytes = 1 << 30

// errSnapshotTooLarge reports an archive that decompresses past the bound.
var errSnapshotTooLarge = errors.New("backup: snapshot is larger than the restore limit")

// decompressZstd writes the plain bytes of a zstd archive to dst.
func decompressZstd(src, dst string) error {
	return decompressZstdLimit(src, dst, maxRestoreBytes)
}

// decompressZstdLimit decompresses at most limit bytes and fails when the
// archive wants to produce more.
func decompressZstdLimit(src, dst string, limit int64) (err error) {
	in, err := os.Open(src) //nolint:gosec // G304: a snapshot path the operator chose
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // G304: our own data dir
	if err != nil {
		return err
	}
	zr, err := zstd.NewReader(in)
	if err != nil {
		_ = out.Close()
		return err
	}
	defer zr.Close()
	n, err := io.Copy(out, io.LimitReader(zr, limit+1))
	if err != nil {
		_ = out.Close()
		return err
	}
	if n > limit {
		_ = out.Close()
		return errSnapshotTooLarge
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// copyFile copies src to dst, creating dst with the private mode the data dir
// uses.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src) //nolint:gosec // G304: our own staging path
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // G304: our own target
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// mirrorTree copies every regular file under src that dst does not already have.
// Blobs are content-addressed, so an existing file is by definition the same
// bytes and is left untouched.
func mirrorTree(ctx context.Context, src, dst string) error {
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".tmp-") {
			return nil // an in-flight store write, not a blob
		}
		target := filepath.Join(dst, rel)
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return copyFile(path, target)
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
