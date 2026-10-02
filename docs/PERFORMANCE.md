# Performance and compression

Status: DRAFT (2026-10-01). Lesson carried from Polaris: it got slow when a long, data-heavy
response arrived all at once. Ivy fixes that structurally from the first commit: compression in the
skeleton, bounded and paged data everywhere, budgets enforced by tests. Numbers below are **starting
budgets to be replaced by measurements** from the potato (`docs/perf.md`, created at the first
measurement).

## 1. Compression

**Over the wire (browser to Ivy over Tailscale; the phone may be on cellular, so bytes still matter
even though WireGuard encrypts the link):**

- **Static assets (the embedded frontend):** at build time, precompress every text asset (JS, CSS,
  HTML, SVG, JSON, fonts that are not already WOFF2) with **brotli at maximum level**, plus **zstd**
  and **gzip** variants, and embed all of them. At runtime the server picks the best variant the
  client's `Accept-Encoding` allows and serves it with zero CPU cost on the potato. Fingerprinted
  filenames get `Cache-Control: public, max-age=31536000, immutable`; `index.html` gets `no-cache`
  plus an `ETag`.
- **Dynamic responses (API JSON, SSE, sanitised HTML):** compressed per request via
  `klauspost/compress/gzhttp` (gzip/zstd) and `andybalholm/brotli`, using a **low-effort level**
  (the potato has four slow cores) and a minimum size threshold. Preference order (S5 and S7,
  `docs/spikes/`): **zstd** where the client advertises it (the cheapest per byte: about 1.6 ms for
  a typical page on the potato; Safari 26.5 on iPad advertises and decodes it), then brotli at
  level 4 to 5, then gzip at 6. Never above brotli 6 on the hot path (brotli 11 took 133 ms for a
  typical page and over a second for a large one). Negotiation is driven strictly by
  `Accept-Encoding`, never by user-agent sniffing.
- **Cache the compressed form** of expensive-to-build, rarely-changing responses (a rendered message
  body's sanitised HTML is stored once and served as-is).
- **SSE:** compressed streams must flush per event; tested so heartbeats still arrive promptly.
- **Large threads:** the API pages a thread (newest messages first, bodies on demand) and the UI
  renders progressively, so an exploding thread is a few small compressed responses instead of one
  huge blob.
- Tests: for each content type, request with each `Accept-Encoding` and assert the encoding chosen,
  `Vary: Accept-Encoding`, correct decoded bytes, and that responses over the threshold are smaller
  than identity.

**At rest (flash is the scarce resource):**

- **Raw mail is tiered by size before it is compressed** (`ARCHITECTURE.md` section 3,
  `STANDARDS.md` 4a): up to 2 MiB a message lives in `raw_blob` (zstd at rest is still deferred and
  now has far less to compress); from 2 MiB to 64 MiB it is a plain `.eml` file in the spool
  (`raw_path`), read from disk in bounded buffers so it is never held in memory; above 64 MiB only
  the envelope is mirrored. Attachments are decoded from the spool file when served, never stored
  decoded. The sanitised HTML and extracted text likewise if measurement says it pays. Embedding
  vectors stay uncompressed (int8 quantisation is the size lever, `ARCHITECTURE.md` section 3).
- Backups are zstd-compressed.
- IMAP: use **`COMPRESS=DEFLATE`** when the server advertises it (Purelymail does) to shrink sync
  traffic. Verified against the mail world, which can advertise it too.

**Implemented (2026-10-02, chunk 1g).** `internal/compress` owns the per-request side:
`Negotiate` (q-values, zstd > brotli > gzip) and a middleware that buffers to `DefaultMinSize`
(1 KB), then streams through pooled zstd (default), brotli 5 and gzip 6 writers, fixing
`Content-Encoding`, `Content-Length`, a per-coding ETag suffix and `Vary: Accept-Encoding`, with
`Flush` for SSE. `internal/asset` owns the build-time side (`Precompress`: brotli 11, zstd best,
gzip 9) and the serving side (`FileServer`: variant negotiation, immutable caching for
fingerprinted files, SPA fallback). `internal/webui` embeds `build/`; `make web-assets` builds the
frontend, copies it in and precompresses it; the gateway mounts it at `/`. Budgets are tests beside
the middleware (a size-ratio tripwire plus per-coding benchmarks).

**Measured on a laptop (2026-10-02; potato re-measure pending) — N4/N5, `papercuts.md`.** The static
server now hashes each embedded file once and serves it without copying (`internal/asset`): a 270 KB
identity asset went 245 µs → 145 µs/op and 1.33 MB → 1.05 MB/op, guarded by
`BenchmarkFileServerIdentity`/`Zstd`. The pooled zstd encoder uses
`zstd.WithEncoderConcurrency(1)` (one response streams at a time): one encoder is 2.33 MB / 30
allocs → 1.76 MB / 17 allocs and is ~35% faster to create, at ~10% single-stream throughput on
this 8-core laptop (`BenchmarkZstdEncoderAlloc`, `BenchmarkMiddleware`). These are relative
laptop numbers, not the section 3 budgets; re-check on the potato when it is next available.

**Implemented (2026-10-02, chunk 2d).** `render/` sanitises a parsed body once, during sync, and
`store.SetMessageBodyHTML` stores the result, so a re-sync cannot lose it and the same sanitised
HTML is served as-is (small, but it is the cached-rendered-body path the plan asked for). Hostile
mail costs bounded time: an XSS corpus, a fuzz target with a 2 s bound and a size cap
(`render.MaxHTMLBytes`) guard it. Laptop benchmark for a ~3 KB HTML body: about 0.23 ms and 140 KB
allocation per body on an M2 (`BenchmarkBody`); a per-message cost, not a per-request one, and the
potato number is a later measurement.

## 2. Frontend load budgets (asserted in CI, after compression)

Initial starting numbers, to tighten once there is something to measure:

| Budget | Starting target |
|---|---|
| Critical path JS (brotli) | <= 80 KB |
| CSS (brotli) | <= 20 KB |
| Fonts on first paint | Lexend + Newsreader subsets, WOFF2, <= 60 KB total, preloaded |
| Inbox first paint, warm cache, simulated 4G + 4x CPU throttle | < 1.0 s |
| Inbox first paint, cold cache, same throttle | < 2.5 s |
| Opening a 200-message thread: first message visible | < 500 ms |

Measured with Playwright (CDP network and CPU throttling) against the real binary and the seeded
mail world. Route-level code splitting keeps rarely used screens (Settings, Stats, Rules) off the
critical path. `webkit` runs for layout; WebKit throttling is limited, so byte budgets are asserted
directly and timing budgets on Chromium.

**Implemented (2026-10-02, chunk 1h).** `web/scripts/size-budget.mjs` (run by `make web-budget`,
and by the `web` CI job after the production build) parses `build/index.html` and asserts the
brotli size of the critical-path JS (budget 80 KiB, currently ~50) and CSS (budget 20 KiB, currently
~6), and fails if the shell references a missing asset. The limits were proved failing when lowered.
Still open: fonts are 303 KiB unsubsetted against the 60 KiB target (and not preloaded), and the
CDP-throttled timing budgets; both tracked in `next_steps.md`.

## 3. Backend budgets (benchmarks that fail when exceeded)

Starting targets on the potato with a seeded **100k-message** mailbox (laptop numbers are only a
sanity check):

| Operation | Starting target |
|---|---|
| Inbox list page (50 rows) from SQLite | < 15 ms |
| Open a thread (metadata + visible bodies) | < 30 ms |
| FTS5 keyword search | < 100 ms |
| Hybrid search (FTS + brute-force vectors over 100k) | < 800 ms |
| Sync apply: 1,000 new messages (headers + flags) | measure; assert no regression > 10% |
| Sanitise one 200 KB HTML message | < 20 ms |
| Resident memory, idle | < 100 MB |
| Resident memory, during backfill + embedding | < 250 MB |
| Cold start to serving | < 2 s |
| Binary size | record; fail on > 25% growth without a note |

Conventions:
- Benchmarks live beside the code (`BenchmarkXxx`, `-benchmem`), use the mail world's seeded
  datasets, and report allocations; allocation counts on hot paths are asserted with
  `testing.AllocsPerRun`.
- `make bench` runs them and writes `bench/<commit>.txt`; `benchstat old new` is how a performance
  claim is made in a commit. CI runs a short benchmark pass as a regression tripwire (on shared
  runners, relative comparisons within one job only; absolute budgets run on the potato).
- Query plans: `EXPLAIN QUERY PLAN` tests prove list/thread/search queries use indexes.
- SQLite tuning is measured, not folklore: WAL, `synchronous=NORMAL`, `mmap_size` and page cache
  sized for the potato, prepared statement reuse, batched write transactions, a single writer.
- Profiling is first-class: a `/debug/pprof` handler behind a config flag, and `ivy doctor`
  prints heap, goroutines and DB size.

## 3b. Dev-loop budgets (settled, round 21: both product and loop speed)

Our own feedback loop is measured like the product, because slow tests stop being run:
- Unit + integration `go test ./...` under about 30 s warm on a dev machine; the day-one E2E smoke
  slice under about 60 s; full Playwright suite parallelised and sharded in CI.
- CI reports per-package test time and the slowest 10 tests; a test over 2 s needs a reason.
- Build caches (Go build, pnpm store, Playwright browsers) are cached in CI.
- A `make check` target runs the fast pre-commit set (format, vet, lint, unit + integration);
  `make e2e`, `make bench`, `make live` are separate.

## 4. The measurement loop ("speed cycles")

For each milestone: (1) write the benchmark/budget test, (2) record the baseline on the potato,
(3) build the feature, (4) compare with `benchstat`, (5) profile anything over budget, fix, repeat.
Results are appended to `docs/perf.md` with date, commit and hardware, so regressions have history.
