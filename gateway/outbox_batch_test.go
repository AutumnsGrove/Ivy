package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// batchServer has an account with an INBOX holding m1..m3 and an Archive folder.
func batchServer(t *testing.T) (*httptest.Server, *store.DBs) {
	t.Helper()
	n := 0
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		s.WithIDFunc(func() string { n++; return fmt.Sprintf("op-%d", n) })
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox, UIDValidity: 1, LastSyncAt: testNow})
	mustFolder(t, dbs, store.Folder{ID: "archive-1", AccountID: "acct-1", Name: "Archive", Role: store.RoleArchive, UIDValidity: 1, LastSyncAt: testNow})
	for _, id := range []string{"m1", "m2", "m3"} {
		mustMessage(t, dbs, inboxMessage(id, "acct-1", "inbox-1", testNow, false))
	}
	return srv, dbs
}

func queuedCount(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	var list api.OutboxList
	getJSON(t, srv.URL+"/api/v1/outbox", &list)
	return len(list.Active)
}

// Issue #11: one request acts on a whole selection, one op per message.
func TestBatchArchiveQueuesOneOpPerMessage(t *testing.T) {
	t.Parallel()
	srv, _ := batchServer(t)

	var res api.OutboxBatchResult
	code := postJSON(t, srv.URL+"/api/v1/outbox/batch", api.OutboxBatch{
		MessageIds: []string{"m1", "m2", "m3", "m2"}, Action: api.OutboxBatchActionArchive,
	}, &res)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if len(res.Ops) != 3 || len(res.Skipped) != 0 {
		t.Fatalf("ops/skipped = %d/%d, want 3/0 (a repeated id counts once)", len(res.Ops), len(res.Skipped))
	}
	for i, id := range []string{"m1", "m2", "m3"} {
		op := res.Ops[i]
		if op.MessageId != id || op.Kind != api.OutboxKindMove || op.DestinationFolderId == nil || *op.DestinationFolderId != "archive-1" {
			t.Errorf("ops[%d] = %+v, want a move of %s to archive-1, in the order given", i, op, id)
		}
	}
	if n := queuedCount(t, srv); n != 3 {
		t.Errorf("%d ops are live, want 3", n)
	}
}

// A message that cannot take the action is named with its reason, and the rest
// still go: the operator sees per message what did not happen.
func TestBatchSkipsWhatCannotBeDoneAndSaysWhy(t *testing.T) {
	t.Parallel()
	srv, dbs := batchServer(t)
	mustMessage(t, dbs, inboxMessage("filed", "acct-1", "archive-1", testNow, true))

	var res api.OutboxBatchResult
	code := postJSON(t, srv.URL+"/api/v1/outbox/batch", api.OutboxBatch{
		MessageIds: []string{"m1", "ghost", "filed"}, Action: api.OutboxBatchActionArchive,
	}, &res)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if len(res.Ops) != 1 || res.Ops[0].MessageId != "m1" {
		t.Errorf("ops = %+v, want only m1", res.Ops)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.MessageId] = s.Code
		if s.Message == "" {
			t.Errorf("skipped %s has no message", s.MessageId)
		}
	}
	if reasons["ghost"] != "not_found" || reasons["filed"] != "same_folder" || len(reasons) != 2 {
		t.Errorf("skipped = %v, want ghost not_found and filed same_folder", reasons)
	}
}

// All or nothing against the cap: a batch that does not fit queues none of it.
func TestBatchOverTheQueueCapQueuesNothing(t *testing.T) {
	t.Parallel()
	srv, dbs := batchServer(t)
	for i := 0; i < store.MaxQueuedOps-2; i++ {
		_, _, err := dbs.EnqueueOutbox(t.Context(), store.OutboxOp{
			ID: fmt.Sprintf("fill-%d", i), AccountID: "acct-1", Kind: store.OutboxFlags,
			ContentKey: fmt.Sprintf("fill-key-%d", i), SourceFolderID: "inbox-1",
			Expect: store.OutboxExpect{FlagsAdd: []string{`\Flagged`}}, CreatedAt: testNow,
		})
		if err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	before := queuedCount(t, srv)

	var e api.Error
	code := postJSON(t, srv.URL+"/api/v1/outbox/batch", api.OutboxBatch{
		MessageIds: []string{"m1", "m2", "m3"}, Action: api.OutboxBatchActionArchive,
	}, &e)
	if code != http.StatusConflict || e.Code != "outbox_full" {
		t.Fatalf("status/code = %d/%q, want 409 outbox_full", code, e.Code)
	}
	if after := queuedCount(t, srv); after != before {
		t.Errorf("queue went from %d to %d; a refused batch must queue nothing", before, after)
	}
}

func TestBatchRefusesWhatIsOutOfBounds(t *testing.T) {
	t.Parallel()
	srv, _ := batchServer(t)
	many := make([]string, 201)
	for i := range many {
		many[i] = fmt.Sprintf("m%d", i)
	}
	for name, body := range map[string]api.OutboxBatch{
		"more than 200 messages": {MessageIds: many, Action: api.OutboxBatchActionFlag},
		"no messages":            {MessageIds: []string{}, Action: api.OutboxBatchActionFlag},
		"an unknown action":      {MessageIds: []string{"m1"}, Action: "expunge"},
	} {
		var e api.Error
		if code := postJSON(t, srv.URL+"/api/v1/outbox/batch", body, &e); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", name, code, e.Code)
		}
	}
	if n := queuedCount(t, srv); n != 0 {
		t.Errorf("%d ops queued by refused requests, want 0", n)
	}
}

// A flag is not a move: the same endpoint carries it, and no confirmation-worthy
// folder change is implied.
func TestBatchFlagsAndMarksRead(t *testing.T) {
	t.Parallel()
	srv, _ := batchServer(t)
	var res api.OutboxBatchResult
	code := postJSON(t, srv.URL+"/api/v1/outbox/batch", api.OutboxBatch{
		MessageIds: []string{"m1", "m2"}, Action: api.OutboxBatchActionSeen,
	}, &res)
	if code != http.StatusAccepted || len(res.Ops) != 2 || res.Ops[0].Kind != api.OutboxKindFlags {
		t.Fatalf("seen batch = %d, %+v; want 202 and two flag ops", code, res)
	}
}
