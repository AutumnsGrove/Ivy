package config

import (
	"path/filepath"
	"testing"
)

// Every setting has a known default (TESTING.md 6): the backup runs at 03:00
// into a folder beside the data directory until the operator says otherwise.
func TestBackupDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Backup.At != DefaultBackupAt {
		t.Errorf("Backup.At = %q, want %q", cfg.Backup.At, DefaultBackupAt)
	}
	targets := cfg.BackupTargets()
	if len(targets) != 1 || targets[0] != filepath.Join(cfg.DataDir, "backups") {
		t.Errorf("BackupTargets() = %v, want [%s]", targets, filepath.Join(cfg.DataDir, "backups"))
	}
}

func TestBackupTargetsAndTimeLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ivy.yaml")
	write(t, path, `
data_dir: /var/lib/ivy
backup:
  at: "21:30"
  targets:
    - /mnt/nas/ivy
    - /srv/backup/ivy
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Backup.At != "21:30" {
		t.Errorf("Backup.At = %q, want 21:30", cfg.Backup.At)
	}
	targets := cfg.BackupTargets()
	if len(targets) != 2 || targets[0] != "/mnt/nas/ivy" || targets[1] != "/srv/backup/ivy" {
		t.Errorf("BackupTargets() = %v", targets)
	}
}

func TestInvalidBackupTimeRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, at := range []string{"25:00", "03:60", "3am", "03", "03:00:00"} {
		path := filepath.Join(dir, "ivy.yaml")
		write(t, path, "backup:\n  at: \""+at+"\"\n")
		if _, err := Load(path); err == nil {
			t.Errorf("Load accepted backup.at %q", at)
		}
	}
}

func TestInvalidBackupTargetRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ivy.yaml")
	write(t, path, "backup:\n  targets:\n    - \"\"\n")
	if _, err := Load(path); err == nil {
		t.Error("Load accepted an empty backup target")
	}
}
