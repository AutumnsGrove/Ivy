package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// Service answers hybrid queries and runs the embed-once queue over the mirror.
type Service struct {
	dbs  *store.DBs
	gate *llm.Gate
}

// New builds the search service. A nil gate disables all embedding.
func New(dbs *store.DBs, gate *llm.Gate) *Service {
	return &Service{dbs: dbs, gate: gate}
}

// VectorSearch returns the content keys nearest to a query embedding by
// brute-force cosine, streamed from SQLite in one pass. A document's score is
// its best chunk. It never loads every vector into memory; only the running
// best score per content key is kept, which is a few bytes per document.
func (s *Service) VectorSearch(ctx context.Context, query llm.Vector, accounts []string, model string, limit int) ([]Hit, error) {
	if limit <= 0 || query.Dims == 0 {
		return nil, nil
	}
	best := make(map[string]float64)
	var hits []Hit
	err := s.dbs.EachEmbedding(ctx, accounts, model, func(e store.Embedding) error {
		v, ok := llm.DecodeVector(e.Vector, e.Scale, e.Norm, e.Dims)
		if !ok || v.Dims != query.Dims {
			return nil // a corrupt or mismatched row is skipped, never scored
		}
		score := query.Cosine(v)
		key := e.AccountID + "\x00" + e.Ref
		if prev, ok := best[key]; ok {
			if score > prev {
				best[key] = score
			}
			return nil
		}
		best[key] = score
		hits = append(hits, Hit{AccountID: e.AccountID, ContentKey: e.Ref})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range hits {
		hits[i] = Hit{AccountID: hits[i].AccountID, ContentKey: hits[i].ContentKey}
	}
	// Sort by the best chunk score, descending.
	sort.SliceStable(hits, func(i, j int) bool {
		return best[hits[i].AccountID+"\x00"+hits[i].ContentKey] > best[hits[j].AccountID+"\x00"+hits[j].ContentKey]
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// Hybrid fuses an FTS list and a vector list with reciprocal rank fusion. A
// nil or empty vector list is fine: the caller has already fallen back to
// keyword-only and the fusion just returns the FTS order.
func (s *Service) Hybrid(fts, vec []Hit, limit int) []Hit {
	return Fuse([][]Hit{fts, vec}, DefaultRRFK, limit)
}

// AccountConfig is one account's embedding policy, from ivy.yaml.
type AccountConfig struct {
	ID       string
	Embedder llm.Embedder
	Model    string
	Enabled  bool
	CapUSD   float64
}

// WorkerOptions tune the embed-once queue.
type WorkerOptions struct {
	Batch    int
	Interval time.Duration
	Now      func() time.Time
}

// EmbedWorker embeds each message and attachment once, keyed by content
// identity, so a move, an archive, a flag change or a UIDVALIDITY reset never
// pays again (ARCHITECTURE.md 3). It is low priority: one job at a time, a
// bounded batch per pass, and it sleeps between passes.
type EmbedWorker struct {
	dbs      *store.DBs
	gate     *llm.Gate
	accounts []AccountConfig
	batch    int
	interval time.Duration
}

// NewEmbedWorker builds the queue worker.
func NewEmbedWorker(dbs *store.DBs, gate *llm.Gate, accounts []AccountConfig, opts WorkerOptions) *EmbedWorker {
	w := &EmbedWorker{dbs: dbs, gate: gate, accounts: accounts, batch: opts.Batch, interval: opts.Interval}
	if w.batch <= 0 {
		w.batch = 32
	}
	if w.interval <= 0 {
		w.interval = time.Minute
	}
	return w
}

// RunOnce embeds one bounded batch for every account that has a provider. It
// returns how many documents it embedded.
func (w *EmbedWorker) RunOnce(ctx context.Context) (int, error) {
	if w.gate == nil {
		return 0, nil
	}
	embedded := 0
	for _, acct := range w.accounts {
		if acct.Embedder == nil {
			continue
		}
		n, err := w.embedBodies(ctx, acct)
		embedded += n
		if err != nil {
			return embedded, err
		}
		n, err = w.embedAttachments(ctx, acct)
		embedded += n
		if err != nil {
			return embedded, err
		}
	}
	return embedded, nil
}

// Run drains the queue, sleeping between passes until ctx is cancelled. A
// provider outage is logged and retried, never fatal: search falls back to
// keyword until it recovers (ARCHITECTURE.md 6).
func (w *EmbedWorker) Run(ctx context.Context) error {
	for {
		if _, err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.WarnContext(ctx, "embed worker pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.interval):
		}
	}
}

func (w *EmbedWorker) embedBodies(ctx context.Context, acct AccountConfig) (int, error) {
	refs, err := w.dbs.PendingBodyRefs(ctx, acct.ID, acct.Model, w.batch)
	if err != nil {
		return 0, err
	}
	embedded := 0
	for _, ref := range refs {
		if ctx.Err() != nil {
			return embedded, ctx.Err()
		}
		chunks := DocChunks(ref.Subject, ref.Body)
		if len(chunks) == 0 {
			continue
		}
		if err := w.embedChunks(ctx, acct, ref.ContentKey, store.ExtractKindBody, chunks); err != nil {
			if errors.Is(err, llm.ErrNotEnabled) || errors.Is(err, llm.ErrCapReached) {
				return embedded, nil // no point trying the rest of this account
			}
			return embedded, err
		}
		embedded++
	}
	return embedded, nil
}

func (w *EmbedWorker) embedAttachments(ctx context.Context, acct AccountConfig) (int, error) {
	refs, err := w.dbs.PendingAttachmentRefs(ctx, acct.ID, acct.Model, w.batch)
	if err != nil {
		return 0, err
	}
	embedded := 0
	for _, ref := range refs {
		if ctx.Err() != nil {
			return embedded, ctx.Err()
		}
		chunks := Chunk(ref.Text, ChunkTokens, ChunkOverlap)
		if len(chunks) == 0 {
			continue
		}
		if err := w.embedChunks(ctx, acct, ref.Hash, store.ExtractKindAttachment, chunks); err != nil {
			if errors.Is(err, llm.ErrNotEnabled) || errors.Is(err, llm.ErrCapReached) {
				return embedded, nil
			}
			return embedded, err
		}
		embedded++
	}
	return embedded, nil
}

// embedChunks sends one document's chunks through the gate in batches of at
// most llm.MaxBatchInputs, and stores only after a whole batch succeeded.
func (w *EmbedWorker) embedChunks(ctx context.Context, acct AccountConfig, ref, kind string, chunks []string) error {
	for start := 0; start < len(chunks); start += llm.MaxBatchInputs {
		end := start + llm.MaxBatchInputs
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[start:end]
		keys := make([]string, len(batch))
		for i := range keys {
			keys[i] = ref
		}
		vectors, err := w.gate.Embed(ctx, llm.EmbedRequest{
			Embedder: acct.Embedder, AccountID: acct.ID, Enabled: acct.Enabled,
			Model: acct.Model, Feature: "search", Inputs: batch,
			ContentKeys: keys, CapUSD: acct.CapUSD,
		})
		if err != nil {
			return err
		}
		items := make([]store.Embedding, len(vectors))
		for i, v := range vectors {
			items[i] = store.Embedding{
				AccountID: acct.ID, Ref: ref, Kind: kind, ChunkIx: start + i,
				Model: acct.Model, Dims: v.Dims, Scale: v.Scale, Norm: v.Norm,
				Vector: v.Encode(),
			}
		}
		if err := w.dbs.UpsertEmbeddings(ctx, items); err != nil {
			return fmt.Errorf("store embeddings for %s: %w", ref, err)
		}
	}
	return nil
}
