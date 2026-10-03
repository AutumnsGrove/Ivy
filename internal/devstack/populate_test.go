package devstack_test

import (
	"context"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/store"
)

// mirrorCount is how many messages the mirror holds, read through the store the
// way the gateway would, after Populate has released its own handle.
func mirrorCount(t *testing.T, dir string) int {
	t.Helper()
	dbs, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbs.Close()
	var n int
	if err := dbs.Mirror.Read.QueryRow(`SELECT count(*) FROM messages`).Scan(&n); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return n
}

func TestPopulateFullSyncsEveryAccountOverIMAP(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	opts.Profile = "minimal"
	opts.Mode = devstack.ModeFull
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = stack.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sum, err := devstack.Populate(ctx, stack, opts)
	if err != nil {
		t.Fatalf("Populate: %v", err)
	}

	if sum.Accounts != len(stack.Config.Accounts) {
		t.Errorf("populated %d accounts, want %d", sum.Accounts, len(stack.Config.Accounts))
	}
	if sum.Messages != stack.Seed.Delivered {
		t.Errorf("populated %d messages, mailworld holds %d", sum.Messages, stack.Seed.Delivered)
	}
	if got := mirrorCount(t, stack.Config.DataDir); got != stack.Seed.Delivered {
		t.Errorf("mirror holds %d messages, want %d", got, stack.Seed.Delivered)
	}
}

func TestPopulateTwiceNeitherDuplicatesNorFails(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	opts.Profile = "minimal"
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = stack.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := range 2 {
		if _, err := devstack.Populate(ctx, stack, opts); err != nil {
			t.Fatalf("Populate #%d: %v", i+1, err)
		}
	}
	if got := mirrorCount(t, stack.Config.DataDir); got != stack.Seed.Delivered {
		t.Errorf("after two runs the mirror holds %d messages, want %d", got, stack.Seed.Delivered)
	}
}

func TestPopulateStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	opts.Profile = "minimal"
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = stack.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := devstack.Populate(ctx, stack, opts); err == nil {
		t.Fatal("Populate ignored a cancelled context")
	}
}

func TestPopulateReportsAnUnreachableMailworld(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	opts.Profile = "minimal"
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = stack.Close() })
	if err := devstack.ApplyState(stack.World, "sync-auth-failed"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := devstack.Populate(ctx, stack, opts); err == nil {
		t.Fatal("Populate swallowed a login failure")
	}
}
