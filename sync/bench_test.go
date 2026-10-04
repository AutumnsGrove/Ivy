package sync_test

import (
	"context"
	"net"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

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

// BenchmarkSyncFullScanHeap reports how much heap one no-change full scan holds
// at its peak, per message. The runner reads every folder's envelopes before it
// writes anything, so this is the memory cost of a pass over an account the
// mirror already has (the first sync and every pass against a server without
// QRESYNC). Run it at two sizes and read the slope; never the 100k profile.
//
// It is an upper bound, not the client's share: the fake server runs in this
// process, so its response-encoding garbage is counted too (the figure tracks the
// seed's message size). BenchmarkSnapshotRetainedHeap isolates the client.
func BenchmarkSyncFullScanHeap(b *testing.B) {
	for _, n := range []int{500, 1000, 2000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			ctx := context.Background()
			_, acct := benchFetcherWorld(b, n)
			dbs, err := store.Open(ctx, b.TempDir())
			if err != nil {
				b.Fatalf("store: %v", err)
			}
			b.Cleanup(func() { _ = dbs.Close() })
			fetcher := ivysync.NewFetcher(dbs)
			if _, err := fetcher.Fetch(ctx, acct); err != nil {
				b.Fatalf("warm fetch: %v", err)
			}
			for i := 0; i < b.N; i++ {
				// Forgetting the modseq makes the next pass a full scan that stores nothing.
				if _, err := dbs.Mirror.Write.ExecContext(ctx, `UPDATE folders SET highestmodseq = 0`); err != nil {
					b.Fatalf("reset modseq: %v", err)
				}
				runtime.GC()
				var base runtime.MemStats
				runtime.ReadMemStats(&base)
				peak := peakHeap(func() {
					if _, err := fetcher.Fetch(ctx, acct); err != nil {
						b.Fatalf("full scan: %v", err)
					}
				})
				grown := float64(0)
				if peak > base.HeapAlloc {
					grown = float64(peak - base.HeapAlloc)
				}
				b.ReportMetric(grown/float64(n), "peak-heap-B/msg")
				b.ReportMetric(grown/(1<<20), "peak-heap-MiB")
			}
		})
	}
}

// peakHeap runs fn and returns the highest HeapAlloc a 1 ms sampler saw.
func peakHeap(fn func()) uint64 {
	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	fn()
	close(stop)
	<-done
	return peak.Load()
}

// BenchmarkSnapshotRetainedHeap reports what the client holds after reading one
// folder's metadata (UID, flags, envelope, date, size), per message. fetchAll
// keeps this for every folder at once until the whole account is reconciled, so
// it is the floor of the runner's memory on a full scan. The FETCH items are the
// ones snapshotFolder asks for.
func BenchmarkSnapshotRetainedHeap(b *testing.B) {
	for _, n := range []int{500, 1000, 2000, 4000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			w, acct := benchFetcherWorld(b, n)
			for i := 0; i < b.N; i++ {
				c, err := imapclient.DialInsecure(w.IMAPAddr(), nil)
				if err != nil {
					b.Fatalf("dial: %v", err)
				}
				if err := c.Login(acct.Username, acct.Password).Wait(); err != nil {
					b.Fatalf("login: %v", err)
				}
				if _, err := c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
					b.Fatalf("select: %v", err)
				}
				runtime.GC()
				var before runtime.MemStats
				runtime.ReadMemStats(&before)
				metas, err := c.Fetch(imap.UIDSet{imap.UIDRange{Start: 1, Stop: 0}},
					&imap.FetchOptions{UID: true, Flags: true}).Collect()
				if err != nil {
					b.Fatalf("fetch: %v", err)
				}
				runtime.GC()
				var after runtime.MemStats
				runtime.ReadMemStats(&after)
				held := float64(after.HeapAlloc) - float64(before.HeapAlloc)
				b.ReportMetric(held/float64(len(metas)), "retained-B/msg")
				b.ReportMetric(float64(len(metas)), "messages")
				runtime.KeepAlive(metas)
				_ = c.Close()
			}
		})
	}
}
