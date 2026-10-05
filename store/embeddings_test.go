package store

import (
	"context"
	"testing"
	"time"
)

func TestEmbeddingsRoundTripAndPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct")
	seedFolder(t, dbs, "acct", "f1")
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "m1", AccountID: "acct", FolderID: "f1", UID: 1,
		ContentKey: "ck1", Subject: "Report", BodyText: "quarterly numbers", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	pending, err := dbs.PendingBodyRefs(ctx, "acct", "model-x", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ContentKey != "ck1" {
		t.Fatalf("pending = %+v, want ck1", pending)
	}

	e := Embedding{AccountID: "acct", Ref: "ck1", Kind: ExtractKindBody, ChunkIx: 0,
		Model: "model-x", Dims: 2, Scale: 0.1, Norm: 1.5, Vector: []byte{2, 3, 4, 5}}
	if err := dbs.UpsertEmbeddings(ctx, []Embedding{e}); err != nil {
		t.Fatal(err)
	}
	if got, _ := dbs.PendingBodyRefs(ctx, "acct", "model-x", 10); len(got) != 0 {
		t.Fatalf("still pending after embedding: %+v", got)
	}
	// A different model is not yet embedded.
	if got, _ := dbs.PendingBodyRefs(ctx, "acct", "model-y", 10); len(got) != 1 {
		t.Fatalf("other model pending = %+v, want 1", got)
	}

	var seen []Embedding
	if err := dbs.EachEmbedding(ctx, []string{"acct"}, "model-x", func(e Embedding) error {
		seen = append(seen, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].Ref != "ck1" || seen[0].Dims != 2 {
		t.Fatalf("streamed = %+v", seen)
	}
}

func TestPendingAttachmentRefs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct")
	seedFolder(t, dbs, "acct", "f1")
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "m1", AccountID: "acct", FolderID: "f1", UID: 1,
		ContentKey: "ck1", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.SetMessageDerived(ctx, "m1", Derived{Version: 1, Attachments: []Attachment{
		{MessageID: "m1", Filename: "invoice.pdf", MIMEType: "application/pdf", Size: 10, ContentHash: "h1", StoragePath: "2"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertExtractedText(ctx, ExtractedText{
		Ref: "h1", Kind: ExtractKindAttachment, Tier: 1, Status: "ok", Text: "invoice total 42",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := dbs.PendingAttachmentRefs(ctx, "acct", "model-x", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Hash != "h1" || got[0].Text != "invoice total 42" {
		t.Fatalf("pending attachments = %+v", got)
	}
	if err := dbs.UpsertEmbeddings(ctx, []Embedding{
		{AccountID: "acct", Ref: "h1", Kind: ExtractKindAttachment, Model: "model-x", Dims: 2, Vector: []byte{0, 0, 0, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := dbs.PendingAttachmentRefs(ctx, "acct", "model-x", 10); len(got) != 0 {
		t.Fatalf("still pending: %+v", got)
	}
}

// An attachment on a disabled message must not be queued for embedding.
func TestPendingAttachmentSkipsHidden(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct")
	seedFolder(t, dbs, "acct", "f1")
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "m1", AccountID: "acct", FolderID: "f1", UID: 1,
		ContentKey: "ck1", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.SetMessageDerived(ctx, "m1", Derived{Version: 1, Attachments: []Attachment{
		{MessageID: "m1", Filename: "x.pdf", MIMEType: "application/pdf", ContentHash: "h1"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertExtractedText(ctx, ExtractedText{Ref: "h1", Kind: ExtractKindAttachment, Status: "ok", Text: "text"}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.DisableMessage(ctx, "m1", "server_removed", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := dbs.PendingAttachmentRefs(ctx, "acct", "model-x", 10); len(got) != 0 {
		t.Fatalf("hidden attachment queued: %+v", got)
	}
}
