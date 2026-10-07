package sync_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// A connection that dies mid-command must not be reused by the next attempt:
// the retry has to dial afresh, or a healthy server still fails every attempt
// until the op exhausts its retries.
func TestOutboxRetriesOnAFreshConnectionAfterADrop(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if err := acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	acc.Deliver("INBOX", rawFor(1))
	f := ivysync.NewFetcher(dbs)
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("sync: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	archive := mustFolder(t, dbs, "acct-1", "Archive")
	op, _, err := dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: "op-1", AccountID: "acct-1", Kind: store.OutboxMove,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect:    store.OutboxExpect{DestFolderID: archive.ID},
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Only the worker's first connection is dropped, mid-command.
	w.Fault(mailworld.DropConnection{After: 4})
	worker := ivysync.NewOutboxWorker(f, acct,
		ivysync.WithOutboxPoll(10*time.Millisecond),
		ivysync.WithOutboxJitter(func(time.Duration) time.Duration { return 20 * time.Millisecond }))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	deadline := time.Now().Add(15 * time.Second)
	var got store.OutboxOp
	for time.Now().Before(deadline) {
		got, err = dbs.GetOutbox(ctx, op.ID)
		if err != nil {
			t.Fatalf("get op: %v", err)
		}
		if got.State == store.OutboxDone || got.State == store.OutboxFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if got.State != store.OutboxDone {
		t.Fatalf("op state = %q after %d attempts (%s: %s), want done on a fresh connection",
			got.State, got.Attempts, got.LastErrorCode, got.LastErrorDetail)
	}
}

// Shutdown must not wait out a server that has gone quiet: cancelling the
// context closes the worker's connection, as sync and the IDLE worker do.
func TestOutboxRunStopsPromptlyWhenCancelledMidCommand(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if err := acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	acc.Deliver("INBOX", rawFor(1))
	f := ivysync.NewFetcher(dbs)
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("sync: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	archive := mustFolder(t, dbs, "acct-1", "Archive")
	if _, _, err := dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: "op-1", AccountID: "acct-1", Kind: store.OutboxMove,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect:    store.OutboxExpect{DestFolderID: archive.ID},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w.Fault(mailworld.Latency{Delay: time.Minute})
	worker := ivysync.NewOutboxWorker(f, acct, ivysync.WithOutboxPoll(10*time.Millisecond))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	time.Sleep(500 * time.Millisecond) // the worker is now waiting on a silent server
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run was still blocked 5s after cancellation; it waits out the stall timeout")
	}
}

// An unreachable server is an outage, not a verdict on the op: the attempt cap
// is for per-op server rejections, so a long outage must leave the op queued
// and let it finish once the server is back.
func TestOutboxOutageDoesNotExhaustAttempts(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if err := acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	acc.Deliver("INBOX", rawFor(1))
	f := ivysync.NewFetcher(dbs)
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("sync: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	archive := mustFolder(t, dbs, "acct-1", "Archive")
	op, _, err := dbs.EnqueueOutbox(ctx, store.OutboxOp{
		ID: "op-1", AccountID: "acct-1", Kind: store.OutboxMove,
		ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
		Expect:    store.OutboxExpect{DestFolderID: archive.ID},
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w.Fault(mailworld.Unreachable{})
	worker := ivysync.NewOutboxWorker(f, acct,
		ivysync.WithOutboxPoll(5*time.Millisecond),
		ivysync.WithOutboxJitter(func(time.Duration) time.Duration { return 5 * time.Millisecond }))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	// Far more failed dials than MaxOutboxAttempts.
	time.Sleep(1500 * time.Millisecond)
	got, err := dbs.GetOutbox(ctx, op.ID)
	if err != nil {
		t.Fatalf("get op: %v", err)
	}
	if got.State != store.OutboxPending || got.Attempts != 0 {
		t.Fatalf("during the outage op is %q with %d attempts (%s), want pending with 0",
			got.State, got.Attempts, got.LastErrorCode)
	}
	if got.LastErrorCode == "" {
		t.Error("the outage left no error code on the op for the queue screen")
	}

	w.ClearFaults()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && got.State != store.OutboxDone {
		time.Sleep(20 * time.Millisecond)
		if got, err = dbs.GetOutbox(ctx, op.ID); err != nil {
			t.Fatalf("get op: %v", err)
		}
	}
	cancel()
	<-done
	if got.State != store.OutboxDone {
		t.Fatalf("after the outage op is %q (%s), want done", got.State, got.LastErrorCode)
	}
}

// Undo needs the message in the destination's mirror the moment the move is
// done, so the worker mirrors the arrival itself (from the server's COPYUID)
// instead of leaving it for the next sync pass.
func TestOutboxMoveMirrorsTheArrivalImmediately(t *testing.T) {
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
	fx.run(t) // no sync pass after the move

	assertOutboxDone(t, fx.dbs, op.ID)
	id, _, err := fx.dbs.MessageRowRef(fx.ctx, "acct-1", contentKeyFor(1), archive.ID)
	if err != nil {
		t.Fatalf("the arrival is not in the Archive mirror: %v", err)
	}
	if _, err := fx.dbs.GetMessage(fx.ctx, id); err != nil {
		t.Fatalf("the arrived row is not live: %v", err)
	}

	// The next sync pass must adopt that row, not mirror the message twice.
	fx.fetch(t)
	again, _, err := fx.dbs.MessageRowRef(fx.ctx, "acct-1", contentKeyFor(1), archive.ID)
	if err != nil || again != id {
		t.Fatalf("after a sync pass the arrival is row %q (%v), want the same row %q", again, err, id)
	}
	page, err := fx.dbs.ListInbox(fx.ctx, store.InboxQuery{Role: store.RoleArchive})
	if err != nil {
		t.Fatalf("list archive: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("Archive lists %d messages after a sync pass, want 1", len(page.Items))
	}
}

// Issue #10: on the real server a message archived a moment ago could not be
// trashed from Archive (message_gone). Both orders matter: trashing straight
// after the move (the mirror holds the row the worker wrote itself) and after
// a sync pass has adopted it.
func TestOutboxMoveArchivedMessageCanBeTrashed(t *testing.T) {
	t.Parallel()
	for _, syncBetween := range []bool{false, true} {
		name := "straight after the move"
		if syncBetween {
			name = "after a sync pass"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fx := newOutboxFixture(t)
			for _, box := range []string{"Archive", "Trash"} {
				if err := fx.acc.CreateMailbox(box); err != nil {
					t.Fatalf("create %s: %v", box, err)
				}
			}
			fx.acc.Deliver("INBOX", rawFor(1))
			fx.fetch(t)
			inbox := mustFolder(t, fx.dbs, "acct-1", "INBOX")
			archive := mustFolder(t, fx.dbs, "acct-1", "Archive")
			trash := mustFolder(t, fx.dbs, "acct-1", "Trash")

			first := fx.enqueue(t, store.OutboxOp{
				ID: "op-1", Kind: store.OutboxMove,
				ContentKey: contentKeyFor(1), SourceFolderID: inbox.ID,
				Expect: store.OutboxExpect{DestFolderID: archive.ID},
			})
			fx.run(t)
			assertOutboxDone(t, fx.dbs, first.ID)
			if syncBetween {
				fx.fetch(t)
			}

			second := fx.enqueue(t, store.OutboxOp{
				ID: "op-2", Kind: store.OutboxMove,
				ContentKey: contentKeyFor(1), SourceFolderID: archive.ID,
				Expect: store.OutboxExpect{DestFolderID: trash.ID},
			})
			fx.run(t)
			assertOutboxDone(t, fx.dbs, second.ID)
			msgs, err := fx.acc.Messages("Trash")
			if err != nil || len(msgs) != 1 {
				t.Fatalf("Trash holds %d messages (%v), want 1", len(msgs), err)
			}
		})
	}
}

// Issue #10: "the message is no longer on the server" was the same text whether
// the folder would not open, the search found nothing or the mirror held no
// Message-ID, so a live failure could not be told apart. The detail now names
// the step that came up empty.
func TestOutboxMoveGoneNamesTheStepThatFoundNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		break_ func(fx *outboxFixture, t *testing.T)
		want   string
	}{
		{"search finds nothing", func(fx *outboxFixture, t *testing.T) {
			if err := fx.acc.Expunge("INBOX", 1); err != nil {
				t.Fatalf("expunge: %v", err)
			}
		}, "found 0 messages"},
		{"folder will not open", func(fx *outboxFixture, t *testing.T) {
			if err := fx.acc.RenameMailbox("INBOX", "Elsewhere"); err != nil {
				t.Skipf("fake cannot rename INBOX: %v", err)
			}
		}, "could not open"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			tc.break_(fx, t)
			fx.run(t)

			got, err := fx.dbs.GetOutbox(fx.ctx, op.ID)
			if err != nil {
				t.Fatalf("get op: %v", err)
			}
			if got.State != store.OutboxFailed || got.LastErrorCode != "message_gone" {
				t.Fatalf("op = %q/%q, want failed/message_gone", got.State, got.LastErrorCode)
			}
			if !strings.Contains(got.LastErrorDetail, tc.want) {
				t.Errorf("detail = %q, want it to mention %q", got.LastErrorDetail, tc.want)
			}
		})
	}
}
