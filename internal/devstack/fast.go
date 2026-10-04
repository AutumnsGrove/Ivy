package devstack

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// populateFast fills the mirror without IMAP. It replays the deterministic
// seeder into a throwaway mailworld and writes each delivery through the same
// sync code a fetch uses (StoreRaw, RecordFolder, Settle), so the rows can only
// differ from a real sync where the inputs do. A throwaway world, rather than
// the live one, keeps memory flat for the large profile and lets the writes
// follow the configured accounts instead of whatever the profile seeds.
func populateFast(ctx context.Context, dbs *store.DBs, stack *Stack, opts Options) (Summary, error) {
	profile, err := mailworld.ParseProfile(opts.Profile)
	if err != nil {
		return Summary{}, err
	}
	world, err := mailworld.New()
	if err != nil {
		return Summary{}, fmt.Errorf("devstack: start seed world: %w", err)
	}
	defer world.Close()

	fetcher := ivysync.NewFetcher(dbs)
	w := &fastWriter{
		ctx:      ctx,
		fetcher:  fetcher,
		accounts: make(map[string]ivysync.Account),
		folders:  make(map[string]store.Folder),
	}
	for _, a := range stack.Config.Accounts {
		acct := syncAccount(a)
		if err := fetcher.EnsureAccount(ctx, acct); err != nil {
			return Summary{}, fmt.Errorf("devstack: populate %s: %w", a.ID, err)
		}
		w.accounts[a.Address] = acct
	}

	res, err := mailworld.Seed(world, profile, mailworld.WithSeed(opts.Seed), mailworld.WithObserver(w.add))
	if err != nil {
		return Summary{}, err
	}
	if w.err != nil {
		return Summary{}, w.err
	}
	// The live world was seeded from the same profile and seed, so a different
	// hash means the generator is not deterministic and the two modes would
	// show different mailboxes.
	if res.Hash != stack.Seed.Hash {
		return Summary{}, errors.New("devstack: the seeder is not deterministic: fast and live worlds differ")
	}

	// Real numbers replace the placeholders the first delivery to each mailbox
	// recorded, then the same settling passes a fetch ends with.
	for _, acct := range w.accounts {
		boxes, err := world.Account(acct.Address, mailworld.SeedPassword).Mailboxes()
		if err != nil {
			return Summary{}, fmt.Errorf("devstack: list %s mailboxes: %w", acct.ID, err)
		}
		for _, b := range boxes {
			if _, err := fetcher.RecordFolder(ctx, acct, b.Name, b.Attrs, b.UIDValidity, b.HighestModSeq); err != nil {
				return Summary{}, fmt.Errorf("devstack: populate %s: %w", acct.ID, err)
			}
		}
		if err := fetcher.Settle(ctx, acct.ID); err != nil {
			return Summary{}, fmt.Errorf("devstack: populate %s: %w", acct.ID, err)
		}
	}
	return Summary{Accounts: len(w.accounts), Messages: w.stored}, nil
}

// fastWriter receives each seeded delivery and stores it. The observer has no
// way to return an error, so the first failure is kept and later deliveries are
// skipped.
type fastWriter struct {
	ctx      context.Context
	fetcher  *ivysync.Fetcher
	accounts map[string]ivysync.Account // by address; only configured accounts
	folders  map[string]store.Folder    // by account id + mailbox
	stored   int
	err      error
}

func (w *fastWriter) add(d mailworld.Delivery) {
	acct, ok := w.accounts[d.Account]
	if w.err != nil || !ok {
		return
	}
	key := acct.ID + "\x00" + d.Mailbox
	folder, ok := w.folders[key]
	if !ok {
		// Folder rows must exist before their messages (foreign keys). The real
		// UIDVALIDITY is only known once seeding ends, so this is overwritten then.
		var err error
		folder, err = w.fetcher.RecordFolder(w.ctx, acct, d.Mailbox, nil, 0, 0)
		if err != nil {
			w.err = err
			return
		}
		w.folders[key] = folder
	}
	meta, err := metaFromRaw(d)
	if err != nil {
		w.err = fmt.Errorf("devstack: read %s UID %d: %w", d.Mailbox, d.UID, err)
		return
	}
	if err := w.fetcher.StoreRaw(w.ctx, acct, folder.ID, meta, d.Raw); err != nil {
		w.err = fmt.Errorf("devstack: store %s UID %d: %w", d.Mailbox, d.UID, err)
		return
	}
	w.stored++
}

// metaFromRaw builds what the first FETCH of a sync returns for the message. The
// envelope comes from the library the fake server itself uses, so it is the
// envelope a sync would have read.
func metaFromRaw(d mailworld.Delivery) (*imapclient.FetchMessageBuffer, error) {
	header, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(d.Raw)))
	if err != nil {
		return nil, err
	}
	return &imapclient.FetchMessageBuffer{
		UID:          imap.UID(d.UID),
		Flags:        d.Flags,
		Envelope:     imapserver.ExtractEnvelope(header),
		InternalDate: d.At,
		RFC822Size:   int64(len(d.Raw)),
	}, nil
}
