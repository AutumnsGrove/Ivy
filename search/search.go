package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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

// scoredRef is the best chunk score of one embedded document: a message body
// (ref is its content key) or an attachment (ref is its content hash).
type scoredRef struct {
	accountID string
	ref       string
	kind      string
	score     float64
}

// VectorSearch returns the messages nearest to a query embedding by
// brute-force cosine, streamed from SQLite in one pass. A document's score is
// its best chunk. It never loads every vector into memory; only the running
// best score per document is kept, which is a few bytes each. An attachment
// match is a match on every message that carries the file, so it is resolved
// back to those messages, best first, until limit messages are found.
func (s *Service) VectorSearch(ctx context.Context, query llm.Vector, accounts []string, model string, limit int) ([]Hit, error) {
	if limit <= 0 || query.Dims == 0 {
		return nil, nil
	}
	best := make(map[string]*scoredRef)
	err := s.dbs.EachEmbedding(ctx, accounts, model, func(e store.Embedding) error {
		v, ok := llm.DecodeVector(e.Vector, e.Scale, e.Norm, e.Dims)
		if !ok || v.Dims != query.Dims {
			return nil // a corrupt or mismatched row is skipped, never scored
		}
		score := query.Cosine(v)
		key := e.AccountID + "\x00" + e.Kind + "\x00" + e.Ref
		if prev, ok := best[key]; ok {
			prev.score = max(prev.score, score)
			return nil
		}
		best[key] = &scoredRef{accountID: e.AccountID, ref: e.Ref, kind: e.Kind, score: score}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ranked := make([]*scoredRef, 0, len(best))
	for _, r := range best {
		ranked = append(ranked, r)
	}
	// Ties break on identity so the order is the same on every run.
	slices.SortFunc(ranked, func(a, b *scoredRef) int {
		return cmp.Or(
			cmp.Compare(b.score, a.score),
			cmp.Compare(a.accountID, b.accountID),
			cmp.Compare(a.ref, b.ref),
		)
	})

	var hits []Hit
	seen := make(map[string]bool)
	add := func(accountID, contentKey string) {
		key := accountID + "\x00" + contentKey
		if seen[key] {
			return
		}
		seen[key] = true
		hits = append(hits, Hit{AccountID: accountID, ContentKey: contentKey})
	}
	for _, r := range ranked {
		if len(hits) >= limit {
			break
		}
		if r.kind != store.ExtractKindAttachment {
			add(r.accountID, r.ref)
			continue
		}
		refs, err := s.dbs.ContentRefsForAttachment(ctx, r.accountID, r.ref)
		if err != nil {
			return nil, err
		}
		for _, m := range refs {
			add(m.AccountID, m.ContentKey)
		}
	}
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

// maxConsecutiveFailures ends a pass when this many documents in a row fail: one
// refused document is skipped, but a run of failures means the provider or the
// store is down, and hammering it pass after pass helps nobody.
const maxConsecutiveFailures = 3

// maxDocumentRejections is how many separate calls a provider may refuse for one
// document (HTTP 400, 413 or 422, recorded as `rejected` in the ledger) before the
// worker stops offering it. Outages and rate limits never count.
const maxDocumentRejections = 5

// embedJob is one pending document: a message body (ref is its content key) or
// an attachment (ref is its content hash) and the chunks to embed.
type embedJob struct {
	ref    string
	kind   string
	chunks []string
}

func (w *EmbedWorker) embedBodies(ctx context.Context, acct AccountConfig) (int, error) {
	refs, err := w.dbs.PendingBodyRefs(ctx, acct.ID, acct.Model, w.batch)
	if err != nil {
		return 0, err
	}
	jobs := make([]embedJob, len(refs))
	for i, ref := range refs {
		jobs[i] = embedJob{ref: ref.ContentKey, kind: store.ExtractKindBody, chunks: DocChunks(ref.Subject, ref.Body)}
	}
	return w.runJobs(ctx, acct, jobs)
}

func (w *EmbedWorker) embedAttachments(ctx context.Context, acct AccountConfig) (int, error) {
	refs, err := w.dbs.PendingAttachmentRefs(ctx, acct.ID, acct.Model, w.batch)
	if err != nil {
		return 0, err
	}
	jobs := make([]embedJob, len(refs))
	for i, ref := range refs {
		jobs[i] = embedJob{ref: ref.Hash, kind: store.ExtractKindAttachment, chunks: Chunk(ref.Text, ChunkTokens, ChunkOverlap)}
	}
	return w.runJobs(ctx, acct, jobs)
}

// runJobs embeds one bounded batch of pending documents. The queue is newest
// first and bounded, so a document that can never leave it would sit at the head
// and starve everything behind it: a document with nothing to embed is recorded
// as done, and one the provider refuses is skipped (and logged) rather than
// ending the pass. A policy refusal ends the pass quietly, since the rest of the
// account would be refused the same way.
func (w *EmbedWorker) runJobs(ctx context.Context, acct AccountConfig, jobs []embedJob) (int, error) {
	embedded, failures := 0, 0
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return embedded, err
		}
		if len(job.chunks) == 0 {
			if err := w.dbs.MarkEmbeddingEmpty(ctx, acct.ID, job.ref, job.kind, acct.Model); err != nil {
				return embedded, err
			}
			continue
		}
		err := w.embedChunks(ctx, acct, job.ref, job.kind, job.chunks)
		switch {
		case err == nil:
			embedded++
			failures = 0
		case errors.Is(err, llm.ErrNotEnabled), errors.Is(err, llm.ErrCapReached):
			return embedded, nil
		case ctx.Err() != nil:
			return embedded, ctx.Err()
		default:
			failures++
			slog.WarnContext(ctx, "embed worker: skipped a document that failed",
				"account", acct.ID, "kind", job.kind, "ref", job.ref, "error", err)
			if err := w.giveUpIfRefusedTooOften(ctx, acct, job); err != nil {
				return embedded, err
			}
			if failures >= maxConsecutiveFailures {
				return embedded, err
			}
		}
	}
	return embedded, nil
}

// giveUpIfRefusedTooOften records a document as skipped once the provider has
// refused it maxDocumentRejections times, so it stops costing a failed call and a
// ledger row every pass. Only refusals of the document itself are counted (the
// gate records them as `rejected`); an outage never gives a document up.
func (w *EmbedWorker) giveUpIfRefusedTooOften(ctx context.Context, acct AccountConfig, job embedJob) error {
	n, err := w.dbs.CountRejectedCalls(ctx, acct.ID, job.ref)
	if err != nil || n < maxDocumentRejections {
		return err
	}
	slog.WarnContext(ctx, "embed worker: giving up on a document the provider keeps refusing",
		"account", acct.ID, "kind", job.kind, "ref", job.ref, "refusals", n)
	return w.dbs.MarkEmbeddingEmpty(ctx, acct.ID, job.ref, job.kind, acct.Model)
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
