package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// manyMessages adds n more messages to the rules fixture's account, each with
// its own content key and a distinct date, and returns their content keys.
func manyMessages(t *testing.T, dbs *store.DBs, n int) []string {
	t.Helper()
	keys := make([]string, n)
	for i := range n {
		id := fmt.Sprintf("bulk-%03d", i)
		keys[i] = "ck:" + id
		mustMessage(t, dbs, store.Message{
			ID: id, AccountID: "acct-1", FolderID: "inbox-1", UID: uint32(1000 + i),
			ContentKey: keys[i], Subject: "Bulk " + id, Snippet: "s", From: store.Address{Address: "bulk@example.test"},
			Date: testNow.Add(-time.Duration(i+1) * time.Minute),
		})
	}
	return keys
}

// walk follows nextCursor from base until it runs out, returning each page's ids.
func walkInbox(t *testing.T, base string) [][]string {
	t.Helper()
	var pages [][]string
	cursor := ""
	for range 20 {
		var in api.Inbox
		u := base
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		if code := getJSON(t, u, &in); code != http.StatusOK {
			t.Fatalf("GET %s = %d", u, code)
		}
		pages = append(pages, inboxIDs(in))
		if in.NextCursor == nil || *in.NextCursor == "" {
			return pages
		}
		cursor = *in.NextCursor
	}
	t.Fatal("the cursor never ran out")
	return nil
}

func assertNoRepeats(t *testing.T, pages [][]string, want int) {
	t.Helper()
	seen := map[string]bool{}
	for _, p := range pages {
		for _, id := range p {
			if seen[id] {
				t.Errorf("%s was listed twice", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != want {
		t.Errorf("%d messages listed across %d pages, want %d", len(seen), len(pages), want)
	}
}

// The tag filter, like every local view, used to answer with the newest 200 and
// no cursor, so anything older was listed nowhere.
func TestTagViewPagesThroughEveryMessage(t *testing.T) {
	t.Parallel()
	srv, dbs := rulesServer(t)
	tag := mustTag(t, dbs, "t-bulk", "bulk")
	for _, key := range manyMessages(t, dbs, 120) {
		if err := dbs.TagMessage(context.Background(), "acct-1", key, tag.ID, store.TagSourceOperator); err != nil {
			t.Fatal(err)
		}
	}
	pages := walkInbox(t, srv.URL+"/api/v1/inbox?tag="+tag.ID)
	assertNoRepeats(t, pages, 120)
	if len(pages) != 3 {
		t.Errorf("%d pages for 120 messages, want 3 (50, 50, 20)", len(pages))
	}
}

func TestSnoozedViewPagesThroughEveryMessage(t *testing.T) {
	t.Parallel()
	srv, dbs := rulesServer(t)
	for _, key := range manyMessages(t, dbs, 120) {
		if err := dbs.SnoozeMessage(context.Background(), "acct-1", key, testNow.Add(24*time.Hour), testNow); err != nil {
			t.Fatal(err)
		}
	}
	pages := walkInbox(t, srv.URL+"/api/v1/inbox?folder=snoozed")
	assertNoRepeats(t, pages, 120)
}

func TestReadingFeedPagesThroughEveryIssue(t *testing.T) {
	t.Parallel()
	srv, dbs := rulesServer(t)
	tag, err := dbs.EnsureReadingTag(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range manyMessages(t, dbs, 120) {
		if err := dbs.TagMessage(context.Background(), "acct-1", key, tag.ID, store.TagSourceOperator); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]bool{}
	cursor, pages := "", 0
	for range 20 {
		var feed api.ReadingFeed
		u := srv.URL + "/api/v1/reading"
		if cursor != "" {
			u += "?cursor=" + url.QueryEscape(cursor)
		}
		if code := getJSON(t, u, &feed); code != http.StatusOK {
			t.Fatalf("GET %s = %d", u, code)
		}
		pages++
		for _, is := range feed.Issues {
			if seen[is.Id] {
				t.Errorf("%s was listed twice", is.Id)
			}
			seen[is.Id] = true
		}
		// The digest counts the whole feed, not the page.
		if feed.Digest != "120 kept out of your inbox" {
			t.Errorf("digest = %q, want the count of the whole feed", feed.Digest)
		}
		if feed.NextCursor == nil || *feed.NextCursor == "" {
			break
		}
		cursor = *feed.NextCursor
	}
	if len(seen) != 120 || pages != 3 {
		t.Errorf("%d issues over %d pages, want 120 over 3", len(seen), pages)
	}
}

func TestAnInvalidCursorIsABadRequest(t *testing.T) {
	t.Parallel()
	srv, _ := rulesServer(t)
	for _, path := range []string{"/api/v1/inbox?folder=snoozed&cursor=!!", "/api/v1/reading?cursor=!!", "/api/v1/people?cursor=!!"} {
		if code := getJSON(t, srv.URL+path, nil); code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, code)
		}
	}
}

// People is the longest list: every address ever seen. It is served 100 at a
// time, most correspondence first, with a cursor.
func TestPeoplePagesInHundreds(t *testing.T) {
	t.Parallel()
	srv, dbs := rulesServer(t)
	for i := range 250 {
		if _, err := dbs.Mirror.Write.ExecContext(context.Background(), `
			INSERT INTO people (address, account_id, name, message_count, first_seen, last_seen, last_subject)
			VALUES (?, 'acct-1', ?, ?, ?, ?, 's')`,
			fmt.Sprintf("p%03d@example.test", i), fmt.Sprintf("P %03d", i), 1000-i,
			"2026-01-01T00:00:00.000000000Z", "2026-02-01T00:00:00.000000000Z"); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]bool{}
	var sizes []int
	var firstCount, lastCount int
	cursor := ""
	for range 10 {
		var page api.PeoplePage
		u := srv.URL + "/api/v1/people"
		if cursor != "" {
			u += "?cursor=" + url.QueryEscape(cursor)
		}
		if code := getJSON(t, u, &page); code != http.StatusOK {
			t.Fatalf("GET %s = %d", u, code)
		}
		sizes = append(sizes, len(page.Items))
		for _, p := range page.Items {
			if seen[p.Id] {
				t.Errorf("%s was listed twice", p.Id)
			}
			seen[p.Id] = true
		}
		if len(sizes) == 1 {
			firstCount = page.Items[0].Count
		}
		lastCount = page.Items[len(page.Items)-1].Count
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 250 || fmt.Sprint(sizes) != "[100 100 50]" {
		t.Errorf("%d people in pages %v, want 250 in [100 100 50]", len(seen), sizes)
	}
	if firstCount != 1000 || lastCount != 751 {
		t.Errorf("counts run %d..%d, want 1000 down to 751 (most correspondence first)", firstCount, lastCount)
	}
}
