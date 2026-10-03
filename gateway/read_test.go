package gateway

import (
	"context"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/render"
	"github.com/AutumnsGrove/Ivy/store"
)

// testNow is the clock every gateway test serves from, so the human time in a
// list row is stable.
var testNow = time.Date(2026, 10, 2, 15, 30, 0, 0, time.UTC)

// newSeededServer opens a real store and serves the full handler from a fixed
// clock. It returns the store so a test can seed mail directly, the way sync
// would.
func newSeededServer(t *testing.T) (*httptest.Server, *store.DBs) {
	t.Helper()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	s := New(dbs, "test-version", testStaticFS())
	s.now = func() time.Time { return testNow }
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, dbs
}

func mustAccount(t *testing.T, dbs *store.DBs, a store.Account) {
	t.Helper()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = testNow
	}
	if err := dbs.UpsertAccount(context.Background(), a); err != nil {
		t.Fatalf("UpsertAccount(%s): %v", a.ID, err)
	}
}

func mustFolder(t *testing.T, dbs *store.DBs, f store.Folder) {
	t.Helper()
	if err := dbs.UpsertFolder(context.Background(), f); err != nil {
		t.Fatalf("UpsertFolder(%s): %v", f.ID, err)
	}
}

func mustMessage(t *testing.T, dbs *store.DBs, m store.Message) {
	t.Helper()
	if err := dbs.UpsertMessage(context.Background(), m); err != nil {
		t.Fatalf("UpsertMessage(%s): %v", m.ID, err)
	}
}

func inboxMessage(id, accountID, folderID string, date time.Time, seen bool) store.Message {
	flags := []string{}
	if seen {
		flags = append(flags, `\Seen`)
	}
	return store.Message{
		ID: id, AccountID: accountID, FolderID: folderID,
		UID:        crc32.ChecksumIEEE([]byte(folderID + "/" + id)),
		ContentKey: "ck:" + id, Subject: "Subject " + id, Snippet: "Snippet " + id,
		From: store.Address{Name: "Sender " + id, Address: "sender@example.com"},
		Date: date, Flags: flags,
	}
}

func TestListAccounts(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com", DisplayName: "Autumn", SortOrder: 0, LLMEnabled: true, Icon: "leaf"})
	mustAccount(t, dbs, store.Account{ID: "acct-2", Address: "hello@example.com", SortOrder: 1})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox, LastSyncAt: testNow.Add(-4 * time.Minute)})
	mustFolder(t, dbs, store.Folder{ID: "inbox-2", AccountID: "acct-2", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, inboxMessage("m1", "acct-1", "inbox-1", testNow, false))
	mustMessage(t, dbs, inboxMessage("m2", "acct-1", "inbox-1", testNow, true))
	mustMessage(t, dbs, inboxMessage("m3", "acct-2", "inbox-2", testNow, false))

	var accounts []api.Account
	if code := getJSON(t, srv.URL+"/api/v1/accounts", &accounts); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}

	one := accounts[0]
	if one.Id != "acct-1" || one.Address != "me@example.com" || one.Short != "me@" || one.Initial != "A" {
		t.Errorf("account = %+v", one)
	}
	if one.Slot != 1 || one.Unread != 1 || !one.Smart {
		t.Errorf("slot/unread/smart = %d/%d/%v, want 1/1/true", one.Slot, one.Unread, one.Smart)
	}
	if one.Sync != api.SyncStateOk || one.SyncNote != "Up to date · synced 4 min ago" {
		t.Errorf("sync = %q %q, want ok / synced 4 min ago", one.Sync, one.SyncNote)
	}

	two := accounts[1]
	if two.Slot != 2 || two.Unread != 1 || two.Smart {
		t.Errorf("account = %+v", two)
	}
	if two.Sync != api.SyncStateSyncing || two.SyncNote != "Reading your mailbox, newest first" {
		t.Errorf("sync = %q %q, want syncing / reading", two.Sync, two.SyncNote)
	}
	if two.Progress != nil {
		t.Errorf("Progress = %v, want nil at zero", *two.Progress)
	}
}

func TestListAccountsEmptyIsAnArray(t *testing.T) {
	t.Parallel()
	srv, _ := newSeededServer(t)
	var accounts []api.Account
	if code := getJSON(t, srv.URL+"/api/v1/accounts", &accounts); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if accounts == nil || len(accounts) != 0 {
		t.Errorf("accounts = %v, want an empty array", accounts)
	}
}

func TestListInboxNewestFirstAndCounts(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustAccount(t, dbs, store.Account{ID: "acct-2", Address: "hello@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustFolder(t, dbs, store.Folder{ID: "inbox-2", AccountID: "acct-2", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, inboxMessage("m1", "acct-1", "inbox-1", testNow.Add(-3*time.Hour), false))
	mustMessage(t, dbs, inboxMessage("m2", "acct-2", "inbox-2", testNow.Add(-2*time.Hour), true))
	mustMessage(t, dbs, inboxMessage("m3", "acct-1", "inbox-1", testNow.Add(-time.Hour), true))

	var inbox api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox", &inbox); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(inbox.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(inbox.Items))
	}
	if inbox.Items[0].Id != "m3" || inbox.Items[2].Id != "m1" {
		t.Errorf("order = %s..%s, want m3..m1", inbox.Items[0].Id, inbox.Items[2].Id)
	}
	if inbox.UnreadCount != 1 || inbox.NeedCount != 0 || inbox.ReadingWaiting != 0 {
		t.Errorf("counts = unread %d needs %d reading %d", inbox.UnreadCount, inbox.NeedCount, inbox.ReadingWaiting)
	}
	if inbox.Items[0].Time != "14:30" {
		t.Errorf("time = %q, want 14:30", inbox.Items[0].Time)
	}
	if inbox.Items[0].From != "Sender m3" || inbox.Items[0].Initials != "SM" {
		t.Errorf("from = %q initials = %q", inbox.Items[0].From, inbox.Items[0].Initials)
	}
	if inbox.NextCursor != nil {
		t.Errorf("NextCursor = %q, want nil on a short page", *inbox.NextCursor)
	}
}

func TestListInboxScopedAndPaged(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustFolder(t, dbs, store.Folder{ID: "inbox-2", AccountID: "acct-1", Name: "INBOX2", Role: store.RoleInbox})

	// One page is capped at the store's default limit, so a mailbox just over
	// it exercises the keyset cursor.
	base := testNow.Add(-24 * time.Hour)
	for i := 0; i < 60; i++ {
		folder := "inbox-1"
		if i%2 == 0 {
			folder = "inbox-2"
		}
		mustMessage(t, dbs, inboxMessage("m"+twoDigits(i), "acct-1", folder, base.Add(time.Duration(i)*time.Minute), false))
	}

	first, err := http.Get(srv.URL + "/api/v1/inbox?account_id=acct-1")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	var page api.Inbox
	if err := json.NewDecoder(first.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 50 || page.NextCursor == nil {
		t.Fatalf("first page = %d items, cursor %v; want 50 and a cursor", len(page.Items), page.NextCursor)
	}

	second, err := http.Get(srv.URL + "/api/v1/inbox?account_id=acct-1&cursor=" + *page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	var rest api.Inbox
	if err := json.NewDecoder(second.Body).Decode(&rest); err != nil {
		t.Fatal(err)
	}
	if len(rest.Items) != 10 {
		t.Errorf("second page = %d items, want 10", len(rest.Items))
	}
}

func TestGetMessage(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, store.Message{
		ID: "m1", AccountID: "acct-1", FolderID: "inbox-1", UID: 7, ContentKey: "ck:m1",
		Subject: "A question", Snippet: "Can you help?", Date: testNow.Add(-time.Hour),
		From:     store.Address{Name: "Mara Linden", Address: "mara@example.com"},
		BodyText: "Hi there.\n\nCan you help me?", BodyHTML: "<p>Hi there.</p>",
		Flags: []string{`\Seen`},
	})
	_, err := dbs.Mirror.Write.ExecContext(context.Background(),
		`INSERT INTO needs_me (account_id, content_key, verdict, reason, state) VALUES (?,?,?,?,?)`,
		"acct-1", "ck:m1", "needs", "asks a question", "new")
	if err != nil {
		t.Fatalf("insert needs_me: %v", err)
	}

	var msg api.MailMessage
	if code := getJSON(t, srv.URL+"/api/v1/messages/m1", &msg); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if msg.Id != "m1" || msg.AccountId != "acct-1" || msg.Subject != "A question" {
		t.Errorf("message = %+v", msg)
	}
	if msg.From != "Mara Linden" || msg.Initials != "ML" || msg.Preview != "Can you help?" {
		t.Errorf("from/initials/preview = %q/%q/%q", msg.From, msg.Initials, msg.Preview)
	}
	if msg.ToShort != "me@" || msg.ToFull != "me@example.com" {
		t.Errorf("to = %q / %q", msg.ToShort, msg.ToFull)
	}
	if !msg.Needs || msg.Unread {
		t.Errorf("needs/unread = %v/%v, want true/false", msg.Needs, msg.Unread)
	}
	if msg.Html == nil || *msg.Html != "<p>Hi there.</p>" {
		t.Errorf("html = %v", msg.Html)
	}
	if len(msg.Paragraphs) != 2 || msg.Paragraphs[1] != "Can you help me?" {
		t.Errorf("paragraphs = %q", msg.Paragraphs)
	}
	if msg.Attachments == nil || len(msg.Attachments) != 0 {
		t.Errorf("attachments = %v, want an empty array", msg.Attachments)
	}
}

func TestGetMessageMissing(t *testing.T) {
	t.Parallel()
	srv, _ := newSeededServer(t)
	var body api.Error
	if code := getJSON(t, srv.URL+"/api/v1/messages/nope", &body); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	if body.Code != "not_found" {
		t.Errorf("code = %q, want not_found", body.Code)
	}
}

func TestGetMessageSummary(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, inboxMessage("m1", "acct-1", "inbox-1", testNow, false))

	var summary api.MailSummary
	if code := getJSON(t, srv.URL+"/api/v1/messages/m1/summary", &summary); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if summary.Id != "m1" || summary.Subject != "Subject m1" || !summary.Unread {
		t.Errorf("summary = %+v", summary)
	}
}

func TestMirrorHealth(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox, LastSyncAt: testNow})

	var health api.HealthOverview
	if code := getJSON(t, srv.URL+"/api/v1/mirror/health", &health); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(health.Accounts) != 1 || health.Accounts[0].Id != "acct-1" {
		t.Errorf("accounts = %+v", health.Accounts)
	}
	if health.Storage == "" || health.Storage == "0 B" {
		t.Errorf("storage = %q, want the mirror file size", health.Storage)
	}
}

func TestListInboxBadCursorIsBadRequest(t *testing.T) {
	t.Parallel()
	srv, _ := newSeededServer(t)
	var body api.Error
	if code := getJSON(t, srv.URL+"/api/v1/inbox?cursor=!!!not-base64!!!", &body); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if body.Code != "bad_request" {
		t.Errorf("code = %q, want bad_request", body.Code)
	}
}

func TestUnknownAPIPathsAreJSON(t *testing.T) {
	t.Parallel()
	srv, _ := newSeededServer(t)

	resp, err := http.Get(srv.URL + "/api/v1/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	var body api.Error
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode 404 body: %v", err)
	}
	if body.Code != "not_found" {
		t.Errorf("code = %q, want not_found", body.Code)
	}

	resp, err = http.Post(srv.URL+"/api/v1/version", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("POST Content-Type = %q, want JSON", ct)
	}
}

func TestMessageBodyDocument(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, store.Message{
		ID: "m1", AccountID: "acct-1", FolderID: "inbox-1", UID: 1, ContentKey: "ck:m1",
		BodyHTML: `<p>Hello <a href="https://example.com">there</a></p>`, BodyStatus: store.BodyOK,
	})

	resp, err := http.Get(srv.URL + "/api/v1/messages/m1/body")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	// The policy must be a header, not a <meta>: Chromium ignores a meta CSP in
	// a frame and Playwright cannot see srcdoc subresources (2d finding).
	if got, want := resp.Header.Get("Content-Security-Policy"), render.ContentSecurityPolicy(render.Options{}); got != want {
		t.Errorf("CSP = %q, want %q", got, want)
	}
	body, _ := io.ReadAll(resp.Body)
	doc := string(body)
	if !strings.Contains(doc, "<!doctype html>") || !strings.Contains(doc, "<p>Hello") {
		t.Errorf("document = %q", doc)
	}
}

func TestMessageBodyDocumentForTooLargeMessage(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, store.Message{
		ID: "m2", AccountID: "acct-1", FolderID: "inbox-1", UID: 2, ContentKey: "ck:m2",
		BodyStatus: store.BodyTooLarge,
	})

	resp, err := http.Get(srv.URL + "/api/v1/messages/m2/body")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "too large") {
		t.Errorf("document = %q, want the too-large note", body)
	}
}

// inlineMessage is a raw message with a text body, a file attachment and an
// inline cid image, the parts the sanitizer rewrote cid: sources against.
func inlineMessage() []byte {
	return []byte("From: a@example.com\r\nSubject: parts\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
		"--outer\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain body\r\n" +
		"--outer\r\nContent-Type: text/plain; name=\"note.txt\"\r\nContent-Disposition: attachment; filename=\"note.txt\"\r\n\r\nattachment bytes\r\n" +
		"--outer\r\nContent-Type: image/png; name=\"logo.png\"\r\nContent-Disposition: inline; filename=\"logo.png\"\r\nContent-ID: <logo@example>\r\n\r\ntiny png\r\n" +
		"--outer--\r\n")
}

func TestInlineImageIsServed(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, store.Message{
		ID: "m1", AccountID: "acct-1", FolderID: "inbox-1", UID: 1, ContentKey: "ck:m1",
		RawBlob: inlineMessage(), HasAttachments: true, BodyStatus: store.BodyOK,
	})

	resp, err := http.Get(srv.URL + "/api/v1/messages/m1/inline/logo@example")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "tiny png" {
		t.Errorf("body = %q", body)
	}

	if code := getJSON(t, srv.URL+"/api/v1/messages/m1/inline/missing", nil); code != http.StatusNotFound {
		t.Errorf("missing cid status = %d, want 404", code)
	}
}

func TestAttachmentIsServedFromSpool(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})

	// A large message lives on disk; serving it must read the file, not a blob.
	rel := filepath.Join("spool", "inbox-1", "1.eml")
	if err := os.MkdirAll(filepath.Join(dbs.Dir, "spool", "inbox-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbs.Dir, rel), inlineMessage(), 0o600); err != nil {
		t.Fatal(err)
	}
	mustMessage(t, dbs, store.Message{
		ID: "m1", AccountID: "acct-1", FolderID: "inbox-1", UID: 1, ContentKey: "ck:m1",
		RawPath: rel, HasAttachments: true, BodyStatus: store.BodyOK,
	})

	resp, err := http.Get(srv.URL + "/api/v1/messages/m1/attachments/2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "note.txt") {
		t.Errorf("Content-Disposition = %q, want the file name", cd)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "attachment bytes" {
		t.Errorf("body = %q", body)
	}

	resp, err = http.Get(srv.URL + "/api/v1/messages/m1/inline/logo@example")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("inline status = %d, want 200", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	if string(body) != "tiny png" {
		t.Errorf("inline body = %q", body)
	}
}

func TestMessageListsAttachments(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, store.Message{
		ID: "m1", AccountID: "acct-1", FolderID: "inbox-1", UID: 1, ContentKey: "ck:m1",
		RawBlob: inlineMessage(), HasAttachments: true,
	})

	var msg api.MailMessage
	if code := getJSON(t, srv.URL+"/api/v1/messages/m1", &msg); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(msg.Attachments) != 2 {
		t.Fatalf("attachments = %+v, want the file and the inline image", msg.Attachments)
	}
	var file, image *api.Attachment
	for i := range msg.Attachments {
		switch msg.Attachments[i].Id {
		case "2":
			file = &msg.Attachments[i]
		case "3":
			image = &msg.Attachments[i]
		}
	}
	if file == nil || file.Name != "note.txt" || file.Kind != api.File {
		t.Errorf("file attachment = %+v", file)
	}
	if image == nil || image.Kind != api.Image || image.Name != "logo.png" {
		t.Errorf("image attachment = %+v", image)
	}
}

func TestMessageListsAttachmentsFromTheTable(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, store.Message{
		ID: "m1", AccountID: "acct-1", FolderID: "inbox-1", UID: 1, ContentKey: "ck:m1",
		HasAttachments: true,
	})
	// No raw message at all: the listing must come from the stored rows.
	if err := dbs.ReplaceMessageAttachments(context.Background(), "m1", []store.Attachment{
		{Filename: "note.txt", MIMEType: "text/plain", Size: 16, ContentHash: "h", StoragePath: "2"},
		{Filename: "logo.png", MIMEType: "image/png", Size: 8, ContentHash: "h2", CID: "logo@example", StoragePath: "3"},
	}); err != nil {
		t.Fatalf("seed attachments: %v", err)
	}

	var msg api.MailMessage
	if code := getJSON(t, srv.URL+"/api/v1/messages/m1", &msg); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(msg.Attachments) != 2 {
		t.Fatalf("attachments = %+v, want the two stored rows", msg.Attachments)
	}
	if msg.Attachments[0].Id != "2" || msg.Attachments[0].Name != "note.txt" || msg.Attachments[0].Kind != api.File {
		t.Errorf("file = %+v", msg.Attachments[0])
	}
	if msg.Attachments[1].Kind != api.Image {
		t.Errorf("image = %+v", msg.Attachments[1])
	}
}

func TestPartsAreServedFromTheTable(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, store.Message{
		ID: "m1", AccountID: "acct-1", FolderID: "inbox-1", UID: 1, ContentKey: "ck:m1",
		RawBlob: inlineMessage(), HasAttachments: true,
	})
	if err := dbs.ReplaceMessageAttachments(context.Background(), "m1", []store.Attachment{
		{Filename: "note.txt", MIMEType: "text/plain", Size: 16, ContentHash: "h", StoragePath: "2"},
		{Filename: "logo.png", MIMEType: "image/png", Size: 8, ContentHash: "h2", CID: "logo@example", StoragePath: "3"},
	}); err != nil {
		t.Fatalf("seed attachments: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/v1/messages/m1/attachments/2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("attachment status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "attachment bytes" {
		t.Errorf("attachment body = %q", body)
	}

	resp, err = http.Get(srv.URL + "/api/v1/messages/m1/inline/logo@example")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("inline status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("inline Content-Type = %q", ct)
	}
	body, _ = io.ReadAll(resp.Body)
	if string(body) != "tiny png" {
		t.Errorf("inline body = %q", body)
	}
}

func twoDigits(n int) string {
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
