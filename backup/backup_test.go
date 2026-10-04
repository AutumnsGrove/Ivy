package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/lockfile"
	"github.com/AutumnsGrove/Ivy/store"
)

var baseTime = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// clock returns a Now function over a time the test advances by hand.
func clock(t *testing.T, at *time.Time) func() time.Time {
	t.Helper()
	return func() time.Time { return *at }
}

func openStore(t *testing.T, dir string) *store.DBs {
	t.Helper()
	dbs, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return dbs
}

func snapshotFiles(t *testing.T, target string) []string {
	t.Helper()
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", target, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), snapPrefix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// A run writes a state.db snapshot to every target and verifies each copy, so
// the backup is known good before any pruning.
func TestRunWritesAVerifiedSnapshotToEveryTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbs := openStore(t, dir)
	defer dbs.Close()
	if err := dbs.SetSetting(ctx, "", "theme", "night"); err != nil {
		t.Fatalf("seed setting: %v", err)
	}

	targetA := filepath.Join(t.TempDir(), "a")
	targetB := filepath.Join(t.TempDir(), "b")
	now := baseTime
	m := New(dbs, []string{targetA, targetB}, WithClock(clock(t, &now)))

	res, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Snapshots) != 2 {
		t.Fatalf("Snapshot count = %d, want 2", len(res.Snapshots))
	}
	for _, path := range []string{targetA, targetB} {
		if n := len(snapshotFiles(t, path)); n != 1 {
			t.Errorf("%s has %d snapshots, want 1", path, n)
		}
	}
	if err := Verify(ctx, res.Snapshots[0].Path); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// The 15-day window with a floor of 10 is the settled policy (round 30): a
// month of daily backups leaves the newest fifteen.
func TestRunPrunesToTheNewestFifteenDays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t, t.TempDir())
	defer dbs.Close()
	target := filepath.Join(t.TempDir(), "backups")
	now := baseTime
	m := New(dbs, []string{target}, WithClock(clock(t, &now)))

	for day := 0; day < 30; day++ {
		now = baseTime.AddDate(0, 0, day)
		if _, err := m.Run(ctx); err != nil {
			t.Fatalf("Run day %d: %v", day, err)
		}
	}
	snaps, err := m.List(target)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snaps) != 15 {
		t.Fatalf("snapshots = %d, want 15", len(snaps))
	}
	if oldest, newest := snaps[len(snaps)-1].Time, snaps[0].Time; !oldest.Equal(baseTime.AddDate(0, 0, 15)) || !newest.Equal(baseTime.AddDate(0, 0, 29)) {
		t.Errorf("kept %s..%s, want day 15..29", oldest, newest)
	}
}

// A clock jump (or a long outage) must never let pruning take the last ten.
func TestPruneKeepsTheFloorWhenTheClockJumps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t, t.TempDir())
	defer dbs.Close()
	target := filepath.Join(t.TempDir(), "backups")
	now := baseTime
	m := New(dbs, []string{target}, WithClock(clock(t, &now)))

	for day := 0; day < 12; day++ {
		now = baseTime.AddDate(0, 0, day)
		if _, err := m.Run(ctx); err != nil {
			t.Fatalf("Run day %d: %v", day, err)
		}
	}
	now = baseTime.AddDate(0, 0, 31)
	if _, err := m.Run(ctx); err != nil {
		t.Fatalf("Run after jump: %v", err)
	}
	snaps, err := m.List(target)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snaps) != 10 {
		t.Errorf("snapshots after a jump = %d, want the floor of 10", len(snaps))
	}
	if !snaps[0].Time.Equal(now) {
		t.Errorf("newest = %s, want the fresh snapshot %s", snaps[0].Time, now)
	}
}

// Pruning runs only after a verified new backup. If the new snapshot cannot be
// written, the old ones stay even though they are past the window.
func TestFailedRunDoesNotPrune(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t, t.TempDir())
	defer dbs.Close()
	target := filepath.Join(t.TempDir(), "backups")
	now := baseTime
	m := New(dbs, []string{target}, WithClock(clock(t, &now)))

	for day := 0; day < 12; day++ {
		now = baseTime.AddDate(0, 0, day)
		if _, err := m.Run(ctx); err != nil {
			t.Fatalf("Run day %d: %v", day, err)
		}
	}
	// Occupy the next snapshot's destination with a directory so its rename fails.
	now = baseTime.AddDate(0, 0, 31)
	dest := filepath.Join(target, snapshotName(now))
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatalf("occupy destination: %v", err)
	}
	if _, err := m.Run(ctx); err == nil {
		t.Fatal("Run succeeded even though its snapshot could not be written")
	}
	if n := len(snapshotFiles(t, target)); n != 12 {
		t.Errorf("snapshots after a failed run = %d, want the 12 untouched", n)
	}
}

// A corrupt snapshot is detected rather than counted as a good backup.
func TestVerifyDetectsACorruptSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t, t.TempDir())
	defer dbs.Close()
	target := filepath.Join(t.TempDir(), "backups")
	now := baseTime
	m := New(dbs, []string{target}, WithClock(clock(t, &now)))
	res, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	path := res.Snapshots[0].Path
	if err := os.WriteFile(path, []byte("not a zstd frame"), 0o600); err != nil {
		t.Fatalf("corrupt snapshot: %v", err)
	}
	if err := Verify(ctx, path); err == nil {
		t.Error("Verify accepted a corrupt snapshot")
	}
}

// New blobs are mirrored to every target and kept there, whatever the 15-day
// rule does to snapshots. Identical bytes are copied once.
func TestRunMirrorsBlobsAndNeverPrunesThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t, t.TempDir())
	defer dbs.Close()
	target := filepath.Join(t.TempDir(), "backups")
	now := baseTime
	m := New(dbs, []string{target}, WithClock(clock(t, &now)))

	hash, _, err := dbs.Blobs.Put(ctx, strings.NewReader("hidden mail"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := m.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	blobPath := filepath.Join(target, blobsDir, hash[:2], hash)
	got, err := os.ReadFile(blobPath)
	if err != nil || string(got) != "hidden mail" {
		t.Fatalf("target blob = %q, %v", got, err)
	}
	before, err := os.Stat(blobPath)
	if err != nil {
		t.Fatalf("stat blob: %v", err)
	}

	for day := 1; day < 30; day++ {
		now = baseTime.AddDate(0, 0, day)
		if _, err := m.Run(ctx); err != nil {
			t.Fatalf("Run day %d: %v", day, err)
		}
	}
	if _, err := os.Stat(blobPath); err != nil {
		t.Errorf("the blob was pruned with the snapshots: %v", err)
	}
	after, err := os.Stat(blobPath)
	if err != nil {
		t.Fatalf("stat blob after: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("an already-mirrored blob was rewritten")
	}
}

// A hidden row whose blob was never stored is healed on the next backup, so an
// old disable or a copy failure cannot leave a message out of the backup.
func TestRunReconcilesDisabledBlobs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t, t.TempDir())
	defer dbs.Close()
	seedDisabledRow(t, dbs, "acct-1", "folder-1", "m1", []byte("old hidden mail"))

	target := filepath.Join(t.TempDir(), "backups")
	now := baseTime
	m := New(dbs, []string{target}, WithClock(clock(t, &now)))
	if _, err := m.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var hash string
	if err := dbs.Mirror.Read.QueryRowContext(ctx,
		`SELECT COALESCE(disabled_blob, '') FROM messages WHERE id = 'm1'`).Scan(&hash); err != nil {
		t.Fatalf("read disabled_blob: %v", err)
	}
	if hash == "" || !dbs.Blobs.Has(hash) {
		t.Fatalf("the disabled row's blob was not stored (hash %q)", hash)
	}
	if _, err := os.Stat(filepath.Join(target, blobsDir, hash[:2], hash)); err != nil {
		t.Errorf("the reconciled blob was not mirrored to the target: %v", err)
	}
}

// Restore brings back the state that was in the chosen snapshot, not the state
// that replaced it, and merges the blobs stored beside the snapshot.
func TestRestoreRoundTripsStateAndBlobs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbs := openStore(t, dir)
	target := filepath.Join(t.TempDir(), "backups")
	now := baseTime
	m := New(dbs, []string{target}, WithClock(clock(t, &now)))

	if err := dbs.SetSetting(ctx, "", "a", "1"); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := dbs.UpsertTag(ctx, store.Tag{ID: "t1", Slug: "work", Name: "Work"}); err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	if err := dbs.TagMessage(ctx, "acct-1", "ck1", "t1", "operator"); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	hash, _, err := dbs.Blobs.Put(ctx, strings.NewReader("disabled bytes"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	first, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}

	now = baseTime.AddDate(0, 0, 1)
	if err := dbs.SetSetting(ctx, "", "b", "2"); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if _, err := m.Run(ctx); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	// The local blob is the only copy on this device; drop it so the restore has
	// to merge the target's store back in.
	if err := os.Remove(dbs.Blobs.Path(hash)); err != nil {
		t.Fatalf("drop local blob: %v", err)
	}
	if err := dbs.Close(); err != nil {
		t.Fatalf("close before restore: %v", err)
	}

	if err := m.Restore(ctx, first.Snapshots[0].Path); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	restored := openStore(t, dir)
	defer restored.Close()
	if got, ok, err := restored.GetSetting(ctx, "", "a"); err != nil || !ok || got != "1" {
		t.Errorf("a after restore = %q,%v,%v, want 1", got, ok, err)
	}
	if _, ok, err := restored.GetSetting(ctx, "", "b"); err != nil || ok {
		t.Errorf("b survived the restore to an older snapshot: %v,%v", ok, err)
	}
	if !restored.Blobs.Has(hash) {
		t.Error("the restored blob store is missing the snapshot's blob")
	}
	var memberships int
	if err := restored.State.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM message_tags WHERE tag_id = 't1'`).Scan(&memberships); err != nil {
		t.Fatalf("read membership: %v", err)
	}
	if memberships != 1 {
		t.Errorf("tag membership after restore = %d, want 1", memberships)
	}
}

// Restore must not run under a live server: it acquires the same lock run holds.
func TestRestoreRefusesWhileTheDataDirIsLocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbs := openStore(t, dir)
	defer dbs.Close()
	target := filepath.Join(t.TempDir(), "backups")
	now := baseTime
	m := New(dbs, []string{target}, WithClock(clock(t, &now)))
	res, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	lock, err := lockfile.Acquire(filepath.Join(dir, LockName))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	if err := m.Restore(ctx, res.Snapshots[0].Path); !errors.Is(err, lockfile.ErrLocked) {
		t.Fatalf("Restore while locked = %v, want ErrLocked", err)
	}
}

// A target that cannot be written does not stop the others, and the error is
// reported, not swallowed.
func TestRunKeepsGoingWhenOneTargetFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t, t.TempDir())
	defer dbs.Close()
	good := filepath.Join(t.TempDir(), "good")
	bad := filepath.Join(t.TempDir(), "bad")
	if err := os.WriteFile(bad, []byte("a file, not a directory"), 0o600); err != nil {
		t.Fatalf("make bad target: %v", err)
	}
	now := baseTime
	m := New(dbs, []string{bad, good}, WithClock(clock(t, &now)))

	res, err := m.Run(ctx)
	if err == nil {
		t.Fatal("Run reported success with a broken target")
	}
	if len(res.Snapshots) != 1 {
		t.Errorf("Snapshots = %d, want the one good target", len(res.Snapshots))
	}
	if n := len(snapshotFiles(t, good)); n != 1 {
		t.Errorf("good target has %d snapshots, want 1", n)
	}
}

// The daily trigger lands on the configured local time, today if it is still
// ahead, tomorrow if it has passed.
func TestNextDaily(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, loc)
	got, err := NextDaily(at, "03:00")
	if err != nil {
		t.Fatalf("NextDaily: %v", err)
	}
	if want := time.Date(2026, 10, 5, 3, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("NextDaily after noon = %s, want %s", got, want)
	}
	got, err = NextDaily(time.Date(2026, 10, 4, 1, 0, 0, 0, loc), "03:00")
	if err != nil {
		t.Fatalf("NextDaily: %v", err)
	}
	if want := time.Date(2026, 10, 4, 3, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("NextDaily before dawn = %s, want %s", got, want)
	}
	if _, err := NextDaily(at, "25:00"); err == nil {
		t.Error("NextDaily accepted an invalid time")
	}
}

func seedDisabledRow(t *testing.T, dbs *store.DBs, accountID, folderID, id string, raw []byte) {
	t.Helper()
	ctx := context.Background()
	if _, err := dbs.Mirror.Write.ExecContext(ctx,
		`INSERT INTO accounts (id, address, imap_host, imap_port, smtp_host, smtp_port, username, created_at)
		 VALUES (?, 'me@example.test', 'imap.test', 993, 'smtp.test', 465, 'me', '2026-10-02T00:00:00Z')`,
		accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := dbs.Mirror.Write.ExecContext(ctx,
		`INSERT INTO folders (id, account_id, name, role) VALUES (?, ?, 'INBOX', 'inbox')`,
		folderID, accountID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	if err := dbs.UpsertMessage(ctx, store.Message{
		ID: id, AccountID: accountID, FolderID: folderID, UID: 1, ContentKey: id,
		RawBlob: raw, DisabledAt: baseTime, DisabledReason: store.DisabledRemoved,
	}); err != nil {
		t.Fatalf("seed disabled row: %v", err)
	}
}
