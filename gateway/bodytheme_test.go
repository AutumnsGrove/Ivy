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
	mustMessage(t, dbs, store.Message{
		ID: "darkmail", AccountID: "acct-1", FolderID: "inbox-1", UID: 3, ContentKey: "ck:dark",
		BodyHTML: `<table bgcolor="#111111"><tr><td>Already dark</td></tr></table>`, BodyStatus: store.BodyOK,
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

	// The scheme must be a real CSS value (dark or light, never the theme's own
	// name): an invalid one is ignored, the frame's scheme then differs from the
	// page's and the browser paints it opaque white.
	// The one exception is light mail inverted at night: it is drawn as a light
	// page and the whole frame is then flipped, over an opaque canvas of its own.
	for _, tc := range []struct{ theme, id, scheme string }{
		{"night", "plain", "dark"},
		{"night", "darkmail", "dark"},
		{"night", "rich", "light"},
		{"day", "plain", "light"},
		{"day", "darkmail", "light"},
		{"day", "rich", "light"},
	} {
		if got := doc(tc.id, "?theme="+tc.theme); !strings.Contains(got, "html{color-scheme:"+tc.scheme+";") || strings.Contains(got, "color-scheme:"+tc.theme) {
			t.Errorf("%s %s should declare color-scheme:%s: %s", tc.theme, tc.id, tc.scheme, got)
		}
	}

	// Rich mail assumes a light page. At night it is inverted (with its images
	// turned back) so the whole reader is dark; mail that already paints itself
	// dark, and everything by day, is left as the sender made it.
	const invert = "invert(1) hue-rotate(180deg)"
	if rich := doc("rich", "?theme=night"); !strings.Contains(rich, "html{") || strings.Count(rich, invert) != 2 || strings.Contains(rich, paper) {
		t.Errorf("night light mail should be inverted with images restored: %s", rich)
	}
	// Edge-to-edge mail leaves text touching (and, once shrunk, clipped by) the
	// frame's edge, so rich mail gets a slim gutter in every variant.
	for _, id := range []string{"rich", "darkmail"} {
		for _, theme := range []string{"night", "day"} {
			if got := doc(id, "?theme="+theme); !strings.Contains(got, "padding:0 .5rem") {
				t.Errorf("%s %s should have a side gutter: %s", theme, id, got)
			}
		}
	}
	if dark := doc("darkmail", "?theme=night"); strings.Contains(dark, invert) {
		t.Errorf("night mail that is already dark must not be inverted: %s", dark)
	}
	if rich := doc("rich", "?theme=day"); strings.Contains(rich, invert) || strings.Contains(rich, paper) {
		t.Errorf("day mail is left as sent: %s", rich)
	}

	hostile := doc("plain", "?theme="+url.QueryEscape(`</style><script>alert(1)</script>`))
	if strings.Contains(hostile, "<script") || strings.Contains(hostile, "alert(1)") || !strings.Contains(hostile, "#f2eddc") {
		t.Errorf("an unknown theme must fall back to night and never appear in the page: %s", hostile)
	}
}
