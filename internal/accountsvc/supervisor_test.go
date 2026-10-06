package accountsvc_test

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/events"
	"github.com/AutumnsGrove/Ivy/internal/accountsvc"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

const rawMessage = "From: a@elsewhere.test\r\nTo: me@grove.test\r\nSubject: hello\r\n" +
	"Message-ID: <one@elsewhere.test>\r\nDate: Mon, 06 Oct 2026 10:00:00 +0000\r\n\r\nbody\r\n"

func newWorld(t *testing.T) *mailworld.World {
	t.Helper()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func newStore(t *testing.T) *store.DBs {
	t.Helper()
	dbs, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	return dbs
}

func provider(t *testing.T, w *mailworld.World) accountsvc.Provider {
	t.Helper()
	host, portStr, err := net.SplitHostPort(w.IMAPAddr())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return accountsvc.Provider{IMAPHost: host, IMAPPort: port, SMTPHost: host, SMTPPort: 1, Insecure: true}
}

func syncAccount(t *testing.T, w *mailworld.World, id, address, password string) ivysync.Account {
	t.Helper()
	p := provider(t, w)
	return ivysync.Account{ID: id, Address: address, Username: address, Password: password,
		IMAPHost: p.IMAPHost, IMAPPort: p.IMAPPort, SMTPHost: p.SMTPHost, SMTPPort: p.SMTPPort, Insecure: true}
}

// eventually polls because a worker runs on its own goroutine; the deadline is
// a failure bound, not a delay, so a passing run returns as soon as it is true.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func syncStatus(dbs *store.DBs, id string) store.SyncStatus {
	s, err := dbs.GetSyncState(context.Background(), id)
	if err != nil {
		return ""
	}
	return s.Status
}

func startSupervisor(t *testing.T, dbs *store.DBs) *accountsvc.Supervisor {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	hub := events.New()
	sup := accountsvc.NewSupervisor(ctx, dbs, hub)
	t.Cleanup(func() { cancel(); sup.Wait(); hub.Close() })
	return sup
}

func TestStartSyncsTheAccount(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret").Deliver("INBOX", []byte(rawMessage))
	dbs := newStore(t)
	sup := startSupervisor(t, dbs)

	sup.Start(syncAccount(t, w, "purelymail", "me@grove.test", "secret"))

	eventually(t, "the first sync to finish", func() bool {
		s, err := dbs.GetSyncState(context.Background(), "purelymail")
		return err == nil && s.Status == store.SyncOK && s.BackfillDone == 1
	})
}

// The "Update password" story: the account stopped on auth_failed, the operator
// types the right password, and the same account recovers without a restart.
func TestStartAgainWithANewPasswordRecovers(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret").Deliver("INBOX", []byte(rawMessage))
	dbs := newStore(t)
	sup := startSupervisor(t, dbs)

	sup.Start(syncAccount(t, w, "purelymail", "me@grove.test", "wrong"))
	eventually(t, "auth_failed", func() bool { return syncStatus(dbs, "purelymail") == store.SyncAuthFailed })

	sup.Start(syncAccount(t, w, "purelymail", "me@grove.test", "secret"))
	eventually(t, "recovery with the right password", func() bool {
		s, err := dbs.GetSyncState(context.Background(), "purelymail")
		return err == nil && s.Status == store.SyncOK && s.BackfillDone == 1
	})
}

func TestWaitReturnsOnceTheContextEnds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	hub := events.New()
	defer hub.Close()
	sup := accountsvc.NewSupervisor(ctx, dbs, hub)
	sup.Start(syncAccount(t, w, "purelymail", "me@grove.test", "secret"))
	eventually(t, "the account to settle", func() bool { return syncStatus(dbs, "purelymail") == store.SyncOK })

	cancel()
	done := make(chan struct{})
	go func() { sup.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return after the context was cancelled")
	}
}

// A Start after shutdown has begun must not leak a worker nobody waits for.
func TestStartAfterShutdownStartsNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	hub := events.New()
	defer hub.Close()
	sup := accountsvc.NewSupervisor(ctx, dbs, hub)
	cancel()

	sup.Start(syncAccount(t, w, "purelymail", "me@grove.test", "secret"))
	sup.Wait()
	time.Sleep(100 * time.Millisecond)
	if got := syncStatus(dbs, "purelymail"); got == store.SyncOK {
		t.Errorf("a worker ran after shutdown (status %q)", got)
	}
}
