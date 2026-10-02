package devstack_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

// controlSocket returns a short unix-socket path: t.TempDir embeds the test
// name and can exceed the 104-byte sun_path limit on macOS.
func controlSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ivyctl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "c.sock")
}

// startControl seeds a minimal world and serves the control protocol on a
// private socket, so tests drive the same commands the CLI does.
func startControl(t *testing.T) (*mailworld.World, *devstack.Client) {
	t.Helper()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if _, err := mailworld.Seed(w, mailworld.Minimal(), mailworld.WithSeed(3)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	path := controlSocket(t)
	srv, err := devstack.NewControlServer(w, path)
	if err != nil {
		t.Fatalf("control server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	client, err := devstack.Dial(path)
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return w, client
}

// mailboxCounts reads a mailbox through the scenario API.
func mailboxCounts(t *testing.T, w *mailworld.World, addr, mailbox string) uint32 {
	t.Helper()
	num, _, err := w.Account(addr, mailworld.SeedPassword).Status(mailbox)
	if err != nil {
		t.Fatalf("status %s/%s: %v", addr, mailbox, err)
	}
	return num
}

// fetchFlags reads one message's flags through a real IMAP session.
func fetchFlags(t *testing.T, w *mailworld.World, addr, mailbox string, uid uint32) []imap.Flag {
	t.Helper()
	c, err := imapclient.DialInsecure(w.IMAPAddr(), nil)
	if err != nil {
		t.Fatalf("dial imap: %v", err)
	}
	defer c.Close()
	if err := c.Login(addr, mailworld.SeedPassword).Wait(); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		t.Fatalf("select: %v", err)
	}
	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("fetched %d messages, want 1", len(msgs))
	}
	return msgs[0].Flags
}

func hasFlag(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

func TestControlDeliver(t *testing.T) {
	t.Parallel()
	w, c := startControl(t)
	addr := "ivy@grove.test"

	before := mailboxCounts(t, w, addr, "INBOX")
	raw := mailworld.Msg().From("friend@example.com").To(addr).Subject("hello").Text("hi").Build()
	uid, err := c.Deliver(addr, "INBOX", raw)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if uid == 0 {
		t.Fatal("deliver returned UID 0")
	}
	if after := mailboxCounts(t, w, addr, "INBOX"); after != before+1 {
		t.Fatalf("INBOX count = %d, want %d", after, before+1)
	}
}

func TestControlFlagMoveExpunge(t *testing.T) {
	t.Parallel()
	w, c := startControl(t)
	addr := "ivy@grove.test"

	before := mailboxCounts(t, w, addr, "INBOX")
	raw := mailworld.Msg().From("friend@example.com").To(addr).Subject("thread").Text("hi").Build()
	uid, err := c.Deliver(addr, "INBOX", raw)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if err := c.Flag(addr, "INBOX", uid, string(imap.FlagSeen)); err != nil {
		t.Fatalf("flag: %v", err)
	}
	if flags := fetchFlags(t, w, addr, "INBOX", uid); !hasFlag(flags, imap.FlagSeen) {
		t.Fatalf("flags = %v, want Seen", flags)
	}

	if err := c.Move(addr, "INBOX", uid, "Archive"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if n := mailboxCounts(t, w, addr, "INBOX"); n != before {
		t.Fatalf("INBOX has %d messages after move, want %d", n, before)
	}
	if n := mailboxCounts(t, w, addr, "Archive"); n != 1 {
		t.Fatalf("Archive has %d messages, want 1", n)
	}

	if err := c.Expunge(addr, "Archive", 1); err != nil {
		t.Fatalf("expunge: %v", err)
	}
	if n := mailboxCounts(t, w, addr, "Archive"); n != 0 {
		t.Fatalf("Archive has %d messages after expunge, want 0", n)
	}
}

func TestControlAdvanceClock(t *testing.T) {
	t.Parallel()
	w, c := startControl(t)
	before := w.Clock().Now()
	if err := c.AdvanceClock(3 * time.Hour); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if got := w.Clock().Now(); !got.Equal(before.Add(3 * time.Hour)) {
		t.Fatalf("clock = %s, want %s", got, before.Add(3*time.Hour))
	}
}

func TestControlFaultRejectsSMTP(t *testing.T) {
	t.Parallel()
	w, c := startControl(t)
	addr := "ivy@grove.test"

	if err := c.Fault(devstack.FaultSpec{Kind: "smtp-reject", Code: 552, Message: "message too large"}); err != nil {
		t.Fatalf("fault: %v", err)
	}

	cl, err := smtp.Dial(w.SMTPAddr())
	if err != nil {
		t.Fatalf("smtp dial: %v", err)
	}
	defer cl.Close()
	if err := cl.Auth(sasl.NewPlainClient("", addr, mailworld.SeedPassword)); err != nil {
		t.Fatalf("smtp auth: %v", err)
	}
	if err := cl.Mail(addr, nil); err == nil {
		t.Fatal("SMTP MAIL succeeded despite an armed 552")
	}
}

func TestControlUnknownOpFails(t *testing.T) {
	t.Parallel()
	_, c := startControl(t)
	if _, err := c.Do(devstack.ControlRequest{Op: "nonsense"}); err == nil {
		t.Fatal("expected an error for an unknown op")
	}
}
