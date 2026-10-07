package gateway

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/store"
)

// Issue #9: the body was a white box in the night theme. Plain text takes the
// reader's own colours; rich HTML, which assumes a light page, sits on a paper
// sheet instead of being inverted. The theme arrives as a query value, so it is
// matched against the two known themes and never echoed.
func TestMessageBodyDocumentFollowsTheTheme(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, store.Message{
		ID: "plain", AccountID: "acct-1", FolderID: "inbox-1", UID: 1, ContentKey: "ck:plain",
		BodyText: "Testing testing", BodyStatus: store.BodyOK,
	})
	mustMessage(t, dbs, store.Message{
		ID: "rich", AccountID: "acct-1", FolderID: "inbox-1", UID: 2, ContentKey: "ck:rich",
		BodyHTML: `<p>Newsletter</p>`, BodyStatus: store.BodyOK,
	})
	doc := func(id, query string) string {
		t.Helper()
		resp, err := http.Get(srv.URL + "/api/v1/messages/" + id + "/body" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	const paper = "#fbf8ef"

	night := doc("plain", "?theme=night")
	if !strings.Contains(night, "#f2eddc") || !strings.Contains(night, "background:transparent") || strings.Contains(night, paper) {
		t.Errorf("night plain text should take the night text colour on a transparent page: %s", night)
	}
	day := doc("plain", "?theme=day")
	if !strings.Contains(day, "#2a2014") || strings.Contains(day, paper) {
		t.Errorf("day plain text should take the day text colour: %s", day)
	}
	if got := doc("plain", ""); !strings.Contains(got, "#f2eddc") {
		t.Errorf("no theme should mean the app's default, night: %s", got)
	}

	for _, theme := range []string{"night", "day"} {
		rich := doc("rich", "?theme="+theme)
		// The root keeps the app's colour-scheme: a frame whose scheme differs from
		// its page is painted opaque, which would bring the white box back.
		if !strings.Contains(rich, paper) || !strings.Contains(rich, "html{color-scheme:"+theme) {
			t.Errorf("%s rich HTML should sit on the paper sheet under the app's scheme: %s", theme, rich)
		}
	}

	hostile := doc("plain", "?theme="+url.QueryEscape(`</style><script>alert(1)</script>`))
	if strings.Contains(hostile, "<script") || strings.Contains(hostile, "alert(1)") || !strings.Contains(hostile, "#f2eddc") {
		t.Errorf("an unknown theme must fall back to night and never appear in the page: %s", hostile)
	}
}
