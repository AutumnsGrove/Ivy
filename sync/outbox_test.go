package sync_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// contentKeyFor is the stable identity a delivered message gets in the mirror.
func contentKeyFor(n int) string { return store.ContentKey(messageID(n), nil) }

// outboxFixture delivers one message to INBOX, syncs, and returns the pieces
// every outbox test needs.
type outboxFixture struct {
	ctx  context.Context
	dbs  *store.DBs
	acc  *mailworld.Account
	acct ivysync.Account
	work *ivysync.OutboxWorker
}

func newOutboxFixture(t *testing.T, opts ...mailworld.Option) *outboxFixture {
	t.Helper()
	ctx := context.Background()
	w := newWorld(t, opts...)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	f := ivysync.NewFetcher(dbs)
	return &outboxFixture{
		ctx: ctx, dbs: dbs, acc: acc, acct: acct,
		work: ivysync.NewOutboxWorker(f, acct),
	}
}

func (fx *outboxFixture) fetch(t *testing.T) {
	t.Helper()
	f := ivysync.NewFetcher(fx.dbs)
	if _, err := f.Fetch(fx.ctx, fx.acct); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

func (fx *outboxFixture) enqueue(t *testing.T, op store.OutboxOp) store.OutboxOp {
	t.Helper()
	op.AccountID = fx.acct.ID
	if op.CreatedAt.IsZero() {
		op.CreatedAt = time.Now()
	}
	stored, _, err := fx.dbs.EnqueueOutbox(fx.ctx, op)
	if err != nil {
		t.Fatalf("enqueue %s: %v", op.Kind, err)
	}
	return stored
}

func (fx *outboxFixture) run(t *testing.T) {
	t.Helper()
	if err := fx.work.RunOnce(fx.ctx); err != nil {
		t.Fatalf("outbox run: %v", err)
	}
}

// A reader's archive is a move to the Archive role, sent to IMAP before the DB
// follows: the source row is hidden as moved and the op is done.
func TestOutboxMoveArchivesAMessage(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	if err := fx.acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	fx.acc.Deliver("INBOX", rawFor(1))
	fx.fetch(t)

	inbox := mustFolder(t, fx.dbs, "acct-1", "INBOX")
	archive := mustFolder(t, fx.dbs, "acct-1", "Archive")
	op := fx.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxMove,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect: store.OutboxExpect{DestFolderID: archive.ID},
	})
	fx.run(t)

	assertOutboxDone(t, fx.dbs, op.ID)
	archiveMsgs, err := fx.acc.Messages("Archive")
	if err != nil {
		t.Fatalf("read Archive: %v", err)
	}
	if len(archiveMsgs) != 1 {
		t.Errorf("Archive holds %d messages, want 1", len(archiveMsgs))
	}
	inboxMsgs, err := fx.acc.Messages("INBOX")
	if err != nil {
		t.Fatalf("read INBOX: %v", err)
	}
	if len(inboxMsgs) != 0 {
		t.Errorf("INBOX still holds %d messages, want 0", len(inboxMsgs))
	}
	assertSourceHidden(t, fx.dbs, inbox.ID, store.DisabledMoved)
}

// Flagging is a STORE; it changes no folder, so it has no confirmation and no
// move, but it still goes to the server first and the mirror follows.
func TestOutboxFlagSetsSeenOnTheServer(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	fx.acc.Deliver("INBOX", rawFor(1))
	fx.fetch(t)

	inbox := mustFolder(t, fx.dbs, "acct-1", "INBOX")
	op := fx.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxFlags,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect: store.OutboxExpect{FlagsAdd: []string{`\Seen`}},
	})
	fx.run(t)

	assertOutboxDone(t, fx.dbs, op.ID)
	msgs, err := fx.acc.Messages("INBOX")
	if err != nil {
		t.Fatalf("read INBOX: %v", err)
	}
	if len(msgs) != 1 || !hasFlag(msgs[0].Flags, `\Seen`) {
		t.Errorf("server flags = %+v, want \\Seen", msgs[0].Flags)
	}
	m := mustMessage(t, fx.dbs, inbox.ID, 1)
	if !hasStringFlag(m.Flags, `\Seen`) {
		t.Errorf("mirror flags = %v, want \\Seen", m.Flags)
	}
}

// Emptying the trash is the only expunge; the message leaves the server and the
// mirror row is hidden as server_removed, never deleted.
func TestOutboxExpungeEmptiesTrash(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	if err := fx.acc.CreateMailbox("Trash"); err != nil {
		t.Fatalf("create Trash: %v", err)
	}
	fx.acc.Deliver("Trash", rawFor(1))
	fx.fetch(t)

	trash := mustFolder(t, fx.dbs, "acct-1", "Trash")
	if trash.Role != store.RoleTrash {
		t.Fatalf("role = %q, want trash", trash.Role)
	}
	op := fx.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxExpunge,
		ContentKey: contentKeyFor(1), SourceFolderID: trash.ID,
	})
	fx.run(t)

	assertOutboxDone(t, fx.dbs, op.ID)
	msgs, err := fx.acc.Messages("Trash")
	if err != nil {
		t.Fatalf("read Trash: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("Trash holds %d messages, want 0", len(msgs))
	}
	assertSourceHidden(t, fx.dbs, trash.ID, store.DisabledRemoved)
}

// The move ack may be lost, leaving the op in_flight. Recovery must notice the
// server already moved the message and finish the op without moving it again.
func TestOutboxMoveRecoversWhenTheServerAlreadyMoved(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	if err := fx.acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	fx.acc.Deliver("INBOX", rawFor(1))
	fx.fetch(t)

	inbox := mustFolder(t, fx.dbs, "acct-1", "INBOX")
	archive := mustFolder(t, fx.dbs, "acct-1", "Archive")
	op := fx.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxMove,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect: store.OutboxExpect{DestFolderID: archive.ID},
	})
	// The command reached the server, but the process died before the DB write.
	if err := fx.acc.Move("INBOX", 1, "Archive"); err != nil {
		t.Fatalf("server move: %v", err)
	}
	if err := fx.dbs.SetOutboxInFlight(fx.ctx, op.ID, inbox.UIDValidity, 1, time.Now()); err != nil {
		t.Fatalf("mark in flight: %v", err)
	}
	fx.run(t)

	assertOutboxDone(t, fx.dbs, op.ID)
	msgs, err := fx.acc.Messages("Archive")
	if err != nil {
		t.Fatalf("read Archive: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("Archive holds %d messages, want exactly 1 (no duplicate move)", len(msgs))
	}
	assertSourceHidden(t, fx.dbs, inbox.ID, store.DisabledMoved)
}

// If the command never reached the server, recovery resets the op to pending
// and the next pass moves it exactly once.
func TestOutboxMoveRecoversWhenTheServerDidNotMove(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	if err := fx.acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	fx.acc.Deliver("INBOX", rawFor(1))
	fx.fetch(t)

	inbox := mustFolder(t, fx.dbs, "acct-1", "INBOX")
	archive := mustFolder(t, fx.dbs, "acct-1", "Archive")
	op := fx.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxMove,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect: store.OutboxExpect{DestFolderID: archive.ID},
	})
	if err := fx.dbs.SetOutboxInFlight(fx.ctx, op.ID, inbox.UIDValidity, 1, time.Now()); err != nil {
		t.Fatalf("mark in flight: %v", err)
	}
	fx.run(t)

	assertOutboxDone(t, fx.dbs, op.ID)
	msgs, err := fx.acc.Messages("Archive")
	if err != nil {
		t.Fatalf("read Archive: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("Archive holds %d messages, want 1", len(msgs))
	}
	assertSourceHidden(t, fx.dbs, inbox.ID, store.DisabledMoved)
}

// N8: identical Message-IDs share a content key, but an op names the folder it
// acts on, so archiving the INBOX copy must leave the Sent copy alone.
func TestOutboxMoveLeavesASameMessageIDCopyInAnotherFolder(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	if err := fx.acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	if err := fx.acc.CreateMailbox("Sent"); err != nil {
		t.Fatalf("create Sent: %v", err)
	}
	raw := rawFor(1)
	fx.acc.Deliver("INBOX", raw)
	fx.acc.Deliver("Sent", raw)
	fx.fetch(t)

	inbox := mustFolder(t, fx.dbs, "acct-1", "INBOX")
	sent := mustFolder(t, fx.dbs, "acct-1", "Sent")
	archive := mustFolder(t, fx.dbs, "acct-1", "Archive")
	op := fx.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxMove,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect: store.OutboxExpect{DestFolderID: archive.ID},
	})
	fx.run(t)

	assertOutboxDone(t, fx.dbs, op.ID)
	sentMsgs, err := fx.acc.Messages("Sent")
	if err != nil {
		t.Fatalf("read Sent: %v", err)
	}
	if len(sentMsgs) != 1 {
		t.Errorf("Sent holds %d messages, want the copy untouched (1)", len(sentMsgs))
	}
	if _, err := fx.dbs.GetMessage(fx.ctx, mustMessage(t, fx.dbs, sent.ID, 1).ID); err != nil {
		t.Errorf("the Sent row is no longer visible: %v", err)
	}
}

func assertOutboxDone(t *testing.T, dbs *store.DBs, id string) {
	t.Helper()
	op, err := dbs.GetOutbox(context.Background(), id)
	if err != nil {
		t.Fatalf("get op %s: %v", id, err)
	}
	if op.State != store.OutboxDone {
		t.Fatalf("op %s state = %q (error %q: %s), want done", id, op.State, op.LastErrorCode, op.LastErrorDetail)
	}
}

func assertSourceHidden(t *testing.T, dbs *store.DBs, folderID, reason string) {
	t.Helper()
	refs, err := dbs.SyncMessageRefs(context.Background(), folderID)
	if err != nil {
		t.Fatalf("sync refs: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("folder still has %d live rows, want 0", len(refs))
	}
	var hidden, match int
	err = dbs.Mirror.Read.QueryRowContext(context.Background(),
		`SELECT count(*), COALESCE(sum(disabled_reason = ?), 0) FROM messages WHERE folder_id = ? AND disabled_at IS NOT NULL`,
		reason, folderID).Scan(&hidden, &match)
	if err != nil {
		t.Fatalf("count hidden: %v", err)
	}
	if hidden == 0 || match != hidden {
		t.Errorf("folder has %d hidden rows, %d with reason %q, want all hidden as %q", hidden, match, reason, reason)
	}
}

func hasFlag(flags []imap.Flag, want string) bool {
	for _, f := range flags {
		if string(f) == want {
			return true
		}
	}
	return false
}

func hasStringFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

// While a move op is live, sync must not hide the source row out from under it:
// the outbox owns that row until it is terminal (CHUNK3-BRIEF.md 1.4).
func TestSyncDefersToAPendingMove(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	if err := fx.acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	fx.acc.Deliver("INBOX", rawFor(1))
	fx.fetch(t)

	inbox := mustFolder(t, fx.dbs, "acct-1", "INBOX")
	archive := mustFolder(t, fx.dbs, "acct-1", "Archive")
	op := fx.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxMove,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect: store.OutboxExpect{DestFolderID: archive.ID},
	})
	// Another client moves it first, so a sync would normally hide the source row.
	if err := fx.acc.Move("INBOX", 1, "Archive"); err != nil {
		t.Fatalf("other client move: %v", err)
	}
	fx.fetch(t)

	refs, err := fx.dbs.SyncMessageRefs(fx.ctx, inbox.ID)
	if err != nil {
		t.Fatalf("sync refs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("sync hid a row owned by a live outbox op: %d live rows, want 1", len(refs))
	}
	// The op still finishes the job itself.
	fx.run(t)
	assertOutboxDone(t, fx.dbs, op.ID)
	assertSourceHidden(t, fx.dbs, inbox.ID, store.DisabledMoved)
}

// A live flag op also owns its row, so a sync must not stomp the pending change.
func TestSyncDefersToAPendingFlag(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	fx.acc.Deliver("INBOX", rawFor(1))
	fx.fetch(t)

	inbox := mustFolder(t, fx.dbs, "acct-1", "INBOX")
	fx.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxFlags,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect: store.OutboxExpect{FlagsAdd: []string{`\Seen`}},
	})
	if err := fx.acc.Flag("INBOX", 1, "\\Flagged"); err != nil {
		t.Fatalf("other client flag: %v", err)
	}
	fx.fetch(t)

	m := mustMessage(t, fx.dbs, inbox.ID, 1)
	if hasStringFlag(m.Flags, `\Flagged`) {
		t.Errorf("sync overwrote a row owned by a pending flag op: flags = %v", m.Flags)
	}
}

// appendFixture adds a Sent folder and a send row an append op targets.
type appendFixture struct {
	*outboxFixture
	sent store.Folder
	send store.SendMessage
	op   store.OutboxOp
}

func newAppendFixture(t *testing.T) *appendFixture {
	t.Helper()
	fx := newOutboxFixture(t)
	if err := fx.acc.CreateMailbox("Sent"); err != nil {
		t.Fatalf("create Sent: %v", err)
	}
	fx.fetch(t)
	sent := mustFolder(t, fx.dbs, fx.acct.ID, "Sent")
	if sent.Role != store.RoleSent {
		t.Fatalf("Sent role = %q, want sent", sent.Role)
	}
	return &appendFixture{outboxFixture: fx, sent: sent}
}

// queue enqueues one send and its Sent append op, both keyed on the same content
// key and Message-ID.
func (a *appendFixture) queue(t *testing.T, id string) {
	t.Helper()
	msgID := "<sent-" + id + "@example.test>"
	body := mailworld.Msg().From("me@grove.test").To("you@example.test").
		Subject("hi").MessageID(msgID).Text("hello").Build()
	send, _, err := a.dbs.EnqueueSend(a.ctx, store.SendMessage{
		ID: id, AccountID: a.acct.ID, MessageID: msgID,
		ContentKey: store.ContentKey(msgID, nil), EnvelopeFrom: "me@grove.test",
		Recipients: []string{"you@example.test"}, WireBody: body, SentBody: body,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("enqueue send: %v", err)
	}
	a.send = send
	a.op = a.enqueue(t, store.OutboxOp{
		ID: id + "-append", Kind: store.OutboxAppend,
		ContentKey: send.ContentKey,
		Expect:     store.OutboxExpect{DestFolderID: a.sent.ID, FlagsAdd: []string{`\Seen`}, SendID: send.ID},
	})
}

// TestOutboxAppendFilesTheSentCopyOnce is the Sent copy path: the body from the
// send queue is APPENDed with \Seen, the op is done, and a second pass files
// nothing more.
func TestOutboxAppendFilesTheSentCopyOnce(t *testing.T) {
	t.Parallel()
	af := newAppendFixture(t)
	af.queue(t, "send-1")
	af.run(t)

	got, err := af.dbs.GetOutbox(af.ctx, af.op.ID)
	if err != nil {
		t.Fatalf("get op: %v", err)
	}
	if got.State != store.OutboxDone {
		t.Fatalf("op state = %q, want done", got.State)
	}
	msgs, err := af.acc.Messages("Sent")
	if err != nil {
		t.Fatalf("read Sent: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Sent has %d messages, want 1", len(msgs))
	}
	if msgs[0].MessageID != af.send.MessageID {
		t.Errorf("filed Message-ID = %q, want %q", msgs[0].MessageID, af.send.MessageID)
	}
	if !slices.Contains(msgs[0].Flags, imap.FlagSeen) {
		t.Errorf("filed copy flags = %v, want \\Seen", msgs[0].Flags)
	}

	af.run(t)
	msgs, _ = af.acc.Messages("Sent")
	if len(msgs) != 1 {
		t.Errorf("a second pass filed a duplicate: %d messages in Sent", len(msgs))
	}
}

// TestOutboxAppendSkipsWhenAlreadyFiled is the lost-acknowledgement case: a copy
// is already in Sent, so the op finishes without a second APPEND.
func TestOutboxAppendSkipsWhenAlreadyFiled(t *testing.T) {
	t.Parallel()
	af := newAppendFixture(t)
	af.queue(t, "send-1")
	if _, err := af.acc.Append("Sent", mailworld.Msg().From("me@grove.test").To("you@example.test").
		Subject("hi").MessageID(af.send.MessageID).Text("hello").Build()); err != nil {
		t.Fatalf("pre-file: %v", err)
	}

	af.run(t)
	msgs, _ := af.acc.Messages("Sent")
	if len(msgs) != 1 {
		t.Errorf("Sent has %d messages, want the pre-filed one only", len(msgs))
	}
	if got, _ := af.dbs.GetOutbox(af.ctx, af.op.ID); got.State != store.OutboxDone {
		t.Errorf("op state = %q, want done", got.State)
	}
}

// TestOutboxAppendRecoversAfterInFlight proves a crash mid-APPEND is decided by
// asking the folder: absent means requeue-and-append, and the copy lands once.
func TestOutboxAppendRecoversAfterInFlight(t *testing.T) {
	t.Parallel()
	af := newAppendFixture(t)
	af.queue(t, "send-1")
	if err := af.dbs.SetOutboxInFlight(af.ctx, af.op.ID, 0, 0, time.Now()); err != nil {
		t.Fatalf("set in flight: %v", err)
	}

	af.run(t)
	msgs, err := af.acc.Messages("Sent")
	if err != nil {
		t.Fatalf("read Sent: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Sent has %d messages after recovery, want 1", len(msgs))
	}
	if got, _ := af.dbs.GetOutbox(af.ctx, af.op.ID); got.State != store.OutboxDone {
		t.Errorf("op state = %q, want done", got.State)
	}
}
