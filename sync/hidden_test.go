package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

func hiddenRows(t *testing.T, dbs *store.DBs, accountID string) int {
	t.Helper()
	var n int
	if err := dbs.Mirror.Read.QueryRowContext(context.Background(),
		`SELECT count(*) FROM messages WHERE account_id = ? AND disabled_at IS NOT NULL`,
		accountID).Scan(&n); err != nil {
		t.Fatalf("count hidden rows: %v", err)
	}
	return n
}

func tagRows(t *testing.T, dbs *store.DBs, contentKey string) int {
	t.Helper()
	var n int
	if err := dbs.State.Read.QueryRowContext(context.Background(),
		`SELECT count(*) FROM message_tags WHERE content_key = ?`, contentKey).Scan(&n); err != nil {
		t.Fatalf("count tag rows: %v", err)
	}
	return n
}

// The fake server empties a mailbox. Every row is hidden rather than deleted,
// the in-row bytes survive, and the spool file of a streamed message is kept
// even though no live row points at it any more (CHUNK3-BRIEF.md 1, 3b).
func TestServerEmptyingAMailboxKeepsRowsBlobsAndFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	for i := 0; i < 3; i++ {
		acc.Deliver("INBOX", rawFor(i))
	}
	// A small inline tier forces one message onto the spool, so the file path is
	// exercised as well as the raw blob.
	f := ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(2<<10, 1<<20))
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	var spooled, inline store.Message
	for uid := uint32(1); uid <= 3; uid++ {
		m := mustMessage(t, dbs, inbox.ID, uid)
		switch {
		case m.RawPath != "":
			spooled = m
		case len(m.RawBlob) > 0:
			inline = m
		}
	}
	if spooled.RawPath == "" || len(inline.RawBlob) == 0 {
		t.Fatalf("wanted one spooled and one in-row message, got %+v / %+v", spooled, inline)
	}
	spoolFile := filepath.Join(dbs.Dir, filepath.FromSlash(spooled.RawPath))

	for uid := uint32(1); uid <= 3; uid++ {
		if err := acc.Expunge("INBOX", uid); err != nil {
			t.Fatalf("expunge %d: %v", uid, err)
		}
	}
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	if n := hiddenRows(t, dbs, "acct-1"); n != 3 {
		t.Errorf("hidden rows = %d, want 3", n)
	}
	if _, err := os.Stat(spoolFile); err != nil {
		t.Errorf("the spool file was taken when the server emptied the mailbox: %v", err)
	}
	var blob []byte
	if err := dbs.Mirror.Read.QueryRowContext(ctx,
		`SELECT raw_blob FROM messages WHERE id = ?`, inline.ID).Scan(&blob); err != nil {
		t.Fatalf("read the hidden row's blob: %v", err)
	}
	if len(blob) == 0 {
		t.Errorf("the hidden row lost its raw bytes")
	}
}

// A provider rebuild changes UIDVALIDITY and loses every UID. The old rows are
// hidden with their data and tags intact, and the message reappearing under the
// new validity becomes a live row again.
func TestUIDValidityResetHidesOldRowsWithoutErasingThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	raw := rawFor(0)
	acc.Deliver("INBOX", raw)
	f := ivysync.NewFetcher(dbs)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	before := mustMessage(t, dbs, inbox.ID, 1)
	if err := dbs.UpsertTag(ctx, store.Tag{ID: "t1", Slug: "work", Name: "Work"}); err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	if err := dbs.TagMessage(ctx, "acct-1", before.ContentKey, "t1", "operator"); err != nil {
		t.Fatalf("tag message: %v", err)
	}

	if err := acc.BumpUIDValidity("INBOX"); err != nil {
		t.Fatalf("bump uidvalidity: %v", err)
	}
	acc.Deliver("INBOX", raw) // the same message comes back under the new validity
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	// The old validity's row is hidden, the new one is live, and both share the
	// content key, so the tag still applies.
	if n := hiddenRows(t, dbs, "acct-1"); n != 1 {
		t.Errorf("hidden rows = %d, want the one old-validity row", n)
	}
	after := mustMessage(t, dbs, inbox.ID, 1)
	if after.ContentKey != before.ContentKey {
		t.Errorf("content key changed across the rebuild: %q -> %q", before.ContentKey, after.ContentKey)
	}
	if tagRows(t, dbs, after.ContentKey) != 1 {
		t.Errorf("the tag did not survive the rebuild")
	}
}

// The server restores a message the mirror had hidden: it becomes visible again
// and keeps its tags, while the hidden copy is still kept (nothing is erased).
func TestRestoredMessageKeepsItsTags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	raw := rawFor(1)
	acc.Deliver("INBOX", raw)
	f := ivysync.NewFetcher(dbs)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	original := mustMessage(t, dbs, inbox.ID, 1)
	if err := dbs.UpsertTag(ctx, store.Tag{ID: "t1", Slug: "work", Name: "Work"}); err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	if err := dbs.TagMessage(ctx, "acct-1", original.ContentKey, "t1", "operator"); err != nil {
		t.Fatalf("tag message: %v", err)
	}

	if err := acc.Expunge("INBOX", 1); err != nil {
		t.Fatalf("expunge: %v", err)
	}
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if n := hiddenRows(t, dbs, "acct-1"); n != 1 {
		t.Fatalf("hidden rows after expunge = %d, want 1", n)
	}

	acc.Deliver("INBOX", raw) // the server restores it with a new UID
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("third fetch: %v", err)
	}

	restored := mustMessage(t, dbs, inbox.ID, 2)
	if restored.ContentKey != original.ContentKey {
		t.Errorf("restored content key = %q, want %q", restored.ContentKey, original.ContentKey)
	}
	if tagRows(t, dbs, restored.ContentKey) != 1 {
		t.Errorf("the restored message lost its tag")
	}
	// The hidden old copy is still there, which is the promise that nothing is
	// ever erased, only hidden.
	if n := hiddenRows(t, dbs, "acct-1"); n != 1 {
		t.Errorf("hidden rows after the restore = %d, want the old copy still kept", n)
	}
}
