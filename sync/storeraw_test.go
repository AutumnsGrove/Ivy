package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// rawWithBody builds a message whose size lands in a chosen tier.
func rawWithBody(id string, bodyBytes int) []byte {
	return mailworld.Msg().
		From("a@example.test").To("b@example.test").
		Subject("tier " + id).MessageID("<" + id + "@example.test>").
		Text(strings.Repeat("x", bodyBytes) + "\n").
		Build()
}

func metaFor(uid uint32, raw []byte) *imapclient.FetchMessageBuffer {
	return &imapclient.FetchMessageBuffer{
		UID:          imap.UID(uid),
		RFC822Size:   int64(len(raw)),
		InternalDate: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		Envelope:     &imap.Envelope{MessageID: "<m" + string(rune('0'+uid)) + "@example.test>", Subject: "tier"},
	}
}

// StoreRaw is the entry the fast dev seeder uses instead of IMAP, so it must
// honour the same three size tiers the fetch does (STANDARDS.md 4a).
func TestStoreRawFollowsTheSizeTiers(t *testing.T) {
	t.Parallel()
	dbs := newStore(t)
	f := ivysync.NewFetcher(dbs, ivysync.WithMessageLimits(1<<10, 4<<10))
	acct := ivysync.Account{ID: "a", Address: "a@example.test"}
	ctx := context.Background()
	if err := f.EnsureAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	folder, err := f.RecordFolder(ctx, acct, "INBOX", nil, 7, 3)
	if err != nil {
		t.Fatalf("RecordFolder: %v", err)
	}
	if folder.Role != store.RoleInbox || folder.UIDValidity != 7 {
		t.Fatalf("folder = %+v", folder)
	}

	small, mid, big := rawWithBody("s", 100), rawWithBody("m", 2<<10), rawWithBody("b", 6<<10)
	for uid, raw := range map[uint32][]byte{1: small, 2: mid, 3: big} {
		if err := f.StoreRaw(ctx, acct, folder.ID, 7, metaFor(uid, raw), raw); err != nil {
			t.Fatalf("StoreRaw uid %d: %v", uid, err)
		}
	}

	got := mustMessage(t, dbs, folder.ID, 1)
	if got.BodyStatus != store.BodyOK || len(got.RawBlob) == 0 || got.RawPath != "" {
		t.Errorf("inline tier: status %q blob %d path %q", got.BodyStatus, len(got.RawBlob), got.RawPath)
	}
	got = mustMessage(t, dbs, folder.ID, 2)
	if got.BodyStatus != store.BodyOK || len(got.RawBlob) != 0 || got.RawPath == "" {
		t.Fatalf("spool tier: status %q blob %d path %q", got.BodyStatus, len(got.RawBlob), got.RawPath)
	}
	if _, err := os.Stat(filepath.Join(dbs.Dir, filepath.FromSlash(got.RawPath))); err != nil {
		t.Errorf("spool file missing: %v", err)
	}
	got = mustMessage(t, dbs, folder.ID, 3)
	if got.BodyStatus != store.BodyTooLarge || got.RawPath != "" || len(got.RawBlob) != 0 {
		t.Errorf("too-large tier: status %q blob %d path %q", got.BodyStatus, len(got.RawBlob), got.RawPath)
	}
}

func TestStoreRawTwiceKeepsOneRow(t *testing.T) {
	t.Parallel()
	dbs := newStore(t)
	f := ivysync.NewFetcher(dbs)
	acct := ivysync.Account{ID: "a", Address: "a@example.test"}
	ctx := context.Background()
	if err := f.EnsureAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	folder, err := f.RecordFolder(ctx, acct, "INBOX", nil, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw := rawWithBody("s", 50)
	for range 2 {
		if err := f.StoreRaw(ctx, acct, folder.ID, 1, metaFor(1, raw), raw); err != nil {
			t.Fatal(err)
		}
	}
	uids, err := dbs.MessageUIDs(ctx, folder.ID)
	if err != nil || len(uids) != 1 {
		t.Fatalf("uids = %v, err %v", uids, err)
	}
}

func TestStoreRawStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	dbs := newStore(t)
	f := ivysync.NewFetcher(dbs)
	acct := ivysync.Account{ID: "a", Address: "a@example.test"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw := rawWithBody("s", 50)
	if err := f.StoreRaw(ctx, acct, "folder", 1, metaFor(1, raw), raw); err == nil {
		t.Fatal("StoreRaw ignored a cancelled context")
	}
}
