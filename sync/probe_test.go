package sync_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

func probeCode(t *testing.T, err error) string {
	t.Helper()
	var pe *ivysync.ProbeError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a *ProbeError", err)
	}
	return pe.Code
}

func TestProbeAcceptsAWorkingLogin(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	acct := accountFor(t, w, "purelymail", "me@grove.test", "secret")

	if err := ivysync.NewFetcher(newStore(t)).Probe(context.Background(), acct); err != nil {
		t.Fatalf("Probe: %v", err)
	}
}

func TestProbeSaysAuthFailedForAWrongPassword(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.Account("me@grove.test", "secret")
	acct := accountFor(t, w, "purelymail", "me@grove.test", "not-the-password-xyz")

	err := ivysync.NewFetcher(newStore(t)).Probe(context.Background(), acct)
	if code := probeCode(t, err); code != "auth_failed" {
		t.Errorf("code = %q, want auth_failed (err: %v)", code, err)
	}
	if strings.Contains(err.Error(), "not-the-password-xyz") {
		t.Errorf("error text echoes the password: %v", err)
	}
}

func TestProbeSaysUnreachableForAHostThatRefuses(t *testing.T) {
	t.Parallel()
	// Bind then close, so the port is known to have nothing listening.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	acct := ivysync.Account{
		ID: "purelymail", Address: "me@grove.test", Username: "me@grove.test",
		Password: "secret", IMAPHost: "127.0.0.1", IMAPPort: port, Insecure: true,
	}

	err = ivysync.NewFetcher(newStore(t)).Probe(context.Background(), acct)
	if code := probeCode(t, err); code != "unreachable" {
		t.Errorf("code = %q, want unreachable (err: %v)", code, err)
	}
}

// A peer that accepts the connection and then says nothing is the stalled-peer
// case: the connect screen must come back with an answer, not hang.
func TestProbeGivesUpOnASilentPeer(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port
	acct := ivysync.Account{
		ID: "purelymail", Address: "me@grove.test", Username: "me@grove.test",
		Password: "secret", IMAPHost: "127.0.0.1", IMAPPort: port, Insecure: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = ivysync.NewFetcher(newStore(t)).Probe(ctx, acct)
	if err == nil {
		t.Fatal("Probe succeeded against a peer that never spoke")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Probe took %v, want it bounded by the context (300ms)", took)
	}
	if code := probeCode(t, err); code != "unreachable" {
		t.Errorf("code = %q, want unreachable (err: %v)", code, err)
	}
}

func TestProbeRefusesToDialABadPort(t *testing.T) {
	t.Parallel()
	acct := ivysync.Account{
		ID: "purelymail", Username: "me@grove.test", Password: "secret",
		IMAPHost: "127.0.0.1", IMAPPort: 0, Insecure: true,
	}
	if err := ivysync.NewFetcher(newStore(t)).Probe(context.Background(), acct); err == nil {
		t.Fatal("Probe accepted port 0 (" + strconv.Itoa(acct.IMAPPort) + ")")
	}
}
