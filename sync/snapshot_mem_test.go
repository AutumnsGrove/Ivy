package sync

import (
	"context"
	"net"
	"runtime"
	"strconv"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxSnapshotBytesPerMessage is the memory budget for what a pass holds per
// message while it reconciles a folder (PERFORMANCE.md 3: 250 MB during backfill
// of a 100k mailbox, so a folder's snapshot may not cost much more than this
// times its message count). The snapshot only has to say which UIDs exist and
// with which flags; envelopes are fetched again for the messages that are new.
const maxSnapshotBytesPerMessage = 300

func TestSnapshotHoldsLittlePerMessage(t *testing.T) {
	ctx := context.Background()
	const messages = 2000
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("mailworld: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if _, err := mailworld.Seed(w, mailworld.Large(messages)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	host, portStr, err := net.SplitHostPort(w.IMAPAddr())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	acct := Account{
		ID: "ivy", Address: "ivy@grove.test", IMAPHost: host, IMAPPort: port,
		Username: "ivy@grove.test", Password: mailworld.SeedPassword, Insecure: true,
	}

	f := NewFetcher(nil)
	c, err := f.dial(ctx, acct)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	mailboxes, _, err := f.negotiate(ctx, c, acct)
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	var inbox *imap.ListData
	for _, mb := range mailboxes {
		if mb.Mailbox == "INBOX" {
			inbox = mb
		}
	}
	if inbox == nil {
		t.Fatal("the seeded account has no INBOX")
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	snap, err := f.snapshotFolder(c, acct, inbox, store.Folder{}, false, false)
	if err != nil {
		t.Fatalf("snapshotFolder: %v", err)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(snap)

	if len(snap.Messages) != messages {
		t.Fatalf("the snapshot holds %d messages, want %d", len(snap.Messages), messages)
	}
	perMessage := (float64(after.HeapAlloc) - float64(before.HeapAlloc)) / float64(len(snap.Messages))
	t.Logf("snapshot retains %.0f bytes per message (budget %d)", perMessage, maxSnapshotBytesPerMessage)
	if perMessage > maxSnapshotBytesPerMessage {
		t.Errorf("snapshot retains %.0f bytes per message, over the %d byte budget", perMessage, maxSnapshotBytesPerMessage)
	}
}
