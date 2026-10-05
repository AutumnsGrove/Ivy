package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// outboxServer is a server with a deterministic op id, plus the account, an
// INBOX and a message it acts on.
func outboxServer(t *testing.T) (*httptest.Server, *store.DBs) {
	t.Helper()
	n := 0
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		s.WithIDFunc(func() string { n++; return "op-" + string(rune('0'+n)) })
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox, UIDValidity: 1, LastSyncAt: testNow})
	mustMessage(t, dbs, inboxMessage("m1", "acct-1", "inbox-1", testNow, false))
	return srv, dbs
}

func postJSON(t *testing.T, url string, body, out any) int {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// Archiving is a move to the Archive-role folder, queued as an op that names the
// postcondition rather than a command.
func TestEnqueueArchiveCreatesAMoveOp(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	mustFolder(t, dbs, store.Folder{ID: "archive-1", AccountID: "acct-1", Name: "Archive", Role: store.RoleArchive})

	var item api.OutboxItem
	code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{
		MessageId: "m1", Action: api.OutboxActionArchive,
	}, &item)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if item.Kind != api.OutboxKindMove || item.State != api.OutboxStatePending {
		t.Errorf("op = %s/%s, want move/pending", item.Kind, item.State)
	}
	if item.DestinationFolderId == nil || *item.DestinationFolderId != "archive-1" {
		t.Errorf("destination = %v, want archive-1", item.DestinationFolderId)
	}
	if item.MessageId != "m1" {
		t.Errorf("messageId = %q, want m1", item.MessageId)
	}
}

// A flag is a STORE, not a move: the op carries the flags to add.
func TestEnqueueFlagCreatesAFlagsOp(t *testing.T) {
	t.Parallel()
	srv, _ := outboxServer(t)

	var item api.OutboxItem
	code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{
		MessageId: "m1", Action: api.OutboxActionFlag,
	}, &item)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if item.Kind != api.OutboxKindFlags {
		t.Fatalf("kind = %s, want flags", item.Kind)
	}
	if item.FlagsAdd == nil || len(*item.FlagsAdd) != 1 || (*item.FlagsAdd)[0] != `\flagged` {
		t.Errorf("flagsAdd = %v, want [\\flagged] (canonical lower-case)", item.FlagsAdd)
	}
}

// Without an Archive folder the action is refused with a reason the UI can word,
// never queued to fail later.
func TestEnqueueArchiveWithoutTheFolderIsRefused(t *testing.T) {
	t.Parallel()
	srv, _ := outboxServer(t)

	var body api.Error
	code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{
		MessageId: "m1", Action: api.OutboxActionArchive,
	}, &body)
	if code != http.StatusConflict || body.Code != "no_archive_folder" {
		t.Fatalf("status/code = %d/%s, want 409/no_archive_folder", code, body.Code)
	}
}

// Expunge is only for the trash, so a call naming an INBOX message is refused:
// a single tap can never erase mail.
func TestEnqueueExpungeOutsideTrashIsRefused(t *testing.T) {
	t.Parallel()
	srv, _ := outboxServer(t)

	var body api.Error
	code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{
		MessageId: "m1", Action: api.OutboxActionExpunge,
	}, &body)
	if code != http.StatusConflict || body.Code != "not_trash" {
		t.Fatalf("status/code = %d/%s, want 409/not_trash", code, body.Code)
	}
}

// A double tap is one action: the repeat returns the existing op.
func TestEnqueueIsIdempotentOnADoubleTap(t *testing.T) {
	t.Parallel()
	srv, _ := outboxServer(t)

	var first, second api.OutboxItem
	postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: api.OutboxActionFlag}, &first)
	postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: api.OutboxActionFlag}, &second)
	if first.Id != second.Id {
		t.Errorf("double tap produced %s then %s, want the same op", first.Id, second.Id)
	}
}

// The overlay reads the live ops; the history reads the terminal ones.
func TestListOutboxSplitsActiveAndRecent(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: api.OutboxActionFlag}, nil)
	if _, _, err := dbs.EnqueueOutbox(context.Background(), store.OutboxOp{
		ID: "op-old", AccountID: "acct-1", Kind: store.OutboxFlags,
		ContentKey: "ck:m1", SourceFolderID: "inbox-1",
		Expect: store.OutboxExpect{FlagsAdd: []string{`\Seen`}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := dbs.SetOutboxFailed(context.Background(), "op-old", "noperm", "no", testNow); err != nil {
		t.Fatalf("fail: %v", err)
	}

	var list api.OutboxList
	if code := getJSON(t, srv.URL+"/api/v1/outbox?account_id=acct-1", &list); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(list.Active) != 1 || list.Active[0].State != api.OutboxStatePending {
		t.Errorf("active = %+v, want one pending op", list.Active)
	}
	if len(list.Recent) != 1 || list.Recent[0].State != api.OutboxStateFailed {
		t.Errorf("recent = %+v, want one failed op", list.Recent)
	}
}

// A failed op can be retried from the UI; a live one cannot.
func TestRetryOutbox(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	if _, _, err := dbs.EnqueueOutbox(context.Background(), store.OutboxOp{
		ID: "op-failed", AccountID: "acct-1", Kind: store.OutboxFlags,
		ContentKey: "ck:m1", SourceFolderID: "inbox-1",
		Expect: store.OutboxExpect{FlagsAdd: []string{`\flagged`}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := dbs.SetOutboxFailed(context.Background(), "op-failed", "noperm", "no", testNow); err != nil {
		t.Fatalf("fail: %v", err)
	}

	var item api.OutboxItem
	if code := postJSON(t, srv.URL+"/api/v1/outbox/op-failed/retry", nil, &item); code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200", code)
	}
	if item.State != api.OutboxStatePending {
		t.Errorf("state = %s, want pending", item.State)
	}

	var live api.OutboxItem
	postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: api.OutboxActionSeen}, &live)
	var body api.Error
	if code := postJSON(t, srv.URL+"/api/v1/outbox/"+live.Id+"/retry", nil, &body); code != http.StatusConflict || body.Code != "not_failed" {
		t.Errorf("retry of a live op = %d/%s, want 409/not_failed", code, body.Code)
	}
}

// A terminal op can be dismissed; a live one is refused.
func TestDismissOutbox(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: api.OutboxActionFlag}, nil)

	var live api.OutboxList
	getJSON(t, srv.URL+"/api/v1/outbox?account_id=acct-1", &live)
	id := live.Active[0].Id

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/outbox/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("dismiss of a live op = %d, want 409", resp.StatusCode)
	}

	if err := dbs.SetOutboxDone(context.Background(), id, testNow); err != nil {
		t.Fatalf("done: %v", err)
	}
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("dismiss of a terminal op = %d, want 204", resp2.StatusCode)
	}
}

// The reader draws its star from the denormalised flagged column, so the inbox
// row must carry it (the flag toggle needs a state to show).
func TestInboxCarriesTheFlaggedState(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	mustMessage(t, dbs, store.Message{
		ID: "m-flagged", AccountID: "acct-1", FolderID: "inbox-1", UID: 99,
		ContentKey: "ck:flagged", Subject: "Starred", Snippet: "hi",
		From: store.Address{Name: "Sender", Address: "sender@example.com"},
		Date: testNow, Flags: []string{`\Flagged`},
	})

	var inbox api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox", &inbox); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	var found bool
	for _, item := range inbox.Items {
		if item.Id == "m-flagged" {
			found = true
			if item.Flagged == nil || !*item.Flagged {
				t.Errorf("flagged = %v, want true", item.Flagged)
			}
		}
	}
	if !found {
		t.Fatal("the flagged message is not in the inbox")
	}
}

// A folder the server no longer lists cannot receive mail, so a move into it is
// refused up front instead of queueing an op that can only fail.
func TestEnqueueMoveToAGoneFolderIsRefused(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	mustFolder(t, dbs, store.Folder{ID: "old-1", AccountID: "acct-1", Name: "Old", Role: store.RoleOther, UIDValidity: 1, LastSyncAt: testNow})
	if err := dbs.SetFolderGone(context.Background(), "old-1", testNow); err != nil {
		t.Fatalf("set gone: %v", err)
	}

	dest := "old-1"
	var body api.Error
	code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{
		MessageId: "m1", Action: api.OutboxActionMove, DestinationFolderId: &dest,
	}, &body)
	if code != http.StatusConflict || body.Code != "bad_destination" {
		t.Fatalf("status/code = %d/%s, want 409/bad_destination", code, body.Code)
	}
}
