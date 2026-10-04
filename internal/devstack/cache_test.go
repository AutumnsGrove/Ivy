package devstack_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/store"
)

func populateIn(t *testing.T, opts devstack.Options) (*devstack.Stack, devstack.Summary) {
	t.Helper()
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sum, err := devstack.Populate(ctx, stack, opts)
	if err != nil {
		_ = stack.Close()
		t.Fatalf("Populate: %v", err)
	}
	return stack, sum
}

// dumpAll renders every table of both databases under dir, for comparing a
// restored directory with a built one.
func dumpAll(t *testing.T, dir string) map[string][]string {
	t.Helper()
	dbs, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	defer dbs.Close()
	out := map[string][]string{}
	for name, db := range map[string]*sql.DB{"mirror": dbs.Mirror.Read, "state": dbs.State.Read} {
		for _, table := range tableNames(t, db) {
			out[name+"."+table] = dumpTable(t, db, table)
		}
	}
	return out
}

func demoOpts(t *testing.T) devstack.Options {
	t.Helper()
	opts := prepareOpts(t)
	opts.Profile = "demo"
	opts.Mode = devstack.ModeFast
	return opts
}

func TestResetThenPopulateRestoresTheBuiltDatabases(t *testing.T) {
	t.Parallel()
	opts := demoOpts(t)

	first, sum := populateIn(t, opts)
	if sum.Restored {
		t.Fatal("the first populate claims to be restored from an empty cache")
	}
	built := dumpAll(t, first.Config.DataDir)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := devstack.Reset(opts.Root); err != nil {
		t.Fatal(err)
	}

	second, sum := populateIn(t, opts)
	t.Cleanup(func() { _ = second.Close() })
	if !sum.Restored {
		t.Fatal("populate after reset rebuilt instead of restoring the cache")
	}
	if sum.Messages != first.Seed.Delivered {
		t.Errorf("restored summary has %d messages, want %d", sum.Messages, first.Seed.Delivered)
	}
	restored := dumpAll(t, second.Config.DataDir)
	for table, want := range built {
		got := restored[table]
		if len(got) != len(want) {
			t.Errorf("%s: restored %d rows, built %d", table, len(got), len(want))
			continue
		}
		for i := range want {
			if want[i] != got[i] {
				t.Errorf("%s row %d differs after restore\n built:    %.400s\n restored: %.400s", table, i, want[i], got[i])
				break
			}
		}
	}

	// The cached accounts carry the previous run's ports; the live ones differ.
	dbs, err := store.Open(context.Background(), second.Config.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer dbs.Close()
	for _, a := range second.Config.Accounts {
		got, err := dbs.GetAccount(context.Background(), a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.IMAPPort != a.IMAPPort || got.SMTPPort != a.SMTPPort {
			t.Errorf("%s: restored ports %d/%d, live %d/%d", a.ID, got.IMAPPort, got.SMTPPort, a.IMAPPort, a.SMTPPort)
		}
	}
}

func TestCacheIsKeyedByTheSeed(t *testing.T) {
	t.Parallel()
	opts := demoOpts(t)
	first, _ := populateIn(t, opts)
	_ = first.Close()
	if err := devstack.Reset(opts.Root); err != nil {
		t.Fatal(err)
	}

	opts.Seed = 99
	second, sum := populateIn(t, opts)
	t.Cleanup(func() { _ = second.Close() })
	if sum.Restored {
		t.Fatal("a different seed restored the first seed's mailbox")
	}
}

// The cache must only ever hold a clean build: a directory the operator has
// already used is neither restored over nor saved from.
func TestCacheIgnoresADirectoryThatAlreadyHoldsData(t *testing.T) {
	t.Parallel()
	opts := demoOpts(t)
	stack, _ := populateIn(t, opts)
	t.Cleanup(func() { _ = stack.Close() })
	before, err := os.ReadDir(devstack.CacheDir(opts.Root))
	if err != nil {
		t.Fatalf("no cache after the first build: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sum, err := devstack.Populate(ctx, stack, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Restored {
		t.Error("restored over a directory that already held data")
	}
	after, _ := os.ReadDir(devstack.CacheDir(opts.Root))
	if len(after) != len(before) {
		t.Errorf("cache entries went from %d to %d on a re-run", len(before), len(after))
	}
}

func TestCorruptCacheFallsBackToABuild(t *testing.T) {
	t.Parallel()
	opts := demoOpts(t)
	first, _ := populateIn(t, opts)
	want := first.Seed.Delivered
	_ = first.Close()

	entries, err := os.ReadDir(devstack.CacheDir(opts.Root))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no cache to corrupt: %v", err)
	}
	bad := filepath.Join(devstack.CacheDir(opts.Root), entries[0].Name(), "mirror.db")
	if err := os.WriteFile(bad, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := devstack.Reset(opts.Root); err != nil {
		t.Fatal(err)
	}

	second, sum := populateIn(t, opts)
	t.Cleanup(func() { _ = second.Close() })
	if sum.Restored {
		t.Fatal("restored a corrupt cache")
	}
	if sum.Messages != want {
		t.Errorf("rebuilt %d messages, want %d", sum.Messages, want)
	}
}
