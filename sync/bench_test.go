package sync_test

import (
	"context"
	"net"
	"strconv"
	"testing"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// benchFetcherWorld starts a seeded fake and returns its account. The profile
// stays small on purpose (PERFORMANCE.md: never the 100k profile on the laptop).
func benchFetcherWorld(b *testing.B, messages int) (*mailworld.World, ivysync.Account) {
	b.Helper()
	w, err := mailworld.New()
	if err != nil {
		b.Fatalf("mailworld: %v", err)
	}
	b.Cleanup(func() { _ = w.Close() })
	if _, err := mailworld.Seed(w, mailworld.Large(messages)); err != nil {
		b.Fatalf("seed: %v", err)
	}
	host, portStr, err := net.SplitHostPort(w.IMAPAddr())
	if err != nil {
		b.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		b.Fatal(err)
	}
	return w, ivysync.Account{
		ID: "ivy", Address: "ivy@grove.test",
		IMAPHost: host, IMAPPort: port,
		Username: "ivy@grove.test", Password: mailworld.SeedPassword,
		Insecure: true,
	}
}

// BenchmarkSyncBackfill measures a cold backfill of a small mailbox: connect,
// envelope+flags, then bodies by size tier. It is the number to compare when the
// parser, the part walk or the store writes change.
func BenchmarkSyncBackfill(b *testing.B) {
	ctx := context.Background()
	_, acct := benchFetcherWorld(b, 200)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dbs, err := store.Open(ctx, b.TempDir())
		if err != nil {
			b.Fatalf("store: %v", err)
		}
		b.StartTimer()
		if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, acct); err != nil {
			b.Fatalf("fetch: %v", err)
		}
		b.StopTimer()
		_ = dbs.Close()
		b.StartTimer()
	}
}

// BenchmarkSyncDelta measures the steady state: a QRESYNC re-sync of an
// already-mirrored mailbox with nothing changed, which is the pass IDLE triggers
// most of the time.
func BenchmarkSyncDelta(b *testing.B) {
	ctx := context.Background()
	_, acct := benchFetcherWorld(b, 200)
	dbs, err := store.Open(ctx, b.TempDir())
	if err != nil {
		b.Fatalf("store: %v", err)
	}
	b.Cleanup(func() { _ = dbs.Close() })
	fetcher := ivysync.NewFetcher(dbs)
	if _, err := fetcher.Fetch(ctx, acct); err != nil {
		b.Fatalf("warm fetch: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fetcher.Fetch(ctx, acct); err != nil {
			b.Fatalf("delta fetch: %v", err)
		}
	}
}
