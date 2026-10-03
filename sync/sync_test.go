package sync_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"go.uber.org/goleak"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func newWorld(t *testing.T) *mailworld.World {
	t.Helper()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func newStore(t *testing.T) *store.DBs {
	t.Helper()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	return dbs
}

func accountFor(t *testing.T, w *mailworld.World, id, address, password string) ivysync.Account {
	t.Helper()
	host, portStr, err := net.SplitHostPort(w.IMAPAddr())
	if err != nil {
		t.Fatalf("split imap addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse imap port: %v", err)
	}
	return ivysync.Account{
		ID: id, Address: address,
		IMAPHost: host, IMAPPort: port,
		Username: address, Password: password,
		Insecure: true, // the fake world speaks plaintext on loopback
	}
}

// mustFolder returns the mirror folder for a name or fails the test.
func mustFolder(t *testing.T, dbs *store.DBs, accountID, name string) store.Folder {
	t.Helper()
	f, err := dbs.GetFolderByName(context.Background(), accountID, name)
	if err != nil {
		t.Fatalf("GetFolderByName(%s): %v", name, err)
	}
	return f
}

func mustMessage(t *testing.T, dbs *store.DBs, folderID string, uid uint32) store.Message {
	t.Helper()
	m, err := dbs.GetMessageByUID(context.Background(), folderID, uid)
	if err != nil {
		t.Fatalf("GetMessageByUID(%s, %d): %v", folderID, uid, err)
	}
	return m
}

func seedAccountRow(t *testing.T, dbs *store.DBs, a store.Account) {
	t.Helper()
	if err := dbs.UpsertAccount(context.Background(), a); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	// The operator's profile is state, not a mirror column, so it has its own writer.
	if a.DisplayName != "" || a.Icon != "" {
		if err := dbs.SetAccountProfile(context.Background(), a.ID, a.DisplayName, a.Icon); err != nil {
			t.Fatalf("SetAccountProfile: %v", err)
		}
	}
}

// TestFetchMirrorsFoldersAndMessages is the core read slice: LIST discovers the
// mailboxes, role heuristics classify them, and envelope+flags+BODY[] land in
// the mirror keyed by (folder, uid).
func TestFetchMirrorsFoldersAndMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	t1 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	t2 := t1.Add(2 * time.Hour)
	raw1 := mailworld.Msg().From("Alice <alice@example.com>").To("me@grove.test").Subject("Hello").Text("hi there").Date(t1).Build()
	raw2 := mailworld.Msg().From("Bob <bob@example.com>").To("me@grove.test").Subject("World").HTML("<p>hi</p>").Date(t2).Build()
	uid1 := acc.Deliver("INBOX", raw1)
	uid2 := acc.Deliver("INBOX", raw2)
	if err := acc.Flag("INBOX", uid2, imap.FlagSeen); err != nil {
		t.Fatalf("flag: %v", err)
	}
	if err := acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create archive: %v", err)
	}
	raw3 := mailworld.Msg().From("Carol <carol@example.com>").Subject("Filed").Text("old").Date(t1.Add(-24 * time.Hour)).Build()
	uid3 := acc.Deliver("Archive", raw3)

	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test", CreatedAt: t1})

	res, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret"))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Folders != 2 || res.Stored != 3 || res.Skipped != 0 {
		t.Errorf("result = %+v, want 2 folders / 3 stored / 0 skipped", res)
	}

	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	if inbox.Role != store.RoleInbox {
		t.Errorf("INBOX role = %q, want %q", inbox.Role, store.RoleInbox)
	}
	if inbox.UIDValidity == 0 || inbox.LastSyncAt.IsZero() {
		t.Errorf("INBOX folder not synced: %+v", inbox)
	}
	archive := mustFolder(t, dbs, "acct-1", "Archive")
	if archive.Role != store.RoleArchive {
		t.Errorf("Archive role = %q, want %q", archive.Role, store.RoleArchive)
	}

	m1 := mustMessage(t, dbs, inbox.ID, uid1)
	if m1.Subject != "Hello" || m1.From.Name != "Alice" || m1.From.Address != "alice@example.com" {
		t.Errorf("m1 header = %+v / %+v, want Hello from Alice", m1.Subject, m1.From)
	}
	if m1.Seen || len(m1.Flags) != 0 {
		t.Errorf("m1 flags = %v, want none", m1.Flags)
	}
	if !bytes.Equal(m1.RawBlob, raw1) {
		t.Errorf("m1 raw blob differs from delivered bytes (%d vs %d)", len(m1.RawBlob), len(raw1))
	}
	if len(m1.ContentKey) != 64 || m1.ContentKey != store.ContentKey(m1.MessageID, nil) {
		t.Errorf("m1 content key = %q, want the Message-ID hash", m1.ContentKey)
	}
	if len(m1.To) != 1 || m1.To[0].Address != "me@grove.test" {
		t.Errorf("m1 To = %+v, want me@grove.test", m1.To)
	}

	m2 := mustMessage(t, dbs, inbox.ID, uid2)
	if !m2.Seen {
		t.Error("m2 Seen = false, want true")
	}
	if m2.Date.Unix() != t2.Unix() {
		t.Errorf("m2 Date = %s, want %s", m2.Date, t2)
	}

	m3 := mustMessage(t, dbs, archive.ID, uid3)
	if m3.Subject != "Filed" {
		t.Errorf("archive message subject = %q, want Filed", m3.Subject)
	}
}

// TestFetchNewestFirstAndResumes proves the checkpoint: a drop mid-fetch leaves
// a newest-first prefix in the mirror, and the next run skips what it already
// has and finishes without duplicates.
func TestFetchNewestFirstAndResumes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		acc.Deliver("INBOX", mailworld.Msg().
			From("sender@example.com").
			Subject("message").
			Date(base.Add(time.Duration(i)*time.Hour)).
			Build())
	}
	fetcher := ivysync.NewFetcher(dbs, ivysync.WithBatchSize(1))

	// LOGIN, LIST, SELECT, then message 1's metadata FETCH and body FETCH, and the
	// connection drops on message 2's metadata FETCH (the sixth command).
	w.Fault(mailworld.DropConnection{After: 6})
	if _, err := fetcher.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err == nil {
		t.Fatal("first Fetch succeeded despite the armed drop, want a partial failure")
	}
	w.ClearFaults()

	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	partial, err := dbs.MessageUIDs(ctx, inbox.ID)
	if err != nil {
		t.Fatalf("MessageUIDs: %v", err)
	}
	if len(partial) != 1 || partial[0] != 5 {
		t.Fatalf("partial UIDs = %v, want [5] (the newest fetched first)", partial)
	}

	res, err := fetcher.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret"))
	if err != nil {
		t.Fatalf("resumed Fetch: %v", err)
	}
	if res.Stored != 4 || res.Skipped != 1 {
		t.Errorf("resumed result = %+v, want 4 stored / 1 skipped", res)
	}
	all, err := dbs.MessageUIDs(ctx, inbox.ID)
	if err != nil {
		t.Fatalf("MessageUIDs: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("UIDs after resume = %v, want all 5 with no duplicates", all)
	}
}

// TestFetchSkipsAlreadyMirrored proves a second full run re-downloads nothing.
func TestFetchSkipsAlreadyMirrored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("one").Build())
	acc.Deliver("INBOX", mailworld.Msg().From("b@example.com").Subject("two").Build())

	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, acct); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	res, err := ivysync.NewFetcher(dbs).Fetch(ctx, acct)
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if res.Stored != 0 || res.Skipped != 2 {
		t.Errorf("second result = %+v, want 0 stored / 2 skipped", res)
	}
}

// TestFetchPreservesExistingDisplayName guards the settings UI's ownership of
// display fields: sync may create the account row but never clobber it.
func TestFetchPreservesExistingDisplayName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{
		ID: "acct-1", Address: "me@grove.test", DisplayName: "Autumn",
		Icon: "leaf", Color: "fern", SortOrder: 3,
	})

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := dbs.GetAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.DisplayName != "Autumn" || got.Icon != "leaf" || got.Color != "fern" || got.SortOrder != 3 {
		t.Errorf("account clobbered: %+v", got)
	}
}

// TestFetchCreatesAccountWhenMissing lets the read path run before the operator
// has customized anything.
func TestFetchCreatesAccountWhenMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := dbs.GetAccount(ctx, "acct-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.Address != "me@grove.test" || got.IMAPPort == 0 || got.Username != "me@grove.test" {
		t.Errorf("created account = %+v, want the connection details", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created account has zero CreatedAt")
	}
}

// TestFetchContentKeyFallsBackToHeaderBlock covers mail with no Message-ID: the
// key must still be stable so derived state can attach to it.
func TestFetchContentKeyFallsBackToHeaderBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	raw := []byte("From: anon@example.com\r\nSubject: No id\r\nDate: Sat, 01 Mar 2026 09:00:00 +0000\r\n\r\nbody\r\n")
	uid := acc.Deliver("INBOX", raw)

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	got := mustMessage(t, dbs, inbox.ID, uid)
	header := raw[:bytes.Index(raw, []byte("\r\n\r\n"))]
	if want := store.ContentKey("", header); got.ContentKey != want {
		t.Errorf("ContentKey = %s, want the header-block hash %s", got.ContentKey, want)
	}
}

// TestFetchCopiesShareContentKey keeps derived state attached across folders:
// the same Message-ID copied to two mailboxes is one message by content key.
func TestFetchCopiesShareContentKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	raw := mailworld.Msg().From("a@example.com").Subject("copy me").Build()
	uidInbox := acc.Deliver("INBOX", raw)
	if err := acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create archive: %v", err)
	}
	uidArchive := acc.Deliver("Archive", raw)

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	archive := mustFolder(t, dbs, "acct-1", "Archive")
	key1 := mustMessage(t, dbs, inbox.ID, uidInbox).ContentKey
	key2 := mustMessage(t, dbs, archive.ID, uidArchive).ContentKey
	if key1 != key2 {
		t.Errorf("copy content keys differ: %s vs %s", key1, key2)
	}
}

// TestFetchParsesRawBodies proves the read path fills the derived fields from
// the raw message (attachments, threading headers, auth results, snippet), not
// just the envelope.
func TestFetchParsesRawBodies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	t1 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	raw := mailworld.Msg().
		From("Alice <alice@example.com>").
		To("me@grove.test").
		Subject("Parsed").
		Date(t1).
		MessageID("<parsed-1@grove.test>").
		Text("plain body here").
		HTML("<p>html body</p>").
		Header("References", "<root@grove.test> <parent@grove.test>").
		Header("In-Reply-To", "<parent@grove.test>").
		Header("Reply-To", "Visitor <visitor@example.com>").
		Header("Delivered-To", "me@grove.test").
		Header("Authentication-Results", "mx.example.net; spf=pass; dkim=pass; dmarc=pass").
		Attach("notes.txt", "text/plain", []byte("attached")).
		Inline("chart.png", "image/png", []byte("pngdata")).
		Build()
	uid := acc.Deliver("INBOX", raw)

	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	acct.TrustedAuthservIDs = []string{"mx.example.net"}
	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, acct); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	m := mustMessage(t, dbs, inbox.ID, uid)

	// The parser enumerated both parts during the walk; sync wrote their rows
	// with the path, decoded size and content hash the reader serves by.
	atts := attachmentsFor(t, dbs, m.ID)
	if len(atts) != 2 {
		t.Fatalf("attachments = %+v, want notes.txt and chart.png", atts)
	}
	notes := atts["notes.txt"]
	if notes.StoragePath == "" || notes.Size != int64(len("attached")) || notes.CID != "" {
		t.Errorf("notes attachment = %+v", notes)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte("attached"))); notes.ContentHash != want {
		t.Errorf("notes hash = %q, want %q", notes.ContentHash, want)
	}
	chart := atts["chart.png"]
	if chart.CID != "chart.png" || chart.Size != int64(len("pngdata")) {
		t.Errorf("chart attachment = %+v", chart)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte("pngdata"))); chart.ContentHash != want {
		t.Errorf("chart hash = %q, want %q", chart.ContentHash, want)
	}

	if !strings.Contains(m.BodyText, "plain body here") {
		t.Errorf("BodyText = %q, want the plain part", m.BodyText)
	}
	if m.Snippet != "plain body here" {
		t.Errorf("Snippet = %q, want the plain preview", m.Snippet)
	}
	if !m.HasAttachments {
		t.Error("HasAttachments = false, want true")
	}
	if !strings.Contains(m.BodyHTML, "<p>html body</p>") {
		t.Errorf("BodyHTML = %q, want the sanitised html part", m.BodyHTML)
	}
	if m.References != "<root@grove.test> <parent@grove.test>" {
		t.Errorf("References = %q, want both ids", m.References)
	}
	if m.InReplyTo != "<parent@grove.test>" {
		t.Errorf("InReplyTo = %q, want the raw header", m.InReplyTo)
	}
	if len(m.ReplyTo) != 1 || m.ReplyTo[0].Name != "Visitor" || m.ReplyTo[0].Address != "visitor@example.com" {
		t.Errorf("ReplyTo = %+v, want Visitor <visitor@example.com>", m.ReplyTo)
	}
	if len(m.DeliveredTo) != 1 || m.DeliveredTo[0].Address != "me@grove.test" {
		t.Errorf("DeliveredTo = %+v, want me@grove.test", m.DeliveredTo)
	}
	if m.AuthResults.SPF != "pass" || m.AuthResults.DKIM != "pass" || m.AuthResults.DMARC != "pass" {
		t.Errorf("AuthResults = %+v, want all pass", m.AuthResults)
	}
	if m.AuthResults.AuthservID != "mx.example.net" {
		t.Errorf("AuthResults.AuthservID = %q, want the trusted server recorded", m.AuthResults.AuthservID)
	}
	if len(m.ParseErrors) != 0 {
		t.Errorf("ParseErrors = %v, want none", m.ParseErrors)
	}
}

// TestFetchSanitizesHostileHTML is the 2d end-to-end slice: the body stored by
// sync is already safe (no script, no event handler, no remote beacon) and its
// cid: images point at the local inline endpoint.
func TestFetchSanitizesHostileHTML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	t1 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	raw := mailworld.Msg().
		From("Attacker <bad@evil.test>").
		To("me@grove.test").
		Subject("Reset your password").
		Date(t1).
		MessageID("<nasty-1@grove.test>").
		HTML(`<p>click <a href="javascript:alert(1)">here</a></p>`+
			`<script>alert(2)</script>`+
			`<img src=x onerror="alert(3)">`+
			`<img src="https://tracker.example/pixel.gif" width="1" height="1">`+
			`<img src="cid:chart.png" alt="chart">`).
		Inline("chart.png", "image/png", []byte("pngdata")).
		Build()
	uid := acc.Deliver("INBOX", raw)

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	m := mustMessage(t, dbs, inbox.ID, uid)

	for _, bad := range []string{"<script", "onerror", "javascript:", "tracker.example"} {
		if strings.Contains(m.BodyHTML, bad) {
			t.Errorf("BodyHTML kept %q:\n%s", bad, m.BodyHTML)
		}
	}
	wantInline := "/api/v1/messages/" + m.ID + "/inline/chart.png"
	if !strings.Contains(m.BodyHTML, wantInline) {
		t.Errorf("BodyHTML = %q, want the cid rewritten to %q", m.BodyHTML, wantInline)
	}
}

// TestFetchIgnoresForgedAuthResults is N9 end to end: a sender-supplied
// Authentication-Results header must not become the trust signal. The account
// trusts a different server, so the forged verdicts are dropped (Purelymail adds
// none of its own, so the naive parser would otherwise believe the attacker).
func TestFetchIgnoresForgedAuthResults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	t1 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	raw := mailworld.Msg().
		From("Attacker <bad@evil.test>").
		To("me@grove.test").
		Subject("Reset your password").
		Date(t1).
		MessageID("<forged-1@grove.test>").
		Header("Authentication-Results", "mx.example.net; spf=pass; dkim=pass; dmarc=pass").
		Text("spoofed").
		Build()
	uid := acc.Deliver("INBOX", raw)

	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	acct.TrustedAuthservIDs = []string{"mail.purelymail.com"}
	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, acct); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	m := mustMessage(t, dbs, inbox.ID, uid)
	if m.AuthResults.SPF != "" || m.AuthResults.DKIM != "" || m.AuthResults.DMARC != "" || m.AuthResults.AuthservID != "" {
		t.Errorf("AuthResults = %+v, want empty: a forged header must not be trusted", m.AuthResults)
	}
	if len(m.AuthResults.Raw) != 1 {
		t.Errorf("AuthResults.Raw = %v, want the raw header kept for debugging", m.AuthResults.Raw)
	}
}

// TestFetchRecordsNonFatalParseErrors keeps a message with a broken part in the
// mirror and records why, instead of dropping it or failing the whole fetch.
func TestFetchRecordsNonFatalParseErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	raw := []byte("From: broken@example.com\r\nSubject: Broken\r\nMessage-ID: <broken-1@grove.test>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"MISSING\"\r\n\r\n" +
		"--MISSING\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nunterminated part")
	uid := acc.Deliver("INBOX", raw)

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	m := mustMessage(t, dbs, inbox.ID, uid)
	if len(m.ParseErrors) == 0 {
		t.Error("ParseErrors is empty, want the missing-boundary error recorded")
	}
}

// TestFetchAuthFailure surfaces a wrapped error and stores nothing.
func TestFetchAuthFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	w.Fault(mailworld.AuthFail{})
	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err == nil {
		t.Fatal("Fetch succeeded under AuthFail, want an error")
	}

	accounts, err := dbs.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts = %d, want the seeded one", len(accounts))
	}
	folders, err := dbs.ListFolders(ctx, "acct-1")
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 0 {
		t.Errorf("folders = %d, want none after a failed login", len(folders))
	}
}

// TestFetchEmptyMailboxes succeeds with no messages and records the folder.
func TestFetchEmptyMailboxes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	res, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret"))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Folders != 1 || res.Stored != 0 {
		t.Errorf("result = %+v, want 1 folder / 0 stored", res)
	}
	if _, err := dbs.GetFolderByName(ctx, "acct-1", "INBOX"); err != nil {
		t.Errorf("INBOX not recorded: %v", err)
	}
}

// TestFetchAccountNotFoundStillWorks makes the "no seeded row" path explicit:
// a missing mirror account is created, never an error.
func TestFetchAccountNotFoundStillWorks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	if _, err := dbs.GetAccount(ctx, "acct-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("precondition: err = %v, want ErrNotFound", err)
	}
	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

// A Date header far in the future (clock-skewed sender or spam) would pin the
// message to the top of the inbox forever, so it sorts by when it arrived
// instead (ARCHITECTURE.md 9a).
func TestFetchSortsFutureDatedMailByInternalDate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	skewed := mailworld.Msg().From("spam@example.com").To("me@grove.test").Subject("Win").Text("x").
		Date(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)).Build()
	uid := acc.Deliver("INBOX", skewed)

	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, acct); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	m := mustMessage(t, dbs, mustFolder(t, dbs, "acct-1", "INBOX").ID, uid)
	if !m.Date.Equal(m.InternalDate) {
		t.Errorf("Date = %v, want the internal date %v for a future-dated message", m.Date, m.InternalDate)
	}
}

// Fetch's context must be able to end a sync whose server has gone quiet; the
// IMAP client itself takes no context, so cancellation has to close the
// connection under it.
func TestFetchStopsWhenContextEndsWhileServerStalls(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	// Every server read takes a second, so an uncancelled sync needs several.
	w.Fault(mailworld.Latency{Delay: time.Second})
	dbs := newStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret"))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Fetch succeeded against a stalled server")
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("Fetch ignored its context while the server was stalled")
	}
}

// The zero value of Account must never send a password in the clear: against a
// plaintext server the TLS handshake fails before any login is attempted.
func TestFetchDefaultsToTLS(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	acct.Insecure = false
	if _, err := ivysync.NewFetcher(dbs).Fetch(context.Background(), acct); err == nil {
		t.Fatal("Fetch logged in over plaintext without Insecure being set")
	}
}

// attachmentMessage builds a text message with one attachment of n bytes.
func attachmentMessage(subject string, n int) []byte {
	return mailworld.Msg().From("Alice <alice@example.com>").To("me@grove.test").Subject(subject).
		Text("the readable part").
		Attach("big.bin", "application/octet-stream", bytes.Repeat([]byte{0x5a}, n)).Build()
}

// attachmentsFor indexes a message's attachment rows by file name.
func attachmentsFor(t *testing.T, dbs *store.DBs, messageID string) map[string]store.Attachment {
	t.Helper()
	atts, err := dbs.ListAttachments(context.Background(), messageID)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	byName := make(map[string]store.Attachment, len(atts))
	for _, a := range atts {
		byName[a.Filename] = a
	}
	return byName
}

func spoolFile(t *testing.T, dbs *store.DBs, m store.Message) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dbs.Dir, filepath.FromSlash(m.RawPath)))
	if err != nil {
		t.Fatalf("read spool file %q: %v", m.RawPath, err)
	}
	return data
}

// A message too big for memory but within the download limit is streamed to a
// file; the database row points at it and holds no raw bytes, and the readable
// text is still parsed out of it.
func TestFetchSpoolsAMidSizeMessageToDisk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	raw := attachmentMessage("mid", 100_000)
	uid := acc.Deliver("INBOX", raw)

	f := ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(4<<10, 1<<20))
	if _, err := f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	m := mustMessage(t, dbs, mustFolder(t, dbs, "acct-1", "INBOX").ID, uid)
	if m.RawPath == "" || len(m.RawBlob) != 0 {
		t.Fatalf("RawPath = %q, len(RawBlob) = %d; want the message on disk and not in the row", m.RawPath, len(m.RawBlob))
	}
	if !bytes.Equal(spoolFile(t, dbs, m), raw) {
		t.Error("the spooled file is not the message the server holds")
	}
	if m.BodyStatus != store.BodyOK || !m.HasAttachments {
		t.Errorf("BodyStatus = %q, HasAttachments = %v", m.BodyStatus, m.HasAttachments)
	}
	if !strings.Contains(m.BodyText, "the readable part") {
		t.Errorf("BodyText = %q, want the text parsed out of the spooled file", m.BodyText)
	}
	if m.Subject != "mid" || m.From.Address != "alice@example.com" {
		t.Errorf("envelope fields lost: %+v", m)
	}
	// A part left on disk is still enumerated and hashed during the walk.
	atts := attachmentsFor(t, dbs, m.ID)
	big, ok := atts["big.bin"]
	if !ok {
		t.Fatalf("attachments = %+v, want big.bin", atts)
	}
	if big.StoragePath == "" || big.Size != 100_000 {
		t.Errorf("big.bin = %+v, want the decoded size", big)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(bytes.Repeat([]byte{0x5a}, 100_000))); big.ContentHash != want {
		t.Errorf("big.bin hash = %q, want %q", big.ContentHash, want)
	}
}

// Above the download limit the body is never requested: the envelope is
// mirrored, the row says why there is no body, and a repeat run leaves it alone.
func TestFetchDoesNotDownloadAnOversizeMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	uid := acc.Deliver("INBOX", attachmentMessage("too big", 200_000))
	f := ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(4<<10, 50<<10))
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	m := mustMessage(t, dbs, mustFolder(t, dbs, "acct-1", "INBOX").ID, uid)
	if m.BodyStatus != store.BodyTooLarge {
		t.Errorf("BodyStatus = %q, want %q", m.BodyStatus, store.BodyTooLarge)
	}
	if m.RawPath != "" || len(m.RawBlob) != 0 || m.BodyText != "" {
		t.Errorf("an undownloaded message has a body: path=%q blob=%d text=%q", m.RawPath, len(m.RawBlob), m.BodyText)
	}
	if m.Subject != "too big" || m.From.Address != "alice@example.com" || m.Size < 200_000 {
		t.Errorf("envelope fields missing: %+v", m)
	}
	if m.ContentKey == "" {
		t.Error("ContentKey is empty; the message needs a stable identity")
	}
	if _, err := os.Stat(filepath.Join(dbs.Dir, "spool")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(dbs.Dir, "spool"))
		if len(entries) != 0 {
			t.Errorf("an oversize message left spool entries: %v", entries)
		}
	}

	res, err := f.Fetch(ctx, acct)
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if res.Stored != 0 || res.Skipped != 1 {
		t.Errorf("second run = %+v, want the message skipped", res)
	}
}

// All three tiers in one folder and one batch.
func TestFetchHandlesEveryTierInOneBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	small := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").To("me@grove.test").Subject("small").Text("tiny").Build())
	mid := acc.Deliver("INBOX", attachmentMessage("mid", 30_000))
	huge := acc.Deliver("INBOX", attachmentMessage("huge", 300_000))

	f := ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(4<<10, 100<<10))
	res, err := f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret"))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Stored != 3 {
		t.Errorf("Stored = %d, want 3", res.Stored)
	}
	folder := mustFolder(t, dbs, "acct-1", "INBOX").ID
	ms, mm, mh := mustMessage(t, dbs, folder, small), mustMessage(t, dbs, folder, mid), mustMessage(t, dbs, folder, huge)
	if len(ms.RawBlob) == 0 || ms.RawPath != "" || ms.BodyStatus != store.BodyOK {
		t.Errorf("small: blob=%d path=%q status=%q; want it in the database", len(ms.RawBlob), ms.RawPath, ms.BodyStatus)
	}
	if mm.RawPath == "" || len(mm.RawBlob) != 0 || mm.BodyStatus != store.BodyOK {
		t.Errorf("mid: blob=%d path=%q status=%q; want it on disk", len(mm.RawBlob), mm.RawPath, mm.BodyStatus)
	}
	if mh.BodyStatus != store.BodyTooLarge || mh.RawPath != "" || len(mh.RawBlob) != 0 {
		t.Errorf("huge: blob=%d path=%q status=%q; want it not downloaded", len(mh.RawBlob), mh.RawPath, mh.BodyStatus)
	}
}

// The spool path is built from the folder row id (a hash) and the UID, never
// from the account id, which the operator can set to anything.
func TestFetchSpoolPathCannotEscapeTheDataDir(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	uid := acc.Deliver("INBOX", attachmentMessage("mid", 30_000))
	f := ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(4<<10, 1<<20))
	if _, err := f.Fetch(ctx, accountFor(t, w, "../../evil", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m := mustMessage(t, dbs, mustFolder(t, dbs, "../../evil", "INBOX").ID, uid)
	if strings.Contains(m.RawPath, "..") || !strings.HasPrefix(m.RawPath, "spool/") {
		t.Errorf("RawPath = %q, want a path under spool/ with no parent references", m.RawPath)
	}
	_ = spoolFile(t, dbs, m)
}

// writeOld writes a file under the spool and backdates it.
func writeOld(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A crash between creating a temp file and renaming it, or a download whose row
// was never written, leaves a file nothing points at. The sweep removes those,
// and only those: a file a row owns is kept even when its message is disabled,
// because nothing is ever erased.
func TestSweepSpoolRemovesOnlyOrphans(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})
	if err := dbs.UpsertFolder(ctx, store.Folder{ID: "f1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox}); err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(dbs.Dir, "spool", "f1")
	long := 3 * time.Hour

	owned := filepath.Join(spool, "1.eml")
	disabled := filepath.Join(spool, "2.eml")
	orphan := filepath.Join(spool, "3.eml")
	staleTemp := filepath.Join(spool, ".spool-123456")
	inFlight := filepath.Join(spool, "4.eml")          // written moments ago, row not stored yet
	inFlightTemp := filepath.Join(spool, ".spool-999") // a download in progress
	elsewhere := filepath.Join(dbs.Dir, "mirror-notes.txt")
	for _, p := range []string{owned, disabled, orphan, staleTemp} {
		writeOld(t, p, long)
	}
	writeOld(t, inFlight, time.Minute)
	writeOld(t, inFlightTemp, time.Minute)
	writeOld(t, elsewhere, long)

	for _, m := range []store.Message{
		{ID: "m1", UID: 1, RawPath: "spool/f1/1.eml"},
		{ID: "m2", UID: 2, RawPath: "spool/f1/2.eml", DisabledAt: time.Now(), DisabledReason: "expunged"},
	} {
		m.AccountID, m.FolderID, m.ContentKey = "acct-1", "f1", "ck"+m.ID
		if err := dbs.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := ivysync.SweepSpool(ctx, dbs, time.Now())
	if err != nil {
		t.Fatalf("SweepSpool: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (the orphan and the stale temp file)", removed)
	}
	for path, want := range map[string]bool{
		owned: true, disabled: true, orphan: false, staleTemp: false,
		inFlight: true, inFlightTemp: true, elsewhere: true,
	} {
		if got := exists(path); got != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(path), got, want)
		}
	}
}

func TestSweepSpoolWithNoSpoolIsFine(t *testing.T) {
	t.Parallel()
	removed, err := ivysync.SweepSpool(context.Background(), newStore(t), time.Now())
	if err != nil || removed != 0 {
		t.Errorf("SweepSpool on a fresh store = %d, %v; want 0, nil", removed, err)
	}
}

// Fetch sweeps before it downloads, so leftovers never outlive the next run.
func TestFetchSweepsOrphansFirst(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	stale := filepath.Join(dbs.Dir, "spool", "gone", ".spool-1")
	writeOld(t, stale, 5*time.Hour)

	if _, err := ivysync.NewFetcher(dbs).Fetch(context.Background(), accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if exists(stale) {
		t.Error("a stale spool temp file survived a sync run")
	}
}

// TestFetchThreadsConversations proves the read path leaves a usable
// conversation behind: a root and its reply share one thread_id, unrelated mail
// does not, and the threads table carries the summary the reader needs.
func TestFetchThreadsConversations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	root := mailworld.Msg().From("Alice <alice@example.com>").Subject("Plan").
		MessageID("<root@grove.test>").Date(t0).Text("root body").Build()
	reply := mailworld.Msg().From("me@grove.test").Subject("Re: Plan").
		MessageID("<reply@grove.test>").
		Header("References", "<root@grove.test>").Header("In-Reply-To", "<root@grove.test>").
		Date(t0.Add(time.Hour)).Text("reply body").Build()
	other := mailworld.Msg().From("Bob <bob@example.com>").Subject("Unrelated").
		MessageID("<other@grove.test>").Date(t0).Text("other body").Build()
	uidRoot := acc.Deliver("INBOX", root)
	uidReply := acc.Deliver("INBOX", reply)
	uidOther := acc.Deliver("INBOX", other)

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	mRoot := mustMessage(t, dbs, inbox.ID, uidRoot)
	mReply := mustMessage(t, dbs, inbox.ID, uidReply)
	mOther := mustMessage(t, dbs, inbox.ID, uidOther)
	if mRoot.ThreadID == "" {
		t.Fatal("root has no thread id")
	}
	if mReply.ThreadID != mRoot.ThreadID {
		t.Errorf("reply thread = %q, root thread = %q; want the same", mReply.ThreadID, mRoot.ThreadID)
	}
	if mOther.ThreadID == mRoot.ThreadID {
		t.Errorf("unrelated message shares thread %q", mRoot.ThreadID)
	}

	threads, err := dbs.ListThreads(ctx, "acct-1")
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("threads = %+v, want 2", threads)
	}
	// Newest last_date first: the Plan thread.
	if threads[0].SubjectNorm != "Plan" || threads[0].MessageCount != 2 {
		t.Errorf("thread summary = %+v, want Plan with 2 messages", threads[0])
	}
	if threads[0].RootMessageID != "<root@grove.test>" {
		t.Errorf("root message id = %q, want <root@grove.test>", threads[0].RootMessageID)
	}
}

// TestFetchThreadsSubjectFallback covers mail with no threading headers: the
// normalized subject still groups a reply with its base.
func TestFetchThreadsSubjectFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	seedAccountRow(t, dbs, store.Account{ID: "acct-1", Address: "me@grove.test"})

	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	uidA := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("Coffee").
		MessageID("<coffee-a@grove.test>").Date(t0).Build())
	uidB := acc.Deliver("INBOX", mailworld.Msg().From("b@example.com").Subject("Re: Coffee").
		MessageID("<coffee-b@grove.test>").Date(t0.Add(time.Minute)).Build())

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	a := mustMessage(t, dbs, inbox.ID, uidA)
	b := mustMessage(t, dbs, inbox.ID, uidB)
	if a.ThreadID == "" || a.ThreadID != b.ThreadID {
		t.Errorf("subject fallback did not group: a=%q b=%q", a.ThreadID, b.ThreadID)
	}
}

// The same email sent to two of the operator's addresses is routine on one
// domain. It has one Message-ID, so one content key, in both mirrors; each
// account still needs its own conversation row.
func TestFetchThreadsTheSameMessageInTwoAccounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	one := w.Account("me@grove.test", "secret")
	two := w.Account("you@grove.test", "secret")
	dbs := newStore(t)
	raw := mailworld.Msg().From("Alice <alice@example.com>").Subject("To both").
		MessageID("<both@grove.test>").Date(time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)).Text("hello both").Build()
	uidOne := one.Deliver("INBOX", raw)
	uidTwo := two.Deliver("INBOX", raw)

	f := ivysync.NewFetcher(dbs)
	if _, err := f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("first account: %v", err)
	}
	if _, err := f.Fetch(ctx, accountFor(t, w, "acct-2", "you@grove.test", "secret")); err != nil {
		t.Fatalf("second account: %v", err)
	}

	m1 := mustMessage(t, dbs, mustFolder(t, dbs, "acct-1", "INBOX").ID, uidOne)
	m2 := mustMessage(t, dbs, mustFolder(t, dbs, "acct-2", "INBOX").ID, uidTwo)
	if m1.ThreadID == "" || m2.ThreadID == "" || m1.ThreadID == m2.ThreadID {
		t.Errorf("thread ids = %q and %q, want one distinct conversation per account", m1.ThreadID, m2.ThreadID)
	}
	for _, acct := range []string{"acct-1", "acct-2"} {
		threads, err := dbs.ListThreads(ctx, acct)
		if err != nil || len(threads) != 1 {
			t.Errorf("%s threads = %+v, %v; want exactly one", acct, threads, err)
		}
	}
}

// Sync is newest-first, so a reply is mirrored before the root it answers. When
// the root arrives in a later run the conversation must keep the id it already
// had (N12), or a snooze or tag attached to it would detach.
func TestFetchKeepsAThreadIdWhenTheRootArrivesLater(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	reply := mailworld.Msg().From("me@grove.test").Subject("Re: Plan").MessageID("<reply@grove.test>").
		Header("References", "<root@grove.test>").Header("In-Reply-To", "<root@grove.test>").
		Date(t0.Add(time.Hour)).Text("reply body").Build()
	uidReply := acc.Deliver("INBOX", reply)

	f := ivysync.NewFetcher(dbs)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	before := mustMessage(t, dbs, inbox.ID, uidReply).ThreadID
	if before == "" {
		t.Fatal("the reply has no thread id after the first run")
	}

	uidRoot := acc.Deliver("INBOX", mailworld.Msg().From("Alice <alice@example.com>").Subject("Plan").
		MessageID("<root@grove.test>").Date(t0).Text("root body").Build())
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if got := mustMessage(t, dbs, inbox.ID, uidReply).ThreadID; got != before {
		t.Errorf("the reply's thread id changed from %q to %q when its root arrived", before, got)
	}
	if got := mustMessage(t, dbs, inbox.ID, uidRoot).ThreadID; got != before {
		t.Errorf("the root's thread id = %q, want it to join the existing conversation %q", got, before)
	}
}
