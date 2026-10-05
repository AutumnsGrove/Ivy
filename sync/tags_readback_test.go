package sync_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
)

type imapFlag = imap.Flag

func (fx *tagFixture) memberSource(t *testing.T, contentKey string) string {
	t.Helper()
	var source string
	err := fx.dbs.State.Read.QueryRowContext(fx.ctx,
		`SELECT source FROM message_tags WHERE account_id = 'acct-1' AND content_key = ? AND tag_id = ?`,
		contentKey, fx.tag.ID).Scan(&source)
	if err != nil {
		t.Fatalf("membership of %s: %v", contentKey, err)
	}
	return source
}

func (fx *tagFixture) activeOps(t *testing.T) []store.OutboxOp {
	t.Helper()
	ops, err := fx.dbs.OutboxByAccount(fx.ctx, "acct-1")
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	return ops
}

// Another client sets a keyword Ivy owns: the message joins the tag, with the
// source that says the server told us.
func TestReadBackAKeywordAnotherClientAdded(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	if err := fx.acc.Flag("INBOX", 1, imapFlag(store.TagKeyword(fx.tag.Slug))); err != nil {
		t.Fatal(err)
	}
	fx.fetch(t)

	if !fx.memberOf(t) {
		t.Fatal("the keyword another client set did not join the message to the tag")
	}
	if got := fx.memberSource(t, contentKeyFor(1)); got != store.TagSourceServer {
		t.Errorf("source = %q, want %q", got, store.TagSourceServer)
	}
	if n := len(fx.activeOps(t)); n != 0 {
		t.Errorf("read-back queued %d ops, want none", n)
	}
}

// A keyword that disappears removes the membership (ARCHITECTURE.md 3).
func TestReadBackAKeywordAnotherClientRemoved(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	fx.enqueue(t, fx.tagOp(true))
	fx.run(t)
	if !fx.memberOf(t) {
		t.Fatal("setup: the message is not in the tag")
	}
	if err := fx.acc.Unflag("INBOX", 1, imapFlag(store.TagKeyword(fx.tag.Slug))); err != nil {
		t.Fatal(err)
	}
	fx.fetch(t)

	if fx.memberOf(t) {
		t.Error("the keyword is gone from the server but the message is still in the tag")
	}
}

// A keyword for a slug Ivy has no tag for creates nothing and is left alone on
// the server (round 51).
func TestReadBackIgnoresAnUnknownSlug(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	if err := fx.acc.Flag("INBOX", 1, "$ivy-nope", "$ivy-wör", "$ivy-"); err != nil {
		t.Fatal(err)
	}
	fx.fetch(t)

	tags, err := fx.dbs.ListTags(fx.ctx)
	if err != nil || len(tags) != 1 {
		t.Fatalf("tags = %+v, %v; want only the operator's tag", tags, err)
	}
	if fx.memberOf(t) {
		t.Error("an unknown keyword put the message in a tag")
	}
	m := mustMessage(t, fx.dbs, fx.inbox.ID, 1)
	if !hasStringFlag(m.Flags, "$ivy-nope") {
		t.Errorf("mirror flags = %v, want the unknown keyword kept", m.Flags)
	}
}

// Another client can put any number of keywords on a message; only the first
// MaxTagKeywordsPerMessage are read.
func TestReadBackBoundsTheKeywordsPerMessage(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	var keywords []imapFlag
	for i := range store.MaxTagKeywordsPerMessage + 8 {
		tag, err := fx.dbs.CreateTag(fx.ctx, fmt.Sprintf("k%d", i), fmt.Sprintf("k%02d", i), "")
		if err != nil {
			t.Fatal(err)
		}
		keywords = append(keywords, imapFlag(store.TagKeyword(tag.Slug)))
	}
	if err := fx.acc.Flag("INBOX", 1, keywords...); err != nil {
		t.Fatal(err)
	}
	fx.fetch(t)

	var n int
	if err := fx.dbs.State.Read.QueryRow(`SELECT count(*) FROM message_tags`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != store.MaxTagKeywordsPerMessage {
		t.Errorf("%d memberships from %d keywords, want the first %d", n, len(keywords), store.MaxTagKeywordsPerMessage)
	}
}

// A message that arrives already carrying the keyword (a rebuilt mirror, or mail
// another client tagged first) joins the tag as it is stored.
func TestReadBackAKeywordOnANewMessage(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	uid := fx.acc.Deliver("INBOX", rawFor(2))
	if err := fx.acc.Flag("INBOX", uid, imapFlag(store.TagKeyword(fx.tag.Slug))); err != nil {
		t.Fatal(err)
	}
	fx.fetch(t)

	members, err := fx.dbs.TagMembers(fx.ctx, fx.tag.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []store.TagMember{{AccountID: "acct-1", ContentKey: contentKeyFor(2)}}; !slices.Equal(members, want) {
		t.Errorf("members = %+v, want %+v", members, want)
	}
}

// Mail kept in two folders under one content key (N8) stays tagged until the
// last copy loses the keyword.
func TestReadBackWaitsForTheLastCopyToLoseTheKeyword(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	if err := fx.acc.CreateMailbox("Archive"); err != nil {
		t.Fatal(err)
	}
	fx.acc.Deliver("Archive", rawFor(1))
	kw := store.TagKeyword(fx.tag.Slug)
	for _, box := range []string{"INBOX", "Archive"} {
		if err := fx.acc.Flag(box, 1, imapFlag(kw)); err != nil {
			t.Fatal(err)
		}
	}
	fx.fetch(t)
	if !fx.memberOf(t) {
		t.Fatal("setup: not a member")
	}

	if err := fx.acc.Unflag("INBOX", 1, imapFlag(kw)); err != nil {
		t.Fatal(err)
	}
	fx.fetch(t)
	if !fx.memberOf(t) {
		t.Error("one copy losing the keyword removed the tag while another copy still carries it")
	}
	if err := fx.acc.Unflag("Archive", 1, imapFlag(kw)); err != nil {
		t.Fatal(err)
	}
	fx.fetch(t)
	if fx.memberOf(t) {
		t.Error("the tag stayed after every copy lost the keyword")
	}
}

// If the mailbox is rebuilt, local membership re-applies the keywords: the new
// row has none, so sync queues a write through the outbox (ARCHITECTURE.md 3).
func TestRebuiltMailboxReappliesTheKeywords(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	fx.enqueue(t, fx.tagOp(true))
	fx.run(t)

	if err := fx.acc.BumpUIDValidity("INBOX"); err != nil {
		t.Fatal(err)
	}
	fx.acc.Deliver("INBOX", rawFor(1))
	fx.fetch(t)

	if !fx.memberOf(t) {
		t.Fatal("the rebuild dropped the local membership")
	}
	ops := fx.activeOps(t)
	if len(ops) != 1 || ops[0].Kind != store.OutboxFlags ||
		!slices.Equal(ops[0].Expect.FlagsAdd, []string{store.TagKeyword(fx.tag.Slug)}) {
		t.Fatalf("queued ops = %+v, want one flags op re-adding the keyword", ops)
	}
	fx.run(t)
	if !fx.serverHasKeyword(t) {
		t.Error("the keyword was not re-applied to the rebuilt mailbox")
	}
}

// A new message nobody tagged queues nothing, and a server that keeps no
// keywords is never asked to re-apply them.
func TestNoReapplyForUntaggedMailOrKeywordlessServers(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	fx.acc.Deliver("INBOX", rawFor(2))
	fx.fetch(t)
	if n := len(fx.activeOps(t)); n != 0 {
		t.Errorf("an untagged new message queued %d ops", n)
	}

	local := newTagFixture(t, mailworld.WithoutKeywords())
	local.enqueue(t, local.tagOp(true))
	local.run(t)
	if err := local.acc.BumpUIDValidity("INBOX"); err != nil {
		t.Fatal(err)
	}
	local.acc.Deliver("INBOX", rawFor(1))
	local.fetch(t)
	if !local.memberOf(t) {
		t.Error("the local-only membership was lost in the rebuild")
	}
	if n := len(local.activeOps(t)); n != 0 {
		t.Errorf("a keywordless server was queued %d ops", n)
	}
}
