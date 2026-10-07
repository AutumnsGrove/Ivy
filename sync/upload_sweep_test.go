package sync_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// The worker's prune pass collects staging blobs no row references, so a crash
// between writing a file and recording it cannot grow the disk without bound.
func TestOutboxRunSweepsOrphanUploads(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	orphan, _, err := fx.dbs.Uploads.Put(fx.ctx, bytes.NewReader([]byte("nothing points here")))
	if err != nil {
		t.Fatalf("put orphan: %v", err)
	}

	ctx, cancel := context.WithCancel(fx.ctx)
	worker := ivysync.NewOutboxWorker(ivysync.NewFetcher(fx.dbs), fx.acct, ivysync.WithOutboxPoll(10*time.Millisecond))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for fx.dbs.Uploads.Has(orphan) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if fx.dbs.Uploads.Has(orphan) {
		t.Fatal("the orphan blob survived the worker's prune pass")
	}
}

// The same pass collects draft body files no row references, so a refused or
// interrupted save cannot leak tens of MiB.
func TestOutboxRunSweepsOrphanDraftBodies(t *testing.T) {
	t.Parallel()
	fx := newOutboxFixture(t)
	orphan, _, err := fx.dbs.DraftBodies.Put(fx.ctx, bytes.NewReader([]byte("a body no row names")))
	if err != nil {
		t.Fatalf("put orphan: %v", err)
	}

	ctx, cancel := context.WithCancel(fx.ctx)
	worker := ivysync.NewOutboxWorker(ivysync.NewFetcher(fx.dbs), fx.acct, ivysync.WithOutboxPoll(10*time.Millisecond))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for fx.dbs.DraftBodies.Has(orphan) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if fx.dbs.DraftBodies.Has(orphan) {
		t.Fatal("the orphan draft body survived the worker's prune pass")
	}
}
