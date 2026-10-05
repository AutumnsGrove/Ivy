package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGHCR stands in for the two-step OCI exchange: an anonymous pull token,
// then a manifest HEAD whose Docker-Content-Digest is the answer. Each status
// is settable so a test can force either step to fail on its own.
func fakeGHCR(t *testing.T, tokenStatus, manifestStatus int, digest string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		if tokenStatus != http.StatusOK {
			w.WriteHeader(tokenStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"fake-anonymous-token"}`))
	})
	mux.HandleFunc("/v2/autumnsgrove/ivy/manifests/latest", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer fake-anonymous-token" {
			t.Errorf("manifest Authorization = %q, want the bearer token", got)
		}
		if manifestStatus != http.StatusOK {
			w.WriteHeader(manifestStatus)
			return
		}
		if digest != "" {
			w.Header().Set("Docker-Content-Digest", digest)
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// withGHCR points the package at a fake registry for the duration of a test.
func withGHCR(t *testing.T, url string) {
	t.Helper()
	original := ghcrBaseURL
	ghcrBaseURL = url
	t.Cleanup(func() { ghcrBaseURL = original })
}

// fakeActions serves GitHub's workflow-runs listing. statusFn is called once
// per poll so a test can return "in_progress" then "completed".
func fakeActions(t *testing.T, statusFn func() (int, string)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/autumnsgrove/ivy/actions/workflows/docker-publish.yml/runs", func(w http.ResponseWriter, _ *http.Request) {
		code, status := statusFn()
		if code != http.StatusOK {
			w.WriteHeader(code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{
			"workflow_runs": []map[string]string{{"status": status}},
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func withActions(t *testing.T, url string) {
	t.Helper()
	original := githubBaseURL
	githubBaseURL = url
	t.Cleanup(func() { githubBaseURL = original })
}

func withTimings(t *testing.T, poll, max time.Duration) {
	t.Helper()
	origPoll, origMax := ciPollInterval, ciMaxWait
	ciPollInterval, ciMaxWait = poll, max
	t.Cleanup(func() { ciPollInterval, ciMaxWait = origPoll, origMax })
}

func TestResolveDigestHappyPath(t *testing.T) {
	srv := fakeGHCR(t, http.StatusOK, http.StatusOK, "sha256:"+strings.Repeat("a", 64))
	withGHCR(t, srv.URL)

	digest, err := ResolveDigest(context.Background(), DefaultRepo, DefaultTag)
	if err != nil {
		t.Fatalf("ResolveDigest: %v", err)
	}
	if want := "sha256:" + strings.Repeat("a", 64); digest != want {
		t.Errorf("digest = %q, want %q", digest, want)
	}
}

func TestResolveDigestTokenFailure(t *testing.T) {
	srv := fakeGHCR(t, http.StatusUnauthorized, http.StatusOK, "sha256:abc")
	withGHCR(t, srv.URL)

	if _, err := ResolveDigest(context.Background(), DefaultRepo, DefaultTag); err == nil {
		t.Fatal("ResolveDigest error = nil, want a failure when the token exchange 401s")
	}
}

func TestResolveDigestManifestFailure(t *testing.T) {
	srv := fakeGHCR(t, http.StatusOK, http.StatusNotFound, "sha256:abc")
	withGHCR(t, srv.URL)

	if _, err := ResolveDigest(context.Background(), DefaultRepo, DefaultTag); err == nil {
		t.Fatal("ResolveDigest error = nil, want a failure when the manifest 404s")
	}
}

// TestResolveDigestMissingHeader guards the empty-digest case: without it the
// watcher would be handed a target ending in "@", which docker rejects only
// after the UI had already reported success.
func TestResolveDigestMissingHeader(t *testing.T) {
	srv := fakeGHCR(t, http.StatusOK, http.StatusOK, "")
	withGHCR(t, srv.URL)

	if _, err := ResolveDigest(context.Background(), DefaultRepo, DefaultTag); err == nil {
		t.Fatal("ResolveDigest error = nil, want a failure when Docker-Content-Digest is absent")
	}
}

func TestWaitForPublishWorkflowAlreadyCompleteIsInstant(t *testing.T) {
	srv := fakeActions(t, func() (int, string) { return http.StatusOK, "completed" })
	withActions(t, srv.URL)
	withTimings(t, time.Millisecond, time.Second)

	start := time.Now()
	waitForPublishWorkflow(context.Background(), DefaultRepo, "")
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("wait took %v, want near-instant for a completed run", elapsed)
	}
}

func TestWaitForPublishWorkflowBlocksWhileInProgress(t *testing.T) {
	var calls atomic.Int32
	srv := fakeActions(t, func() (int, string) {
		if calls.Add(1) < 3 {
			return http.StatusOK, "in_progress"
		}
		return http.StatusOK, "completed"
	})
	withActions(t, srv.URL)
	withTimings(t, time.Millisecond, time.Second)

	waitForPublishWorkflow(context.Background(), DefaultRepo, "")
	if got := calls.Load(); got < 3 {
		t.Errorf("polled %d times, want at least 3 (it kept waiting)", got)
	}
}

func TestWaitForPublishWorkflowAPIErrorReturnsImmediately(t *testing.T) {
	srv := fakeActions(t, func() (int, string) { return http.StatusInternalServerError, "" })
	withActions(t, srv.URL)
	withTimings(t, time.Millisecond, time.Second)

	start := time.Now()
	waitForPublishWorkflow(context.Background(), DefaultRepo, "")
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("wait took %v, want an immediate best-effort fallback on an API error", elapsed)
	}
}

func TestWaitForPublishWorkflowGivesUpAtDeadline(t *testing.T) {
	srv := fakeActions(t, func() (int, string) { return http.StatusOK, "queued" }) // never completes
	withActions(t, srv.URL)
	withTimings(t, time.Millisecond, 50*time.Millisecond)

	start := time.Now()
	waitForPublishWorkflow(context.Background(), DefaultRepo, "")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("wait took %v, want it to give up near the 50ms deadline", elapsed)
	}
}

func TestWriteSignalIsAtomicPendingThenReadable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "update-signal")
	target := "ghcr.io/autumnsgrove/ivy@sha256:" + strings.Repeat("b", 64)

	if err := WriteSignal(dir, target); err != nil {
		t.Fatalf("WriteSignal: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "requested"))
	if err != nil {
		t.Fatalf("reading requested: %v", err)
	}
	if string(got) != target {
		t.Errorf("requested = %q, want %q", got, target)
	}
	if _, err := os.Stat(filepath.Join(dir, "requested.tmp")); !os.IsNotExist(err) {
		t.Errorf("requested.tmp should not survive the rename, stat err = %v", err)
	}
	if !Pending(dir) {
		t.Error("Pending = false, want true while requested exists")
	}

	// The host watcher's result, written the way the shell script writes it.
	res := Result{Status: "ok", Target: target, FinishedAt: "2026-10-06T00:00:00Z"}
	body, _ := json.Marshal(res)
	if err := os.WriteFile(filepath.Join(dir, "result"), body, 0o600); err != nil {
		t.Fatalf("writing result: %v", err)
	}
	_ = os.Remove(filepath.Join(dir, "requested"))
	if Pending(dir) {
		t.Error("Pending = true after requested was removed")
	}
	got2 := ReadResult(dir)
	if got2 == nil || got2.Status != "ok" || got2.Target != target {
		t.Errorf("ReadResult = %+v, want the written result", got2)
	}
}

func TestReadResultMissingOrMalformedIsNil(t *testing.T) {
	dir := t.TempDir()
	if got := ReadResult(dir); got != nil {
		t.Errorf("ReadResult on an empty dir = %+v, want nil", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "result"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadResult(dir); got != nil {
		t.Errorf("ReadResult on malformed JSON = %+v, want nil", got)
	}
}

func TestClientRequestResolvesWaitsAndSignals(t *testing.T) {
	digest := "sha256:" + strings.Repeat("c", 64)
	ghcr := fakeGHCR(t, http.StatusOK, http.StatusOK, digest)
	withGHCR(t, ghcr.URL)
	actions := fakeActions(t, func() (int, string) { return http.StatusOK, "completed" })
	withActions(t, actions.URL)

	dir := filepath.Join(t.TempDir(), "update-signal")
	c := &Client{Repo: DefaultRepo, SignalDir: dir}
	target, err := c.Request(context.Background())
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	want := "ghcr.io/autumnsgrove/ivy@" + digest
	if target != want {
		t.Errorf("target = %q, want %q", target, want)
	}
	got, err := os.ReadFile(filepath.Join(dir, "requested"))
	if err != nil {
		t.Fatalf("reading signal: %v", err)
	}
	if string(got) != want {
		t.Errorf("signal = %q, want %q", got, want)
	}
}

// TestClientRequestRejectsANonDigest stops a buggy or hostile registry from
// handing the root watcher a floating tag it would silently pull.
func TestClientRequestRejectsANonDigest(t *testing.T) {
	ghcr := fakeGHCR(t, http.StatusOK, http.StatusOK, "latest")
	withGHCR(t, ghcr.URL)
	actions := fakeActions(t, func() (int, string) { return http.StatusOK, "completed" })
	withActions(t, actions.URL)

	c := &Client{Repo: DefaultRepo, SignalDir: filepath.Join(t.TempDir(), "update-signal")}
	if _, err := c.Request(context.Background()); err == nil {
		t.Fatal("Request error = nil, want a refusal when the registry returns a non-digest")
	}
}

func TestValidDigest(t *testing.T) {
	good := "sha256:" + strings.Repeat("0", 64)
	if !validDigest(good) {
		t.Errorf("validDigest(%q) = false, want true", good)
	}
	for _, bad := range []string{"", "latest", "sha256:abc", "sha256:" + strings.Repeat("g", 64), "sha512:" + strings.Repeat("0", 64)} {
		if validDigest(bad) {
			t.Errorf("validDigest(%q) = true, want false", bad)
		}
	}
}

// The watcher writes `result` once and never clears it, so a request that the
// watcher never ran (not installed, stopped, or hung past the wait) would read
// the previous run's "ok" and the UI would report an update that did not
// happen. A new request has to start with no result.
func TestWriteSignalDropsTheLastRunsResult(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "update-signal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"status":"ok","detail":"updated","target":"ghcr.io/autumnsgrove/ivy@sha256:` + strings.Repeat("a", 64) + `","finished_at":"2026-10-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "result"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if ReadResult(dir) == nil {
		t.Fatal("setup: the old result should be readable")
	}

	if err := WriteSignal(dir, "ghcr.io/autumnsgrove/ivy@sha256:"+strings.Repeat("b", 64)); err != nil {
		t.Fatalf("WriteSignal: %v", err)
	}
	if got := ReadResult(dir); got != nil {
		t.Errorf("ReadResult after a new request = %+v, want nil until the watcher finishes", got)
	}
}
