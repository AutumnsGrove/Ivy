package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// Decision 2: replying, archiving or deleting a message clears its needs-attention
// flag, kept locally; reading and organising it (flag, seen, tag) do not.

func flagNeeds(t *testing.T, dbs *store.DBs, key string) {
	t.Helper()
	if err := dbs.PutNeedsMe(context.Background(), store.NeedsMeRow{
		AccountID: "acct-1", ContentKey: key, Verdict: "needs", Reason: "Ada wants a reply.", Model: "m", State: store.NeedsOpen,
	}); err != nil {
		t.Fatal(err)
	}
}

func stillNeeds(t *testing.T, dbs *store.DBs, key string) bool {
	t.Helper()
	on, err := dbs.MessageNeeds(context.Background(), "acct-1", key)
	if err != nil {
		t.Fatal(err)
	}
	return on
}

func TestHandlingAMessageClearsItsNeedsFlag(t *testing.T) {
	t.Parallel()
	for _, action := range []api.OutboxActionAction{api.OutboxActionArchive, api.OutboxActionTrash, api.OutboxActionSpam} {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()
			srv, dbs := outboxServer(t)
			mustFolder(t, dbs, store.Folder{ID: "archive-1", AccountID: "acct-1", Name: "Archive", Role: store.RoleArchive})
			mustFolder(t, dbs, store.Folder{ID: "trash-1", AccountID: "acct-1", Name: "Trash", Role: store.RoleTrash})
			mustFolder(t, dbs, store.Folder{ID: "junk-1", AccountID: "acct-1", Name: "Junk", Role: store.RoleJunk})
			flagNeeds(t, dbs, "ck:m1")
			if code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: action}, nil); code != http.StatusAccepted {
				t.Fatalf("status = %d", code)
			}
			if stillNeeds(t, dbs, "ck:m1") {
				t.Fatalf("%s left the flag on a message that was dealt with", action)
			}
		})
	}
}

// Organising a message is not dealing with it, and a refused action did nothing.
func TestOrganisingOrARefusedActionLeavesTheFlag(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	flagNeeds(t, dbs, "ck:m1")
	for _, action := range []api.OutboxActionAction{api.OutboxActionFlag, api.OutboxActionSeen, api.OutboxActionUnseen} {
		if code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: action}, nil); code != http.StatusAccepted {
			t.Fatalf("%s status = %d", action, code)
		}
	}
	// No Archive folder exists on this account, so the move is refused (409).
	if code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: api.OutboxActionArchive}, nil); code != http.StatusConflict {
		t.Fatalf("archive without an Archive folder = %d, want 409", code)
	}
	if !stillNeeds(t, dbs, "ck:m1") {
		t.Fatal("an action that only organises, or was refused, cleared the flag")
	}
}

func TestABatchArchiveClearsEachFlag(t *testing.T) {
	t.Parallel()
	srv, dbs := batchServer(t)
	flagNeeds(t, dbs, "ck:m1")
	flagNeeds(t, dbs, "ck:m3")
	code := postJSON(t, srv.URL+"/api/v1/outbox/batch", api.OutboxBatch{
		MessageIds: []string{"m1", "m2"}, Action: api.OutboxBatchActionArchive,
	}, nil)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d", code)
	}
	if stillNeeds(t, dbs, "ck:m1") {
		t.Error("m1 was archived in the batch and is still flagged")
	}
	if !stillNeeds(t, dbs, "ck:m3") {
		t.Error("m3 was not in the batch and lost its flag")
	}
}

// A reply names its parent by Message-ID, and the parent's content key is a hash of
// it, so sending the reply clears the parent's flag without a lookup.
func TestSendingAReplyClearsTheParentsFlag(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	parentID := "<parent-1@example.com>"
	flagNeeds(t, dbs, store.ContentKey(parentID, nil))
	flagNeeds(t, dbs, "someone-else")

	req := sendRequest("send-1")
	req.InReplyTo = &parentID
	if code := postJSON(t, srv.URL+"/api/v1/send", req, nil); code != http.StatusAccepted {
		t.Fatalf("status = %d", code)
	}
	if stillNeeds(t, dbs, store.ContentKey(parentID, nil)) {
		t.Fatal("the reply was queued and the parent is still flagged")
	}
	if !stillNeeds(t, dbs, "someone-else") {
		t.Fatal("replying cleared an unrelated message")
	}
}

// A send that is refused did not reply to anything.
func TestARefusedSendLeavesTheFlag(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	parentID := "<parent-2@example.com>"
	flagNeeds(t, dbs, store.ContentKey(parentID, nil))
	req := sendRequest("send-2")
	req.InReplyTo = &parentID
	req.From = "stranger@elsewhere.test" // not an identity of the account
	if code := postJSON(t, srv.URL+"/api/v1/send", req, nil); code == http.StatusAccepted {
		t.Fatal("setup: the send should have been refused")
	}
	if !stillNeeds(t, dbs, store.ContentKey(parentID, nil)) {
		t.Fatal("a refused send cleared the flag")
	}
}
