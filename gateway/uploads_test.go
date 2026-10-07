package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// postRaw sends a raw request body with the given content type and query.
func postRaw(t *testing.T, url, contentType string, body []byte, out any) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

// The upload endpoint stages bytes, serves them back for a thumbnail, and frees
// them on delete.
func TestUploadAttachmentRoundTrips(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com", SortOrder: 0})

	content := []byte(pngMagic + "an image")
	var up api.Upload
	code := postRaw(t, srv.URL+"/api/v1/accounts/acct-1/uploads?name=photo.png", "image/png", content, &up)
	if code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", code)
	}
	if up.Id == "" || up.Name != "photo.png" || up.Mime != "image/png" || up.Size != int64(len(content)) {
		t.Fatalf("upload = %+v", up)
	}

	resp, err := http.Get(srv.URL + "/api/v1/accounts/acct-1/uploads/" + up.Id)
	if err != nil {
		t.Fatalf("GET upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content type = %q, want image/png", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Errorf("disposition = %q, want inline for a raster image", cd)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, content) {
		t.Errorf("bytes = %q, want the staged content", got)
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete, srv.URL+"/api/v1/accounts/acct-1/uploads/"+up.Id, nil)
	del, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}
	if code := getJSON(t, srv.URL+"/api/v1/accounts/acct-1/uploads/"+up.Id, nil); code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", code)
	}
}

// A file over the cap is refused with 413 and nothing is staged.
func TestUploadAttachmentRefusesOversize(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com", SortOrder: 0})

	body := bytes.Repeat([]byte("x"), 25<<20+1)
	if code := postRaw(t, srv.URL+"/api/v1/accounts/acct-1/uploads?name=big.bin", "application/octet-stream", body, nil); code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", code)
	}
}

// A dangerous type or extension, and a header-hostile name, are refused.
func TestUploadAttachmentRejectsBadInput(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com", SortOrder: 0})

	cases := []struct {
		url  string
		ct   string
		want int
	}{
		{srv.URL + "/api/v1/accounts/acct-1/uploads?name=evil.exe", "application/octet-stream", http.StatusBadRequest},
		{srv.URL + "/api/v1/accounts/acct-1/uploads?name=page.html", "text/html", http.StatusBadRequest},
		{srv.URL + "/api/v1/accounts/acct-1/uploads?name=logo.svg", "image/svg+xml", http.StatusBadRequest},
		{srv.URL + "/api/v1/accounts/acct-1/uploads?name=a%0d%0aBcc%3A+x%40y", "text/plain", http.StatusBadRequest},
		{srv.URL + "/api/v1/accounts/acct-1/uploads", "text/plain", http.StatusBadRequest},
	}
	for _, tc := range cases {
		if code := postRaw(t, tc.url, tc.ct, []byte("data"), nil); code != tc.want {
			t.Errorf("POST %s = %d, want %d", tc.url, code, tc.want)
		}
	}
}

// An unknown account is a 404, so an upload can never be staged for someone
// else's mailbox.
func TestUploadAttachmentUnknownAccount(t *testing.T) {
	t.Parallel()
	srv, _ := newSeededServer(t)
	if code := postRaw(t, srv.URL+"/api/v1/accounts/nope/uploads?name=a.txt", "text/plain", []byte("x"), nil); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

// rawWithAttachment is a minimal multipart message whose second part is the
// attachment row a from-mail copy streams.
const rawWithAttachment = "From: sender@example.com\r\n" +
	"To: me@example.com\r\n" +
	"Subject: with a file\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"b\"\r\n" +
	"\r\n" +
	"--b\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"the body\r\n" +
	"--b\r\n" +
	"Content-Type: text/plain; name=\"doc.txt\"\r\n" +
	"Content-Disposition: attachment; filename=\"doc.txt\"\r\n" +
	"\r\n" +
	"file contents\r\n" +
	"--b--\r\n"

func seedMailWithAttachment(t *testing.T, dbs *store.DBs) {
	t.Helper()
	ctx := context.Background()
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com", SortOrder: 0})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "Inbox", Role: store.RoleInbox})
	m := inboxMessage("m1", "acct-1", "inbox-1", testNow, false)
	m.RawBlob = []byte(rawWithAttachment)
	mustMessage(t, dbs, m)
	if err := dbs.ReplaceMessageAttachments(ctx, "m1", []store.Attachment{{
		Filename: "doc.txt", MIMEType: "text/plain", Size: int64(len("file contents")), StoragePath: "2",
	}}); err != nil {
		t.Fatalf("attachments: %v", err)
	}
}

// "From your mail" lists a mirrored attachment, and a copy stages it server-side
// with no browser upload.
func TestMailAttachmentsListAndCopy(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	seedMailWithAttachment(t, dbs)

	var list api.MailAttachmentList
	if code := getJSON(t, srv.URL+"/api/v1/accounts/acct-1/mail-attachments", &list); code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", code)
	}
	if len(list.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want one", list.Attachments)
	}
	if list.Attachments[0].Name != "doc.txt" || list.Attachments[0].Path != "2" {
		t.Errorf("row = %+v", list.Attachments[0])
	}

	var up api.Upload
	code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/accounts/acct-1/uploads/from-mail",
		api.UploadFromMailInput{MessageId: "m1", Path: "2"}, &up, nil)
	if code != http.StatusCreated {
		t.Fatalf("copy status = %d, want 201", code)
	}
	if up.Name != "doc.txt" || up.Mime != "text/plain" || up.Size != int64(len("file contents")) {
		t.Fatalf("copy = %+v", up)
	}
	resp, err := http.Get(srv.URL + "/api/v1/accounts/acct-1/uploads/" + up.Id)
	if err != nil {
		t.Fatalf("get copy: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != "file contents" {
		t.Errorf("copy bytes = %q, want the mirrored part", got)
	}
}

// A trailing space or dot hides the extension from path.Ext but not from the
// systems that strip it, and a malformed Content-Type parameter makes
// ParseMediaType report an error alongside the right media type.
func TestAttachmentDenyListSurvivesDisguises(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, mime string }{
		{"evil.exe ", "application/octet-stream"},
		{"evil.exe.", "application/octet-stream"},
		{"evil.EXE\t", "application/octet-stream"},
		{"notes.txt", "text/html; =x"},
		{"notes.txt", "TEXT/HTML ; charset"},
	}
	for _, tc := range cases {
		if err := checkAttachmentType(tc.name, tc.mime); err == nil {
			t.Errorf("checkAttachmentType(%q, %q) allowed it, want a refusal", tc.name, tc.mime)
		}
	}
}

// Copying a mirrored attachment is staging for send like any upload, so the
// deny list applies to it.
func TestUploadFromMailAppliesTheDenyList(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	seedMailWithAttachment(t, dbs)
	if err := dbs.ReplaceMessageAttachments(context.Background(), "m1", []store.Attachment{{
		Filename: "setup.exe", MIMEType: "application/x-msdownload", Size: 13, StoragePath: "2",
	}}); err != nil {
		t.Fatalf("attachments: %v", err)
	}
	code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/accounts/acct-1/uploads/from-mail",
		api.UploadFromMailInput{MessageId: "m1", Path: "2"}, nil, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("copy status = %d, want 400", code)
	}
}
