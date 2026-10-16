package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

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

type fakeBar struct{ bar store.UrgencyBar }

func (f fakeBar) UrgencyBar() store.UrgencyBar { return f.bar }

// viewServer has three open verdicts: an older one Jev rated urgent, and two newer
// ones it did not.
func viewServer(t *testing.T, bar *fakeBar) (string, *store.DBs) {
	t.Helper()
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		if bar != nil {
			s.WithNeedsAttention(*bar)
		}
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	for i, id := range []string{"old", "mid", "new"} {
		mustMessage(t, dbs, inboxMessage(id, "acct-1", "inbox-1", testNow.Add(time.Duration(i)*time.Hour), false))
		flagNeeds(t, dbs, "ck:"+id)
	}
	if err := dbs.PutDecisions(context.Background(), []store.Decision{{
		AccountID: "acct-1", ContentKey: "ck:old", QuestionID: "urgency", InstructionHash: "u1", Model: "jev-latest",
		Choice: "high", Probabilities: map[string]float64{"high": 0.95}, Confidence: 0.95, DecidedAt: testNow,
	}}); err != nil {
		t.Fatal(err)
	}
	return srv.URL + "/api/v1", dbs
}

var testBar = fakeBar{bar: store.UrgencyBar{QuestionID: "urgency", Hash: "u1", Model: "jev-latest", High: "high", Threshold: 0.8}}

func ids(items []api.MailSummary) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Id
	}
	return out
}

func TestNeedsAttentionViewListsUrgentFirstWithReasons(t *testing.T) {
	t.Parallel()
	base, _ := viewServer(t, &testBar)
	var page api.Inbox
	if code := getJSON(t, base+"/needs-attention", &page); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got := ids(page.Items); len(got) != 3 || got[0] != "old" || got[1] != "new" || got[2] != "mid" {
		t.Fatalf("order = %v, want the urgent old one first, then newest first", got)
	}
	if page.NeedCount != 3 {
		t.Fatalf("needCount = %d, want 3", page.NeedCount)
	}
	for _, it := range page.Items {
		if !it.Needs || it.NeedsReason == nil || *it.NeedsReason == "" {
			t.Fatalf("every row in this view carries its reason: %+v", it)
		}
	}
}

// With nothing wired the view is the same list, plainly newest first: urgency is an
// ordering nicety and its absence must not break the view.
func TestNeedsAttentionViewWorksWithoutAnUrgencySource(t *testing.T) {
	t.Parallel()
	base, _ := viewServer(t, nil)
	var page api.Inbox
	getJSON(t, base+"/needs-attention", &page)
	if got := ids(page.Items); len(got) != 3 || got[0] != "new" || got[2] != "old" {
		t.Fatalf("order = %v, want newest first", got)
	}
}

func TestNeedsAttentionViewPagesAndRejectsABadCursor(t *testing.T) {
	t.Parallel()
	base, _ := viewServer(t, &testBar)
	var seen []string
	cursor := ""
	for i := 0; i < 5; i++ {
		var page api.Inbox
		url := base + "/needs-attention?limit=1"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		if code := getJSON(t, url, &page); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		seen = append(seen, ids(page.Items)...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 3 || seen[0] != "old" || seen[1] != "new" || seen[2] != "mid" {
		t.Fatalf("paged = %v, want all three once, in order", seen)
	}
	if code := getJSON(t, base+"/needs-attention?cursor=!!!", nil); code != http.StatusBadRequest {
		t.Fatalf("a bad cursor = %d, want 400", code)
	}
	if code := getJSON(t, base+"/needs-attention?limit=0", nil); code != http.StatusBadRequest {
		t.Fatalf("limit 0 = %d, want 400", code)
	}
}

// Snoozed mail is hidden from the view as it is from the inbox, and a dismissed or
// handled message leaves it.
func TestNeedsAttentionViewHidesSnoozedAndDismissed(t *testing.T) {
	t.Parallel()
	base, dbs := viewServer(t, &testBar)
	if err := dbs.DismissNeedsMe(context.Background(), "acct-1", "ck:mid"); err != nil {
		t.Fatal(err)
	}
	if code := doJSON(t, http.MethodPost, base+"/messages/new/snooze", map[string]string{"preset": "tomorrow"}, nil, nil); code != http.StatusNoContent {
		t.Fatalf("snooze setup = %d, want 204", code)
	}
	var page api.Inbox
	getJSON(t, base+"/needs-attention", &page)
	if got := ids(page.Items); len(got) != 1 || got[0] != "old" {
		t.Fatalf("items = %v, want only the one neither dismissed nor snoozed", got)
	}
}

// The "needs you" marker and the placed "Needs you" tag are one fact (clearing either
// is the dismissal), so a row must show it once and leave the tag slot to real tags.
func TestThePlacedNeedsTagIsNotShownTwice(t *testing.T) {
	t.Parallel()
	base, dbs := needsServer(t)
	ctx := context.Background()
	if err := dbs.PlaceNeedsTag(ctx, "acct-1", "ck:m1"); err != nil {
		t.Fatal(err)
	}

	var inbox api.Inbox
	getJSON(t, base+"/inbox", &inbox)
	if row := inbox.Items[0]; !row.Needs || row.Tag != nil {
		t.Fatalf("row = needs %v tag %v; the marker is shown, so the tag must not repeat it", row.Needs, row.Tag)
	}
	var msg api.MailMessage
	getJSON(t, base+"/messages/m1", &msg)
	if !msg.Needs || msg.Tag != nil {
		t.Fatalf("message = needs %v tag %v", msg.Needs, msg.Tag)
	}
	var sum api.MailSummary
	getJSON(t, base+"/messages/m1/summary", &sum)
	if sum.Tag != nil {
		t.Fatalf("summary tag = %v", *sum.Tag)
	}

	// A real tag on the same message still shows, whichever order they were added in.
	tag, err := dbs.CreateTag(ctx, "t-zzz", "Zebra", "#aabbcc")
	if err != nil {
		t.Fatal(err)
	}
	if err := dbs.TagMessage(ctx, "acct-1", "ck:m1", tag.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	getJSON(t, base+"/inbox", &inbox)
	if row := inbox.Items[0]; row.Tag == nil || *row.Tag != "Zebra" {
		t.Fatalf("with a real tag as well, row tag = %v, want Zebra", row.Tag)
	}
	getJSON(t, base+"/messages/m1", &msg)
	if msg.Tag == nil || *msg.Tag != "Zebra" {
		t.Fatalf("message tag = %v, want Zebra", msg.Tag)
	}
}
