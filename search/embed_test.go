package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// hostedAccount is the one account these tests embed for, using the hosted
// provider kind and a model name.
var hostedAccount = AccountConfig{ID: "acct", Provider: llm.ProviderOpenRouter, Model: "m"}

// optIn stores the account's smart-features switch the way the app does. The gate
// reads it from there on every call; no request or worker setting can carry it.
func optIn(t *testing.T, dbs *store.DBs, id string, on bool) {
	t.Helper()
	if err := dbs.SaveAccountConfig(context.Background(), store.AccountConfig{
		ID: id, Address: id + "@example.test", Username: id,
		IMAPHost: "imap.example.test", IMAPPort: 993, SMTPHost: "smtp.example.test", SMTPPort: 465,
		LLMEnabled: on, EmbedProvider: "openrouter", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("save account config: %v", err)
	}
}

// gateAt builds a gate whose hosted provider is the server at base.
func gateAt(dbs *store.DBs, base string) *llm.Gate {
	return llm.NewGate(dbs, llm.WithProviders(llm.ProviderConfig{OpenRouterBase: base, APIKey: "k"}))
}

// markerServer is a hosted embeddings provider that answers every input with one
// vector, except that a request containing marker is answered with status, the
// way a provider refuses a single document it will not take (or is down). calls
// counts the requests that carried the marker.
func markerServer(t *testing.T, marker string, status int, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var data []string
		for i, in := range req.Input {
			if marker != "" && strings.Contains(in, marker) {
				calls.Add(1)
				w.WriteHeader(status)
				return
			}
			data = append(data, fmt.Sprintf(`{"index":%d,"embedding":[0.5,0,0,0]}`, i))
		}
		_, _ = fmt.Fprintf(w, `{"data":[%s],"usage":{"prompt_tokens":%d,"cost":0.0001}}`, strings.Join(data, ","), len(req.Input))
	}))
	t.Cleanup(srv.Close)
	return srv
}

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

// The queue depth on the health page is the worker's own: per account, the
// documents it has yet to embed, falling as it works.
func TestEmbedWorkerReportsItsBacklog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "acct", true)
	worker := NewEmbedWorker(dbs, gateAt(dbs, w.OpenRouterURL()), []AccountConfig{
		hostedAccount, {ID: "no-provider"},
	}, WorkerOptions{Batch: 32})

	seedMail(t, dbs, "acct", "f1", "m1", "ck1", "Domain renewal", "Your domain invoice is attached")
	// seedMail takes the UID from the id's length, so the ids must differ in length.
	seedMail(t, dbs, "acct", "f1", "msg-two", "ck2", "Lunch", "Are you free on Friday")

	got, err := worker.Backlog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got["acct"] != 2 {
		t.Errorf("backlog = %v, want 2 for acct", got)
	}
	if _, listed := got["no-provider"]; listed {
		t.Errorf("an account with no provider is in the backlog: %v", got)
	}

	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ = worker.Backlog(ctx); got["acct"] != 0 {
		t.Errorf("backlog after a pass = %v, want 0", got)
	}
}

func TestEmbedWorkerEmbedsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "acct", true)
	gate := gateAt(dbs, w.OpenRouterURL())
	worker := NewEmbedWorker(dbs, gate, []AccountConfig{hostedAccount}, WorkerOptions{Batch: 32})

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
	optIn(t, dbs, "acct", false)
	gate := gateAt(dbs, w.OpenRouterURL())
	worker := NewEmbedWorker(dbs, gate, []AccountConfig{hostedAccount}, WorkerOptions{})
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
	optIn(t, dbs, "acct", true)
	gate := gateAt(dbs, w.OpenRouterURL())
	worker := NewEmbedWorker(dbs, gate, []AccountConfig{hostedAccount}, WorkerOptions{})
	seedMail(t, dbs, "acct", "f1", "m1", "ck1", "Domain renewal", "The domain invoice renews in March")
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	query, err := gate.Embed(ctx, llm.EmbedRequest{
		Provider: llm.ProviderOpenRouter, AccountID: "acct", Model: "m", Feature: "search",
		Inputs: []string{"domain invoice renewal"}, ContentKeys: []string{"acct:query"},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(dbs, gate)
	hits, err := svc.VectorSearch(ctx, query[0], []string{"acct"}, "m", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ContentKey != "ck1" {
		t.Fatalf("hits = %+v, want ck1 first", hits)
	}
}

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
	optIn(t, dbs, "acct", true)
	var calls atomic.Int32
	srv := markerServer(t, "", 0, &calls)
	worker := NewEmbedWorker(dbs, gateAt(dbs, srv.URL), []AccountConfig{hostedAccount}, WorkerOptions{Batch: 2})

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
	optIn(t, dbs, "acct", true)
	var calls atomic.Int32
	srv := markerServer(t, "POISON", http.StatusInternalServerError, &calls)
	worker := NewEmbedWorker(dbs, gateAt(dbs, srv.URL), []AccountConfig{hostedAccount}, WorkerOptions{Batch: 2})

	for range 3 {
		_, _ = worker.RunOnce(ctx)
	}
	got := embeddedRefs(t, dbs)
	if !got["ck-ok1"] || !got["ck-ok2"] {
		t.Fatalf("embedded = %v, want both good messages despite the refused one", got)
	}
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
	optIn(t, dbs, "acct", true)
	var calls atomic.Int32
	srv := markerServer(t, "POISON", http.StatusUnprocessableEntity, &calls)
	worker := NewEmbedWorker(dbs, gateAt(dbs, srv.URL), []AccountConfig{hostedAccount}, WorkerOptions{Batch: 2})

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
	optIn(t, dbs, "acct", true)
	var calls atomic.Int32
	srv := markerServer(t, "POISON", http.StatusServiceUnavailable, &calls)
	worker := NewEmbedWorker(dbs, gateAt(dbs, srv.URL), []AccountConfig{hostedAccount}, WorkerOptions{Batch: 2})

	for range 8 {
		_, _ = worker.RunOnce(ctx)
	}
	pending, _ := dbs.PendingBodyRefs(ctx, "acct", "m", 10)
	if len(pending) != 1 {
		t.Fatalf("pending = %v, want the document still queued through an outage", pending)
	}
}

// Smart features are switched from the app while Ivy runs. Turning them off must
// stop remote calls at once, not at the next restart, and turning them on must
// start them without one (for an account that already has a provider).
func TestEmbedWorkerFollowsTheSwitchWhileRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "acct", true)
	worker := NewEmbedWorker(dbs, gateAt(dbs, w.OpenRouterURL()), []AccountConfig{hostedAccount}, WorkerOptions{})
	seedMail(t, dbs, "acct", "f1", "m1", "ck1", "Subject", "body")

	if err := dbs.SetAccountSmart(ctx, "acct", false); err != nil {
		t.Fatal(err)
	}
	if n, err := worker.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("pass with smart off = %d, %v; want 0", n, err)
	}
	if len(w.Calls()) != 0 {
		t.Fatal("the hosted provider was called after smart features were turned off")
	}

	if err := dbs.SetAccountSmart(ctx, "acct", true); err != nil {
		t.Fatal(err)
	}
	if n, err := worker.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("pass after turning it back on = %d, %v; want 1", n, err)
	}
}
