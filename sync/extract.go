package sync

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/AutumnsGrove/Ivy/extract"
	mailmime "github.com/AutumnsGrove/Ivy/mime"
	"github.com/AutumnsGrove/Ivy/store"
)

// extractBatch bounds how many attachments one settle pass reads, so a mailbox
// full of PDFs heals across runs instead of stalling a fetch.
const extractBatch = 100

// indexBatch bounds how many messages one settle pass gives a search document to
// when they predate the index.
const indexBatch = 500

// extractTimeout bounds one attachment's extraction. A hostile or enormous
// document cannot hold a sync worker past this (STANDARDS.md 4a).
const extractTimeout = 20 * time.Second

// ExtractTexts pulls the text out of up to limit attachments that have not been
// read yet, records it in extracted_text and reindexes the messages that carry
// them, so attachment text becomes searchable. It is a background pass: a slow
// PDF never stalls the fetch itself. Every outcome is recorded, including
// unsupported and failed, so an attachment is tried once.
func (f *Fetcher) ExtractTexts(ctx context.Context, accountID string, limit int) (int, error) {
	pending, err := f.dbs.PendingExtractions(ctx, accountID, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, p := range pending {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		m, err := f.dbs.GetMessage(ctx, p.MessageID)
		if errors.Is(err, store.ErrNotFound) {
			continue // hidden since the list was taken
		}
		if err != nil {
			return done, err
		}
		res := f.extractOne(ctx, m, p)
		if err := f.dbs.UpsertExtractedText(ctx, store.ExtractedText{
			Ref: p.Hash, Kind: store.ExtractKindAttachment,
			Tier: res.Tier, Status: res.Status, Text: res.Text,
			DerivedVersion: DerivedVersion,
		}); err != nil {
			return done, err
		}
		// The text is keyed by the file's hash, so every message that carries the
		// file gets it in its search document, not only the one picked to read it.
		refs, err := f.dbs.ContentRefsForAttachment(ctx, "", p.Hash)
		if err != nil {
			return done, err
		}
		for _, ref := range refs {
			if err := f.dbs.ReindexContent(ctx, ref.AccountID, ref.ContentKey); err != nil {
				return done, err
			}
		}
		done++
	}
	return done, nil
}

// extractOne reads one attachment's decoded bytes, bounded, and extracts its
// text. Anything over the input cap is recorded as too_large without reading.
func (f *Fetcher) extractOne(ctx context.Context, m store.Message, p store.PendingExtraction) extract.Result {
	if p.Size > extract.MaxInputBytes {
		return extract.Result{Tier: extract.TierDocument, Status: extract.StatusTooLarge}
	}
	rc, err := f.openRaw(m)
	if err != nil {
		return extract.Result{Tier: extract.TierDocument, Status: extract.StatusFailed}
	}
	defer func() { _ = rc.Close() }()

	var buf bytes.Buffer
	if err := mailmime.CopyPart(rc, p.Path, &buf); err != nil {
		// A corrupt base64 part is a fact about that attachment, not the message.
		return extract.Result{Tier: extract.TierDocument, Status: extract.StatusFailed}
	}
	ectx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()
	return extract.Extract(ectx, p.Filename, p.MIME, buf.Bytes())
}

// openRaw opens a message's original bytes, from its spool file or its inline
// blob, for reading one part out of it.
func (f *Fetcher) openRaw(m store.Message) (io.ReadCloser, error) {
	if m.RawPath != "" {
		return os.Open(filepath.Join(f.dbs.Dir, filepath.FromSlash(m.RawPath))) //nolint:gosec // G304: a path this package built from a folder id and a UID
	}
	return io.NopCloser(bytes.NewReader(m.RawBlob)), nil
}
