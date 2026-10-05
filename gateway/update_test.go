package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/store"
	"github.com/AutumnsGrove/Ivy/update"
)

// fakeUpdater is a controllable Updater: Request can be made to block (so a
// second click can race it) or fail, and Pending/Result answer immediately so
// the watcher-poll loop in the handler ends fast under test.
type fakeUpdater struct {
	mu      sync.Mutex
	target  string
	reqErr  error
	result  *update.Result
	pending bool

	started chan struct{}
	release chan struct{}
}

func (f *fakeUpdater) Request(ctx context.Context) (string, error) {
	if f.started != nil {
		close(f.started)
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if f.reqErr != nil {
		return "", f.reqErr
	}
	return f.target, nil
}

func (f *fakeUpdater) Pending() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending
}

func (f *fakeUpdater) Result() *update.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.result
}

func newUpdateServer(t *testing.T, u Updater) (*httptest.Server, *events.Hub, *Server) {
	t.Helper()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { dbs.Close() })
	hub := events.New()
	srv := New(dbs, "test-version", testStaticFS()).WithEvents(hub).WithUpdate(t.Context(), u)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, hub, srv
}

func waitUpdateDone(t *testing.T, url string) updateStatusResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var body updateStatusResponse
		if code := getJSON(t, url+"/api/v1/update", &body); code != http.StatusOK {
			t.Fatalf("GET /api/v1/update status = %d, want 200", code)
		}
		if body.Done {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("update never finished: %+v", body)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func postUpdate(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Post(url+"/api/v1/update", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/v1/update: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPostUpdateRunsAndReportsSuccess(t *testing.T) {
	t.Parallel()
	up := &fakeUpdater{
		target: "ghcr.io/autumnsgrove/ivy@sha256:" + strings.Repeat("a", 64),
		result: &update.Result{Status: "ok", FinishedAt: "2026-10-06T00:00:00Z"},
	}
	ts, hub, _ := newUpdateServer(t, up)
	sub, err := hub.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(sub.Close)

	resp := postUpdate(t, ts.URL)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST status = %d, want 202", resp.StatusCode)
	}

	body := waitUpdateDone(t, ts.URL)
	if !body.Success {
		t.Errorf("success = false, want true (%+v)", body)
	}
	if body.Target != up.target {
		t.Errorf("target = %q, want %q", body.Target, up.target)
	}
	if body.Watcher == nil || body.Watcher.Status != "ok" {
		t.Errorf("watcher = %+v, want the ok result", body.Watcher)
	}

	// The client is told over SSE that something changed. The hub coalesces
	// identical pending hints, so one update.state is enough: the client refetches
	// the status endpoint, which is where the outcome lives.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		e, err := sub.Next(ctx)
		if err != nil {
			t.Fatalf("waiting for an update.state hint: %v", err)
		}
		if e.Type == events.UpdateState {
			break
		}
	}
}

func TestPostUpdateWithoutUpdaterIsUnavailable(t *testing.T) {
	t.Parallel()
	ts, _, _ := newUpdateServer(t, nil)
	resp := postUpdate(t, ts.URL)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	var body api.Error
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "update_unavailable" {
		t.Errorf("code = %q, want update_unavailable", body.Code)
	}
}

func TestPostUpdateTwiceIsConflict(t *testing.T) {
	t.Parallel()
	up := &fakeUpdater{
		target:  "ghcr.io/autumnsgrove/ivy@sha256:" + strings.Repeat("a", 64),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	ts, _, _ := newUpdateServer(t, up)

	if resp := postUpdate(t, ts.URL); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("first POST status = %d, want 202", resp.StatusCode)
	}
	<-up.started
	resp := postUpdate(t, ts.URL)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second POST status = %d, want 409", resp.StatusCode)
	}
	var body api.Error
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "update_running" {
		t.Errorf("code = %q, want update_running", body.Code)
	}
	close(up.release)
	_ = waitUpdateDone(t, ts.URL)
}

func TestPostUpdateReportsResolveFailure(t *testing.T) {
	t.Parallel()
	up := &fakeUpdater{reqErr: errors.New("registry is down")}
	ts, _, _ := newUpdateServer(t, up)

	postUpdate(t, ts.URL)
	body := waitUpdateDone(t, ts.URL)
	if body.Success {
		t.Error("success = true, want false")
	}
	if !strings.Contains(body.Error, "registry is down") {
		t.Errorf("error = %q, want the resolve failure", body.Error)
	}
}

func TestGetUpdateStatusWithoutUpdater(t *testing.T) {
	t.Parallel()
	ts, _, _ := newUpdateServer(t, nil)
	var body updateStatusResponse
	if code := getJSON(t, ts.URL+"/api/v1/update", &body); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !body.Unavailable {
		t.Errorf("unavailable = %v, want true with no updater", body.Unavailable)
	}
}
