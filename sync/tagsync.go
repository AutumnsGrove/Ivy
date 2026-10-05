package sync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
)

// Tags are kept both locally and as IMAP keywords $ivy-<slug> (ARCHITECTURE.md
// 3). The outbox writes the keyword; this file is the way back: what other
// clients did to a keyword becomes membership, and a mailbox that lost its
// keywords gets them again from local membership.
//
// Read-back works on transitions of a row's keyword set, never on absence alone.
// A tag the server cannot keep (local-only) or whose write is still queued has no
// keyword, and that must not read as "someone removed it".

// tagIndex maps a slug to its tag. It is empty when the operator has no tags,
// which makes every read-back below a no-op.
type tagIndex map[string]store.Tag

func (f *Fetcher) loadTagIndex(ctx context.Context) (tagIndex, error) {
	tags, err := f.dbs.ListTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync: load tags: %w", err)
	}
	if len(tags) == 0 {
		return nil, nil
	}
	ix := make(tagIndex, len(tags))
	for _, t := range tags {
		ix[t.Slug] = t.Tag
	}
	return ix, nil
}

// keywordTags returns the tags whose keyword is among flags. Another client can
// put any text on a message, so only the first MaxTagKeywordsPerMessage
// well-formed Ivy keywords (in slug order, so the choice is stable) are read,
// and a slug with no tag is ignored: it is left on the server, creates nothing.
func (ix tagIndex) keywordTags(flags []string) []store.Tag {
	if len(ix) == 0 {
		return nil
	}
	var slugs []string
	for _, flag := range flags {
		if slug, ok := store.SlugFromKeyword(flag); ok {
			slugs = append(slugs, slug)
		}
	}
	slices.Sort(slugs)
	slugs = slices.Compact(slugs)
	if len(slugs) > store.MaxTagKeywordsPerMessage {
		slugs = slugs[:store.MaxTagKeywordsPerMessage]
	}
	var out []store.Tag
	for _, slug := range slugs {
		if tag, ok := ix[slug]; ok {
			out = append(out, tag)
		}
	}
	return out
}

func hasTag(tags []store.Tag, id string) bool {
	return slices.ContainsFunc(tags, func(t store.Tag) bool { return t.ID == id })
}

// readBackChange applies the keyword changes between a row's mirrored flags and
// the flags the server now reports. It runs before the new flags are stored, so a
// failure retries on the next pass instead of losing the transition.
func (f *Fetcher) readBackChange(ctx context.Context, accountID string, ref store.SyncMessageRef, now []string, ix tagIndex) error {
	if len(ix) == 0 {
		return nil
	}
	before, after := ix.keywordTags(ref.Flags), ix.keywordTags(now)
	for _, t := range after {
		if hasTag(before, t.ID) {
			continue
		}
		if err := f.dbs.TagMessage(ctx, accountID, ref.ContentKey, t.ID, store.TagSourceServer); err != nil {
			return err
		}
	}
	for _, t := range before {
		if hasTag(after, t.ID) {
			continue
		}
		// The same mail can sit in two folders under one content key (N8); the tag
		// goes only when the last copy has lost the keyword.
		carried, err := f.dbs.AnotherRowCarriesKeyword(ctx, accountID, ref.ContentKey, ref.ID, store.TagKeyword(t.Slug))
		if err != nil {
			return err
		}
		if carried {
			continue
		}
		if err := f.dbs.UntagMessage(ctx, accountID, ref.ContentKey, t.ID); err != nil {
			return err
		}
	}
	return nil
}

// reapplyBatch bounds one membership lookup, which binds a variable per key.
const reapplyBatch = 500

// readBackNew handles the rows a pass just stored. A row that arrives carrying
// the keyword joins the tag. A row that arrives without it, for mail the
// operator has already tagged (a rebuilt mailbox, a new UIDVALIDITY), gets the
// keyword written again through the outbox, when the server can keep it.
// Re-applying is best effort: a full outbox is logged and the rest are skipped,
// because membership is still right and only the server copy is missing.
func (f *Fetcher) readBackNew(ctx context.Context, acct Account, folderID string, snap folderSnapshot, newUIDs []uint32, ix tagIndex) error {
	if len(ix) == 0 || len(newUIDs) == 0 {
		return nil
	}
	fresh := make(map[uint32]bool, len(newUIDs))
	for _, uid := range newUIDs {
		fresh[uid] = true
	}
	refs, err := f.dbs.SyncMessageRefs(ctx, folderID)
	if err != nil {
		return err
	}
	var rows []store.SyncMessageRef
	for _, ref := range refs {
		if fresh[ref.UID] && ref.UIDValidity == snap.UIDValidity {
			rows = append(rows, ref)
		}
	}
	for _, ref := range rows {
		for _, t := range ix.keywordTags(ref.Flags) {
			if err := f.dbs.TagMessage(ctx, acct.ID, ref.ContentKey, t.ID, store.TagSourceServer); err != nil {
				return err
			}
		}
	}
	if !snap.KeywordsOK {
		return nil
	}
	for start := 0; start < len(rows); start += reapplyBatch {
		chunk := rows[start:min(start+reapplyBatch, len(rows))]
		keys := make([]string, len(chunk))
		for i, ref := range chunk {
			keys[i] = ref.ContentKey
		}
		members, err := f.dbs.TagsForMessages(ctx, acct.ID, keys)
		if err != nil {
			return err
		}
		for _, ref := range chunk {
			present := ix.keywordTags(ref.Flags)
			var missing []string
			for _, t := range members[ref.ContentKey] {
				if !hasTag(present, t.ID) {
					missing = append(missing, store.TagKeyword(t.Slug))
				}
			}
			if len(missing) == 0 {
				continue
			}
			_, _, err := f.dbs.EnqueueOutbox(ctx, store.OutboxOp{
				ID: f.newID(), AccountID: acct.ID, Kind: store.OutboxFlags,
				ContentKey: ref.ContentKey, SourceFolderID: folderID,
				Expect: store.OutboxExpect{FlagsAdd: missing}, CreatedAt: f.now(),
			})
			if errors.Is(err, store.ErrOutboxFull) {
				slog.WarnContext(ctx, "sync: the outbox is full; tag keywords were not re-applied to the rest", "account", acct.ID)
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// WithIDs overrides the op id generator; tests inject a counter.
func WithIDs(fn func() string) Option {
	return func(f *Fetcher) {
		if fn != nil {
			f.newID = fn
		}
	}
}

func randomOpID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on the platforms Ivy runs on; the clock keeps
		// an op creatable if it somehow does, as the gateway's ids do.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
