package sync_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// badBase64 is routine real mail: a good body and an attachment whose base64 is
// mangled. Before #48 it was mirrored with no body at all.
func badBase64(subject, id string, date time.Time) []byte {
	return []byte("From: Alice <alice@example.com>\r\nTo: me@grove.test\r\nSubject: " + subject + "\r\n" +
		"Message-ID: <" + id + "@grove.test>\r\nDate: " + date.Format(time.RFC1123Z) + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nthe real body text\r\n" +
		"--b\r\nContent-Type: application/pdf; name=\"a.pdf\"\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\nQUJD*#$%^&REVGR0hJ\r\nzzz!!\r\n--b--\r\n")
}

// stale puts a message back to how it looked before the current pipeline: no
// version, the wrong body and no attachment rows.
func stale(t *testing.T, dbs *store.DBs, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := dbs.Mirror.Write.ExecContext(ctx, `
		UPDATE messages SET derived_version = 0, body_html_sanitized = '<p>stale</p>',
			body_text = '', snippet = '', has_attachments = 0, body_status = 'unparsed'
		WHERE id = ?`, id); err != nil {
		t.Fatalf("stale %s: %v", id, err)
	}
	if _, err := dbs.Mirror.Write.ExecContext(ctx, `DELETE FROM attachments WHERE message_id = ?`, id); err != nil {
		t.Fatalf("stale attachments %s: %v", id, err)
	}
}

func versionOf(t *testing.T, dbs *store.DBs, id string) int {
	t.Helper()
	m, err := dbs.GetMessage(context.Background(), id)
	if err != nil {
		t.Fatalf("GetMessage %s: %v", id, err)
	}
	return m.DerivedVersion
}

func TestFetchStampsTheDerivedVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	uidHTML := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("html").Date(t0).
		MessageID("<h@grove.test>").Text("t").HTML("<p>h</p>").Attach("n.txt", "text/plain", []byte("x")).Build())
	uidPlain := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("plain").Date(t0.Add(time.Hour)).
		MessageID("<p@grove.test>").Text("plain").Build())

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	for _, uid := range []uint32{uidHTML, uidPlain} {
		m := mustMessage(t, dbs, inbox.ID, uid)
		if m.DerivedVersion != ivysync.DerivedVersion {
			t.Errorf("uid %d: DerivedVersion = %d, want %d", uid, m.DerivedVersion, ivysync.DerivedVersion)
		}
	}
}

// Rows written before the current pipeline are re-derived from the raw message,
// a bounded number per pass and newest first, so a big backlog heals over
// successive passes without ever stalling a sync.
func TestRederiveHealsRowsBehindInBoundedPasses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	uids := []uint32{
		acc.Deliver("INBOX", badBase64("old", "old", t0)),
		acc.Deliver("INBOX", badBase64("mid", "mid", t0.Add(time.Hour))),
		acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("new").Date(t0.Add(2*time.Hour)).
			MessageID("<new@grove.test>").Text("new body").HTML("<p>new</p><script>x()</script>").
			Attach("n.txt", "text/plain", []byte("attached")).Build()),
	}

	f := ivysync.NewFetcher(dbs)
	if _, err := f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	ids := make([]string, len(uids))
	for i, uid := range uids {
		ids[i] = mustMessage(t, dbs, inbox.ID, uid).ID
		stale(t, dbs, ids[i])
	}

	n, err := f.Rederive(ctx, "acct-1", 2)
	if err != nil || n != 2 {
		t.Fatalf("first pass = %d, %v; want 2 healed", n, err)
	}
	if versionOf(t, dbs, ids[2]) != ivysync.DerivedVersion || versionOf(t, dbs, ids[1]) != ivysync.DerivedVersion {
		t.Error("the two newest rows were not healed first")
	}
	if versionOf(t, dbs, ids[0]) != 0 {
		t.Error("the oldest row was healed beyond the pass limit")
	}

	if n, err = f.Rederive(ctx, "acct-1", 2); err != nil || n != 1 {
		t.Fatalf("second pass = %d, %v; want 1 healed", n, err)
	}
	if n, err = f.Rederive(ctx, "acct-1", 2); err != nil || n != 0 {
		t.Fatalf("third pass = %d, %v; want nothing left", n, err)
	}

	fresh, _ := dbs.GetMessage(ctx, ids[2])
	if strings.Contains(fresh.BodyHTML, "stale") || strings.Contains(fresh.BodyHTML, "<script") ||
		!strings.Contains(fresh.BodyHTML, "<p>new</p>") {
		t.Errorf("new BodyHTML = %q, want a fresh sanitised body", fresh.BodyHTML)
	}
	if fresh.BodyText != "new body" || fresh.BodyStatus != store.BodyOK || !fresh.HasAttachments {
		t.Errorf("new message = text %q status %q attachments %v", fresh.BodyText, fresh.BodyStatus, fresh.HasAttachments)
	}
	atts := attachmentsFor(t, dbs, ids[2])
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte("attached"))); atts["n.txt"].ContentHash != want {
		t.Errorf("n.txt = %+v, want its rows rebuilt with the content hash", atts["n.txt"])
	}

	// The mangled-attachment message gets its body back, which is the point of
	// versioning the parser (#48).
	healed, _ := dbs.GetMessage(ctx, ids[0])
	if !strings.Contains(healed.BodyText, "the real body text") || healed.BodyStatus != store.BodyOK {
		t.Errorf("mangled-attachment message = text %q status %q, want the body back", healed.BodyText, healed.BodyStatus)
	}
	if _, ok := attachmentsFor(t, dbs, ids[0])["a.pdf"]; !ok {
		t.Error("the mangled attachment was not listed")
	}
}

// A sync run heals the account's backlog on its way out, so nothing extra has to
// be scheduled for the fix to reach mail that is already mirrored.
func TestFetchHealsRowsBehindAtTheEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	uid := acc.Deliver("INBOX", badBase64("hello", "h", time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)))
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	f := ivysync.NewFetcher(dbs)
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	id := mustMessage(t, dbs, mustFolder(t, dbs, "acct-1", "INBOX").ID, uid).ID
	stale(t, dbs, id)

	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if versionOf(t, dbs, id) != ivysync.DerivedVersion {
		t.Error("a second Fetch left the stale row behind")
	}
}

func TestRederiveReadsASpooledMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	uid := acc.Deliver("INBOX", attachmentMessage("mid", 100_000))
	f := ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(4<<10, 1<<20))
	if _, err := f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m := mustMessage(t, dbs, mustFolder(t, dbs, "acct-1", "INBOX").ID, uid)
	if m.RawPath == "" {
		t.Fatal("the test needs a spooled message")
	}
	stale(t, dbs, m.ID)

	if n, err := f.Rederive(ctx, "acct-1", 10); err != nil || n != 1 {
		t.Fatalf("Rederive = %d, %v; want 1", n, err)
	}
	got, _ := dbs.GetMessage(ctx, m.ID)
	want := fmt.Sprintf("%x", sha256.Sum256(bytes.Repeat([]byte{0x5a}, 100_000)))
	if big := attachmentsFor(t, dbs, m.ID)["big.bin"]; big.ContentHash != want || big.Size != 100_000 {
		t.Errorf("big.bin = %+v, want it rebuilt from the spool file", big)
	}
	if !strings.Contains(got.BodyText, "the readable part") || got.DerivedVersion != ivysync.DerivedVersion {
		t.Errorf("spooled message = %+v", got)
	}
}

// One message whose raw bytes are gone must not stop the others from healing.
func TestRederiveSkipsAnUnreadableMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	uidA := acc.Deliver("INBOX", attachmentMessage("a", 100_000))
	uidB := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("b").Date(t0).
		MessageID("<b@grove.test>").Text("b body").Build())
	f := ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(4<<10, 1<<20))
	if _, err := f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	a, b := mustMessage(t, dbs, inbox.ID, uidA), mustMessage(t, dbs, inbox.ID, uidB)
	stale(t, dbs, a.ID)
	stale(t, dbs, b.ID)
	if err := os.Remove(filepath.Join(dbs.Dir, filepath.FromSlash(a.RawPath))); err != nil {
		t.Fatalf("remove spool file: %v", err)
	}

	n, err := f.Rederive(ctx, "acct-1", 10)
	if err != nil {
		t.Fatalf("Rederive returned %v, want the unreadable message skipped", err)
	}
	if n != 1 || versionOf(t, dbs, b.ID) != ivysync.DerivedVersion {
		t.Errorf("healed %d, b version %d; want b healed", n, versionOf(t, dbs, b.ID))
	}
	if versionOf(t, dbs, a.ID) != 0 {
		t.Error("the unreadable message was stamped as derived")
	}
}

// Two spooled messages of the same size: "newest" is delivered last, so it is
// first in the newest-first list.
func twoSpooled(t *testing.T) (*ivysync.Fetcher, *store.DBs, store.Message, store.Message) {
	t.Helper()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	// Explicit dates: "newest" must really be first in the newest-first list, not
	// first by the tiebreak on a hashed id.
	spooled := func(subject string, date time.Time) []byte {
		return mailworld.Msg().From("Alice <alice@example.com>").To("me@grove.test").Subject(subject).
			MessageID("<"+subject+"@grove.test>").Date(date).Text("the readable part").
			Attach("big.bin", "application/octet-stream", bytes.Repeat([]byte{0x5a}, 100_000)).Build()
	}
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	uidOld := acc.Deliver("INBOX", spooled("older", t0))
	uidNew := acc.Deliver("INBOX", spooled("newest", t0.Add(time.Hour)))
	f := ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(4<<10, 1<<20))
	if _, err := f.Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	older, newest := mustMessage(t, dbs, inbox.ID, uidOld), mustMessage(t, dbs, inbox.ID, uidNew)
	if older.RawPath == "" || newest.RawPath == "" {
		t.Fatal("the test needs spooled messages")
	}
	return f, dbs, older, newest
}

// A message whose spool file is gone is marked failed for this version, so it
// stops occupying the first slot of every pass and the healthy message behind it
// still heals, within the same pass.
func TestRederiveMarksAMissingSpoolFileAndHealsPastIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, dbs, older, newest := twoSpooled(t)
	stale(t, dbs, older.ID)
	stale(t, dbs, newest.ID)
	if err := os.Remove(filepath.Join(dbs.Dir, filepath.FromSlash(newest.RawPath))); err != nil {
		t.Fatalf("remove spool file: %v", err)
	}

	// limit 1: the unreadable newest message would take the only slot, forever.
	n, err := f.Rederive(ctx, "acct-1", 1)
	if err != nil || n != 1 {
		t.Fatalf("first pass = %d, %v; want the older message healed past the broken one", n, err)
	}
	if versionOf(t, dbs, older.ID) != ivysync.DerivedVersion {
		t.Error("the healthy message behind the broken one was not healed")
	}
	if versionOf(t, dbs, newest.ID) != 0 {
		t.Error("the unreadable message was stamped as derived")
	}
	ids, err := dbs.MessageIDsBehind(ctx, "acct-1", ivysync.DerivedVersion, 10)
	if err != nil || len(ids) != 0 {
		t.Errorf("still behind after marking = %v, %v; want the failed row out of the pass", ids, err)
	}
	if n, err = f.Rederive(ctx, "acct-1", 1); err != nil || n != 0 {
		t.Errorf("second pass = %d, %v; want nothing left to do", n, err)
	}
}

// A file that is merely unreadable right now (permissions, a busy disk) may
// recover, so it is skipped without being marked and retried on the next pass.
func TestRederiveDoesNotMarkATransientFailure(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	ctx := context.Background()
	f, dbs, _, newest := twoSpooled(t)
	stale(t, dbs, newest.ID)
	spool := filepath.Join(dbs.Dir, filepath.FromSlash(newest.RawPath))
	if err := os.Chmod(spool, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(spool, 0o600) })

	if n, err := f.Rederive(ctx, "acct-1", 5); err != nil || n != 0 {
		t.Fatalf("pass over an unreadable file = %d, %v; want skipped without error", n, err)
	}
	if err := os.Chmod(spool, 0o600); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n, err := f.Rederive(ctx, "acct-1", 5); err != nil || n != 1 {
		t.Fatalf("pass after recovery = %d, %v; want the message healed (it must not have been marked)", n, err)
	}
	if versionOf(t, dbs, newest.ID) != ivysync.DerivedVersion {
		t.Error("the recovered message was not derived")
	}
}
