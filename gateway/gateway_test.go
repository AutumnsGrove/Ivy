package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/AutumnsGrove/Ivy/store"
)

func testStaticFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Ivy</title>")},
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dbs, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { dbs.Close() })
	srv := httptest.NewServer(New(dbs, "test-version", testStaticFS()).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

func TestVersion(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	var body versionResponse
	if code := getJSON(t, srv.URL+"/api/v1/version", &body); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body.Version != "test-version" {
		t.Errorf("version = %q, want test-version", body.Version)
	}
}

func TestHealthOK(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	var body healthResponse
	if code := getJSON(t, srv.URL+"/api/v1/health", &body); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Databases["mirror"] != "ok" || body.Databases["state"] != "ok" {
		t.Errorf("databases = %v", body.Databases)
	}
}

func TestHealthUnavailableWhenDatabaseClosed(t *testing.T) {
	t.Parallel()
	dbs, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	srv := httptest.NewServer(New(dbs, "test-version", nil).Handler())
	defer srv.Close()
	dbs.Close()

	var body healthResponse
	if code := getJSON(t, srv.URL+"/api/v1/health", &body); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if body.Status == "ok" {
		t.Errorf("status = %q, want a failure status", body.Status)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	if code := getJSON(t, srv.URL+"/api/v1/nope", nil); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestStaticIndexIsServed(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Ivy") {
		t.Errorf("body = %q, want the index placeholder", body)
	}
}

func TestAPIResponsesVaryOnEncoding(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/version", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept-Encoding", "zstd")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding", resp.Header.Get("Vary"))
	}
}

func TestWrongMethodIs405(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/v1/version", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}
