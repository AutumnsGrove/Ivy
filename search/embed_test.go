package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
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

// stubEmbedder is a hosted provider that answers every input with one vector,
// except that it rejects any batch containing a marked input, the way a
// provider refuses a single document it will not take.
type stubEmbedder struct {
	reject string
}

func (stubEmbedder) Name() string { return "stub" }

func (s stubEmbedder) Embed(_ context.Context, _ string, inputs []string) (llm.EmbedResult, error) {
	res := llm.EmbedResult{InputTokens: len(inputs), CostUSD: 0.0001}
	for _, in := range inputs {
		if s.reject != "" && strings.Contains(in, s.reject) {
			return llm.EmbedResult{}, errStubRejected
		}
		res.Vectors = append(res.Vectors, llm.Quantise([]float32{0.5, 0, 0, 0}))
	}
	return res, nil
}

var errStubRejected = errors.New("stub provider rejected the input")

func seedDated(t *testing.T, dbs *store.DBs, id, key, subject, body string, at time.Time) {
	t.Helper()
	if err := dbs.UpsertMessage(context.Background(), store.Message{
		ID: id, AccountID: "acct", FolderID: "f1", UID: uint32(len(id)) + uint32(at.Day()),
		ContentKey: key, Subject: subject, BodyText: body, Date: at,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func seedDatedAccount(t *testing.T, dbs *store.DBs) {
	t.Helper()
	ctx := context.Background()
	if err := dbs.UpsertAccount(ctx, store.Account{ID: "acct", Address: "acct@example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertFolder(ctx, store.Folder{ID: "f1", AccountID: "acct", Name: "f1", Role: store.RoleInbox}); err != nil {
		t.Fatal(err)
	}
}

func embeddedRefs(t *testing.T, dbs *store.DBs) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	err := dbs.EachEmbedding(context.Background(), []string{"acct"}, "m", func(e store.Embedding) error {
		if e.Dims > 0 {
			got[e.Ref] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A message whose body is only whitespace and which has no subject yields no
// chunks, so nothing was ever stored for it and it stayed first in the queue:
// the queue is newest first and bounded, so enough of them at the head starved
// every older message of its embedding for good.
func TestEmbedWorkerDoesNotStallBehindMailWithNothingToEmbed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	seedDatedAccount(t, dbs)
	day := func(d int) time.Time { return time.Date(2026, 9, d, 9, 0, 0, 0, time.UTC) }
	seedDated(t, dbs, "old", "ck-old", "Invoice", "the domain invoice", day(1))
	for i := range 3 {
		seedDated(t, dbs, fmt.Sprintf("blank%d", i), fmt.Sprintf("ck-blank%d", i), "", "  \n ", day(10+i))
	}
	worker := NewEmbedWorker(dbs, llm.NewGate(dbs), []AccountConfig{
		{ID: "acct", Embedder: stubEmbedder{}, Model: "m", Enabled: true, CapUSD: 5},
	}, WorkerOptions{Batch: 2})

	for range 4 {
		if _, err := worker.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	}
	if !embeddedRefs(t, dbs)["ck-old"] {
		t.Fatal("the older message was never embedded: the queue is stuck behind mail with nothing to embed")
	}
}

// One document the provider refuses must not hold up the rest. The pass used to
// stop at the first provider error, and the next pass met the same document
// first again, so everything behind it waited on it forever (paying a failed
// call each minute).
func TestEmbedWorkerSkipsOneRefusedDocument(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	seedDatedAccount(t, dbs)
	day := func(d int) time.Time { return time.Date(2026, 9, d, 9, 0, 0, 0, time.UTC) }
	seedDated(t, dbs, "poison", "ck-poison", "Weird", "this one is POISON", day(20))
	seedDated(t, dbs, "ok1", "ck-ok1", "One", "the first good message", day(10))
	seedDated(t, dbs, "ok2", "ck-ok2", "Two", "the second good message", day(5))
	worker := NewEmbedWorker(dbs, llm.NewGate(dbs), []AccountConfig{
		{ID: "acct", Embedder: stubEmbedder{reject: "POISON"}, Model: "m", Enabled: true, CapUSD: 5},
	}, WorkerOptions{Batch: 2})

	for range 3 {
		_, _ = worker.RunOnce(ctx)
	}
	got := embeddedRefs(t, dbs)
	if !got["ck-ok1"] || !got["ck-ok2"] {
		t.Fatalf("embedded = %v, want both good messages despite the refused one", got)
	}
}

// countingRejecter refuses any input containing the marker the way a provider
// refuses a document it will not take (422), and counts the calls that carried
// it.
type countingRejecter struct {
	marker string
	status int
	calls  *atomic.Int32
}

func (countingRejecter) Name() string { return "stub" }

func (c countingRejecter) Embed(_ context.Context, _ string, inputs []string) (llm.EmbedResult, error) {
	res := llm.EmbedResult{InputTokens: len(inputs), CostUSD: 0.0001}
	for _, in := range inputs {
		if strings.Contains(in, c.marker) {
			c.calls.Add(1)
			return llm.EmbedResult{}, &llm.StatusError{Provider: "stub", Status: c.status, Body: "no"}
		}
		res.Vectors = append(res.Vectors, llm.Quantise([]float32{0.5, 0, 0, 0}))
	}
	return res, nil
}

// A document the provider refuses every time used to cost a failed call and a
// ledger row on every pass, for as long as it existed. After the fifth refusal
// it is recorded as skipped and leaves the queue.
func TestEmbedWorkerGivesUpOnADocumentAfterFiveRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	seedDatedAccount(t, dbs)
	day := func(d int) time.Time { return time.Date(2026, 9, d, 9, 0, 0, 0, time.UTC) }
	seedDated(t, dbs, "poison", "ck-poison", "Weird", "this one is POISON", day(20))
	seedDated(t, dbs, "ok1", "ck-ok1", "One", "the first good message", day(10))
	var calls atomic.Int32
	worker := NewEmbedWorker(dbs, llm.NewGate(dbs), []AccountConfig{{
		ID: "acct", Model: "m", Enabled: true, CapUSD: 5,
		Embedder: countingRejecter{marker: "POISON", status: 422, calls: &calls},
	}}, WorkerOptions{Batch: 2})

	for range 10 {
		_, _ = worker.RunOnce(ctx)
	}
	if got := calls.Load(); got != maxDocumentRejections {
		t.Errorf("the provider was asked for the refused document %d times, want %d", got, maxDocumentRejections)
	}
	if !embeddedRefs(t, dbs)["ck-ok1"] {
		t.Error("the good message was not embedded")
	}
	pending, err := dbs.PendingBodyRefs(ctx, "acct", "m", 10)
	if err != nil || len(pending) != 0 {
		t.Errorf("pending = %v, %v; want the refused document out of the queue", pending, err)
	}
}

// An outage (503) is not the document's fault, however long it lasts, so it is
// never given up on.
func TestEmbedWorkerNeverGivesUpOnADocumentBecauseOfAnOutage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	seedDatedAccount(t, dbs)
	seedDated(t, dbs, "m1", "ck1", "Subject", "an ordinary POISON-marked body", time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))
	var calls atomic.Int32
	worker := NewEmbedWorker(dbs, llm.NewGate(dbs), []AccountConfig{{
		ID: "acct", Model: "m", Enabled: true, CapUSD: 5,
		Embedder: countingRejecter{marker: "POISON", status: 503, calls: &calls},
	}}, WorkerOptions{Batch: 2})

	for range 8 {
		_, _ = worker.RunOnce(ctx)
	}
	pending, _ := dbs.PendingBodyRefs(ctx, "acct", "m", 10)
	if len(pending) != 1 {
		t.Fatalf("pending = %v, want the document still queued through an outage", pending)
	}
}
