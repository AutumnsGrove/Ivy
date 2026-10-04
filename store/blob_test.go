package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A disabled message's bytes are the only copy the server no longer holds, so
// the row remembers the content hash of its backup blob. The first disable
// wins, like the reason beside it.
func TestDisableMessageRecordsTheBlobHash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedLiveMessage(t, dbs, "m1", "acct-1", "folder-1", 1)

	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if err := dbs.DisableMessage(ctx, "m1", DisabledPending, at, "abc123"); err != nil {
		t.Fatalf("DisableMessage: %v", err)
	}
	if got := disabledBlobOf(t, dbs, "m1"); got != "abc123" {
		t.Errorf("disabled_blob = %q, want abc123", got)
	}

	// A later pass must not rewrite the explanation or the blob of the first.
	if err := dbs.DisableMessage(ctx, "m1", DisabledRemoved, at.Add(time.Hour), "other"); err != nil {
		t.Fatalf("second DisableMessage: %v", err)
	}
	if got := disabledBlobOf(t, dbs, "m1"); got != "abc123" {
		t.Errorf("disabled_blob after second disable = %q, want abc123", got)
	}
	var reason string
	if err := dbs.Mirror.Read.QueryRow(`SELECT disabled_reason FROM messages WHERE id='m1'`).Scan(&reason); err != nil {
		t.Fatalf("read reason: %v", err)
	}
	if reason != DisabledPending {
		t.Errorf("reason = %q, want %q", reason, DisabledPending)
	}
}

// Re-appearing on the server clears the hidden state, blob hash included, so a
// live row never claims a backup copy it no longer needs.
func TestUpsertClearsTheBlobHashOnReEnable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedLiveMessage(t, dbs, "m1", "acct-1", "folder-1", 1)
	if err := dbs.DisableMessage(ctx, "m1", DisabledRemoved, time.Now(), "abc123"); err != nil {
		t.Fatalf("DisableMessage: %v", err)
	}

	if err := dbs.UpsertMessage(ctx, Message{
		ID: "m1", AccountID: "acct-1", FolderID: "folder-1", UID: 1, ContentKey: "m1",
	}); err != nil {
		t.Fatalf("re-enable UpsertMessage: %v", err)
	}
	if got := disabledBlobOf(t, dbs, "m1"); got != "" {
		t.Errorf("disabled_blob after re-enable = %q, want empty", got)
	}
}

func TestSetDisabledBlobFillsInAMissingHash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedLiveMessage(t, dbs, "m1", "acct-1", "folder-1", 1)
	if err := dbs.DisableMessage(ctx, "m1", DisabledRemoved, time.Now(), ""); err != nil {
		t.Fatalf("DisableMessage: %v", err)
	}
	if err := dbs.SetDisabledBlob(ctx, "m1", "latehash"); err != nil {
		t.Fatalf("SetDisabledBlob: %v", err)
	}
	if got := disabledBlobOf(t, dbs, "m1"); got != "latehash" {
		t.Errorf("disabled_blob = %q, want latehash", got)
	}
	if err := dbs.SetDisabledBlob(ctx, "missing", "hash"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetDisabledBlob(unknown) = %v, want ErrNotFound", err)
	}
}

// The backup reconcile walks every hidden row's raw bytes without loading them
// all at once: inline blobs come from the row, spooled ones from disk.
func TestEachDisabledRawYieldsBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")

	spoolRel := filepath.ToSlash(filepath.Join("spool", "folder-1", "2.eml"))
	if err := os.MkdirAll(filepath.Join(dbs.Dir, "spool", "folder-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbs.Dir, filepath.FromSlash(spoolRel)), []byte("spooled bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	seedDisabledRaw(t, dbs, "m-inline", "acct-1", "folder-1", DisabledRemoved, 1, []byte("inline bytes"), "")
	seedDisabledRaw(t, dbs, "m-spool", "acct-1", "folder-1", DisabledRemoved, 2, nil, spoolRel)
	// A message above the download limit has no bytes at all and must be skipped.
	seedDisabledRaw(t, dbs, "m-empty", "acct-1", "folder-1", DisabledRemoved, 3, nil, "")
	seedLiveMessage(t, dbs, "m-live", "acct-1", "folder-1", 4)

	got := map[string]string{}
	err := dbs.EachDisabledRaw(ctx, func(id, _ string, raw io.Reader) error {
		data, err := io.ReadAll(raw)
		if err != nil {
			return err
		}
		got[id] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("EachDisabledRaw: %v", err)
	}
	if got["m-inline"] != "inline bytes" {
		t.Errorf("inline bytes = %q", got["m-inline"])
	}
	if got["m-spool"] != "spooled bytes" {
		t.Errorf("spool bytes = %q", got["m-spool"])
	}
	if _, ok := got["m-empty"]; ok {
		t.Errorf("a message with no raw bytes was yielded")
	}
	if _, ok := got["m-live"]; ok {
		t.Errorf("a live message was yielded")
	}
}

// A cancelled reconcile stops at the next row.
func TestEachDisabledRawStopsOnCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledRaw(t, dbs, "m1", "acct-1", "folder-1", DisabledRemoved, 1, []byte("x"), "")
	cancel()
	err := dbs.EachDisabledRaw(ctx, func(string, string, io.Reader) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("EachDisabledRaw with cancelled context = %v, want context.Canceled", err)
	}
}

// A message never downloaded has no bytes to copy, and an unknown id is a client
// error, not a silent empty blob.
func TestMessageRawReaderReportsMissingBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "m1", AccountID: "acct-1", FolderID: "folder-1", UID: 1, ContentKey: "m1",
		BodyStatus: BodyTooLarge,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := dbs.MessageRawReader(ctx, "m1"); !errors.Is(err, ErrNoRaw) {
		t.Errorf("MessageRawReader(no bytes) = %v, want ErrNoRaw", err)
	}
	if _, err := dbs.MessageRawReader(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("MessageRawReader(unknown) = %v, want ErrNotFound", err)
	}
}

// A spool file that has gone missing is reported to the callback as a read
// error, and the walk continues to the next hidden row instead of aborting.
func TestEachDisabledRawReportsAnUnreadableSpoolAndContinues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedDisabledRaw(t, dbs, "m-broken", "acct-1", "folder-1", DisabledRemoved, 1, nil, "spool/missing.eml")
	seedDisabledRaw(t, dbs, "m-ok", "acct-1", "folder-1", DisabledRemoved, 2, []byte("good bytes"), "")

	seen := map[string]bool{}
	err := dbs.EachDisabledRaw(ctx, func(id, _ string, raw io.Reader) error {
		data, readErr := io.ReadAll(raw)
		seen[id] = true
		if id == "m-broken" {
			if readErr == nil {
				t.Errorf("reading the missing spool returned no error")
			}
			return nil
		}
		if readErr != nil || string(data) != "good bytes" {
			t.Errorf("%s = %q, %v", id, data, readErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("EachDisabledRaw: %v", err)
	}
	if !seen["m-broken"] || !seen["m-ok"] {
		t.Errorf("walk did not visit both rows: %v", seen)
	}
}

func seedLiveMessage(t *testing.T, dbs *DBs, id, accountID, folderID string, uid uint32) {
	t.Helper()
	if err := dbs.UpsertMessage(context.Background(), Message{
		ID: id, AccountID: accountID, FolderID: folderID, UID: uid, ContentKey: id,
		RawBlob: []byte("raw " + id),
	}); err != nil {
		t.Fatalf("seed live %s: %v", id, err)
	}
}

func seedDisabledRaw(t *testing.T, dbs *DBs, id, accountID, folderID, reason string, uid uint32, blob []byte, rawPath string) {
	t.Helper()
	if err := dbs.UpsertMessage(context.Background(), Message{
		ID: id, AccountID: accountID, FolderID: folderID, UID: uid, ContentKey: id,
		RawBlob: blob, RawPath: rawPath,
		DisabledAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), DisabledReason: reason,
	}); err != nil {
		t.Fatalf("seed disabled %s: %v", id, err)
	}
}

func disabledBlobOf(t *testing.T, dbs *DBs, id string) string {
	t.Helper()
	var blob string
	if err := dbs.Mirror.Read.QueryRow(
		`SELECT COALESCE(disabled_blob, '') FROM messages WHERE id = ?`, id).Scan(&blob); err != nil {
		t.Fatalf("read disabled_blob of %s: %v", id, err)
	}
	return blob
}
