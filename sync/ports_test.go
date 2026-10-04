package sync_test

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"testing"

	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// timeWaitOn counts the sockets that are lingering in TIME_WAIT against a local
// port. Every connection the test process closes first parks its port for tens
// of seconds, and one run of this package opens thousands of them, so the next
// run starts with the temporary-port pool nearly empty ("can't assign requested
// address"). Test connections to the fake therefore close with a reset instead.
func timeWaitOn(t *testing.T, port string) int {
	t.Helper()
	out, err := exec.Command("netstat", "-an").Output()
	if err != nil {
		t.Skipf("netstat is not available to count TIME_WAIT sockets: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "TIME_WAIT") &&
			(strings.Contains(line, "."+port+" ") || strings.Contains(line, ":"+port+" ")) {
			n++
		}
	}
	return n
}

func TestTestConnectionsToTheFakeDoNotLeaveTimeWaitSockets(t *testing.T) {
	// Not parallel: other tests closing connections would inflate nothing here (the
	// count is per this world's port), but the count is only meaningful measured
	// straight after the connections close.
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	f := ivysync.NewFetcher(newStore(t))
	_, port, err := net.SplitHostPort(w.IMAPAddr())
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 40; i++ {
		acc.Deliver("INBOX", rawFor(i))               // the harness's own connection
		if _, err := f.Fetch(ctx, acct); err != nil { // the runner's connection
			t.Fatalf("fetch %d: %v", i, err)
		}
	}
	if n := timeWaitOn(t, port); n > 8 {
		t.Errorf("%d sockets are in TIME_WAIT against the fake's port after 80 short connections, want a handful at most", n)
	}
}
