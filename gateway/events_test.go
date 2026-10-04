package gateway

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/store"
)

const eventsWait = 3 * time.Second

func newEventsServer(t *testing.T, configure func(*Server)) (*httptest.Server, *events.Hub) {
	t.Helper()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { dbs.Close() })
	hub := events.New()
	s := New(dbs, "test-version", testStaticFS()).WithEvents(hub)
	if configure != nil {
		configure(s)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(hub.Close) // before srv.Close, which would wait on open streams
	return srv, hub
}

// sseReader reads SSE frames with a deadline, so a stream that stops flushing
// fails a test instead of hanging it.
type sseReader struct {
	t    *testing.T
	resp *http.Response
	br   *bufio.Reader
	wrap func(io.Reader) (io.Reader, error)
}

func openStream(t *testing.T, url string, header http.Header) *sseReader {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	t.Cleanup(client.CloseIdleConnections)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &sseReader{t: t, resp: resp}
}

// frame returns the next blank-line-terminated block, without its final blank line.
func (r *sseReader) frame() (string, error) {
	type result struct {
		s   string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		if r.br == nil {
			var src io.Reader = r.resp.Body
			if r.wrap != nil {
				w, err := r.wrap(src)
				if err != nil {
					ch <- result{"", err}
					return
				}
				src = w
			}
			r.br = bufio.NewReader(src)
		}
		var lines []string
		for {
			line, err := r.br.ReadString('\n')
			if err != nil {
				ch <- result{"", err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				ch <- result{strings.Join(lines, "\n"), nil}
				return
			}
			lines = append(lines, line)
		}
	}()
	select {
	case res := <-ch:
		return res.s, res.err
	case <-time.After(eventsWait):
		r.resp.Body.Close() // unblocks the reader goroutine
		return "", errors.New("timed out waiting for a frame")
	}
}

func (r *sseReader) mustFrame() string {
	r.t.Helper()
	f, err := r.frame()
	if err != nil {
		r.t.Fatalf("frame: %v", err)
	}
	return f
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(eventsWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEventsStreamStartsWithHeadersAndARetryHint(t *testing.T) {
	t.Parallel()
	srv, _ := newEventsServer(t, nil)
	r := openStream(t, srv.URL+"/api/v1/events", nil)

	if got := r.resp.StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
	if got := r.resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := r.resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	// Sent at once so the browser's EventSource opens and knows how long to wait
	// before reconnecting; after a reconnect it refetches, so a short delay is right.
	if got := r.mustFrame(); got != "retry: 3000" {
		t.Errorf("first frame = %q, want the retry hint", got)
	}
}

func TestEventsDeliversAHintAsOneNamedJSONFrameWithNoID(t *testing.T) {
	t.Parallel()
	srv, hub := newEventsServer(t, nil)
	r := openStream(t, srv.URL+"/api/v1/events", nil)
	r.mustFrame() // retry
	waitFor(t, "the subscription", func() bool { return hub.Subscribers() == 1 })

	hub.Publish(events.Event{Type: events.FolderChanged, AccountID: "acct-1", Folder: "INBOX"})
	want := "event: folder.changed\n" +
		`data: {"type":"folder.changed","accountId":"acct-1","folder":"INBOX"}`
	got := r.mustFrame()
	if got != want {
		t.Errorf("frame = %q, want %q", got, want)
	}
	// Round 37: no replay, so no ids. An id would invite a Last-Event-ID resume
	// that nothing implements.
	if strings.Contains(got, "id:") {
		t.Errorf("frame carries an id line: %q", got)
	}
}

// The folder name comes from the server and is hostile input. It must stay
// inside one JSON string, not become a forged frame.
func TestEventsFrameSurvivesAHostileFolderName(t *testing.T) {
	t.Parallel()
	srv, hub := newEventsServer(t, nil)
	r := openStream(t, srv.URL+"/api/v1/events", nil)
	r.mustFrame()
	waitFor(t, "the subscription", func() bool { return hub.Subscribers() == 1 })

	hub.Publish(events.Event{Type: events.FolderChanged, AccountID: "a", Folder: "x\nevent: evil\ndata: {}\n\nretry: 1\r\n\r\n"})
	hub.Publish(events.Event{Type: events.SyncState, AccountID: "a"})

	first := r.mustFrame()
	lines := strings.Split(first, "\n")
	if len(lines) != 2 || lines[0] != "event: folder.changed" || !strings.HasPrefix(lines[1], "data: ") {
		t.Fatalf("hostile hint did not stay one frame: %q", first)
	}
	var got events.Event
	if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &got); err != nil || got.Folder == "" {
		t.Errorf("data line is not the event: %q (%v)", lines[1], err)
	}
	if second := r.mustFrame(); !strings.HasPrefix(second, "event: sync.state") {
		t.Errorf("second frame = %q, want the next real hint and nothing forged", second)
	}
}

// The real handler chain wraps the stream in the compression middleware and the
// JSON error rewriter; a wrapper that hides Flush turns a stream into a buffer.
func TestEventsReachTheClientPromptlyThroughTheFullChain(t *testing.T) {
	t.Parallel()
	for _, enc := range []string{"", "gzip"} {
		t.Run("accept-encoding="+enc, func(t *testing.T) {
			t.Parallel()
			srv, hub := newEventsServer(t, nil)
			header := http.Header{}
			var wrap func(io.Reader) (io.Reader, error)
			if enc != "" {
				header.Set("Accept-Encoding", enc)
				wrap = func(src io.Reader) (io.Reader, error) { return gzip.NewReader(src) }
			}
			r := openStream(t, srv.URL+"/api/v1/events", header)
			if enc != "" && r.resp.Header.Get("Content-Encoding") != "gzip" {
				// The server may reasonably leave a stream uncompressed; the point
				// is promptness, so read it as it was sent.
				wrap = nil
			}
			r.wrap = wrap

			if got := r.mustFrame(); got != "retry: 3000" {
				t.Fatalf("first frame = %q", got)
			}
			waitFor(t, "the subscription", func() bool { return hub.Subscribers() == 1 })
			hub.Publish(events.Event{Type: events.SyncState, AccountID: "acct-1"})
			if got := r.mustFrame(); !strings.HasPrefix(got, "event: sync.state") {
				t.Errorf("frame = %q, want the sync.state hint", got)
			}
		})
	}
}

func TestEventsSendsKeepalivesWhileIdle(t *testing.T) {
	t.Parallel()
	srv, _ := newEventsServer(t, func(s *Server) { s.WithEventsHeartbeat(20 * time.Millisecond) })
	r := openStream(t, srv.URL+"/api/v1/events", nil)
	r.mustFrame() // retry
	for range 2 {
		if got := r.mustFrame(); got != ": keepalive" {
			t.Errorf("idle frame = %q, want a keepalive comment", got)
		}
	}
}

func TestEventsEndsAndFreesItsSlotWhenTheClientLeaves(t *testing.T) {
	t.Parallel()
	srv, hub := newEventsServer(t, nil)
	r := openStream(t, srv.URL+"/api/v1/events", nil)
	r.mustFrame()
	waitFor(t, "the subscription", func() bool { return hub.Subscribers() == 1 })

	r.resp.Body.Close()
	waitFor(t, "the slot to free", func() bool { return hub.Subscribers() == 0 })
}

// A server shutdown never finds a stream idle, so closing the hub is what ends it.
func TestEventsEndWhenTheHubCloses(t *testing.T) {
	t.Parallel()
	srv, hub := newEventsServer(t, nil)
	r := openStream(t, srv.URL+"/api/v1/events", nil)
	r.mustFrame()
	waitFor(t, "the subscription", func() bool { return hub.Subscribers() == 1 })

	hub.Close()
	if _, err := r.frame(); !errors.Is(err, io.EOF) {
		t.Errorf("after the hub closed: err = %v, want io.EOF", err)
	}
}

func TestEventsRefuseStreamsOverTheLimitAndRecover(t *testing.T) {
	t.Parallel()
	srv, hub := newEventsServer(t, nil)
	var open []*sseReader
	for range events.MaxSubscribers {
		r := openStream(t, srv.URL+"/api/v1/events", nil)
		r.mustFrame()
		open = append(open, r)
	}
	waitFor(t, "every subscription", func() bool { return hub.Subscribers() == events.MaxSubscribers })

	resp, err := http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("over the limit: %v", err)
	}
	var env api.Error
	_ = json.NewDecoder(resp.Body).Decode(&env)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || env.Code != "too_many_streams" {
		t.Fatalf("over the limit: status %d code %q, want 503 too_many_streams", resp.StatusCode, env.Code)
	}

	open[0].resp.Body.Close()
	waitFor(t, "a slot to free", func() bool { return hub.Subscribers() == events.MaxSubscribers-1 })
	again := openStream(t, srv.URL+"/api/v1/events", nil)
	if again.resp.StatusCode != http.StatusOK {
		t.Errorf("second attempt after a slot freed: status %d, want 200", again.resp.StatusCode)
	}
}

func TestEventsWithoutAHubIsAnUnknownPath(t *testing.T) {
	t.Parallel()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { dbs.Close() })
	srv := httptest.NewServer(New(dbs, "test", testStaticFS()).Handler())
	t.Cleanup(srv.Close)

	var env api.Error
	if code := getJSON(t, srv.URL+"/api/v1/events", &env); code != http.StatusNotFound || env.Code != "not_found" {
		t.Errorf("status %d code %q, want 404 not_found", code, env.Code)
	}
}

func TestEventsAreBehindTheHostGuard(t *testing.T) {
	t.Parallel()
	srv, _ := newEventsServer(t, nil)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/v1/events", nil)
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a foreign Host", resp.StatusCode)
	}
}

// recorder is a ResponseWriter that logs the order of deadlines and writes and
// can start failing, to model a peer that stopped reading.
type recorder struct {
	*httptest.ResponseRecorder
	mu        sync.Mutex
	ops       []string
	failAfter int // writes allowed before Write errors; 0 means never fail
	writes    int
}

func (r *recorder) SetWriteDeadline(t time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Until(t) <= 0 || time.Until(t) > time.Minute {
		r.ops = append(r.ops, "bad-deadline")
	} else {
		r.ops = append(r.ops, "deadline")
	}
	return nil
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes++
	r.ops = append(r.ops, "write")
	if r.failAfter > 0 && r.writes > r.failAfter {
		return 0, errors.New("peer stopped reading")
	}
	return r.ResponseRecorder.Write(p)
}

// WriteString is promoted from the embedded recorder and io.WriteString would
// pick it, skipping the Write above; route it through Write.
func (r *recorder) WriteString(s string) (int, error) { return r.Write([]byte(s)) }

func (r *recorder) snapshot() ([]string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ops...), r.writes
}

func runHandler(t *testing.T, rec *recorder, hub *events.Hub) (cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { dbs.Close() })
	s := New(dbs, "test", testStaticFS()).WithEvents(hub).WithEventsHeartbeat(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := make(chan struct{})
	go func() {
		defer close(d)
		s.handleEvents(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/events", nil))
	}()
	return cancel, d
}

// The server has no WriteTimeout, so each write sets its own deadline; without it
// a peer that stops reading would hold a goroutine and a slot forever.
func TestEventsSetAWriteDeadlineBeforeEveryWrite(t *testing.T) {
	t.Parallel()
	hub := events.New()
	rec := &recorder{ResponseRecorder: httptest.NewRecorder()}
	cancel, done := runHandler(t, rec, hub)
	waitFor(t, "the subscription", func() bool { return hub.Subscribers() == 1 })
	hub.Publish(events.Event{Type: events.SyncState, AccountID: "acct-1"})
	waitFor(t, "two writes", func() bool { _, n := rec.snapshot(); return n >= 2 })

	cancel()
	select {
	case <-done:
	case <-time.After(eventsWait):
		t.Fatal("handler did not return after its request was cancelled")
	}
	ops, _ := rec.snapshot()
	for i, op := range ops {
		switch {
		case op == "bad-deadline":
			t.Errorf("op %d: a deadline outside (now, now+1m]", i)
		case op == "write" && (i == 0 || ops[i-1] != "deadline"):
			t.Errorf("op %d: write without a fresh deadline; ops = %v", i, ops)
		}
	}
}

func TestEventsEndWhenAWriteFails(t *testing.T) {
	t.Parallel()
	hub := events.New()
	rec := &recorder{ResponseRecorder: httptest.NewRecorder(), failAfter: 1}
	_, done := runHandler(t, rec, hub)
	waitFor(t, "the subscription", func() bool { return hub.Subscribers() == 1 })

	hub.Publish(events.Event{Type: events.SyncState, AccountID: "acct-1"}) // the second write fails
	select {
	case <-done:
	case <-time.After(eventsWait):
		t.Fatal("handler kept running after the peer stopped reading")
	}
	waitFor(t, "the slot to free", func() bool { return hub.Subscribers() == 0 })
}
