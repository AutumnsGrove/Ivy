package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
