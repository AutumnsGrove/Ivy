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
  (the potato has four slow cores) and a minimum size threshold. Preference order: brotli
  (guaranteed Safari support), then zstd where the client advertises it, then gzip. **Safari's
  zstd support must be verified on the operator's devices before relying on it**, so negotiation is
  driven strictly by `Accept-Encoding`, never by user-agent sniffing.
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

- `raw_blob` and attachment bytes are stored **zstd-compressed** (default level, tunable); the
  sanitised HTML and extracted text likewise if measurement says it pays. Embedding vectors stay
  uncompressed (int8 quantisation is the size lever, `ARCHITECTURE.md` section 3).
- Backups are zstd-compressed.
- IMAP: use **`COMPRESS=DEFLATE`** when the server advertises it (Purelymail does) to shrink sync
  traffic. Verified against the mail world, which can advertise it too.

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

## 4. The measurement loop ("speed cycles")

For each milestone: (1) write the benchmark/budget test, (2) record the baseline on the potato,
(3) build the feature, (4) compare with `benchstat`, (5) profile anything over budget, fix, repeat.
Results are appended to `docs/perf.md` with date, commit and hardware, so regressions have history.
