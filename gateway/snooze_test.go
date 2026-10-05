package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

func inboxIDs(in api.Inbox) []string {
	out := make([]string, len(in.Items))
	for i, m := range in.Items {
		out[i] = m.Id
	}
	return out
}

func TestSnoozeHidesAndWakesAMessage(t *testing.T) {
	t.Parallel()
	srv, _ := rulesServer(t)

	var before api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox", &before); code != http.StatusOK || len(before.Items) != 3 {
		t.Fatalf("inbox before = %d %d items, want 3", code, len(before.Items))
	}

	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/messages/m1/snooze", map[string]string{"preset": "tomorrow"}, nil); code != http.StatusNoContent {
		t.Fatalf("snooze status = %d, want 204", code)
	}
	var after api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox", &after); code != http.StatusOK {
		t.Fatalf("inbox after status = %d", code)
	}
	if got := inboxIDs(after); len(got) != 2 || got[0] != "m3" || got[1] != "m2" {
		t.Errorf("inbox after = %v, want [m3 m2] without the snoozed m1", got)
	}
	// The snoozed view is how a hidden message is found again.
	var snoozed api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox?folder=snoozed", &snoozed); code != http.StatusOK || len(snoozed.Items) != 1 || snoozed.Items[0].Id != "m1" {
		t.Errorf("snoozed view = %d %v, want [m1]", code, inboxIDs(snoozed))
	}

	if code := sendJSON(t, http.MethodDelete, srv.URL+"/api/v1/messages/m1/snooze", nil, nil); code != http.StatusNoContent {
		t.Fatalf("unsnooze status = %d, want 204", code)
	}
	var woken api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox", &woken); code != http.StatusOK || len(woken.Items) != 3 {
		t.Errorf("inbox after wake = %d items, want 3", len(woken.Items))
	}
}

func TestReadingIsReservedTagAndHides(t *testing.T) {
	t.Parallel()
	srv, dbs := rulesServer(t)
	tag, err := dbs.EnsureReadingTag(context.Background())
	if err != nil {
		t.Fatalf("EnsureReadingTag: %v", err)
	}
	if err := dbs.TagMessage(context.Background(), "acct-1", "ck:m2", tag.ID, store.TagSourceOperator); err != nil {
		t.Fatalf("TagMessage: %v", err)
	}

	var inbox api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox", &inbox); code != http.StatusOK {
		t.Fatalf("inbox status = %d", code)
	}
	if got := inboxIDs(inbox); len(got) != 2 {
		t.Errorf("inbox = %v, want two rows without the Reading mail", got)
	} else {
		for _, id := range got {
			if id == "m2" {
				t.Errorf("inbox = %v, the Reading mail still shows", got)
			}
		}
	}
	if inbox.ReadingWaiting != 1 {
		t.Errorf("readingWaiting = %d, want 1", inbox.ReadingWaiting)
	}

	var feed api.ReadingFeed
	if code := getJSON(t, srv.URL+"/api/v1/reading", &feed); code != http.StatusOK || len(feed.Issues) != 1 {
		t.Fatalf("reading = %d %+v, want one issue", code, feed)
	}
	if feed.Issues[0].Title != "Garden Weekly #3" || feed.Issues[0].Sender == "" {
		t.Errorf("issue = %+v", feed.Issues[0])
	}

	// The tag filter shows exactly the Reading mail.
	var tagged api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox?tag="+tag.ID, &tagged); code != http.StatusOK || len(tagged.Items) != 1 || tagged.Items[0].Id != "m2" {
		t.Errorf("tag filter = %d %v, want [m2]", code, inboxIDs(tagged))
	}
}

// A message's content key is the hash of its Message-ID, so a mailing-list post
// delivered to two of the operator's accounts has the same key in both. Snooze
// and Reading are per account, but the lists of hidden keys were not, so hiding
// the copy in one account hid the other account's copy too.
func twoAccountsSharingAMessage(t *testing.T) (*httptest.Server, *store.DBs) {
	t.Helper()
	srv, dbs := rulesServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-2", Address: "work@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-2", AccountID: "acct-2", Name: "INBOX", Role: store.RoleInbox, UIDValidity: 1, LastSyncAt: testNow})
	mustMessage(t, dbs, store.Message{
		ID: "w2", AccountID: "acct-2", FolderID: "inbox-2", UID: 1,
		ContentKey: "ck:m2", Subject: "Garden Weekly #3", Snippet: "Snippet w2",
		From: store.Address{Address: "news@wildflowers.test"}, Date: testNow,
	})
	return srv, dbs
}

func TestSnoozingOneAccountsCopyKeepsTheOthersInTheCombinedInbox(t *testing.T) {
	t.Parallel()
	srv, _ := twoAccountsSharingAMessage(t)

	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/messages/m2/snooze", map[string]string{"preset": "tomorrow"}, nil); code != http.StatusNoContent {
		t.Fatalf("snooze status = %d, want 204", code)
	}
	var inbox api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox", &inbox); code != http.StatusOK {
		t.Fatalf("inbox status = %d", code)
	}
	var sawSnoozed, sawOther bool
	for _, id := range inboxIDs(inbox) {
		sawSnoozed = sawSnoozed || id == "m2"
		sawOther = sawOther || id == "w2"
	}
	if sawSnoozed {
		t.Errorf("inbox = %v, the snoozed copy still shows", inboxIDs(inbox))
	}
	if !sawOther {
		t.Errorf("inbox = %v, the other account's copy was hidden by someone else's snooze", inboxIDs(inbox))
	}
}

func TestReadingOneAccountsCopyKeepsTheOthersInItsInbox(t *testing.T) {
	t.Parallel()
	srv, dbs := twoAccountsSharingAMessage(t)
	tag, err := dbs.EnsureReadingTag(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := dbs.TagMessage(context.Background(), "acct-1", "ck:m2", tag.ID, store.TagSourceOperator); err != nil {
		t.Fatal(err)
	}

	var inbox api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox?account_id=acct-2", &inbox); code != http.StatusOK {
		t.Fatalf("inbox status = %d", code)
	}
	if got := inboxIDs(inbox); len(got) != 1 || got[0] != "w2" {
		t.Errorf("acct-2 inbox = %v, want its own copy [w2], which was never tagged", got)
	}
	if inbox.ReadingWaiting != 0 {
		t.Errorf("acct-2 readingWaiting = %d, want 0", inbox.ReadingWaiting)
	}
	var feed api.ReadingFeed
	if code := getJSON(t, srv.URL+"/api/v1/reading", &feed); code != http.StatusOK || len(feed.Issues) != 1 {
		t.Errorf("reading = %d %d issues, want only acct-1's tagged copy", code, len(feed.Issues))
	}
}
