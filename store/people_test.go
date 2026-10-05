package store

import (
	"context"
	"hash/crc32"
	"testing"
	"time"
)

func seedPersonMessage(t *testing.T, dbs *DBs, accountID, folderID, id string, at time.Time, from Address, to, cc []Address) {
	t.Helper()
	if err := dbs.UpsertMessage(context.Background(), Message{
		ID: id, AccountID: accountID, FolderID: folderID, UID: crc32.ChecksumIEEE([]byte(id)),
		ContentKey: "ck:" + id, Date: at, From: from, To: to, CC: cc, Subject: "Subject " + id,
	}); err != nil {
		t.Fatalf("seedPersonMessage(%s): %v", id, err)
	}
}

func TestRebuildPeopleAggregatesCorrespondents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1") // address acct-1@example.test
	seedFolder(t, dbs, "acct-1", "inbox-1")
	me := Address{Address: "acct-1@example.test"}
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)

	seedPersonMessage(t, dbs, "acct-1", "inbox-1", "m1", t1, Address{Name: "Alice", Address: "alice@example.com"}, []Address{me}, nil)
	seedPersonMessage(t, dbs, "acct-1", "inbox-1", "m2", t2, Address{Address: "bob@example.org"}, []Address{{Address: "alice@example.com"}}, nil)
	// The operator's own address never becomes a correspondent.
	seedPersonMessage(t, dbs, "acct-1", "inbox-1", "m3", t2, me, []Address{{Address: "carol@example.net"}}, nil)

	if err := dbs.RebuildPeople(ctx, "acct-1"); err != nil {
		t.Fatalf("RebuildPeople: %v", err)
	}
	rows, err := dbs.ListPeople(ctx)
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	byAddr := map[string]PersonRow{}
	for _, r := range rows {
		byAddr[r.Address] = r
	}
	// The operator's own address is derived like any other and filtered out
	// when read (the gateway), so the result never depends on mirror order.
	if len(byAddr) != 4 {
		t.Fatalf("people = %+v, want alice, bob, carol and the operator's own address", byAddr)
	}
	alice := byAddr["alice@example.com"]
	if alice.MessageCount != 2 || alice.Name != "Alice" {
		t.Errorf("alice = %+v, want 2 messages and the display name", alice)
	}
	if !alice.FirstSeen.Equal(t1) || !alice.LastSeen.Equal(t2) {
		t.Errorf("alice seen = %s..%s, want %s..%s", alice.FirstSeen, alice.LastSeen, t1, t2)
	}
	if alice.LastSubject != "Subject m2" {
		t.Errorf("alice last subject = %q, want the newest", alice.LastSubject)
	}
	if byAddr["carol@example.net"].MessageCount != 1 {
		t.Errorf("carol = %+v, want 1", byAddr["carol@example.net"])
	}

	// Rebuilding is idempotent (it replaces the account's rows).
	if err := dbs.RebuildPeople(ctx, "acct-1"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := dbs.Mirror.Read.QueryRowContext(ctx, `SELECT count(*) FROM people`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("%d rows after a second rebuild, want 4", n)
	}
}

func TestPersonLinksMergeAndUnlink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	if err := dbs.LinkPerson(ctx, "work@example.com", "home@example.com", at); err != nil {
		t.Fatalf("LinkPerson: %v", err)
	}
	links, err := dbs.PersonLinks(ctx)
	if err != nil {
		t.Fatalf("PersonLinks: %v", err)
	}
	if links["work@example.com"] != "home@example.com" {
		t.Errorf("links = %+v, want work -> home", links)
	}
	if err := dbs.UnlinkPerson(ctx, "work@example.com"); err != nil {
		t.Fatalf("UnlinkPerson: %v", err)
	}
	links, _ = dbs.PersonLinks(ctx)
	if len(links) != 0 {
		t.Errorf("links after unlink = %+v, want none", links)
	}
}
