package gateway

import (
	"context"
	"net/http"
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
