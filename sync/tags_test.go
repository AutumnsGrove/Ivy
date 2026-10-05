package sync_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
)

// tagFixture is an outbox fixture with one mirrored INBOX message and a tag.
type tagFixture struct {
	*outboxFixture
	inbox store.Folder
	tag   store.Tag
}

func newTagFixture(t *testing.T, opts ...mailworld.Option) *tagFixture {
	t.Helper()
	fx := newOutboxFixture(t, opts...)
	fx.acc.Deliver("INBOX", rawFor(1))
	fx.fetch(t)
	tag, err := fx.dbs.CreateTag(fx.ctx, "t-work", "Work", "sky")
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	return &tagFixture{outboxFixture: fx, inbox: mustFolder(t, fx.dbs, "acct-1", "INBOX"), tag: tag}
}

func (fx *tagFixture) tagOp(add bool) store.OutboxOp {
	expect := store.OutboxExpect{FlagsAdd: []string{store.TagKeyword(fx.tag.Slug)}}
	if !add {
		expect = store.OutboxExpect{FlagsClear: []string{store.TagKeyword(fx.tag.Slug)}}
	}
	return store.OutboxOp{
		ID: "op-" + map[bool]string{true: "tag", false: "untag"}[add], Kind: store.OutboxFlags,
		ContentKey: contentKeyFor(1), SourceFolderID: fx.inbox.ID, Expect: expect,
	}
}

func (fx *tagFixture) memberOf(t *testing.T) bool {
	t.Helper()
	members, err := fx.dbs.TagMembers(fx.ctx, fx.tag.ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	return slices.Contains(members, store.TagMember{AccountID: "acct-1", ContentKey: contentKeyFor(1)})
}

func (fx *tagFixture) serverHasKeyword(t *testing.T) bool {
	t.Helper()
	msgs, err := fx.acc.Messages("INBOX")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("read INBOX: %+v, %v", msgs, err)
	}
	for _, f := range msgs[0].Flags {
		if strings.EqualFold(string(f), store.TagKeyword(fx.tag.Slug)) {
			return true
		}
	}
	return false
}

// A tag is written to the server first, as a keyword; the membership in
// state.db follows the acknowledged write (IMAP first, round 30).
func TestTagOpWritesTheKeywordThenTheMembership(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	op := fx.enqueue(t, fx.tagOp(true))
	if fx.memberOf(t) {
		t.Fatal("the membership was written before the server acknowledged the keyword")
	}
	fx.run(t)

	assertOutboxDone(t, fx.dbs, op.ID)
	if !fx.serverHasKeyword(t) {
		t.Error("the server does not carry the keyword")
	}
	if !fx.memberOf(t) {
		t.Error("the message is not in the tag after the op finished")
	}
	m := mustMessage(t, fx.dbs, fx.inbox.ID, 1)
	if !hasStringFlag(m.Flags, store.TagKeyword(fx.tag.Slug)) {
		t.Errorf("mirror flags = %v, want the keyword", m.Flags)
	}
}

func TestUntagOpClearsTheKeywordAndTheMembership(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	fx.enqueue(t, fx.tagOp(true))
	fx.run(t)
	untag := fx.enqueue(t, fx.tagOp(false))
	fx.run(t)

	assertOutboxDone(t, fx.dbs, untag.ID)
	if fx.serverHasKeyword(t) {
		t.Error("the keyword is still on the server")
	}
	if fx.memberOf(t) {
		t.Error("the message is still in the tag")
	}
}

// A server that keeps no custom keywords cannot hold the tag, so the tag stays
// local: the op finishes, nothing is sent, and the operator sees no failure.
func TestTagOpFallsBackToLocalOnlyWithoutKeywords(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t, mailworld.WithoutKeywords())
	op := fx.enqueue(t, fx.tagOp(true))
	fx.run(t)

	assertOutboxDone(t, fx.dbs, op.ID)
	if fx.serverHasKeyword(t) {
		t.Error("a keyword reached a server that does not keep them")
	}
	if !fx.memberOf(t) {
		t.Error("the local-only tag was not recorded")
	}

	// Sync must not read the missing keyword as the tag having been removed.
	fx.fetch(t)
	if !fx.memberOf(t) {
		t.Error("a sync pass dropped a local-only membership")
	}
	untag := fx.enqueue(t, fx.tagOp(false))
	fx.run(t)
	assertOutboxDone(t, fx.dbs, untag.ID)
	if fx.memberOf(t) {
		t.Error("untagging a local-only tag left the membership")
	}
}

// IMAP first: a tag the server never took leaves no membership behind.
func TestTagOpOnAMessageTheServerLostLeavesNoMembership(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	if err := fx.acc.Expunge("INBOX", 1); err != nil {
		t.Fatalf("expunge: %v", err)
	}
	op := fx.enqueue(t, fx.tagOp(true))
	fx.run(t)

	got, err := fx.dbs.GetOutbox(fx.ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.OutboxFailed || got.LastErrorCode != "message_gone" {
		t.Errorf("op = %q/%q, want failed/message_gone", got.State, got.LastErrorCode)
	}
	if fx.memberOf(t) {
		t.Error("a failed tag op still put the message in the tag")
	}
}

// A keyword that is already on the server is the postcondition, so the op
// finishes without a second write and still records the membership.
func TestTagOpIsIdempotentWhenTheKeywordIsAlreadyThere(t *testing.T) {
	t.Parallel()
	fx := newTagFixture(t)
	if err := fx.acc.Flag("INBOX", 1, "$IVY-WORK"); err != nil {
		t.Fatal(err)
	}
	op := fx.enqueue(t, fx.tagOp(true))
	fx.run(t)

	assertOutboxDone(t, fx.dbs, op.ID)
	if !fx.memberOf(t) {
		t.Error("the message is not in the tag")
	}
}
