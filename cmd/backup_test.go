package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/backup"
	"github.com/AutumnsGrove/Ivy/internal/lockfile"
	"github.com/AutumnsGrove/Ivy/store"
)

func snapshotNames(t *testing.T, target string) []string {
	t.Helper()
	entries, err := os.ReadDir(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("ReadDir(%s): %v", target, err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "state-") && strings.HasSuffix(e.Name(), ".db.zst") {
			names = append(names, e.Name())
		}
	}
	return names
}

// `ivy backup` runs one snapshot into the configured target.
func TestBackupCommandWritesASnapshot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "backups")
	configPath := writeConfig(t, dir, "data_dir: "+dir+"\nbackup:\n  targets:\n    - "+target+"\n")

	root := New("test")
	root.SetArgs([]string{"--config", configPath, "backup"})
	root.SetOut(os.Stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if names := snapshotNames(t, target); len(names) != 1 {
		t.Errorf("target has %d snapshots, want 1", len(names))
	}
}

// `ivy restore` puts a chosen snapshot back in place, and the state that
// replaced it is moved aside rather than deleted.
func TestRestoreCommandReplacesState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "backups")
	configPath := writeConfig(t, dir, "data_dir: "+dir+"\nbackup:\n  targets:\n    - "+target+"\n")

	dbs, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := dbs.SetSetting(ctx, "", "a", "1"); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := dbs.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	root := New("test")
	root.SetArgs([]string{"--config", configPath, "backup"})
	root.SetOut(os.Stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("backup: %v", err)
	}
	names := snapshotNames(t, target)
	if len(names) != 1 {
		t.Fatalf("snapshots = %v, want one", names)
	}

	dbs, err = store.Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open after backup: %v", err)
	}
	if err := dbs.SetSetting(ctx, "", "a", "2"); err != nil {
		t.Fatalf("mutate a: %v", err)
	}
	if err := dbs.Close(); err != nil {
		t.Fatalf("close after mutate: %v", err)
	}

	restore := New("test")
	restore.SetArgs([]string{"--config", configPath, "restore", filepath.Join(target, names[0])})
	restore.SetOut(os.Stderr)
	if err := restore.Execute(); err != nil {
		t.Fatalf("restore: %v", err)
	}

	dbs, err = store.Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open after restore: %v", err)
	}
	defer dbs.Close()
	got, ok, err := dbs.GetSetting(ctx, "", "a")
	if err != nil || !ok || got != "1" {
		t.Errorf("a after restore = %q,%v,%v, want 1", got, ok, err)
	}
}

// Doctor tells the operator when every target shares the data directory's disk,
// because that backup does not survive the device dying.
func TestDoctorWarnsWhenBackupsShareTheDataDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := writeConfig(t, dir, "data_dir: "+dir+"\n")

	var out bytes.Buffer
	root := New("test")
	root.SetArgs([]string{"--config", configPath, "doctor"})
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), "same disk") {
		t.Errorf("doctor did not warn about a same-disk backup:\n%s", out.String())
	}
}

// A second server on the same data directory is refused rather than allowed to
// race the first for the writer connection.
func TestRunRefusesWhenAnotherServerHoldsTheLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := writeConfig(t, dir, "data_dir: "+dir+"\n")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := lockfile.Acquire(filepath.Join(dir, backup.LockName))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	root := New("test")
	root.SetArgs([]string{"--config", configPath, "run"})
	root.SetOut(os.Stderr)
	root.SetErr(os.Stderr)
	err = root.Execute()
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("run with a held lock = %v, want an already-running error", err)
	}
}

// A running server holds the lock, so restore can see it is not safe.
func TestRunHoldsTheDataDirLock(t *testing.T) {
	t.Parallel()
	addr := freeAddr(t)
	dir := t.TempDir()
	configPath := writeConfig(t, dir, "listen: "+addr+"\ndata_dir: "+dir+"\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := New("test")
	root.SetArgs([]string{"--config", configPath, "run"})
	root.SetOut(os.Stderr)
	root.SetErr(os.Stderr)
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()

	waitForHealth(t, "http://"+addr+"/api/v1/health")
	if _, err := lockfile.Acquire(filepath.Join(dir, backup.LockName)); !errors.Is(err, lockfile.ErrLocked) {
		t.Errorf("Acquire while running = %v, want ErrLocked", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned: %v", err)
	}
}
