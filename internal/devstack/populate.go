package devstack

import (
	"context"
	"fmt"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// Summary reports what Populate wrote.
type Summary struct {
	Accounts int
	Messages int
}

// Populate fills the dev databases from the prepared mailworld, so both `up`
// paths serve a mailbox the moment they start. The supervised Ivy is a separate
// process that only opens what is already under the data directory, which is
// why the harness does this and not Ivy.
func Populate(ctx context.Context, stack *Stack, opts Options) (Summary, error) {
	if opts.Mode != ModeFull {
		return Summary{}, fmt.Errorf("devstack: mode %q cannot populate yet", opts.Mode)
	}
	dbs, err := store.Open(ctx, stack.Config.DataDir)
	if err != nil {
		return Summary{}, fmt.Errorf("devstack: open store: %w", err)
	}
	defer dbs.Close()
	return populateFull(ctx, dbs, stack.Config.Accounts)
}

// populateFull runs the real sync once per account over IMAP. The fetcher skips
// UIDs the mirror already holds, so a second run adds nothing.
func populateFull(ctx context.Context, dbs *store.DBs, accounts []config.Account) (Summary, error) {
	var sum Summary
	fetcher := ivysync.NewFetcher(dbs)
	for _, a := range accounts {
		res, err := fetcher.Fetch(ctx, ivysync.Account{
			ID:       a.ID,
			Address:  a.Address,
			IMAPHost: a.IMAPHost,
			IMAPPort: a.IMAPPort,
			SMTPHost: a.SMTPHost,
			SMTPPort: a.SMTPPort,
			Username: a.Username,
			Password: a.Password,
			// Mailworld speaks plaintext on loopback; ValidateConfig has already
			// refused any other host.
			Insecure:           true,
			TrustedAuthservIDs: a.TrustedAuthservIDs,
		})
		if err != nil {
			return sum, fmt.Errorf("devstack: populate %s: %w", a.ID, err)
		}
		sum.Accounts++
		sum.Messages += res.Stored
	}
	return sum, nil
}
