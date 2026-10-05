package search

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

func openStore(t *testing.T) *store.DBs {
	t.Helper()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	return dbs
}

func seedMail(t *testing.T, dbs *store.DBs, account, folder, id, key, subject, body string) {
	t.Helper()
	ctx := context.Background()
	if err := dbs.UpsertAccount(ctx, store.Account{ID: account, Address: account + "@example.test"}); err != nil {
		t.Fatalf("account: %v", err)
	}
	if err := dbs.UpsertFolder(ctx, store.Folder{ID: folder, AccountID: account, Name: folder, Role: store.RoleInbox}); err != nil {
		t.Fatalf("folder: %v", err)
	}
	if err := dbs.UpsertMessage(ctx, store.Message{
		ID: id, AccountID: account, FolderID: folder, UID: uint32(len(id)),
		ContentKey: key, Subject: subject, BodyText: body, Date: time.Now(),
	}); err != nil {
		t.Fatalf("message: %v", err)
	}
}

func TestChunkBoundsAndOverlaps(t *testing.T) {
	t.Parallel()
	if got := Chunk("   ", 10, 2); got != nil {
		t.Fatalf("blank text = %v, want nil", got)
	}
	long := strings.Repeat("word ", 5000)
	chunks := Chunk(long, 100, 10)
	if len(chunks) == 0 || len(chunks) > MaxChunks {
		t.Fatalf("chunks = %d, want 1..%d", len(chunks), MaxChunks)
	}
	for i, c := range chunks {
		if len(c) > 100*BytesPerToken {
			t.Errorf("chunk %d is %d bytes, over budget", i, len(c))
		}
	}
	// Adjacent chunks share their overlap, so a phrase on a boundary survives.
	if len(chunks) >= 2 {
		tail := strings.Fields(chunks[0])
		head := strings.Fields(chunks[1])
		if len(tail) == 0 || len(head) == 0 || tail[len(tail)-1] != head[len(head)-1] {
			t.Errorf("no overlap between %q and %q", chunks[0], chunks[1])
		}
	}
}

func TestChunkCutsAnOversizeWord(t *testing.T) {
	t.Parallel()
	word := strings.Repeat("a", 10000)
	chunks := Chunk(word, 10, 2) // 40-byte budget
	if len(chunks) == 0 || len(chunks[0]) > 40 {
		t.Fatalf("oversize word not cut: %d bytes", len(chunks[0]))
	}
}

func TestFusePrefersAHitInBothLists(t *testing.T) {
	t.Parallel()
	fts := []Hit{{AccountID: "a", ContentKey: "only-fts"}, {AccountID: "a", ContentKey: "both"}}
	vec := []Hit{{AccountID: "a", ContentKey: "both"}, {AccountID: "a", ContentKey: "only-vec"}}
	got := Fuse([][]Hit{fts, vec}, DefaultRRFK, 10)
	if len(got) != 3 {
		t.Fatalf("fused = %+v, want 3 unique", got)
	}
	if got[0].ContentKey != "both" {
		t.Fatalf("top = %+v, want the hit in both lists", got[0])
	}
	if limited := Fuse([][]Hit{fts, vec}, DefaultRRFK, 1); len(limited) != 1 {
		t.Fatalf("limit not applied: %+v", limited)
	}
}

func newWorld(t *testing.T) *mailworld.World {
	t.Helper()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("mailworld: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestEmbedWorkerEmbedsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	embedder := llm.NewOpenRouter(w.OpenRouterURL(), "k")
	gate := llm.NewGate(dbs)
	worker := NewEmbedWorker(dbs, gate, []AccountConfig{
		{ID: "acct", Embedder: embedder, Model: "m", Enabled: true, CapUSD: 5},
	}, WorkerOptions{Batch: 32})

	seedMail(t, dbs, "acct", "f1", "m1", "ck1", "Domain renewal", "Your domain invoice is attached")

	if n, err := worker.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("first pass = %d, %v; want 1", n, err)
	}
	if calls := len(w.Calls()); calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
	count := 0
	_ = dbs.EachEmbedding(ctx, []string{"acct"}, "m", func(store.Embedding) error { count++; return nil })
	if count == 0 {
		t.Fatal("no vectors stored")
	}

	// A second pass pays nothing.
	if n, _ := worker.RunOnce(ctx); n != 0 {
		t.Fatalf("second pass embedded %d, want 0", n)
	}
	if calls := len(w.Calls()); calls != 1 {
		t.Fatalf("provider called again: %d calls", calls)
	}

	// A move (disable the old copy, add a live copy with the same content key)
	// and a UIDVALIDITY reset both keep the same identity, so neither re-embeds.
	if err := dbs.DisableMessage(ctx, "m1", "moved", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertFolder(ctx, store.Folder{ID: "f2", AccountID: "acct", Name: "f2", Role: store.RoleArchive}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertMessage(ctx, store.Message{
		ID: "m1b", AccountID: "acct", FolderID: "f2", UID: 1, UIDValidity: 77,
		ContentKey: "ck1", Subject: "Domain renewal", BodyText: "Your domain invoice is attached", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := worker.RunOnce(ctx); n != 0 {
		t.Fatalf("move or UIDVALIDITY reset re-embedded %d", n)
	}
	if calls := len(w.Calls()); calls != 1 {
		t.Fatalf("provider calls after move = %d, want 1", calls)
	}
}

func TestEmbedWorkerSkipsANonEnabledHostedAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	gate := llm.NewGate(dbs)
	worker := NewEmbedWorker(dbs, gate, []AccountConfig{
		{ID: "acct", Embedder: llm.NewOpenRouter(w.OpenRouterURL(), "k"), Model: "m", Enabled: false},
	}, WorkerOptions{})
	seedMail(t, dbs, "acct", "f1", "m1", "ck1", "Subject", "body")
	if n, err := worker.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("pass = %d, %v; want 0", n, err)
	}
	if len(w.Calls()) != 0 {
		t.Fatal("hosted provider called for an account with smart features off")
	}
}

func TestVectorSearchRanksTheMatchingDocument(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	embedder := llm.NewOpenRouter(w.OpenRouterURL(), "k")
	gate := llm.NewGate(dbs)
	worker := NewEmbedWorker(dbs, gate, []AccountConfig{
		{ID: "acct", Embedder: embedder, Model: "m", Enabled: true, CapUSD: 5},
	}, WorkerOptions{})
	seedMail(t, dbs, "acct", "f1", "m1", "ck1", "Domain renewal", "The domain invoice renews in March")
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	query, err := embedder.Embed(ctx, "m", []string{"domain invoice renewal"})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(dbs, gate)
	hits, err := svc.VectorSearch(ctx, query.Vectors[0], []string{"acct"}, "m", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ContentKey != "ck1" {
		t.Fatalf("hits = %+v, want ck1 first", hits)
	}
}
