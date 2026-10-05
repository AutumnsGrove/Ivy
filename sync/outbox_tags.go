package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/AutumnsGrove/Ivy/store"
)

// A tag is an IMAP keyword written through the same flags op as \Seen, so it
// inherits the idempotency key, the inverse cancellation and crash recovery. What
// is specific to tags lives here: a server that cannot keep the keyword, and the
// membership that follows the server's acknowledgement.

// withoutTagKeywords returns the expectation with Ivy's tag keywords removed.
func withoutTagKeywords(e store.OutboxExpect) store.OutboxExpect {
	keep := func(in []string) []string {
		var out []string
		for _, f := range in {
			if _, ok := store.SlugFromKeyword(f); !ok {
				out = append(out, f)
			}
		}
		return out
	}
	e.FlagsAdd, e.FlagsClear = keep(e.FlagsAdd), keep(e.FlagsClear)
	return e
}

// serverSide returns the op as the server is asked to satisfy it. When the folder
// does not keep custom keywords (no \* in PERMANENTFLAGS) the tag keywords are
// dropped, and an op that was only keywords has nothing left for the server:
// local reports that, and the tag stays local-only (ARCHITECTURE.md 3).
func (w *OutboxWorker) serverSide(op store.OutboxOp) (server store.OutboxOp, local bool) {
	if w.keywordsOK {
		return op, false
	}
	server = op
	server.Expect = withoutTagKeywords(op.Expect)
	empty := len(server.Expect.FlagsAdd) == 0 && len(server.Expect.FlagsClear) == 0
	return server, empty && (len(op.Expect.FlagsAdd) > 0 || len(op.Expect.FlagsClear) > 0)
}

// reflectTags applies an op's tag keywords to state.db membership. It runs only
// after the server has taken the keyword (or, local-only, could never hold it),
// so the membership never runs ahead of IMAP (CLAUDE.md non-negotiable 5). A
// keyword whose tag no longer exists, because the operator deleted it while the
// op waited, is skipped.
func (w *OutboxWorker) reflectTags(ctx context.Context, op store.OutboxOp) error {
	apply := func(flags []string, add bool) error {
		for _, flag := range flags {
			slug, ok := store.SlugFromKeyword(flag)
			if !ok {
				continue
			}
			tag, err := w.fetcher.dbs.TagBySlug(ctx, slug)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if add {
				err = w.fetcher.dbs.TagMessage(ctx, op.AccountID, op.ContentKey, tag.ID, store.TagSourceOperator)
			} else {
				err = w.fetcher.dbs.UntagMessage(ctx, op.AccountID, op.ContentKey, tag.ID)
			}
			if err != nil {
				return fmt.Errorf("reflect tag %s: %w", slug, err)
			}
		}
		return nil
	}
	if err := apply(op.Expect.FlagsAdd, true); err != nil {
		return err
	}
	return apply(op.Expect.FlagsClear, false)
}

// finishLocalTags completes an op that asked only for tag keywords on a server
// that cannot keep them. Nothing was sent, so the mirror row's flags are left as
// the server reports them.
func (w *OutboxWorker) finishLocalTags(ctx context.Context, op store.OutboxOp) error {
	if err := w.reflectTags(ctx, op); err != nil {
		return err
	}
	return w.done(ctx, op)
}
