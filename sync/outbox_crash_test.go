package sync

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
)

// errSimulatedCrash stands in for the process dying between the IMAP ack and the
// DB write. The op is left in_flight, exactly as a kill -9 would leave it.
var errSimulatedCrash = errors.New("simulated crash between ack and DB write")

// crashWorld is a self-contained harness for the white-box crash test, which
// needs the unexported afterAck seam.
type crashWorld struct {
	w    *mailworld.World
	acc  *mailworld.Account
	dbs  *store.DBs
	acct Account
}

func newCrashWorld(t *testing.T, address string) *crashWorld {
	t.Helper()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	host, portStr, err := net.SplitHostPort(w.IMAPAddr())
	if err != nil {
		t.Fatalf("split imap addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	acc := w.Account(address, "secret")
	return &crashWorld{
		w: w, acc: acc, dbs: dbs,
		acct: Account{
			ID: "acct-1", Address: address,
			IMAPHost: host, IMAPPort: port,
			Username: address, Password: "secret", Insecure: true,
		},
	}
}

func (cw *crashWorld) fetch(t *testing.T) {
	t.Helper()
	f := NewFetcher(cw.dbs)
	if _, err := f.Fetch(context.Background(), cw.acct); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

func (cw *crashWorld) enqueue(t *testing.T, op store.OutboxOp) store.OutboxOp {
	t.Helper()
	op.AccountID = cw.acct.ID
	op.CreatedAt = time.Now()
	stored, _, err := cw.dbs.EnqueueOutbox(context.Background(), op)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return stored
}

// crashOnce runs the worker with the crash seam armed on its first pass, then
// returns a fresh worker and runs it again, so recovery is a separate process
// like a restart.
func (cw *crashWorld) crashOnce(t *testing.T) (recovered store.OutboxOp) {
	t.Helper()
	ctx := context.Background()
	poisoned := NewOutboxWorker(NewFetcher(cw.dbs), cw.acct)
	armed := false
	poisoned.afterAck = func(store.OutboxOp) error {
		if armed {
			return nil
		}
		armed = true
		return errSimulatedCrash
	}
	if err := poisoned.RunOnce(ctx); err == nil {
		t.Fatal("the first pass did not crash as armed")
	}
	// The process is gone; a fresh worker on a fresh connection recovers.
	recovery := NewOutboxWorker(NewFetcher(cw.dbs), cw.acct)
	if err := recovery.RunOnce(ctx); err != nil {
		t.Fatalf("recovery pass: %v", err)
	}
	return
}

func crashRaw(n int) []byte {
	return mailworld.Msg().
		From("Sender <sender@example.com>").To("me@grove.test").
		Subject(fmt.Sprintf("crash %d", n)).MessageID(fmt.Sprintf("<crash%d@test>", n)).
		Date(time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)).
		Text("hello").Build()
}

// The crash window is between the ack and the DB write. Over many messages the
// move must apply exactly once: the message ends in the destination, the source
// row is hidden as moved, and no copy is duplicated or lost.
func TestOutboxMoveCrashWindowRecoversExactlyOnce(t *testing.T) {
	t.Parallel()
	for n := 0; n < 16; n++ {
		t.Run(fmt.Sprintf("move-%d", n), func(t *testing.T) {
			ctx := context.Background()
			cw := newCrashWorld(t, "me@grove.test")
			if err := cw.acc.CreateMailbox("Archive"); err != nil {
				t.Fatalf("create Archive: %v", err)
			}
			cw.acc.Deliver("INBOX", crashRaw(n))
			cw.fetch(t)

			inbox, err := cw.dbs.GetFolderByName(ctx, "acct-1", "INBOX")
			if err != nil {
				t.Fatal(err)
			}
			archive, err := cw.dbs.GetFolderByName(ctx, "acct-1", "Archive")
			if err != nil {
				t.Fatal(err)
			}
			op := cw.enqueue(t, store.OutboxOp{
				ID: "op-1", Kind: store.OutboxMove,
				ContentKey:     store.ContentKey(fmt.Sprintf("<crash%d@test>", n), nil),
				SourceFolderID: inbox.ID, Expect: store.OutboxExpect{DestFolderID: archive.ID},
			})
			t.Logf("recovered op %s", cw.crashOnce(t).ID)

			stored, err := cw.dbs.GetOutbox(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != store.OutboxDone {
				t.Fatalf("op state = %q (%s: %s), want done", stored.State, stored.LastErrorCode, stored.LastErrorDetail)
			}
			msgs, err := cw.acc.Messages("Archive")
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 1 {
				t.Errorf("Archive holds %d messages, want exactly 1 (no duplicate move)", len(msgs))
			}
			live, err := cw.dbs.SyncMessageRefs(ctx, inbox.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(live) != 0 {
				t.Errorf("INBOX still has %d live rows, want 0", len(live))
			}
			var hidden int
			if err := cw.dbs.Mirror.Read.QueryRowContext(ctx,
				`SELECT count(*) FROM messages WHERE folder_id = ? AND disabled_reason = ?`,
				inbox.ID, store.DisabledMoved).Scan(&hidden); err != nil {
				t.Fatal(err)
			}
			if hidden != 1 {
				t.Errorf("hidden-as-moved rows = %d, want 1", hidden)
			}
		})
	}
}

// A flag op that crashes after the STORE ack recovers by reading the flag set:
// the postcondition already holds, so it finishes without re-sending blind.
func TestOutboxFlagCrashWindowRecovers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cw := newCrashWorld(t, "me@grove.test")
	cw.acc.Deliver("INBOX", crashRaw(1))
	cw.fetch(t)

	inbox, err := cw.dbs.GetFolderByName(ctx, "acct-1", "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	op := cw.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxFlags,
		ContentKey: store.ContentKey("<crash1@test>", nil), SourceFolderID: inbox.ID,
		Expect: store.OutboxExpect{FlagsAdd: []string{`\Seen`}},
	})
	cw.crashOnce(t)

	stored, err := cw.dbs.GetOutbox(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != store.OutboxDone {
		t.Fatalf("op state = %q, want done", stored.State)
	}
	msgs, err := cw.acc.Messages("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || !serverHasFlag(msgs[0].Flags, `\Seen`) {
		t.Errorf("server flags = %+v, want \\Seen", msgs[0].Flags)
	}
}

// An expunge that crashes after the ack recovers by seeing the UID is gone.
func TestOutboxExpungeCrashWindowRecovers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cw := newCrashWorld(t, "me@grove.test")
	if err := cw.acc.CreateMailbox("Trash"); err != nil {
		t.Fatalf("create Trash: %v", err)
	}
	cw.acc.Deliver("Trash", crashRaw(1))
	cw.fetch(t)

	trash, err := cw.dbs.GetFolderByName(ctx, "acct-1", "Trash")
	if err != nil {
		t.Fatal(err)
	}
	op := cw.enqueue(t, store.OutboxOp{
		ID: "op-1", Kind: store.OutboxExpunge,
		ContentKey: store.ContentKey("<crash1@test>", nil), SourceFolderID: trash.ID,
	})
	cw.crashOnce(t)

	stored, err := cw.dbs.GetOutbox(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != store.OutboxDone {
		t.Fatalf("op state = %q, want done", stored.State)
	}
	msgs, err := cw.acc.Messages("Trash")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("Trash holds %d messages, want 0", len(msgs))
	}
}

func serverHasFlag(flags []imap.Flag, want string) bool {
	for _, f := range flags {
		if string(f) == want {
			return true
		}
	}
	return false
}

// AckThenDrop is the fake server's half of the crash window: it acknowledges
// the MOVE and then drops the link, so the client either sees the ack and
// finishes or sees a drop and retries. Either way the message must move exactly
// once after recovery.
func TestOutboxMoveAckThenDropConverges(t *testing.T) {
	t.Parallel()
	for n := 0; n < 8; n++ {
		t.Run(fmt.Sprintf("ackdrop-%d", n), func(t *testing.T) {
			ctx := context.Background()
			cw := newCrashWorld(t, "me@grove.test")
			if err := cw.acc.CreateMailbox("Archive"); err != nil {
				t.Fatalf("create Archive: %v", err)
			}
			cw.acc.Deliver("INBOX", crashRaw(n))
			cw.fetch(t)

			inbox, err := cw.dbs.GetFolderByName(ctx, "acct-1", "INBOX")
			if err != nil {
				t.Fatal(err)
			}
			archive, err := cw.dbs.GetFolderByName(ctx, "acct-1", "Archive")
			if err != nil {
				t.Fatal(err)
			}
			op := cw.enqueue(t, store.OutboxOp{
				ID: "op-1", Kind: store.OutboxMove,
				ContentKey:     store.ContentKey(fmt.Sprintf("<crash%d@test>", n), nil),
				SourceFolderID: inbox.ID, Expect: store.OutboxExpect{DestFolderID: archive.ID},
			})

			now := time.Now()
			cw.w.Fault(mailworld.AckThenDrop{})
			worker := NewOutboxWorker(NewFetcher(cw.dbs, WithClock(func() time.Time { return now })), cw.acct)
			if err := worker.RunOnce(ctx); err != nil {
				t.Fatalf("ack-then-drop pass: %v", err)
			}
			cw.w.ClearFaults()
			// Skip any backoff the dropped ack earned, then let recovery finish.
			now = now.Add(30 * time.Minute)
			recovery := NewOutboxWorker(NewFetcher(cw.dbs, WithClock(func() time.Time { return now })), cw.acct)
			if err := recovery.RunOnce(ctx); err != nil {
				t.Fatalf("recovery pass: %v", err)
			}

			stored, err := cw.dbs.GetOutbox(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != store.OutboxDone {
				t.Fatalf("op state = %q (%s: %s), want done", stored.State, stored.LastErrorCode, stored.LastErrorDetail)
			}
			msgs, err := cw.acc.Messages("Archive")
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 1 {
				t.Errorf("Archive holds %d messages, want exactly 1", len(msgs))
			}
		})
	}
}
