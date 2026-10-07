package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jhillyerd/enmime"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/store"
)

// sendServer is a server with a mutable clock, a deterministic id source and one
// account, so a test can cross the undo deadline without sleeping.
func sendServer(t *testing.T) (*httptest.Server, *store.DBs, *time.Time, *events.Hub) {
	t.Helper()
	now := testNow
	n := 0
	hub := events.New()
	t.Cleanup(hub.Close)
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		s.WithClock(func() time.Time { return now })
		s.WithIDFunc(func() string { n++; return "id-" + strconv.Itoa(n) })
		s.WithEvents(hub)
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	return srv, dbs, &now, hub
}

func sendRequest(id string) api.SendRequest {
	return api.SendRequest{
		Id:        &id,
		AccountId: "acct-1",
		From:      "me@example.com",
		To:        []string{"you@example.com"},
		Subject:   "hello",
		Text:      "**hi** there",
		Markdown:  boolPtr(true),
	}
}

// A send is queued, built into both copies, and given the configured undo
// deadline; nothing goes to SMTP here.
func TestSendQueuesAMessageWithAnUndoDeadline(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)

	var st api.SendStatus
	if code := postJSON(t, srv.URL+"/api/v1/send", sendRequest("s1"), &st); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if st.State != api.SendStateQueued {
		t.Errorf("state = %q, want queued", st.State)
	}
	if st.UndoDeadline == nil || !st.UndoDeadline.Equal(testNow.Add(10*time.Second)) {
		t.Errorf("undoDeadline = %v, want %v", st.UndoDeadline, testNow.Add(10*time.Second))
	}
	if st.MessageId == nil || !strings.HasPrefix(*st.MessageId, "<") {
		t.Errorf("messageId = %v, want an injected <...>", st.MessageId)
	}

	wire, err := enmime.ReadEnvelope(strings.NewReader(string(storedWire(t, dbs, "s1"))))
	if err != nil {
		t.Fatalf("parse wire body: %v", err)
	}
	if wire.GetHeader("Bcc") != "" {
		t.Errorf("wire copy carries Bcc, want none")
	}
	if wire.GetHeader("Subject") != "hello" {
		t.Errorf("wire subject = %q, want hello", wire.GetHeader("Subject"))
	}
	if wire.Text != "**hi** there" {
		t.Errorf("text/plain = %q, want the raw markdown", wire.Text)
	}
	if !strings.Contains(wire.HTML, "<strong>hi</strong>") {
		t.Errorf("text/html did not render markdown: %q", wire.HTML)
	}
}

// A send that came from an autosaved draft records the draft version, so the
// worker removes exactly that server copy after the 250.
func TestSendRecordsTheDraftItCameFrom(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	req := sendRequest("s1")
	draftID := "<draft-1@example.com>"
	req.DraftMessageId = &draftID
	if code := postJSON(t, srv.URL+"/api/v1/send", req, nil); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	row, err := dbs.GetSend(context.Background(), "s1")
	if err != nil {
		t.Fatalf("get send: %v", err)
	}
	if row.DraftMessageID != draftID {
		t.Errorf("draftMessageID = %q, want %q", row.DraftMessageID, draftID)
	}
}

func TestSendRejectsForeignFrom(t *testing.T) {
	t.Parallel()
	srv, _, _, _ := sendServer(t)
	req := sendRequest("s1")
	req.From = "someone-else@example.com"
	var e api.Error
	if code := postJSON(t, srv.URL+"/api/v1/send", req, &e); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if e.Code != "bad_from" {
		t.Errorf("code = %q, want bad_from", e.Code)
	}
}

func TestSendUnknownAccountAndInvalidMessage(t *testing.T) {
	t.Parallel()
	srv, _, _, _ := sendServer(t)

	req := sendRequest("s1")
	req.AccountId = "missing"
	if code := postJSON(t, srv.URL+"/api/v1/send", req, nil); code != http.StatusNotFound {
		t.Errorf("unknown account status = %d, want 404", code)
	}

	bad := sendRequest("s2")
	bad.Subject = "ok\r\nBcc: evil@example.test"
	var e api.Error
	if code := postJSON(t, srv.URL+"/api/v1/send", bad, &e); code != http.StatusBadRequest {
		t.Errorf("injected subject status = %d, want 400", code)
	}
	if e.Code != "invalid_message" {
		t.Errorf("code = %q, want invalid_message", e.Code)
	}
}

func TestSendBodyTooLargeIsRefused(t *testing.T) {
	t.Parallel()
	srv, _, _, _ := sendServer(t)
	req := sendRequest("s1")
	req.Text = strings.Repeat("x", maxSendBodyBytes+1)
	if code := postJSON(t, srv.URL+"/api/v1/send", req, nil); code != http.StatusBadRequest {
		t.Errorf("oversize status = %d, want 400", code)
	}
}

// The client id is the idempotency key: a second tap returns the queued row.
func TestSendIsIdempotentOnTheClientID(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)

	var first, second api.SendStatus
	if code := postJSON(t, srv.URL+"/api/v1/send", sendRequest("tap"), &first); code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", code)
	}
	if code := postJSON(t, srv.URL+"/api/v1/send", sendRequest("tap"), &second); code != http.StatusAccepted {
		t.Fatalf("second status = %d, want 202", code)
	}
	if first.Id != second.Id || first.MessageId == nil || second.MessageId == nil || *first.MessageId != *second.MessageId {
		t.Errorf("second tap created a different send: %+v vs %+v", first, second)
	}
	rows, err := dbs.SendsByAccount(context.Background(), "acct-1", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("stored %d sends, want 1", len(rows))
	}
}

// Undo before the deadline cancels the row and returns the draft; at the
// deadline it is refused.
func TestUndoSendBoundary(t *testing.T) {
	t.Parallel()
	srv, dbs, now, _ := sendServer(t)

	// Two sends with the same deadline: the first is undone a second early, the
	// second is refused exactly at the deadline.
	var st api.SendStatus
	if code := postJSON(t, srv.URL+"/api/v1/send", sendRequest("s1"), &st); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if code := postJSON(t, srv.URL+"/api/v1/send", sendRequest("s2"), &st); code != http.StatusAccepted {
		t.Fatalf("second send status = %d, want 202", code)
	}

	// The second before the deadline succeeds and hands the draft back.
	*now = testNow.Add(9 * time.Second)
	var undone api.SendStatus
	if code := postJSON(t, srv.URL+"/api/v1/send/s1/undo", nil, &undone); code != http.StatusOK {
		t.Fatalf("undo status = %d, want 200", code)
	}
	if undone.State != api.SendStateCancelled {
		t.Errorf("state = %q, want cancelled", undone.State)
	}
	if undone.Draft == nil || !strings.Contains(*undone.Draft, `"subject":"hello"`) {
		t.Errorf("draft = %v, want the stored request back", undone.Draft)
	}
	if _, err := dbs.NextQueuedSend(context.Background(), "acct-1", *now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a cancelled send is still queued: %v", err)
	}

	// A second send is refused at the deadline itself.
	*now = testNow.Add(10 * time.Second)
	var e api.Error
	if code := postJSON(t, srv.URL+"/api/v1/send/s2/undo", nil, &e); code != http.StatusConflict {
		t.Fatalf("undo at the deadline = %d, want 409", code)
	}
	if e.Code != "too_late" {
		t.Errorf("code = %q, want too_late", e.Code)
	}
}

func TestUndoSendUnknownIsNotFound(t *testing.T) {
	t.Parallel()
	srv, _, _, _ := sendServer(t)
	if code := postJSON(t, srv.URL+"/api/v1/send/missing/undo", nil, nil); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

// A delay of 0 disables undo entirely: the row has no deadline and undo is
// immediately too late.
func TestUndoSendDisabledByAZeroDelay(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	if err := dbs.SetUndoSendDelay(context.Background(), "", 0); err != nil {
		t.Fatalf("set delay: %v", err)
	}
	var st api.SendStatus
	if code := postJSON(t, srv.URL+"/api/v1/send", sendRequest("s1"), &st); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if st.UndoDeadline != nil {
		t.Errorf("undoDeadline = %v, want none", st.UndoDeadline)
	}
	var e api.Error
	if code := postJSON(t, srv.URL+"/api/v1/send/s1/undo", nil, &e); code != http.StatusConflict {
		t.Fatalf("undo status = %d, want 409", code)
	}
}

// Listing and fetching expose the live send, and a change publishes the
// send.state hint the compose screen follows.
func TestSendListGetAndHint(t *testing.T) {
	t.Parallel()
	srv, _, _, hub := sendServer(t)
	sub, err := hub.Subscribe()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(sub.Close)

	var st api.SendStatus
	if code := postJSON(t, srv.URL+"/api/v1/send", sendRequest("s1"), &st); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ev, err := sub.Next(ctx)
	if err != nil {
		t.Fatalf("next hint: %v", err)
	}
	if ev.Type != events.SendState {
		t.Errorf("hint = %q, want send.state", ev.Type)
	}

	var list api.SendList
	if code := getJSON(t, srv.URL+"/api/v1/send?account_id=acct-1", &list); code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", code)
	}
	if len(list.Active) != 1 || list.Active[0].Id != "s1" {
		t.Errorf("active = %+v, want s1", list.Active)
	}
	var one api.SendStatus
	if code := getJSON(t, srv.URL+"/api/v1/send/s1", &one); code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", code)
	}
	if one.State != api.SendStateQueued {
		t.Errorf("state = %q, want queued", one.State)
	}
}

// The client id becomes the row's primary key and a URL segment, so it has a
// documented maximum like every other input (STANDARDS 4a).
func TestSendRefusesAnOversizedClientID(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	long := strings.Repeat("x", maxSendIDBytes+1)
	var e api.Error
	if code := postJSON(t, srv.URL+"/api/v1/send", sendRequest(long), &e); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if e.Code != "bad_request" {
		t.Errorf("code = %q, want bad_request", e.Code)
	}
	if rows, _ := dbs.SendsByAccount(context.Background(), "", 0); len(rows) != 0 {
		t.Errorf("%d rows queued for a refused id, want none", len(rows))
	}
	ok := strings.Repeat("x", maxSendIDBytes)
	if code := postJSON(t, srv.URL+"/api/v1/send", sendRequest(ok), nil); code != http.StatusAccepted {
		t.Errorf("an id at the limit = %d, want 202", code)
	}
}

// TestSendBuildsAnHTMLBody: bodyFormat: html narrows the operator's markup and
// derives a readable text/plain alternative, so the two parts cannot disagree.
func TestSendBuildsAnHTMLBody(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	req := sendRequest("s1")
	req.Markdown = nil
	bf := api.Html
	req.BodyFormat = &bf
	req.Text = `<p>Hi <strong>there</strong></p><script>alert(1)</script>`
	if code := postJSON(t, srv.URL+"/api/v1/send", req, nil); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	wire, err := enmime.ReadEnvelope(strings.NewReader(string(storedWire(t, dbs, "s1"))))
	if err != nil {
		t.Fatalf("parse wire body: %v", err)
	}
	if !strings.Contains(wire.HTML, "<strong>there</strong>") {
		t.Errorf("html part lost bold: %q", wire.HTML)
	}
	if strings.Contains(strings.ToLower(wire.HTML), "<script") {
		t.Errorf("html part kept script: %q", wire.HTML)
	}
	if strings.Contains(wire.Text, "<") {
		t.Errorf("text part carries markup: %q", wire.Text)
	}
	if !strings.Contains(wire.Text, "Hi there") {
		t.Errorf("text part = %q, want the derived plain text", wire.Text)
	}
}

// TestSendLegacyMarkdownFlagStillWorks keeps older clients and stored draft JSON
// sending the markdown they always did.
func TestSendLegacyMarkdownFlagStillWorks(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	if code := postJSON(t, srv.URL+"/api/v1/send", sendRequest("s1"), nil); code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	wire, err := enmime.ReadEnvelope(strings.NewReader(string(storedWire(t, dbs, "s1"))))
	if err != nil {
		t.Fatalf("parse wire body: %v", err)
	}
	if !strings.Contains(wire.HTML, "<strong>hi</strong>") {
		t.Errorf("legacy markdown flag did not render: %q", wire.HTML)
	}
}

// The draft Message-ID a send names is stored and later searched for on the
// server, so it has a bound like every other client-sized field.
func TestSendRefusesAnOversizedDraftMessageID(t *testing.T) {
	t.Parallel()
	srv, dbs, _, _ := sendServer(t)
	req := sendRequest("with-draft")
	long := "<" + strings.Repeat("x", 400) + "@example.test>"
	req.DraftMessageId = &long
	if code := postJSON(t, srv.URL+"/api/v1/send", req, nil); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if rows, _ := dbs.SendsByAccount(context.Background(), "", 0); len(rows) != 0 {
		t.Errorf("%d rows queued for a refused draft id, want none", len(rows))
	}
}

// storedWire reads a queued send's SMTP copy back from the body store, since no
// read of the row itself carries the bodies.
func storedWire(t *testing.T, dbs *store.DBs, id string) []byte {
	t.Helper()
	rc, _, err := dbs.OpenSendWire(context.Background(), id)
	if err != nil {
		t.Fatalf("open wire body of %s: %v", id, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read wire body of %s: %v", id, err)
	}
	return b
}

// storedSent reads a queued send's Sent copy (Bcc kept) the same way.
func storedSent(t *testing.T, dbs *store.DBs, id string) []byte {
	t.Helper()
	sb, err := dbs.SendBodyForAppend(context.Background(), id)
	if err != nil {
		t.Fatalf("sent body of %s: %v", id, err)
	}
	return sb.Body
}
