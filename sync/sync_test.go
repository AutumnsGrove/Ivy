package sync_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
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
	dbs, err := store.Open(t.TempDir())
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

	// LOGIN, LIST, SELECT, FETCH #1, then the connection drops on FETCH #2.
	w.Fault(mailworld.DropConnection{After: 5})
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
