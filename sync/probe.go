package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
)

// probeTimeout bounds one connection test end to end, so the connect screen
// always gets an answer even from a host that accepts and then says nothing.
const probeTimeout = 30 * time.Second

// ProbeError is why a connection test failed. Code is the same stable vocabulary
// the sync state uses (auth_failed, unreachable), plus "error" for the rest, so
// the screen can say the right thing. It never carries the password.
type ProbeError struct {
	Code string
	Err  error
}

func (e *ProbeError) Error() string {
	return "connection test failed (" + e.Code + "): " + e.Err.Error()
}
func (e *ProbeError) Unwrap() error { return e.Err }

// Probe opens a connection the way a sync worker does, logs in and logs out. It
// shares dial and login with the workers on purpose: a test that passed through
// different code could pass where the real worker then fails.
func (f *Fetcher) Probe(ctx context.Context, acct Account) error {
	if acct.IMAPHost == "" || acct.IMAPPort < 1 || acct.IMAPPort > 65535 {
		return &ProbeError{Code: "error", Err: errors.New("the mail server address is not valid")}
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	c, err := f.dial(ctx, acct)
	if err != nil {
		return probeFailure(ctx, err)
	}
	defer c.Close()
	// Closing the connection is what unblocks a command waiting on a silent peer.
	defer context.AfterFunc(ctx, func() { _ = c.Close() })()

	defer c.watch()()
	if err := c.Login(acct.Username, acct.Password).Wait(); err != nil {
		return probeFailure(ctx, err)
	}
	_ = c.Logout().Wait() // best effort: the login already proved the account
	return nil
}

func probeFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &ProbeError{Code: "unreachable", Err: fmt.Errorf("no answer from the mail server: %w", ctx.Err())}
	}
	status, code, _ := classifySyncError(err)
	if status == store.SyncError {
		code = "error"
	}
	return &ProbeError{Code: code, Err: err}
}
