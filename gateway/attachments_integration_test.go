package gateway

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/compose"
	"github.com/AutumnsGrove/Ivy/store"
)

// stageUpload puts bytes in staging directly, as the upload endpoint would.
func stageUpload(t *testing.T, dbs *store.DBs, id, name, mime, content string) {
	t.Helper()
	_, err := dbs.StageUpload(context.Background(), store.Upload{
		ID: id, AccountID: "acct-1", Name: name, MIMEType: mime, CreatedAt: testNow,
	}, strings.NewReader(content), compose.MaxAttachmentBytes)
	if err != nil {
		t.Fatalf("stage %s: %v", id, err)
	}
}

func parseParts(t *testing.T, raw []byte) *enmime.Envelope {
	t.Helper()
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse built message: %v", err)
	}
	return env
}

// A send resolves staged uploads into the builder, so both the wire and the Sent
// copy carry the attachment.
func TestSendWithAttachmentBuildsBothCopies(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	stageUpload(t, dbs, "up-1", "note.txt", "text/plain", "attached bytes")

	req := sendRequest("s1")
	req.Attachments = &[]api.ComposeAttachment{{Id: "up-1"}}
	if code := postJSON(t, srv.URL+"/api/v1/send", req, nil); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	for name, body := range map[string][]byte{"wire": storedWire(t, dbs, "s1"), "sent": storedSent(t, dbs, "s1")} {
		env := parseParts(t, body)
		if len(env.Attachments) != 1 {
			t.Fatalf("%s copy attachments = %d, want 1", name, len(env.Attachments))
		}
		if env.Attachments[0].FileName != "note.txt" || string(env.Attachments[0].Content) != "attached bytes" {
			t.Errorf("%s attachment = %+v", name, env.Attachments[0])
		}
	}
}

// An id that was never staged is a clear refusal, before anything is queued.
func TestSendRejectsUnknownAttachment(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	req := sendRequest("s1")
	req.Attachments = &[]api.ComposeAttachment{{Id: "nope"}}
	var e api.Error
	if code := postJSON(t, srv.URL+"/api/v1/send", req, &e); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if e.Code != "invalid_message" {
		t.Errorf("code = %q, want invalid_message", e.Code)
	}
	if _, err := dbs.GetSend(context.Background(), "s1"); err == nil {
		t.Errorf("a send row was committed despite the bad attachment")
	}
}

// Attachments that together exceed the total cap are refused before any build.
func TestSendRejectsAttachmentsOverTotal(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	half := strings.Repeat("x", compose.MaxTotalAttachmentsBytes/2+1)
	stageUpload(t, dbs, "up-1", "a.bin", "application/octet-stream", half)
	stageUpload(t, dbs, "up-2", "b.bin", "application/octet-stream", half)

	req := sendRequest("s1")
	req.Attachments = &[]api.ComposeAttachment{{Id: "up-1"}, {Id: "up-2"}}
	var e api.Error
	if code := postJSON(t, srv.URL+"/api/v1/send", req, &e); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if e.Code != "invalid_message" {
		t.Errorf("code = %q, want invalid_message", e.Code)
	}
}

// A resumed draft re-materialises its attachments from the stored body, so the
// screen gets fresh ids whose bytes are fetchable.
func TestDraftResumeRematerialisesAttachments(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	mustFolder(t, dbs, store.Folder{ID: "drafts-1", AccountID: "acct-1", Name: "Drafts", Role: store.RoleDrafts})
	stageUpload(t, dbs, "up-1", "chart.png", "image/png", pngMagic+"pixels")

	req := api.DraftRequest{
		Id: strPtr("v1"), AccountId: "acct-1", From: "me@example.com",
		To: []string{"you@example.com"}, Subject: strPtr("with a chart"),
		Text: "see attached", Markdown: boolPtr(true),
		Attachments: &[]api.ComposeAttachment{{Id: "up-1"}},
	}
	var saved api.DraftSummary
	if code := postJSON(t, srv.URL+"/api/v1/drafts", req, &saved); code != http.StatusOK {
		t.Fatalf("save status = %d, want 200", code)
	}

	var resumed api.DraftResume
	if code := getJSON(t, srv.URL+"/api/v1/drafts/"+saved.Id+"?account_id=acct-1", &resumed); code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200", code)
	}
	if resumed.Attachments == nil || len(*resumed.Attachments) != 1 {
		t.Fatalf("attachments = %v, want one", resumed.Attachments)
	}
	att := (*resumed.Attachments)[0]
	if att.Name != "chart.png" || att.Size != int64(len(pngMagic+"pixels")) {
		t.Fatalf("attachment = %+v", att)
	}
	if att.Id == nil || *att.Id == "" || *att.Id == "up-1" {
		t.Fatalf("attachment id = %v, want a fresh staged id", att.Id)
	}
	resp, err := http.Get(srv.URL + "/api/v1/accounts/acct-1/uploads/" + *att.Id)
	if err != nil {
		t.Fatalf("get re-materialised upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-materialised upload status = %d, want 200", resp.StatusCode)
	}
	got := new(bytes.Buffer)
	if _, err := got.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got.Bytes(), []byte(pngMagic+"pixels")) {
		t.Errorf("re-materialised bytes = %q", got.Bytes())
	}
}

// Undo hands the draft back with its attachments still staged, so the compose
// screen can restore them.
func TestUndoKeepsAttachments(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	stageUpload(t, dbs, "up-1", "note.txt", "text/plain", "attached")

	req := sendRequest("s1")
	req.Attachments = &[]api.ComposeAttachment{{Id: "up-1"}}
	if code := postJSON(t, srv.URL+"/api/v1/send", req, nil); code != http.StatusAccepted {
		t.Fatalf("send status = %d, want 202", code)
	}
	var undone api.SendStatus
	if code := postJSON(t, srv.URL+"/api/v1/send/s1/undo", nil, &undone); code != http.StatusOK {
		t.Fatalf("undo status = %d, want 200", code)
	}
	if undone.Draft == nil || !strings.Contains(*undone.Draft, "up-1") {
		t.Fatalf("undone draft = %v, want the attachment id", undone.Draft)
	}
	if code := getJSON(t, srv.URL+"/api/v1/accounts/acct-1/uploads/up-1", nil); code != http.StatusOK {
		t.Errorf("staged upload after undo = %d, want 200", code)
	}
}

// One send exposes its attachments' metadata, so the undo screen can restore
// them with their names and sizes.
func TestGetSendExposesAttachmentInfo(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	stageUpload(t, dbs, "up-1", "note.txt", "text/plain", "attached")

	req := sendRequest("s1")
	req.Attachments = &[]api.ComposeAttachment{{Id: "up-1"}}
	if code := postJSON(t, srv.URL+"/api/v1/send", req, nil); code != http.StatusAccepted {
		t.Fatalf("send status = %d, want 202", code)
	}
	var st api.SendStatus
	if code := getJSON(t, srv.URL+"/api/v1/send/s1", &st); code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", code)
	}
	if st.Attachments == nil || len(*st.Attachments) != 1 {
		t.Fatalf("attachments = %v, want one", st.Attachments)
	}
	att := (*st.Attachments)[0]
	if att.Name != "note.txt" || att.Size != int64(len("attached")) || att.Id == nil || *att.Id != "up-1" {
		t.Errorf("attachment = %+v, want the staged row", att)
	}
}
