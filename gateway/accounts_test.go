package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	icon := "leaf"
	var got api.Account
	code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1",
		api.AccountProfile{DisplayName: &name, Icon: &icon}, &got, nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Name != "Autumn" || got.Icon != "leaf" || got.Initial != "A" {
		t.Errorf("account = %+v, want renamed Autumn with the leaf icon", got)
	}

	// The change is durable, not just echoed.
	var accounts []api.Account
	getJSON(t, srv.URL+"/api/v1/accounts", &accounts)
	if len(accounts) != 1 || accounts[0].Name != "Autumn" || accounts[0].Icon != "leaf" {
		t.Errorf("accounts = %+v, want the rename to persist", accounts)
	}
}

func TestUpdateAccountProfilePartial(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com", DisplayName: "Autumn"})

	icon := "mail"
	var got api.Account
	if code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1",
		api.AccountProfile{Icon: &icon}, &got, nil); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got.Name != "Autumn" || got.Icon != "mail" {
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

// A browser sends the Host it was pointed at, so a DNS-rebinding page has an
// attacker-chosen name there even though it is "same-origin" with the Origin
// header. Only loopback and the configured names may reach the API.
func TestAPIRejectsUnknownHosts(t *testing.T) {
	t.Parallel()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	handler := New(dbs, "test", testStaticFS()).WithAllowedHosts([]string{"ivy.tail1234.ts.net", "100.64.0.7"}).Handler()

	cases := []struct {
		host string
		want int
	}{
		{"127.0.0.1:8787", http.StatusOK},
		{"localhost:8787", http.StatusOK},
		{"[::1]:8787", http.StatusOK},
		{"ivy.tail1234.ts.net", http.StatusOK},
		{"IVY.tail1234.ts.net:8787", http.StatusOK},
		{"ivy.tail1234.ts.net.:8787", http.StatusOK},
		{"100.64.0.7:8787", http.StatusOK},
		{"evil.example", http.StatusForbidden},
		{"evil.example:8787", http.StatusForbidden},
		{"127.0.0.1.evil.example", http.StatusForbidden},
		{"ivy.tail1234.ts.net.evil.example", http.StatusForbidden},
		{"sub.localhost", http.StatusForbidden},
		{"100.64.0.8", http.StatusForbidden},
		{"", http.StatusForbidden},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
		req.Host = tc.host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("Host %q: status = %d, want %d", tc.host, rec.Code, tc.want)
		}
		if tc.want == http.StatusForbidden && !strings.Contains(rec.Body.String(), `"forbidden"`) {
			t.Errorf("Host %q: body = %q, want the forbidden envelope", tc.host, rec.Body.String())
		}
	}

	// The static build is public and carries nothing of the operator's, and a
	// rebound page's API calls are refused above, so the shell is not guarded.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("static shell with a foreign Host: status = %d, want 200", rec.Code)
	}
}

// Issue #14: the icon is a Lucide name from a closed list, because it is drawn
// on every screen. Anything else is refused, and clearing it still works.
func TestUpdateAccountProfileIconIsAClosedList(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	patch := func(icon string) (int, api.Account) {
		var got api.Account
		code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1", api.AccountProfile{Icon: &icon}, &got, nil)
		return code, got
	}

	for _, bad := range []string{"🌿", "not-an-icon", "Leaf", "leaf ", "<script>", "../leaf"} {
		if bad == "leaf " {
			continue // surrounding space is trimmed, so this is the valid "leaf"
		}
		if code, _ := patch(bad); code != http.StatusBadRequest {
			t.Errorf("icon %q: status = %d, want 400", bad, code)
		}
	}
	if code, got := patch("tree-deciduous"); code != http.StatusOK || got.Icon != "tree-deciduous" {
		t.Errorf("a listed icon: status %d icon %q, want 200 tree-deciduous", code, got.Icon)
	}
	if code, got := patch(""); code != http.StatusOK || got.Icon != "" {
		t.Errorf("clearing: status %d icon %q, want 200 and empty", code, got.Icon)
	}
}

// The smart-features switch must stick: it is stored, and a fresh read shows it.
func TestUpdateAccountSmartPersists(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	if err := dbs.SaveAccountConfig(context.Background(), store.AccountConfig{
		ID: "acct-1", Address: "me@example.com", Username: "me",
		IMAPHost: "imap.example.com", IMAPPort: 993, SMTPHost: "smtp.example.com", SMTPPort: 465, CreatedAt: testNow,
	}); err != nil {
		t.Fatal(err)
	}

	on := true
	var got api.Account
	if code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1", api.AccountProfile{Smart: &on}, &got, nil); code != http.StatusOK || !got.Smart {
		t.Fatalf("turn on: status %d smart %v, want 200 true", code, got.Smart)
	}
	var accounts []api.Account
	getJSON(t, srv.URL+"/api/v1/accounts", &accounts)
	if len(accounts) != 1 || !accounts[0].Smart {
		t.Errorf("after a refresh smart = %+v, want it still on", accounts)
	}

	off := false
	if code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1", api.AccountProfile{Smart: &off}, &got, nil); code != http.StatusOK || got.Smart {
		t.Fatalf("turn off: status %d smart %v, want 200 false", code, got.Smart)
	}
	getJSON(t, srv.URL+"/api/v1/accounts", &accounts)
	if accounts[0].Smart {
		t.Error("after turning it off and refreshing, smart is on")
	}
}

// An account declared in ivy.yaml is changed in the file; the app says so rather
// than pretending to save a switch that would be reset at the next start.
func TestUpdateAccountSmartRefusesAnIvyYAMLAccount(t *testing.T) {
	t.Parallel()
	srv, dbs := newConfiguredServer(t, func(s *Server) { s.WithConfiguredSmart(map[string]bool{"acct-1": false}) })
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})

	on := true
	var e api.Error
	if code := doJSON(t, http.MethodPatch, srv.URL+"/api/v1/accounts/acct-1", api.AccountProfile{Smart: &on}, &e, nil); code != http.StatusConflict || e.Code != "configured_in_yaml" {
		t.Fatalf("status/code = %d/%q, want 409 configured_in_yaml", code, e.Code)
	}
}
