package devstack

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// Summary reports what Populate wrote. Restored is true when the databases came
// from the cache instead of being built; Discarded says why a cache that existed
// was thrown away, so a fallback to a slow build is never silent.
type Summary struct {
	Accounts  int
	Messages  int
	Restored  bool
	Discarded string
}

// Populate fills the dev databases from the prepared mailworld, so both `up`
// paths serve a mailbox the moment they start. The supervised Ivy is a separate
// process that only opens what is already under the data directory, which is
// why the harness does this and not Ivy.
//
// An empty data directory is first restored from the cache of an earlier build
// of the same profile and seed; a build into an empty directory is cached for
// next time. A directory that already holds data is topped up and left uncached.
func Populate(ctx context.Context, stack *Stack, opts Options) (Summary, error) {
	dir := filepath.Join(CacheDir(opts.Root), cacheKey(stack, opts))
	fresh := !hasData(stack.Config.DataDir)
	var discarded string
	if fresh {
		sum, ok, why := restoreCache(ctx, dir, stack)
		if ok {
			return sum, nil
		}
		discarded = why
	}
	sum, err := build(ctx, stack, opts)
	sum.Discarded = discarded
	if err != nil {
		return sum, err
	}
	if fresh {
		return sum, saveCache(dir, stack.Config.DataDir)
	}
	return sum, nil
}

// build runs the chosen mode into the data directory and seeds the operator
// state, closing the databases before it returns so a copy of them is complete.
func build(ctx context.Context, stack *Stack, opts Options) (Summary, error) {
	dbs, err := store.Open(ctx, stack.Config.DataDir)
	if err != nil {
		return Summary{}, fmt.Errorf("devstack: open store: %w", err)
	}
	defer dbs.Close()
	var sum Summary
	switch opts.Mode {
	case ModeFull:
		sum, err = populateFull(ctx, dbs, stack.Config.Accounts)
	case ModeFast:
		sum, err = populateFast(ctx, dbs, stack, opts)
	default:
		err = fmt.Errorf("devstack: unknown mode %q", opts.Mode)
	}
	if err != nil {
		return sum, err
	}
	return sum, seedState(ctx, dbs, stack.Config.Accounts)
}

// syncAccount is the connection descriptor sync uses for a configured dev
// account. Mailworld speaks plaintext on loopback; ValidateConfig has already
// refused any other host.
func syncAccount(a config.Account) ivysync.Account {
	return ivysync.Account{
		ID:                 a.ID,
		Address:            a.Address,
		IMAPHost:           a.IMAPHost,
		IMAPPort:           a.IMAPPort,
		SMTPHost:           a.SMTPHost,
		SMTPPort:           a.SMTPPort,
		Username:           a.Username,
		Password:           a.Password,
		Insecure:           true,
		TrustedAuthservIDs: a.TrustedAuthservIDs,
	}
}

// populateFull runs the real sync once per account over IMAP. The fetcher skips
// UIDs the mirror already holds, so a second run adds nothing.
func populateFull(ctx context.Context, dbs *store.DBs, accounts []config.Account) (Summary, error) {
	var sum Summary
	fetcher := ivysync.NewFetcher(dbs)
	for _, a := range accounts {
		res, err := fetcher.Fetch(ctx, syncAccount(a))
		if err != nil {
			return sum, fmt.Errorf("devstack: populate %s: %w", a.ID, err)
		}
		sum.Accounts++
		sum.Messages += res.Stored
	}
	return sum, nil
}
