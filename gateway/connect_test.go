package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// fakeConnector records what the handler passed it and answers with a canned
// result, so these tests cover the HTTP contract (validation, status codes, no
// password in any response). The real probe, file and worker live in
// internal/accountsvc and are tested there against the fake mail world.
type fakeConnector struct {
	err                     error
	id                      string
	gotAddress, gotPassword string
	gotSmart                bool
	gotUpdateID             string
	calls                   int
}

func (f *fakeConnector) Connect(_ context.Context, address, password string, smart bool) (string, error) {
	f.calls++
	f.gotAddress, f.gotPassword, f.gotSmart = address, password, smart
	return f.id, f.err
}

func (f *fakeConnector) UpdatePassword(_ context.Context, id, password string) error {
	f.calls++
	f.gotUpdateID, f.gotPassword = id, password
	return f.err
}

func newConnectServer(t *testing.T, c AccountConnector) *httptest.Server {
	t.Helper()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { dbs.Close() })
	srv := New(dbs, "test-version", testStaticFS())
	if c != nil {
		srv = srv.WithAccountConnector(c)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestConnectAccountSucceeds(t *testing.T) {
	t.Parallel()
	fc := &fakeConnector{id: "purelymail"}
	ts := newConnectServer(t, fc)

	var out api.ConnectedAccount
	status := doJSON(t, http.MethodPost, ts.URL+"/api/v1/accounts",
		map[string]any{"address": "  me@grove.test ", "password": "hunter2", "smart": true}, &out, nil)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}
	if out.Id != "purelymail" {
		t.Errorf("id = %q", out.Id)
	}
	if fc.gotAddress != "me@grove.test" || fc.gotPassword != "hunter2" || !fc.gotSmart {
		t.Errorf("connector got %q / %q / %v", fc.gotAddress, fc.gotPassword, fc.gotSmart)
	}
}

func TestConnectAccountMapsFailuresToTheContract(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"wrong password": {&ConnectError{Code: "auth_failed"}, http.StatusUnprocessableEntity, "auth_failed"},
		"unreachable":    {&ConnectError{Code: "unreachable"}, http.StatusBadGateway, "unreachable"},
		"other failure":  {&ConnectError{Code: "error"}, http.StatusBadGateway, "connect_failed"},
		"duplicate":      {ErrAlreadyConnected, http.StatusConflict, "already_connected"},
		"unexpected":     {context.DeadlineExceeded, http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ts := newConnectServer(t, &fakeConnector{err: tc.err})
			var out api.Error
			status := doJSON(t, http.MethodPost, ts.URL+"/api/v1/accounts",
				map[string]any{"address": "me@grove.test", "password": "hunter2"}, &out, nil)
			if status != tc.status || out.Code != tc.code {
				t.Errorf("got %d %q, want %d %q", status, out.Code, tc.status, tc.code)
			}
		})
	}
}

func TestConnectAccountValidatesBeforeCallingTheConnector(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]map[string]any{
		"no address":        {"password": "x"},
		"not an address":    {"address": "nope", "password": "x"},
		"display name form": {"address": "Me <me@grove.test>", "password": "x"},
		"too long address":  {"address": strings.Repeat("a", 250) + "@grove.test", "password": "x"},
		"no password":       {"address": "me@grove.test"},
		"empty password":    {"address": "me@grove.test", "password": ""},
		"huge password":     {"address": "me@grove.test", "password": strings.Repeat("p", 1025)},
		"newline password":  {"address": "me@grove.test", "password": "a\nb"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fc := &fakeConnector{id: "purelymail"}
			ts := newConnectServer(t, fc)
			var out api.Error
			status := doJSON(t, http.MethodPost, ts.URL+"/api/v1/accounts", body, &out, nil)
			if status != http.StatusBadRequest || out.Code != "bad_request" {
				t.Errorf("got %d %q, want 400 bad_request", status, out.Code)
			}
			if fc.calls != 0 {
				t.Error("the connector was called with invalid input")
			}
		})
	}
}

func TestConnectAccountRejectsAnOversizedBody(t *testing.T) {
	t.Parallel()
	fc := &fakeConnector{id: "purelymail"}
	ts := newConnectServer(t, fc)
	huge := map[string]any{"address": "me@grove.test", "password": "x", "padding": strings.Repeat("z", 64<<10)}
	if status := doJSON(t, http.MethodPost, ts.URL+"/api/v1/accounts", huge, nil, nil); status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if fc.calls != 0 {
		t.Error("the connector was called for an oversized body")
	}
}

func TestConnectAccountIsUnavailableWithoutAConnector(t *testing.T) {
	t.Parallel()
	ts := newConnectServer(t, nil)
	var out api.Error
	status := doJSON(t, http.MethodPost, ts.URL+"/api/v1/accounts",
		map[string]any{"address": "me@grove.test", "password": "hunter2"}, &out, nil)
	if status != http.StatusServiceUnavailable || out.Code != "connect_unavailable" {
		t.Errorf("got %d %q, want 503 connect_unavailable", status, out.Code)
	}
}

// The password is a request-only field: nothing the server says back may carry
// it, even in an error.
func TestNoResponseEchoesThePassword(t *testing.T) {
	t.Parallel()
	const pw = "s3cret-needle-9"
	for name, err := range map[string]error{"ok": nil, "bad": &ConnectError{Code: "auth_failed"}, "boom": context.Canceled} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ts := newConnectServer(t, &fakeConnector{id: "purelymail", err: err})
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/api/v1/accounts",
				strings.NewReader(`{"address":"me@grove.test","password":"`+pw+`"}`))
			req.Header.Set("Content-Type", "application/json")
			resp, rerr := http.DefaultClient.Do(req)
			if rerr != nil {
				t.Fatal(rerr)
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			if strings.Contains(string(raw), pw) {
				t.Errorf("response echoes the password: %s", raw)
			}
		})
	}
}

func TestUpdateAccountPassword(t *testing.T) {
	t.Parallel()
	fc := &fakeConnector{}
	ts := newConnectServer(t, fc)

	status := doJSON(t, http.MethodPut, ts.URL+"/api/v1/accounts/purelymail/password",
		map[string]any{"password": "newpass"}, nil, nil)
	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
	if fc.gotUpdateID != "purelymail" || fc.gotPassword != "newpass" {
		t.Errorf("connector got %q / %q", fc.gotUpdateID, fc.gotPassword)
	}
}

func TestUpdateAccountPasswordFailures(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		err    error
		body   map[string]any
		status int
		code   string
	}{
		"wrong":       {&ConnectError{Code: "auth_failed"}, map[string]any{"password": "x"}, http.StatusUnprocessableEntity, "auth_failed"},
		"unreachable": {&ConnectError{Code: "unreachable"}, map[string]any{"password": "x"}, http.StatusBadGateway, "unreachable"},
		"unknown":     {store.ErrNotFound, map[string]any{"password": "x"}, http.StatusNotFound, "not_found"},
		"empty":       {nil, map[string]any{"password": ""}, http.StatusBadRequest, "bad_request"},
		"newline":     {nil, map[string]any{"password": "a\nb"}, http.StatusBadRequest, "bad_request"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ts := newConnectServer(t, &fakeConnector{err: tc.err})
			var out api.Error
			status := doJSON(t, http.MethodPut, ts.URL+"/api/v1/accounts/purelymail/password", tc.body, &out, nil)
			if status != tc.status || out.Code != tc.code {
				t.Errorf("got %d %q, want %d %q", status, out.Code, tc.status, tc.code)
			}
		})
	}
}

// A page on another site open in the same browser must not be able to post here.
func TestConnectAccountRefusesAForeignOrigin(t *testing.T) {
	t.Parallel()
	fc := &fakeConnector{id: "purelymail"}
	ts := newConnectServer(t, fc)
	status := doJSON(t, http.MethodPost, ts.URL+"/api/v1/accounts",
		map[string]any{"address": "me@grove.test", "password": "hunter2"}, nil,
		map[string]string{"Origin": "https://evil.example"})
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
	if fc.calls != 0 {
		t.Error("the connector was called for a foreign origin")
	}
}
