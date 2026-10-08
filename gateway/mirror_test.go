package gateway

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

func disabledInboxMessage(id, accountID, folderID, reason string) store.Message {
	m := inboxMessage(id, accountID, folderID, testNow, false)
	m.DisabledAt = testNow
	m.DisabledReason = reason
	return m
}

// Mirror health is where the operator sees how much mail is kept but hidden, so
// the count must be on the account view with its breakdown.
func TestMirrorHealthShowsHiddenCount(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox, LastSyncAt: testNow})
	mustMessage(t, dbs, disabledInboxMessage("m1", "acct-1", "inbox-1", store.DisabledRemoved))
	mustMessage(t, dbs, disabledInboxMessage("m2", "acct-1", "inbox-1", store.DisabledRemoved))
	mustMessage(t, dbs, disabledInboxMessage("m3", "acct-1", "inbox-1", store.DisabledMoved))
	mustMessage(t, dbs, disabledInboxMessage("m4", "acct-1", "inbox-1", store.DisabledPending))

	var health api.HealthOverview
	if code := getJSON(t, srv.URL+"/api/v1/mirror/health", &health); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(health.Accounts) != 1 || health.Accounts[0].Hidden == nil {
		t.Fatalf("accounts = %+v, want one account with hidden mail", health.Accounts)
	}
	got := *health.Accounts[0].Hidden
	if got.Total != 4 || got.Removed != 2 || got.Moved != 1 || got.Pending != 1 {
		t.Errorf("hidden = %+v, want total 4 removed 2 moved 1 pending 1", got)
	}
}

// fakeBacklog stands in for the embed worker's queue report.
type fakeBacklog struct {
	counts map[string]int
	err    error
}

func (f fakeBacklog) Backlog(context.Context) (map[string]int, error) { return f.counts, f.err }

func getHealth(t *testing.T, url string) api.HealthOverview {
	t.Helper()
	var health api.HealthOverview
	if code := getJSON(t, url+"/api/v1/mirror/health", &health); code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", code)
	}
	return health
}

var humanBytes = regexp.MustCompile(`^\d+(\.\d)? (B|KB|MB|GB)$`)

// Search and meaning search used to read "Not built yet" after both were built.
// With no embed queue wired the page says so plainly, and it states the index
// size and the process memory (the board has little to spare).
func TestMirrorHealthStatesTheIndexAndMemory(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	ctx := context.Background()
	for _, key := range []string{"k1", "k2", "k3"} {
		if err := dbs.IndexSearchDoc(ctx, store.SearchDoc{AccountID: "a", ContentKey: key, Subject: key}); err != nil {
			t.Fatal(err)
		}
	}

	health := getHealth(t, srv.URL)
	if health.SearchIndex != "3 messages" {
		t.Errorf("searchIndex = %q, want %q", health.SearchIndex, "3 messages")
	}
	if health.MeaningSearch != "Off" || health.EmbeddingQueue != 0 {
		t.Errorf("meaning search = %q with %d queued, want Off and 0 with no queue wired", health.MeaningSearch, health.EmbeddingQueue)
	}
	if !humanBytes.MatchString(health.Memory) || health.Memory == "0 B" {
		t.Errorf("memory = %q, want a real size such as 38.2 MB", health.Memory)
	}
}

func TestMirrorHealthSaysNothingIsIndexedYet(t *testing.T) {
	t.Parallel()
	srv, _ := newSeededServer(t)
	if got := getHealth(t, srv.URL).SearchIndex; got != "Nothing indexed yet" {
		t.Errorf("searchIndex = %q, want %q", got, "Nothing indexed yet")
	}
}

// An account with smart features off will never be embedded, so its mail is not
// "waiting"; counting it would show a queue that cannot drain.
func TestMirrorHealthCountsTheQueueOfSmartAccountsOnly(t *testing.T) {
	t.Parallel()
	backlog := fakeBacklog{counts: map[string]int{"on": 1200, "off": 9}}
	srv, dbs := newConfiguredServer(t, func(s *Server) { s.WithEmbedBacklog(backlog) })
	mustAccount(t, dbs, store.Account{ID: "on", Address: "on@example.com", LLMEnabled: true})
	mustAccount(t, dbs, store.Account{ID: "off", Address: "off@example.com"})

	health := getHealth(t, srv.URL)
	if health.EmbeddingQueue != 1200 || health.MeaningSearch != "1,200 waiting" {
		t.Errorf("queue = %d, meaning search = %q; want 1200 and %q", health.EmbeddingQueue, health.MeaningSearch, "1,200 waiting")
	}

	backlog.counts["on"] = 0
	srv, dbs = newConfiguredServer(t, func(s *Server) { s.WithEmbedBacklog(backlog) })
	mustAccount(t, dbs, store.Account{ID: "on", Address: "on@example.com", LLMEnabled: true})
	if got := getHealth(t, srv.URL).MeaningSearch; got != "Up to date" {
		t.Errorf("a drained queue reads %q, want %q", got, "Up to date")
	}

	srv, dbs = newConfiguredServer(t, func(s *Server) { s.WithEmbedBacklog(backlog) })
	mustAccount(t, dbs, store.Account{ID: "off", Address: "off@example.com"})
	if health := getHealth(t, srv.URL); health.MeaningSearch != "Off" || health.EmbeddingQueue != 0 {
		t.Errorf("every account off: %q with %d queued, want Off and 0", health.MeaningSearch, health.EmbeddingQueue)
	}
}

// The health page is how the operator finds out something is wrong, so a counter
// that fails must not take the page with it.
func TestMirrorHealthSurvivesAQueueThatCannotBeCounted(t *testing.T) {
	t.Parallel()
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		s.WithEmbedBacklog(fakeBacklog{err: errors.New("mirror busy")})
	})
	mustAccount(t, dbs, store.Account{ID: "on", Address: "on@example.com", LLMEnabled: true})
	health := getHealth(t, srv.URL)
	if health.MeaningSearch != "Unavailable" || health.EmbeddingQueue != 0 || len(health.Accounts) != 1 {
		t.Errorf("health = %+v, want the page with meaning search Unavailable", health)
	}
}

// Restoring one message is the per-message escape hatch: it clears the hidden
// flag and nothing else. An unknown id is a 404.
func TestRestoreMessageEndpoint(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, disabledInboxMessage("m1", "acct-1", "inbox-1", store.DisabledRemoved))

	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/mirror/messages/m1/restore", nil, nil, nil); code != http.StatusNoContent {
		t.Fatalf("restore status = %d, want 204", code)
	}
	var health api.HealthOverview
	getJSON(t, srv.URL+"/api/v1/mirror/health", &health)
	if h := health.Accounts[0].Hidden; h != nil && h.Total != 0 {
		t.Errorf("hidden after restore = %+v, want none", h)
	}

	var body api.Error
	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/mirror/messages/nope/restore", nil, &body, nil); code != http.StatusNotFound {
		t.Errorf("unknown restore status = %d, want 404 (code %q)", code, body.Code)
	}
}

// The account-wide restore backs the one-click answer to a mass-disable alert.
// It counts what it made visible and leaves an unclassified row alone.
func TestRestoreAccountHiddenEndpoint(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, disabledInboxMessage("m1", "acct-1", "inbox-1", store.DisabledRemoved))
	mustMessage(t, dbs, disabledInboxMessage("m2", "acct-1", "inbox-1", store.DisabledMoved))
	mustMessage(t, dbs, disabledInboxMessage("m3", "acct-1", "inbox-1", store.DisabledPending))

	var out api.RestoreResult
	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/mirror/accounts/acct-1/restore", nil, &out, nil); code != http.StatusOK {
		t.Fatalf("restore status = %d, want 200", code)
	}
	if out.Restored != 1 {
		t.Errorf("restored = %d, want 1 (pending is unclassified, moved mail is live elsewhere)", out.Restored)
	}
	var health api.HealthOverview
	getJSON(t, srv.URL+"/api/v1/mirror/health", &health)
	if h := health.Accounts[0].Hidden; h == nil || h.Total != 2 || h.Pending != 1 || h.Moved != 1 {
		t.Errorf("hidden after restore = %+v, want the pending and the moved row", h)
	}
}

// Purge is the one erasure: the row, its attachments and its spool file all go.
// A live message is refused, so the reader's ordinary delete can never reach it.
func TestPurgeMessageEndpoint(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})

	rel := filepath.Join("spool", "inbox-1", "99.eml")
	if err := os.MkdirAll(filepath.Join(dbs.Dir, "spool", "inbox-1"), 0o700); err != nil {
		t.Fatalf("mkdir spool: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dbs.Dir, rel), []byte("raw"), 0o600); err != nil {
		t.Fatalf("write spool: %v", err)
	}
	m := disabledInboxMessage("m1", "acct-1", "inbox-1", store.DisabledRemoved)
	m.RawPath = rel
	m.RawBlob = []byte("raw")
	mustMessage(t, dbs, m)
	if err := dbs.ReplaceMessageAttachments(context.Background(), "m1", []store.Attachment{
		{ID: "att-1", MessageID: "m1", Filename: "a.pdf", StoragePath: "1"},
	}); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}

	if code := doJSON(t, http.MethodDelete, srv.URL+"/api/v1/mirror/messages/m1", nil, nil, nil); code != http.StatusNoContent {
		t.Fatalf("purge status = %d, want 204", code)
	}
	if _, err := os.Stat(filepath.Join(dbs.Dir, rel)); !os.IsNotExist(err) {
		t.Errorf("spool file still present after purge (err %v)", err)
	}
	var body api.Error
	if code := doJSON(t, http.MethodDelete, srv.URL+"/api/v1/mirror/messages/m1", nil, &body, nil); code != http.StatusNotFound {
		t.Errorf("second purge status = %d, want 404 (code %q)", code, body.Code)
	}

	mustMessage(t, dbs, inboxMessage("live", "acct-1", "inbox-1", testNow, false))
	if code := doJSON(t, http.MethodDelete, srv.URL+"/api/v1/mirror/messages/live", nil, &body, nil); code != http.StatusConflict {
		t.Errorf("live purge status = %d, want 409 (code %q)", code, body.Code)
	}
	if body.Code != "not_disabled" {
		t.Errorf("live purge code = %q, want not_disabled", body.Code)
	}
}

// Restore changes what every open screen shows, so it must publish the same
// refetch hint a sync does.
// Purge is the one erasure: the row, its attachments and its spool file all go,
// and the blob store copy goes too when nothing else needs it (N24).
func TestPurgeErasesTheBlobLocallyAndFromTargets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "backup")
	srv, dbs := newConfiguredServer(t, func(s *Server) { s.WithBackupTargets([]string{target}) })
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})

	hash, _, err := dbs.Blobs.Put(ctx, strings.NewReader("raw bytes"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	m := disabledInboxMessage("m1", "acct-1", "inbox-1", store.DisabledRemoved)
	m.DisabledBlob = hash
	mustMessage(t, dbs, m)
	// The daily backup has already mirrored the blob to the target.
	targetBlob := filepath.Join(target, "blobs", hash[:2], hash)
	if err := os.MkdirAll(filepath.Dir(targetBlob), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetBlob, []byte("raw bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := doJSON(t, http.MethodDelete, srv.URL+"/api/v1/mirror/messages/m1", nil, nil, nil); code != http.StatusNoContent {
		t.Fatalf("purge status = %d, want 204", code)
	}
	if dbs.Blobs.Has(hash) {
		t.Error("the local blob survived the purge")
	}
	if _, err := os.Stat(targetBlob); !os.IsNotExist(err) {
		t.Errorf("the target blob survived the purge (err %v)", err)
	}
	if pending, err := dbs.PendingBlobDeletions(ctx); err != nil || len(pending) != 0 {
		t.Errorf("pending after purge = %v (%v), want none", pending, err)
	}
}

// Two hidden rows that share bytes keep the blob when one is purged; the last
// one erases it.
func TestPurgeKeepsASharedBlob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})

	hash, _, err := dbs.Blobs.Put(ctx, strings.NewReader("shared bytes"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	for _, id := range []string{"m1", "m2"} {
		m := disabledInboxMessage(id, "acct-1", "inbox-1", store.DisabledRemoved)
		m.DisabledBlob = hash
		mustMessage(t, dbs, m)
	}

	if code := doJSON(t, http.MethodDelete, srv.URL+"/api/v1/mirror/messages/m1", nil, nil, nil); code != http.StatusNoContent {
		t.Fatalf("first purge status = %d, want 204", code)
	}
	if !dbs.Blobs.Has(hash) {
		t.Error("purging one of two rows evicted the shared blob")
	}
	if pending, err := dbs.PendingBlobDeletions(ctx); err != nil || len(pending) != 0 {
		t.Errorf("pending after first purge = %v (%v), want none", pending, err)
	}

	if code := doJSON(t, http.MethodDelete, srv.URL+"/api/v1/mirror/messages/m2", nil, nil, nil); code != http.StatusNoContent {
		t.Fatalf("second purge status = %d, want 204", code)
	}
	if dbs.Blobs.Has(hash) {
		t.Error("the last reference left the blob behind")
	}
}

func TestRestoreMessagePublishesAHint(t *testing.T) {
	t.Parallel()
	srv, _ := newEventsServer(t, func(s *Server) {
		mustAccount(t, s.dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
		mustFolder(t, s.dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
		mustMessage(t, s.dbs, disabledInboxMessage("m1", "acct-1", "inbox-1", store.DisabledRemoved))
	})
	r := openStream(t, srv.URL+"/api/v1/events", nil)
	r.mustFrame() // the retry hint the stream opens with

	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/mirror/messages/m1/restore", nil, nil, nil); code != http.StatusNoContent {
		t.Fatalf("restore status = %d, want 204", code)
	}
	if got := r.mustFrame(); !strings.HasPrefix(got, "event: message.changed") {
		t.Errorf("frame = %q, want a message.changed hint", got)
	}
}

// A pending row is not restorable by hand either: the API answers 409 and the
// row stays hidden until a completed pass settles it.
func TestRestoreMessageEndpointRefusesAPendingRow(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, disabledInboxMessage("m1", "acct-1", "inbox-1", store.DisabledPending))

	var body api.Error
	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/mirror/messages/m1/restore", nil, &body, nil); code != http.StatusConflict {
		t.Fatalf("restore of a pending row status = %d, want 409", code)
	}
	if body.Code != "pending_classification" {
		t.Errorf("error code = %q, want pending_classification", body.Code)
	}
	var health api.HealthOverview
	getJSON(t, srv.URL+"/api/v1/mirror/health", &health)
	if h := health.Accounts[0].Hidden; h == nil || h.Pending != 1 {
		t.Errorf("hidden after the refused restore = %+v, want the pending row still hidden", h)
	}
}
