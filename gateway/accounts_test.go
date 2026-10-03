package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

const pngMagic = "\x89PNG\r\n\x1a\n"

func doJSON(t *testing.T, method, url string, body any, out any, headers map[string]string) int {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

func TestUpdateAccountProfile(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com", SortOrder: 0})

	name := "Autumn"
	icon := "🌿"
	var got api.Account
	code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1",
		api.AccountProfile{DisplayName: &name, Icon: &icon}, &got, nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Name != "Autumn" || got.Icon != "🌿" || got.Initial != "A" {
		t.Errorf("account = %+v, want renamed Autumn with the leaf icon", got)
	}

	// The change is durable, not just echoed.
	var accounts []api.Account
	getJSON(t, srv.URL+"/api/v1/accounts", &accounts)
	if len(accounts) != 1 || accounts[0].Name != "Autumn" || accounts[0].Icon != "🌿" {
		t.Errorf("accounts = %+v, want the rename to persist", accounts)
	}
}

func TestUpdateAccountProfilePartial(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com", DisplayName: "Autumn"})

	icon := "📬"
	var got api.Account
	if code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1",
		api.AccountProfile{Icon: &icon}, &got, nil); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Name != "Autumn" || got.Icon != "📬" {
		t.Errorf("account = %+v, want the name kept and the icon set", got)
	}
}

func TestUpdateAccountProfileMissing(t *testing.T) {
	t.Parallel()
	srv, _ := newSeededServer(t)
	var body api.Error
	if code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/nope",
		api.AccountProfile{}, &body, nil); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	if body.Code != "not_found" {
		t.Errorf("code = %q, want not_found", body.Code)
	}
}

func TestUpdateAccountProfileRejectsOverlong(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})

	long := strings.Repeat("a", maxDisplayNameRunes+1)
	var body api.Error
	if code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1",
		api.AccountProfile{DisplayName: &long}, &body, nil); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if body.Code != "bad_request" {
		t.Errorf("code = %q, want bad_request", body.Code)
	}
}

func TestAccountPhotoRoundTrip(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	photo := []byte(pngMagic + "pretend png bytes")

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut,
		srv.URL+"/api/v1/accounts/acct-1/photo", bytes.NewReader(photo))
	req.Header.Set("Content-Type", "image/png")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT photo: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}

	var accounts []api.Account
	getJSON(t, srv.URL+"/api/v1/accounts", &accounts)
	if len(accounts) != 1 || !accounts[0].Photo {
		t.Errorf("accounts = %+v, want photo true", accounts)
	}

	got, err := http.Get(srv.URL + "/api/v1/accounts/acct-1/photo")
	if err != nil {
		t.Fatalf("GET photo: %v", err)
	}
	defer got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", got.StatusCode)
	}
	if ct := got.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want image/png", ct)
	}
	if got.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("cache-control = %q, want no-store", got.Header.Get("Cache-Control"))
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(got.Body); err != nil {
		t.Fatalf("read photo: %v", err)
	}
	if !bytes.Equal(body.Bytes(), photo) {
		t.Errorf("photo bytes differ: %v", body.Bytes())
	}

	// DELETE clears it and the account reports no photo.
	del, err := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		srv.URL+"/api/v1/accounts/acct-1/photo", nil)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	dres, err := http.DefaultClient.Do(del)
	if err != nil {
		t.Fatalf("DELETE photo: %v", err)
	}
	dres.Body.Close()
	if dres.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", dres.StatusCode)
	}
	missing, _ := http.Get(srv.URL + "/api/v1/accounts/acct-1/photo")
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", missing.StatusCode)
	}
}

func TestAccountPhotoRejectsNonImage(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})

	put := func(contentType, body string) int {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut,
			srv.URL+"/api/v1/accounts/acct-1/photo", strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT photo: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// SVG can script when served same-origin, and a text payload is not an image.
	if code := put("image/svg+xml", "<svg xmlns=\"http://www.w3.org/2000/svg\"/>"); code != http.StatusBadRequest {
		t.Errorf("svg status = %d, want 400", code)
	}
	if code := put("text/html", "<script>alert(1)</script>"); code != http.StatusBadRequest {
		t.Errorf("html status = %d, want 400", code)
	}
	if code := put("image/png", strings.Repeat("x", maxAccountPhotoBytes+1)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized status = %d, want 413", code)
	}
}

func TestOriginGuard(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	name := "x"

	// A cross-site write is refused before the handler runs.
	var body api.Error
	code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1",
		api.AccountProfile{DisplayName: &name}, &body, map[string]string{"Origin": "https://evil.example"})
	if code != http.StatusForbidden {
		t.Fatalf("cross-site PATCH = %d, want 403", code)
	}

	// A same-origin write passes.
	code = doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1",
		api.AccountProfile{DisplayName: &name}, nil, map[string]string{"Origin": "http://" + strings.TrimPrefix(srv.URL, "http://")})
	if code != http.StatusOK {
		t.Fatalf("same-origin PATCH = %d, want 200", code)
	}

	// A read from another site is allowed; only writes are at risk.
	var accounts []api.Account
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/v1/accounts", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cross-site GET = %d, want 200", resp.StatusCode)
	}
	_ = json.NewDecoder(resp.Body).Decode(&accounts)
}
