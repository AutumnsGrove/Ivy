package sync_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// The bytes of a message the server drops are copied into the blob store as it
// is hidden: they are the one thing a mirror rebuild cannot bring back, so the
// backup must find them there (ARCHITECTURE.md 9). Both the in-row and the
// spooled path are covered.
func TestDisableStoresTheRawBytesAsABlob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	raws := [][]byte{rawFor(0), rawFor(1)}
	for _, raw := range raws {
		acc.Deliver("INBOX", raw)
	}
	f := ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(2<<10, 1<<20))
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	for uid := uint32(1); uid <= 2; uid++ {
		if err := acc.Expunge("INBOX", uid); err != nil {
			t.Fatalf("expunge %d: %v", uid, err)
		}
	}
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	for uid, want := range map[uint32][]byte{1: raws[0], 2: raws[1]} {
		var hash string
		if err := dbs.Mirror.Read.QueryRowContext(ctx,
			`SELECT COALESCE(disabled_blob, '') FROM messages WHERE folder_id = ? AND uid = ?`,
			inbox.ID, uid).Scan(&hash); err != nil {
			t.Fatalf("read disabled_blob of uid %d: %v", uid, err)
		}
		if hash == "" {
			t.Fatalf("uid %d has no blob hash after being hidden", uid)
		}
		if !dbs.Blobs.Has(hash) {
			t.Fatalf("uid %d hash %s is not in the blob store", uid, hash)
		}
		got, err := os.ReadFile(dbs.Blobs.Path(hash))
		if err != nil {
			t.Fatalf("read blob: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("uid %d blob = %q, want %q", uid, got, want)
		}
	}
}

// Identical mail disabled twice is one blob. This is what keeps a repeated
// newsletter from filling the backup target.
func TestDisableDeduplicatesIdenticalBlobs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)

	// Two deliveries of the same bytes carry the same Message-ID, so they share
	// a content key but are two rows in the mirror; the blob must still be one.
	for i := 0; i < 2; i++ {
		acc.Deliver("INBOX", rawFor(0))
	}
	f := ivysync.NewFetcher(dbs)
	acct := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	for uid := uint32(1); uid <= 2; uid++ {
		if err := acc.Expunge("INBOX", uid); err != nil {
			t.Fatalf("expunge %d: %v", uid, err)
		}
	}
	if _, err := f.Fetch(ctx, acct); err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	if n := countBlobFiles(t, dbs.Blobs.Dir()); n != 1 {
		t.Errorf("blob files = %d after two identical hides, want 1", n)
	}
}

func countBlobFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk blob store: %v", err)
	}
	return n
}
