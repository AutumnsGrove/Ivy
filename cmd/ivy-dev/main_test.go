package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"go.uber.org/goleak"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// shortRoot keeps the control socket path under the 104-byte sun_path limit.
func shortRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ivycli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func prepared(t *testing.T) (*devstack.Stack, string) {
	t.Helper()
	root := shortRoot(t)
	opts := devstack.DefaultOptions()
	opts.Root = root
	opts.Listen = "127.0.0.1:0"
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = stack.Close() })
	return stack, root
}

func runCLI(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCommand()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(append([]string{"--root", root}, args...))
	err := cmd.Execute()
	if err != nil {
		t.Logf("stderr: %s", errb.String())
	}
	return out.String(), err
}

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

func TestDeliverFlagMoveExpungeThroughCLI(t *testing.T) {
	stack, root := prepared(t)
	addr := stack.Seed.Accounts[0].Address

	before, _, err := stack.World.Account(addr, mailworld.SeedPassword).Status("INBOX")
	if err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, root, "deliver", "--account", addr, "--subject", "hello from the cli")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	var uid uint32
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &uid); err != nil || uid == 0 {
		t.Fatalf("deliver printed %q, want a UID", out)
	}

	if _, err := runCLI(t, root, "flag", "--account", addr, "--uid", fmt.Sprint(uid), "seen"); err != nil {
		t.Fatalf("flag: %v", err)
	}
	if flags := fetchFlags(t, stack.World, addr, "INBOX", uid); !slicesContains(flags, imap.FlagSeen) {
		t.Fatalf("flags = %v, want Seen", flags)
	}

	if _, err := runCLI(t, root, "move", "--account", addr, "--uid", fmt.Sprint(uid), "--dest", "Archive"); err != nil {
		t.Fatalf("move: %v", err)
	}
	after, _, _ := stack.World.Account(addr, mailworld.SeedPassword).Status("INBOX")
	if after != before {
		t.Fatalf("INBOX count = %d after move, want %d", after, before)
	}
	if n, _, _ := stack.World.Account(addr, mailworld.SeedPassword).Status("Archive"); n != 1 {
		t.Fatalf("Archive count = %d, want 1", n)
	}

	if _, err := runCLI(t, root, "expunge", "--account", addr, "--mailbox", "Archive", "--uid", "1"); err != nil {
		t.Fatalf("expunge: %v", err)
	}
	if n, _, _ := stack.World.Account(addr, mailworld.SeedPassword).Status("Archive"); n != 0 {
		t.Fatalf("Archive count = %d after expunge, want 0", n)
	}
}

func TestFaultCommandArmsSMTPReject(t *testing.T) {
	stack, root := prepared(t)
	addr := stack.Seed.Accounts[0].Address

	if _, err := runCLI(t, root, "fault", "--kind", "smtp-reject", "--code", "552", "--message", "too big"); err != nil {
		t.Fatalf("fault: %v", err)
	}
	cl, err := smtp.Dial(stack.World.SMTPAddr())
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

func TestStateCommandListsAndApplies(t *testing.T) {
	stack, root := prepared(t)
	addr := stack.Seed.Accounts[0].Address

	out, err := runCLI(t, root, "state", "--list")
	if err != nil {
		t.Fatalf("state --list: %v", err)
	}
	if !strings.Contains(out, "sync-auth-failed") || !strings.Contains(out, "mirror-healthy") {
		t.Fatalf("state list = %q", out)
	}

	if _, err := runCLI(t, root, "state", "sync-auth-failed"); err != nil {
		t.Fatalf("state apply: %v", err)
	}
	c, err := imapclient.DialInsecure(stack.World.IMAPAddr(), nil)
	if err != nil {
		t.Fatalf("dial imap: %v", err)
	}
	defer c.Close()
	if err := c.Login(addr, mailworld.SeedPassword).Wait(); err == nil {
		t.Fatal("login succeeded under sync-auth-failed")
	}

	if _, err := runCLI(t, root, "state", "mirror-healthy"); err != nil {
		t.Fatalf("state healthy: %v", err)
	}
}

func TestAdvanceClockCommand(t *testing.T) {
	stack, root := prepared(t)
	before := stack.World.Clock().Now()
	if _, err := runCLI(t, root, "advance-clock", "90m"); err != nil {
		t.Fatalf("advance-clock: %v", err)
	}
	if got := stack.World.Clock().Now(); !got.Equal(before.Add(90 * time.Minute)) {
		t.Fatalf("clock = %s, want %s", got, before.Add(90*time.Minute))
	}
}

func TestSeedCommandPrintsResult(t *testing.T) {
	root := shortRoot(t)
	out, err := runCLI(t, root, "seed", "--profile", "minimal")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !strings.Contains(out, "delivered") || !strings.Contains(out, "ivy@grove.test") {
		t.Fatalf("seed output = %q", out)
	}
}

func TestResetCommandRemovesBuiltState(t *testing.T) {
	_, root := prepared(t)
	if _, err := runCLI(t, root, "reset"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := os.Stat(devstack.DataDir(root)); !os.IsNotExist(err) {
		t.Fatalf("data dir survived reset: %v", err)
	}
}

func TestSnapshotCommands(t *testing.T) {
	_, root := prepared(t)
	if _, err := runCLI(t, root, "snapshot", "save", "nightly"); err != nil {
		t.Fatalf("snapshot save: %v", err)
	}
	out, err := runCLI(t, root, "snapshot", "list")
	if err != nil {
		t.Fatalf("snapshot list: %v", err)
	}
	if !strings.Contains(out, "nightly") {
		t.Fatalf("snapshot list = %q", out)
	}
	if _, err := runCLI(t, root, "snapshot", "restore", "nightly"); err != nil {
		t.Fatalf("snapshot restore: %v", err)
	}
}

func TestUpNoWebServesHealthAndShutsDown(t *testing.T) {
	root := shortRoot(t)
	addr := freeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := newRootCommand()
	cmd.SetArgs([]string{"--root", root, "up", "--no-web", "--watch=false", "--profile", "minimal", "--listen", addr})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	waitForHealth(t, "http://"+addr+"/api/v1/health")
	if _, err := os.Stat(devstack.ConfigPath(root)); err != nil {
		t.Fatalf("config missing while up: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("up returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("up did not shut down")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func waitForHealth(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never became healthy", url)
}

func slicesContains(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}
