package devstack_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
)

func TestSaveAndRestoreSnapshot(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	opts.Profile = "demo"
	opts.Seed = 7
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	hash := stack.Seed.Hash
	if err := stack.Close(); err != nil {
		t.Fatal(err)
	}

	if err := devstack.SaveSnapshot(opts.Root, "before"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := devstack.Reset(opts.Root); err != nil {
		t.Fatalf("reset: %v", err)
	}
	names, err := devstack.Snapshots(opts.Root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(names) != 1 || names[0] != "before" {
		t.Fatalf("snapshots = %v, want [before]", names)
	}

	recipe, err := devstack.RestoreSnapshot(opts.Root, "before")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if recipe.Profile != "demo" || recipe.Seed != 7 || recipe.Hash != hash {
		t.Fatalf("recipe = %+v, want demo/7/%s", recipe, hash)
	}

	// Restoring then preparing reproduces the exact same mailbox.
	restored := recipe.Options(opts)
	stack2, err := devstack.Prepare(restored)
	if err != nil {
		t.Fatalf("re-prepare: %v", err)
	}
	t.Cleanup(func() { _ = stack2.Close() })
	if stack2.Seed.Hash != hash {
		t.Fatalf("restored hash = %s, want %s", stack2.Seed.Hash, hash)
	}
}

func TestSnapshotRejectsUnsafeName(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	for _, name := range []string{"../escape", "with/slash", "", "space name"} {
		if err := devstack.SaveSnapshot(opts.Root, name); err == nil {
			t.Errorf("SaveSnapshot accepted %q", name)
		}
	}
}

func TestResetKeepsSnapshots(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := stack.Close(); err != nil {
		t.Fatal(err)
	}
	if err := devstack.SaveSnapshot(opts.Root, "keep"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := devstack.Reset(opts.Root); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(devstack.SnapshotDir(opts.Root), "keep.json")); err != nil {
		t.Fatalf("snapshot did not survive reset: %v", err)
	}
	if _, err := os.Stat(devstack.DataDir(opts.Root)); !os.IsNotExist(err) {
		t.Fatalf("data dir survived reset: %v", err)
	}
}
