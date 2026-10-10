package gateway

import (
	"net/http"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

func needsServer(t *testing.T) (string, *store.DBs) {
	t.Helper()
	srv, dbs := outboxServer(t)
	mustMessage(t, dbs, inboxMessage("m2", "acct-1", "inbox-1", testNow.Add(-1), false))
	flagNeeds(t, dbs, "ck:m1")
	return srv.URL + "/api/v1", dbs
}

// The marker needs its reason on the same rows the inbox already serves, and only a
// flagged message carries one.
func TestInboxAndMessageCarryTheReason(t *testing.T) {
	t.Parallel()
	base, _ := needsServer(t)

	var inbox api.Inbox
	if code := getJSON(t, base+"/inbox", &inbox); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if len(inbox.Items) != 2 || inbox.NeedCount != 1 {
		t.Fatalf("inbox = %+v", inbox)
	}
	flagged, quiet := inbox.Items[0], inbox.Items[1]
	if !flagged.Needs || flagged.NeedsReason == nil || *flagged.NeedsReason != "Ada wants a reply." {
		t.Fatalf("flagged row = %+v", flagged)
	}
	if quiet.Needs || quiet.NeedsReason != nil {
		t.Fatalf("an unflagged row carries a reason: %+v", quiet)
	}

	var msg api.MailMessage
	if code := getJSON(t, base+"/messages/m1", &msg); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !msg.Needs || msg.NeedsReason == nil || *msg.NeedsReason != "Ada wants a reply." {
		t.Fatalf("message = needs %v reason %v", msg.Needs, msg.NeedsReason)
	}
	var sum api.MailSummary
	getJSON(t, base+"/messages/m1/summary", &sum)
	if !sum.Needs || sum.NeedsReason == nil {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestDismissingClearsTheFlagThroughTheAPI(t *testing.T) {
	t.Parallel()
	base, _ := needsServer(t)
	for i := 0; i < 2; i++ { // a double tap is not an error
		if code := doJSON(t, http.MethodPost, base+"/messages/m1/needs/dismiss", nil, nil, nil); code != http.StatusNoContent {
			t.Fatalf("dismiss #%d = %d, want 204", i+1, code)
		}
	}
	var inbox api.Inbox
	getJSON(t, base+"/inbox", &inbox)
	if inbox.NeedCount != 0 || inbox.Items[0].Needs || inbox.Items[0].NeedsReason != nil {
		t.Fatalf("inbox after dismissing = %+v", inbox)
	}
	var msg api.MailMessage
	getJSON(t, base+"/messages/m1", &msg)
	if msg.Needs || msg.NeedsReason != nil {
		t.Fatalf("message after dismissing: needs %v reason %v", msg.Needs, msg.NeedsReason)
	}
	// A message nobody judged can still be dismissed: the click is the operator's.
	if code := doJSON(t, http.MethodPost, base+"/messages/m2/needs/dismiss", nil, nil, nil); code != http.StatusNoContent {
		t.Fatalf("dismissing an unjudged message = %d, want 204", code)
	}
	if code := doJSON(t, http.MethodPost, base+"/messages/nope/needs/dismiss", nil, nil, nil); code != http.StatusNotFound {
		t.Fatalf("dismissing an unknown message = %d, want 404", code)
	}
}
