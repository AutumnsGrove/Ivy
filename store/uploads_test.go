package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

// uploadNow keeps staging timestamps deterministic so the sweep is asserted
// without sleeping.
var uploadNow = time.Date(2026, 10, 6, 17, 0, 0, 0, time.UTC)

func newUpload(id, account, name, mime string, content string) (Upload, io.Reader) {
	return Upload{
		ID: id, AccountID: account, Name: name, MIMEType: mime, CreatedAt: uploadNow,
	}, bytes.NewReader([]byte(content))
}

// A staged upload is readable back by its account and id, and its bytes are on
// disk under the content hash.
func TestStageUploadRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	up, r := newUpload("u1", "acct-1", "photo.jpg", "image/jpeg", "the bytes")
	staged, err := dbs.StageUpload(ctx, up, r, 1024)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if staged.Hash == "" || staged.Size != int64(len("the bytes")) {
		t.Fatalf("staged = %+v, want a hash and the byte size", staged)
	}
	got, err := dbs.GetUpload(ctx, "acct-1", "u1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "photo.jpg" || got.MIMEType != "image/jpeg" {
		t.Errorf("got = %+v, want the stored name and mime", got)
	}
	rc, err := dbs.OpenUpload(got)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "the bytes" {
		t.Errorf("bytes = %q, want the staged content", data)
	}
}

// Identical bytes are stored once, so the same photo attached twice costs one
// file; deleting one upload must not remove the other's file.
func TestStageUploadDedupesAndKeepsSharedFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	first, r1 := newUpload("u1", "acct-1", "a.jpg", "image/jpeg", "same")
	a, err := dbs.StageUpload(ctx, first, r1, 1024)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, r2 := newUpload("u2", "acct-1", "b.jpg", "image/jpeg", "same")
	b, err := dbs.StageUpload(ctx, second, r2, 1024)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a.Hash != b.Hash {
		t.Fatalf("hashes differ: %q and %q, want one file", a.Hash, b.Hash)
	}
	if _, err := os.Stat(dbs.Uploads.Path(a.Hash)); err != nil {
		t.Fatalf("file missing: %v", err)
	}
	if err := dbs.DeleteUpload(ctx, "acct-1", "u1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(dbs.Uploads.Path(b.Hash)); err != nil {
		t.Errorf("shared file removed with the first row: %v", err)
	}
	if err := dbs.DeleteUpload(ctx, "acct-1", "u2"); err != nil {
		t.Fatalf("delete second: %v", err)
	}
	if _, err := os.Stat(dbs.Uploads.Path(b.Hash)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still present after the last row went: %v", err)
	}
}

// A file over the per-upload cap is refused and leaves nothing behind.
func TestStageUploadRefusesTooLarge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	up, r := newUpload("u1", "acct-1", "big.bin", "application/octet-stream", "0123456789")
	if _, err := dbs.StageUpload(ctx, up, r, 4); !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("err = %v, want ErrUploadTooLarge", err)
	}
	if _, err := dbs.GetUpload(ctx, "acct-1", "u1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("row survived the refusal: %v", err)
	}
}

// A retried stage with the same id returns the existing row and does not write
// a second file.
func TestStageUploadRepeatIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	up, r := newUpload("u1", "acct-1", "a.jpg", "image/jpeg", "one")
	first, err := dbs.StageUpload(ctx, up, r, 1024)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, r2 := newUpload("u1", "acct-1", "a.jpg", "image/jpeg", "two")
	second, err := dbs.StageUpload(ctx, again, r2, 1024)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.Hash != first.Hash || second.Size != first.Size {
		t.Errorf("repeat = %+v, want the stored row %+v", second, first)
	}
	rc, _ := dbs.OpenUpload(second)
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "one" {
		t.Errorf("bytes = %q, want the first staged content", data)
	}
}

// An upload is scoped to its account: another account's id is not found and its
// delete is refused.
func TestUploadsAreAccountScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	up, r := newUpload("u1", "acct-1", "a.jpg", "image/jpeg", "bytes")
	if _, err := dbs.StageUpload(ctx, up, r, 1024); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, err := dbs.GetUpload(ctx, "acct-2", "u1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account get: %v, want ErrNotFound", err)
	}
	if err := dbs.DeleteUpload(ctx, "acct-2", "u1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account delete: %v, want ErrNotFound", err)
	}
	if _, err := dbs.GetUpload(ctx, "acct-1", "u1"); err != nil {
		t.Errorf("the row was removed by another account: %v", err)
	}
}

// The sweep drops uploads older than the cutoff, and their files once no row
// shares the hash; a newer upload and a shared file survive.
func TestSweepUploadsDropsOnlyTheOldAndUnshared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	old := Upload{ID: "old", AccountID: "acct-1", Name: "old.bin", MIMEType: "application/octet-stream", CreatedAt: uploadNow.Add(-48 * time.Hour)}
	if _, err := dbs.StageUpload(ctx, old, bytes.NewReader([]byte("old")), 1024); err != nil {
		t.Fatalf("old: %v", err)
	}
	fresh := Upload{ID: "fresh", AccountID: "acct-1", Name: "fresh.bin", MIMEType: "application/octet-stream", CreatedAt: uploadNow}
	if _, err := dbs.StageUpload(ctx, fresh, bytes.NewReader([]byte("fresh")), 1024); err != nil {
		t.Fatalf("fresh: %v", err)
	}
	// A fresh upload sharing the old bytes keeps that file even as the old row goes.
	shared := Upload{ID: "shared", AccountID: "acct-1", Name: "shared.bin", MIMEType: "application/octet-stream", CreatedAt: uploadNow}
	if _, err := dbs.StageUpload(ctx, shared, bytes.NewReader([]byte("old")), 1024); err != nil {
		t.Fatalf("shared: %v", err)
	}

	n, err := dbs.SweepUploads(ctx, uploadNow.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("swept %d rows, want 1", n)
	}
	if _, err := dbs.GetUpload(ctx, "acct-1", "old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old row survived: %v", err)
	}
	if _, err := dbs.GetUpload(ctx, "acct-1", "fresh"); err != nil {
		t.Errorf("fresh row removed: %v", err)
	}
	if _, err := dbs.GetUpload(ctx, "acct-1", "shared"); err != nil {
		t.Errorf("shared row removed: %v", err)
	}
	kept, _ := dbs.GetUpload(ctx, "acct-1", "shared")
	if _, err := os.Stat(dbs.Uploads.Path(kept.Hash)); err != nil {
		t.Errorf("file shared with a live row was removed: %v", err)
	}
}

// gateReader serves its first half, then blocks until the gate opens, so a test
// can hold a stage mid-flight.
type gateReader struct {
	first, rest *bytes.Reader
	started     chan struct{}
	gate        chan struct{}
	begun       bool
}

func (g *gateReader) Read(p []byte) (int, error) {
	if !g.begun {
		g.begun = true
		close(g.started)
	}
	if n, err := g.first.Read(p); n > 0 || err == nil {
		return n, nil
	}
	<-g.gate
	return g.rest.Read(p)
}

// A delete that runs while another stage of the same bytes is mid-flight must not
// remove the shared blob: the stage's row is not inserted yet, so counting rows
// would call the file unreferenced and leave the new row pointing at nothing. The
// delete still returns promptly (it never waits behind a slow upload); removing
// the file is left to the orphan sweep.
func TestDeleteUploadKeepsABlobAStageIsStillUsing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	first, r := newUpload("b", "acct-1", "one.bin", "application/octet-stream", "same-bytes")
	staged, err := dbs.StageUpload(ctx, first, r, 1024)
	if err != nil {
		t.Fatalf("stage b: %v", err)
	}

	g := &gateReader{
		first: bytes.NewReader([]byte("same-")), rest: bytes.NewReader([]byte("bytes")),
		started: make(chan struct{}), gate: make(chan struct{}),
	}
	second, _ := newUpload("a", "acct-1", "two.bin", "application/octet-stream", "")
	stageErr := make(chan error, 1)
	go func() {
		_, err := dbs.StageUpload(ctx, second, g, 1024)
		stageErr <- err
	}()
	<-g.started

	deleted := make(chan error, 1)
	go func() { deleted <- dbs.DeleteUpload(ctx, "acct-1", "b") }()
	select {
	case err := <-deleted:
		if err != nil {
			t.Fatalf("delete b: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delete waited behind an in-flight upload")
	}
	if !dbs.Uploads.Has(staged.Hash) {
		t.Fatal("the blob was removed while a stage of the same bytes was in flight")
	}

	close(g.gate)
	if err := <-stageErr; err != nil {
		t.Fatalf("stage a: %v", err)
	}
	got, err := dbs.GetUpload(ctx, "acct-1", "a")
	if err != nil {
		t.Fatalf("get a: %v", err)
	}
	rc, err := dbs.OpenUpload(got)
	if err != nil {
		t.Fatalf("open a: %v", err)
	}
	defer rc.Close()
	if data, _ := io.ReadAll(rc); string(data) != "same-bytes" {
		t.Errorf("bytes = %q, want the staged content", data)
	}
}

// A blob with no row (a crash or failed insert after the write) and a stale temp
// file from an interrupted write are collected; a referenced blob is kept.
func TestSweepOrphanUploadsCollectsUnreferencedFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	up, r := newUpload("keep", "acct-1", "keep.bin", "application/octet-stream", "referenced")
	kept, err := dbs.StageUpload(ctx, up, r, 1024)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	orphan, _, err := dbs.Uploads.Put(ctx, bytes.NewReader([]byte("no row points here")))
	if err != nil {
		t.Fatalf("put orphan: %v", err)
	}
	stale := dbs.Uploads.Dir() + "/.tmp-interrupted"
	if err := os.WriteFile(stale, []byte("half"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}

	n, err := dbs.SweepOrphanUploads(ctx)
	if err != nil {
		t.Fatalf("sweep orphans: %v", err)
	}
	if n != 2 {
		t.Errorf("removed %d files, want the orphan blob and the temp", n)
	}
	if dbs.Uploads.Has(orphan) {
		t.Error("the unreferenced blob survived")
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stale temp survived: %v", err)
	}
	if !dbs.Uploads.Has(kept.Hash) {
		t.Error("the referenced blob was removed")
	}
}

// While a stage is mid-flight its blob has no row yet and its temp file is live, so
// the sweep stands down instead of waiting or deleting.
func TestSweepOrphanUploadsStandsDownDuringAStage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	orphan, _, err := dbs.Uploads.Put(ctx, bytes.NewReader([]byte("an orphan")))
	if err != nil {
		t.Fatalf("put orphan: %v", err)
	}
	g := &gateReader{
		first: bytes.NewReader([]byte("in-")), rest: bytes.NewReader([]byte("flight")),
		started: make(chan struct{}), gate: make(chan struct{}),
	}
	up, _ := newUpload("a", "acct-1", "a.bin", "application/octet-stream", "")
	done := make(chan error, 1)
	go func() {
		_, err := dbs.StageUpload(ctx, up, g, 1024)
		done <- err
	}()
	<-g.started

	n, err := dbs.SweepOrphanUploads(ctx)
	if err != nil {
		t.Fatalf("sweep orphans: %v", err)
	}
	if n != 0 || !dbs.Uploads.Has(orphan) {
		t.Errorf("removed %d with a stage in flight, want the sweep to stand down", n)
	}
	close(g.gate)
	if err := <-done; err != nil {
		t.Fatalf("stage: %v", err)
	}
	if got, err := dbs.GetUpload(ctx, "acct-1", "a"); err != nil || !dbs.Uploads.Has(got.Hash) {
		t.Errorf("the in-flight stage lost its blob: %v", err)
	}
}
