package sync_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// pdfWith builds a one-page PDF with a text layer, so extraction has something
// real to find.
func pdfWith(text string) []byte {
	var b bytes.Buffer
	var offsets []int
	writeObj := func(n int, body string) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	b.WriteString("%PDF-1.4\n")
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	content := fmt.Sprintf("BT /F1 24 Tf 72 720 Td (%s) Tj ET", text)
	writeObj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	writeObj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	xref := b.Len()
	b.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return b.Bytes()
}

func TestFetchExtractsAndIndexesAttachmentText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	t0 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	uid := acc.Deliver("INBOX", mailworld.Msg().From("billing@example.com").Subject("Invoice").
		Date(t0).MessageID("<inv@grove.test>").Text("see attached").
		Attach("invoice.pdf", "application/pdf", pdfWith("Invoice total forty two")).Build())

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	m := mustMessage(t, dbs, inbox.ID, uid)
	atts, err := dbs.ListAttachments(ctx, m.ID)
	if err != nil || len(atts) != 1 {
		t.Fatalf("attachments = %+v, %v", atts, err)
	}
	et, err := dbs.GetExtractedText(ctx, atts[0].ContentHash, store.ExtractKindAttachment)
	if err != nil {
		t.Fatalf("extracted text: %v", err)
	}
	if et.Status != "ok" || !strings.Contains(et.Text, "Invoice total forty two") {
		t.Fatalf("extracted = %+v, want the PDF's words", et)
	}
	// The attachment text reaches the full-text index.
	hits, err := dbs.SearchFTS(ctx, "forty", []string{"acct-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ContentKey != m.ContentKey {
		t.Fatalf("search hits = %+v, want the message with the invoice", hits)
	}
}

func TestUnsupportedAttachmentIsRecordedOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("Photo").
		Date(time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)).MessageID("<p@grove.test>").
		Text("a picture").Attach("photo.bin", "application/octet-stream", []byte{1, 2, 3}).Build())

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	inbox := mustFolder(t, dbs, "acct-1", "INBOX")
	m := mustMessage(t, dbs, inbox.ID, uid)
	atts, _ := dbs.ListAttachments(ctx, m.ID)
	et, err := dbs.GetExtractedText(ctx, atts[0].ContentHash, store.ExtractKindAttachment)
	if err != nil || et.Status != "unsupported" {
		t.Fatalf("extracted = %+v, %v; want unsupported", et, err)
	}
	// A recorded outcome means it is not queued again.
	pending, err := dbs.PendingExtractions(ctx, "acct-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %+v, want none", pending)
	}
}

// One file shared by several messages is read once, keyed by its content hash.
// Only the one message the pending query happened to pick was reindexed, so the
// text was findable through one of them and silently missing from the rest.
func TestSharedAttachmentTextIsIndexedForEveryMessageCarryingIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	pdf := pdfWith("Quarterly forecast figures")
	for i, id := range []string{"<a@grove.test>", "<b@grove.test>"} {
		acc.Deliver("INBOX", mailworld.Msg().From("fin@example.com").Subject(fmt.Sprintf("Forecast %d", i)).
			Date(time.Date(2026, 4, 1+i, 9, 0, 0, 0, time.UTC)).MessageID(id).Text("see attached").
			Attach("forecast.pdf", "application/pdf", pdf).Build())
	}

	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, accountFor(t, w, "acct-1", "me@grove.test", "secret")); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// "figures" is only in the PDF, not in either subject or body.
	hits, err := dbs.SearchFTS(ctx, "figures", []string{"acct-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("%d messages found by the shared attachment's text, want both", len(hits))
	}
}
