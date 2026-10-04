package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
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
	if out.Restored != 2 {
		t.Errorf("restored = %d, want 2 (pending is not restorable)", out.Restored)
	}
	var health api.HealthOverview
	getJSON(t, srv.URL+"/api/v1/mirror/health", &health)
	if h := health.Accounts[0].Hidden; h == nil || h.Total != 1 || h.Pending != 1 {
		t.Errorf("hidden after restore = %+v, want only the pending row", h)
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
