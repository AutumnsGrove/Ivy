package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AutumnsGrove/Ivy/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dbs, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { dbs.Close() })
	srv := httptest.NewServer(New(dbs, "test-version").Handler())
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
	srv := httptest.NewServer(New(dbs, "test-version").Handler())
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
