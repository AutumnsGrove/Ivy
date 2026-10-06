package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// draftServer is the send server plus a Drafts-role folder in the mirror, which
// every draft write needs to resolve.
func draftServer(t *testing.T) (*httptest.Server, *store.DBs, *time.Time) {
	t.Helper()
	srv, dbs, now, _ := sendServer(t)
	mustFolder(t, dbs, store.Folder{ID: "drafts-1", AccountID: "acct-1", Name: "Drafts", Role: store.RoleDrafts})
	return srv, dbs, now
}

func strPtr(s string) *string { return &s }

func draftRequest(text string) api.DraftRequest {
	return api.DraftRequest{
		AccountId: "acct-1",
		From:      "me@example.com",
		To:        []string{"you@example.com"},
		Subject:   strPtr("hello"),
		Text:      text,
	}
}

func draftHead(t *testing.T, dbs *store.DBs) []store.Draft {
	t.Helper()
	heads, err := dbs.LiveDraftHeads(context.Background(), "acct-1", 50)
	if err != nil {
		t.Fatalf("draft heads: %v", err)
	}
	return heads
}

// A save files one version and its outbox op, and the list shows it.
func TestSaveDraftFilesAVersion(t *testing.T) {
	t.Parallel()
	srv, dbs, _ := draftServer(t)

	var sum api.DraftSummary
	if code := postJSON(t, srv.URL+"/api/v1/drafts", draftRequest("the first pass"), &sum); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if sum.Source != api.DraftSourceLocal || sum.Version != 1 {
		t.Fatalf("summary = %+v, want a local version 1", sum)
	}
	if sum.DraftId == nil || *sum.DraftId == "" {
		t.Fatal("no draftId was assigned")
	}
	ops, err := dbs.OutboxByAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != store.OutboxDraft {
		t.Fatalf("ops = %+v, want one draft op", ops)
	}
}

// A retried save (same client id) returns the stored version, not a new one.
func TestSaveDraftIsIdempotentOnTheClientID(t *testing.T) {
	t.Parallel()
	srv, _, _ := draftServer(t)

	req := draftRequest("only once")
	req.Id = strPtr("v1")
	var first api.DraftSummary
	if code := postJSON(t, srv.URL+"/api/v1/drafts", req, &first); code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", code)
	}
	second := draftRequest("changed")
	second.Id = strPtr("v1")
	second.DraftId = first.DraftId
	var again api.DraftSummary
	if code := postJSON(t, srv.URL+"/api/v1/drafts", second, &again); code != http.StatusOK {
		t.Fatalf("repeat status = %d, want 200", code)
	}
	if again.Version != 1 || again.DraftId == nil || first.DraftId == nil || *again.DraftId != *first.DraftId {
		t.Fatalf("repeat = %+v, want the stored version 1", again)
	}
}

// A replace is a new version; a save from a stale version is refused with the
// newer content, which is how a losing tab is told.
func TestSaveDraftReplaceAndConflict(t *testing.T) {
	t.Parallel()
	srv, _, _ := draftServer(t)

	var v1 api.DraftSummary
	if code := postJSON(t, srv.URL+"/api/v1/drafts", draftRequest("one"), &v1); code != http.StatusOK {
		t.Fatalf("v1 status = %d, want 200", code)
	}
	second := draftRequest("two")
	second.DraftId = v1.DraftId
	second.BaseVersion = intPtr(1)
	var v2 api.DraftSummary
	if code := postJSON(t, srv.URL+"/api/v1/drafts", second, &v2); code != http.StatusOK {
		t.Fatalf("v2 status = %d, want 200", code)
	}
	if v2.Version != 2 {
		t.Fatalf("v2 = %+v, want version 2", v2)
	}

	stale := draftRequest("stale")
	stale.DraftId = v1.DraftId
	stale.BaseVersion = intPtr(1)
	var resume api.DraftResume
	if code := postJSON(t, srv.URL+"/api/v1/drafts", stale, &resume); code != http.StatusConflict {
		t.Fatalf("stale status = %d, want 409", code)
	}
	if resume.Text != "two" || resume.Version != 2 {
		t.Fatalf("conflict body = %+v, want the newer version 2 with its text", resume)
	}
}

// The list merges Ivy's local drafts with the mirrored Drafts folder, so a draft
// made in another client appears, newest first.
func TestListDraftsMergesLocalAndServer(t *testing.T) {
	t.Parallel()
	srv, dbs, _ := draftServer(t)

	var local api.DraftSummary
	if code := postJSON(t, srv.URL+"/api/v1/drafts", draftRequest("local"), &local); code != http.StatusOK {
		t.Fatalf("save status = %d, want 200", code)
	}
	mustMessage(t, dbs, store.Message{
		ID: "srv-1", AccountID: "acct-1", FolderID: "drafts-1", UID: 1,
		ContentKey: "ck:srv-1", MessageID: "<srv-1@example.test>",
		Subject: "From Apple Mail", To: []store.Address{{Address: "them@example.com"}},
		Date: testNow.Add(time.Hour),
	})

	var list api.DraftList
	if code := getJSON(t, srv.URL+"/api/v1/drafts?account_id=acct-1", &list); code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", code)
	}
	if len(list.Drafts) != 2 {
		t.Fatalf("drafts = %+v, want the local and the server copy", list.Drafts)
	}
	if list.Drafts[0].Source != api.DraftSourceServer || list.Drafts[0].Id != "srv-1" {
		t.Errorf("first = %+v, want the newer server draft", list.Drafts[0])
	}
	if list.Drafts[1].Source != api.DraftSourceLocal {
		t.Errorf("second = %+v, want the local draft", list.Drafts[1])
	}
}

// A local draft resumes through the exact compose request it was saved with.
func TestGetDraftResumesLocal(t *testing.T) {
	t.Parallel()
	srv, _, _ := draftServer(t)

	req := draftRequest("resume me exactly")
	req.Subject = strPtr("a subject")
	var sum api.DraftSummary
	if code := postJSON(t, srv.URL+"/api/v1/drafts", req, &sum); code != http.StatusOK {
		t.Fatalf("save status = %d, want 200", code)
	}
	var resume api.DraftResume
	url := srv.URL + "/api/v1/drafts/" + *sum.DraftId + "?account_id=acct-1"
	if code := getJSON(t, url, &resume); code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200", code)
	}
	if resume.Text != "resume me exactly" || resume.Subject == nil || *resume.Subject != "a subject" {
		t.Fatalf("resume = %+v, want the saved compose fields", resume)
	}
	if resume.Source != api.DraftSourceLocal || resume.Version != 1 {
		t.Errorf("resume source/version = %v/%d, want local/1", resume.Source, resume.Version)
	}
}

// A draft created in another client has no local row; it resumes from the
// mirrored raw message.
func TestGetDraftResumesServerOnly(t *testing.T) {
	t.Parallel()
	srv, dbs, _ := draftServer(t)

	mustMessage(t, dbs, store.Message{
		ID: "srv-1", AccountID: "acct-1", FolderID: "drafts-1", UID: 1,
		ContentKey: "ck:srv-1", MessageID: "<srv-1@example.test>",
		Subject: "From Apple Mail", To: []store.Address{{Address: "them@example.com"}},
		Date: testNow, Size: 64,
		RawBlob: []byte("From: me@example.com\r\nTo: them@example.com\r\n" +
			"Subject: From Apple Mail\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello from mail\r\n"),
	})
	var resume api.DraftResume
	if code := getJSON(t, srv.URL+"/api/v1/drafts/srv-1", &resume); code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200", code)
	}
	if resume.Source != api.DraftSourceServer || !strings.Contains(resume.Text, "hello from mail") {
		t.Fatalf("resume = %+v, want the parsed server draft", resume)
	}
}

// Discarding a local draft hides it and queues the expunge of its server copy.
func TestDeleteDraftDiscardsLocal(t *testing.T) {
	t.Parallel()
	srv, dbs, _ := draftServer(t)

	var sum api.DraftSummary
	if code := postJSON(t, srv.URL+"/api/v1/drafts", draftRequest("throw me away"), &sum); code != http.StatusOK {
		t.Fatalf("save status = %d, want 200", code)
	}
	url := srv.URL + "/api/v1/drafts/" + *sum.DraftId + "?account_id=acct-1"
	if code := doJSON(t, http.MethodDelete, url, nil, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", code)
	}
	if heads := draftHead(t, dbs); len(heads) != 0 {
		t.Fatalf("draft heads = %+v, want none after discard", heads)
	}
	ops, err := dbs.OutboxByAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	foundRemove := false
	for _, op := range ops {
		if op.Kind == store.OutboxDraft && op.Expect.Remove {
			foundRemove = true
		}
	}
	if !foundRemove {
		t.Fatalf("ops = %+v, want a remove draft op", ops)
	}
}

func TestSaveDraftRejectsForeignFromAndOversize(t *testing.T) {
	t.Parallel()
	srv, _, _ := draftServer(t)

	foreign := draftRequest("hi")
	foreign.From = "someone-else@example.com"
	var e api.Error
	if code := postJSON(t, srv.URL+"/api/v1/drafts", foreign, &e); code != http.StatusBadRequest || e.Code != "bad_from" {
		t.Fatalf("foreign From = %d %+v, want 400 bad_from", code, e)
	}

	large := draftRequest(strings.Repeat("x", maxDraftBodyBytes+1))
	if code := postJSON(t, srv.URL+"/api/v1/drafts", large, nil); code != http.StatusBadRequest {
		t.Errorf("oversize status = %d, want 400", code)
	}
}

func intPtr(n int) *int { return &n }
