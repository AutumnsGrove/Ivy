package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

func seedSearchMail(t *testing.T, dbs *store.DBs) {
	t.Helper()
	ctx := context.Background()
	if err := dbs.UpsertAccount(ctx, store.Account{ID: "a1", Address: "a1@example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertFolder(ctx, store.Folder{ID: "f1", AccountID: "a1", Name: "INBOX", Role: store.RoleInbox}); err != nil {
		t.Fatal(err)
	}
	for i, m := range []struct{ id, key, subject, body string }{
		{"m1", "ck1", "Domain renewal", "Your domain invoice is attached"},
		{"m2", "ck2", "Lunch plans", "See you at noon"},
	} {
		if err := dbs.UpsertMessage(ctx, store.Message{
			ID: m.id, AccountID: "a1", FolderID: "f1", UID: uint32(i + 1),
			ContentKey: m.key, Subject: m.subject, BodyText: m.body, Date: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := dbs.ReindexContent(ctx, "a1", m.key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchReturnsKeywordHits(t *testing.T) {
	t.Parallel()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	seedSearchMail(t, dbs)
	srv := httptest.NewServer(New(dbs, "test", testStaticFS()).WithSearch(nil).Handler())
	t.Cleanup(srv.Close)

	var out api.SearchResults
	if code := getJSON(t, srv.URL+"/api/v1/search?q=invoice", &out); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if out.Total != 1 || len(out.Hits) != 1 || out.Hits[0].Id != "m1" {
		t.Fatalf("hits = %+v, want m1 only", out.Hits)
	}
}

func TestSearchRejectsAnEmptyQuery(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	if code := getJSON(t, srv.URL+"/api/v1/search?q=%20", nil); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestSearchExcludesHiddenMail(t *testing.T) {
	t.Parallel()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	seedSearchMail(t, dbs)
	if err := dbs.DisableMessage(context.Background(), "m1", "server_removed", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(dbs, "test", testStaticFS()).WithSearch(nil).Handler())
	t.Cleanup(srv.Close)

	var out api.SearchResults
	getJSON(t, srv.URL+"/api/v1/search?q=invoice", &out)
	if out.Total != 0 {
		t.Fatalf("hidden mail returned: %+v", out.Hits)
	}
}

// A provider that cannot embed must never break search: it falls back to
// keyword-only and says so quietly.
type failingEmbedder struct{}

func (failingEmbedder) EmbedQuery(context.Context, string, string) (llm.Vector, string, error) {
	return llm.Vector{}, "", errors.New("provider down")
}

func TestSearchFallsBackWhenTheQueryCannotBeEmbedded(t *testing.T) {
	t.Parallel()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	seedSearchMail(t, dbs)
	srv := httptest.NewServer(New(dbs, "test", testStaticFS()).WithSearch(failingEmbedder{}).Handler())
	t.Cleanup(srv.Close)

	var out api.SearchResults
	if code := getJSON(t, srv.URL+"/api/v1/search?q=lunch&account_id=a1", &out); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if out.Total != 1 || out.Hits[0].Subject != "Lunch plans" {
		body, _ := json.Marshal(out)
		t.Fatalf("fallback search = %s, want the keyword hit", body)
	}
}

// fixedEmbedder answers every query with one vector, as a working provider
// would, and counts the (paid) calls it was asked for.
type fixedEmbedder struct {
	vec   llm.Vector
	model string
	calls atomic.Int32
}

func (f *fixedEmbedder) EmbedQuery(context.Context, string, string) (llm.Vector, string, error) {
	f.calls.Add(1)
	return f.vec, f.model, nil
}

func storeBodyVector(t *testing.T, dbs *store.DBs, account, key, model string, vals []float32) {
	t.Helper()
	v := llm.Quantise(vals)
	if err := dbs.UpsertEmbeddings(context.Background(), []store.Embedding{{
		AccountID: account, Ref: key, Kind: store.ExtractKindBody, Model: model,
		Dims: v.Dims, Scale: v.Scale, Norm: v.Norm, Vector: v.Encode(),
	}}); err != nil {
		t.Fatal(err)
	}
}

// The search screen always asks for "All accounts" and sends no account_id. The
// handler only embedded the query when an account was named, so meaning-based
// search never ran from the screen at all, while the worker kept paying to embed
// every message for it.
func TestSearchWithoutAnAccountStillSearchesByMeaning(t *testing.T) {
	t.Parallel()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	seedSearchMail(t, dbs)
	storeBodyVector(t, dbs, "a1", "ck1", "m", []float32{0, 0.5, 0, 0})
	storeBodyVector(t, dbs, "a1", "ck2", "m", []float32{0.5, 0, 0, 0})
	emb := &fixedEmbedder{vec: llm.Quantise([]float32{0.5, 0, 0, 0}), model: "m"}
	srv := httptest.NewServer(New(dbs, "test", testStaticFS()).WithSearch(emb).Handler())
	t.Cleanup(srv.Close)

	// No word of the query is in either message: only meaning can find it.
	var out api.SearchResults
	if code := getJSON(t, srv.URL+"/api/v1/search?q=midday+meal", &out); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(out.Hits) == 0 || out.Hits[0].Id != "m2" {
		body, _ := json.Marshal(out)
		t.Fatalf("hits = %s, want the lunch message first by meaning", body)
	}
	if got := emb.calls.Load(); got != 1 {
		t.Errorf("%d query embeddings, want exactly 1 paid call", got)
	}
}

// An attachment is embedded under its content hash, shared by every message
// that carries it. A vector hit on it is a hit on those messages, but the hit
// was handed on with the hash as if it were a message's content key, which
// resolves to nothing, so everything paid for attachment meaning was dropped.
func TestSearchByMeaningFindsTheMessageCarryingAnAttachment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	seedSearchMail(t, dbs)
	if err := dbs.ReplaceMessageAttachments(ctx, "m2", []store.Attachment{{
		ID: "att1", MessageID: "m2", Filename: "menu.pdf", MIMEType: "application/pdf",
		Size: 10, ContentHash: "hash-menu", StoragePath: "1",
	}}); err != nil {
		t.Fatal(err)
	}
	v := llm.Quantise([]float32{0.5, 0, 0, 0})
	if err := dbs.UpsertEmbeddings(ctx, []store.Embedding{{
		AccountID: "a1", Ref: "hash-menu", Kind: store.ExtractKindAttachment, Model: "m",
		Dims: v.Dims, Scale: v.Scale, Norm: v.Norm, Vector: v.Encode(),
	}}); err != nil {
		t.Fatal(err)
	}
	emb := &fixedEmbedder{vec: v, model: "m"}
	srv := httptest.NewServer(New(dbs, "test", testStaticFS()).WithSearch(emb).Handler())
	t.Cleanup(srv.Close)

	var out api.SearchResults
	if code := getJSON(t, srv.URL+"/api/v1/search?q=midday+meal", &out); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(out.Hits) != 1 || out.Hits[0].Id != "m2" {
		body, _ := json.Marshal(out)
		t.Fatalf("hits = %s, want the message that carries the attachment", body)
	}
}

// hangingEmbedder is a provider that accepts the request and never answers.
type hangingEmbedder struct{}

func (hangingEmbedder) EmbedQuery(ctx context.Context, _, _ string) (llm.Vector, string, error) {
	<-ctx.Done()
	return llm.Vector{}, "", ctx.Err()
}

// "A provider outage is not an error; search quietly falls back to keyword" only
// holds if the fallback arrives in time. With no deadline on the query
// embedding, a provider that hangs held every search open for the HTTP client's
// full minute.
func TestSearchFallsBackQuicklyWhenTheProviderHangs(t *testing.T) {
	old := semanticTimeout
	semanticTimeout = 100 * time.Millisecond
	t.Cleanup(func() { semanticTimeout = old })

	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	seedSearchMail(t, dbs)
	srv := httptest.NewServer(New(dbs, "test", testStaticFS()).WithSearch(hangingEmbedder{}).Handler())
	t.Cleanup(srv.Close)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(srv.URL + "/api/v1/search?q=invoice")
	if err != nil {
		t.Fatalf("search did not answer while the provider hung: %v", err)
	}
	defer resp.Body.Close()
	var out api.SearchResults
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) != 1 || out.Hits[0].Id != "m1" {
		t.Fatalf("hits = %+v, want the keyword hit", out.Hits)
	}
}

// The query is sent to a paid provider verbatim, so it has a documented maximum
// instead of whatever the URL allows.
func TestSearchRejectsAnOversizeQuery(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	long := strings.Repeat("a", maxSearchQueryBytes+1)
	if code := getJSON(t, srv.URL+"/api/v1/search?q="+long, nil); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a %d-byte query", code, len(long))
	}
}
