package sync_test

import (
	"slices"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
)

// draftFixture delivers no mail but gives the worker a Drafts folder and a
// helper that saves a version through the real store path, so the op the worker
// sees is the one the API would have committed.
type draftFixture struct {
	*outboxFixture
	drafts store.Folder
}

func newDraftFixture(t *testing.T) *draftFixture {
	t.Helper()
	fx := newOutboxFixture(t)
	if err := fx.acc.CreateMailbox("Drafts"); err != nil {
		t.Fatalf("create Drafts: %v", err)
	}
	fx.fetch(t)
	drafts := mustFolder(t, fx.dbs, fx.acct.ID, "Drafts")
	if drafts.Role != store.RoleDrafts {
		t.Fatalf("Drafts role = %q, want drafts", drafts.Role)
	}
	return &draftFixture{outboxFixture: fx, drafts: drafts}
}

// save commits one version and its op, exactly as the API handler will.
func (df *draftFixture) save(t *testing.T, id, draftID, msgID string, base int) store.Draft {
	t.Helper()
	body := mailworld.Msg().From("me@grove.test").To("you@example.test").
		Subject("draft").MessageID(msgID).Text("hello").Build()
	d, err := df.dbs.SaveDraft(df.ctx, store.SaveDraftInput{
		ID: id, DraftID: draftID, AccountID: df.acct.ID,
		DestFolderID: df.drafts.ID, MessageID: msgID,
		Subject: "draft", To: []string{"you@example.test"},
		Compose: []byte(`{"subject":"draft"}`), Body: body,
		BaseVersion: base, OpID: id + "-op", Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("save draft %s: %v", id, err)
	}
	return d
}

func (df *draftFixture) draftMessages(t *testing.T) []mailworld.ServerMessage {
	t.Helper()
	msgs, err := df.acc.Messages("Drafts")
	if err != nil {
		t.Fatalf("read Drafts: %v", err)
	}
	return msgs
}

// A saved version is one APPEND with \Draft, the version is marked saved, and a
// second pass files nothing more.
func TestOutboxDraftFilesTheVersionOnce(t *testing.T) {
	t.Parallel()
	df := newDraftFixture(t)
	d := df.save(t, "v1", "d1", "<draft-1@example.test>", 0)
	df.run(t)

	msgs := df.draftMessages(t)
	if len(msgs) != 1 {
		t.Fatalf("Drafts has %d messages, want 1", len(msgs))
	}
	if msgs[0].MessageID != d.MessageID {
		t.Errorf("filed Message-ID = %q, want %q", msgs[0].MessageID, d.MessageID)
	}
	if !slices.Contains(msgs[0].Flags, imap.FlagDraft) {
		t.Errorf("filed flags = %v, want \\Draft", msgs[0].Flags)
	}
	if op, _ := df.dbs.GetOutbox(df.ctx, "v1-op"); op.State != store.OutboxDone {
		t.Errorf("op state = %q, want done", op.State)
	}
	if got, _ := df.dbs.DraftVersion(df.ctx, "v1"); got.State != store.DraftSaved {
		t.Errorf("version state = %q, want saved", got.State)
	}

	df.run(t)
	if msgs := df.draftMessages(t); len(msgs) != 1 {
		t.Errorf("a second pass filed a duplicate: %d messages in Drafts", len(msgs))
	}
}

// A replace leaves exactly one copy: the new version is filed and the version it
// supersedes is expunged, even though the old copy was never mirrored.
func TestOutboxDraftReplaceLeavesOneCopy(t *testing.T) {
	t.Parallel()
	df := newDraftFixture(t)
	df.save(t, "v1", "d1", "<draft-1@example.test>", 0)
	df.run(t)
	df.save(t, "v2", "d1", "<draft-2@example.test>", 1)
	df.run(t)

	msgs := df.draftMessages(t)
	if len(msgs) != 1 {
		t.Fatalf("Drafts has %d messages, want 1", len(msgs))
	}
	if msgs[0].MessageID != "<draft-2@example.test>" {
		t.Errorf("filed Message-ID = %q, want the newest version", msgs[0].MessageID)
	}
}

// A lost acknowledgement cannot file a copy twice: the Message-ID is already in
// Drafts, so the op finishes without a second APPEND.
func TestOutboxDraftSkipsWhenAlreadyFiled(t *testing.T) {
	t.Parallel()
	df := newDraftFixture(t)
	body := mailworld.Msg().From("me@grove.test").To("you@example.test").
		Subject("draft").MessageID("<draft-1@example.test>").Text("hello").Build()
	if _, err := df.acc.Append("Drafts", body); err != nil {
		t.Fatalf("pre-file: %v", err)
	}
	df.save(t, "v1", "d1", "<draft-1@example.test>", 0)
	df.run(t)

	if msgs := df.draftMessages(t); len(msgs) != 1 {
		t.Errorf("Drafts has %d messages, want the pre-filed one only", len(msgs))
	}
	if op, _ := df.dbs.GetOutbox(df.ctx, "v1-op"); op.State != store.OutboxDone {
		t.Errorf("op state = %q, want done", op.State)
	}
}

// A crash mid-save is decided by asking the folder: absent means the append
// never landed and is safe to re-issue, and it lands exactly once.
func TestOutboxDraftRecoversAfterInFlight(t *testing.T) {
	t.Parallel()
	df := newDraftFixture(t)
	df.save(t, "v1", "d1", "<draft-1@example.test>", 0)
	if err := df.dbs.SetOutboxInFlight(df.ctx, "v1-op", 0, 0, time.Now()); err != nil {
		t.Fatalf("set in flight: %v", err)
	}
	df.run(t)

	msgs := df.draftMessages(t)
	if len(msgs) != 1 {
		t.Fatalf("Drafts has %d messages after recovery, want 1", len(msgs))
	}
	if op, _ := df.dbs.GetOutbox(df.ctx, "v1-op"); op.State != store.OutboxDone {
		t.Errorf("op state = %q, want done", op.State)
	}
	if got, _ := df.dbs.DraftVersion(df.ctx, "v1"); got.State != store.DraftSaved {
		t.Errorf("version state = %q, want saved", got.State)
	}
}

// Discarding a draft removes its copy and leaves nothing behind; a second pass
// is a no-op.
func TestOutboxDraftRemoveExpunges(t *testing.T) {
	t.Parallel()
	df := newDraftFixture(t)
	df.save(t, "v1", "d1", "<draft-1@example.test>", 0)
	df.run(t)
	if _, err := df.dbs.DiscardDraft(df.ctx, df.acct.ID, "d1", "discard-op", time.Now()); err != nil {
		t.Fatalf("discard: %v", err)
	}
	df.run(t)

	if msgs := df.draftMessages(t); len(msgs) != 0 {
		t.Errorf("Drafts has %d messages after discard, want 0", len(msgs))
	}
	if op, _ := df.dbs.GetOutbox(df.ctx, "discard-op"); op.State != store.OutboxDone {
		t.Errorf("discard op state = %q, want done", op.State)
	}
}

// A draft may only ever be filed in the Drafts role: a folder id pointing
// anywhere else fails the op rather than writing into someone's mail.
func TestOutboxDraftRejectsANonDraftsFolder(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	fx.acc.Deliver("INBOX", rawFor(1))
	fx.fetch(t)
	inbox := mustFolder(t, fx.dbs, "acct-1", "INBOX")
	op := fx.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxDraft,
		ContentKey: store.ContentKey("<draft@example.test>", nil), SourceFolderID: inbox.ID,
		Expect: store.OutboxExpect{
			DestFolderID: inbox.ID, DraftID: "d1",
			Supersedes: []string{"<draft@example.test>"}, Remove: true,
		},
	})
	fx.run(t)

	got, err := fx.dbs.GetOutbox(fx.ctx, op.ID)
	if err != nil {
		t.Fatalf("get op: %v", err)
	}
	if got.State != store.OutboxFailed || got.LastErrorCode != "not_drafts" {
		t.Fatalf("op = %+v, want failed not_drafts", got)
	}
	if msgs, _ := fx.acc.Messages("INBOX"); len(msgs) != 1 {
		t.Errorf("INBOX has %d messages, want the delivered one untouched", len(msgs))
	}
}
