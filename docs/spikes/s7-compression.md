# S7: what does compression cost on the board?

Run 2026-10-02. Code: `spikes/s7-compress/` (throwaway; removed from the tree, recover it with
`git show a049bfb:spikes/s7-compress/`), cross-compiled for linux/arm64 and run on
the board and on a dev laptop (Apple silicon, 8 cores) for comparison. Every codec ran
single-threaded. The board was near idle (load 0.2, about 930 MiB available); under real load
these numbers get worse.

Corpus: the real built frontend (145 compressible files, 374 KiB raw; the rest of the 1.1 MB build
is already-compressed WOFF2 fonts), and synthetic mailbox-shaped API JSON built from the repo's
own prose: a typical 25-item list page (10.7 KB), a 200-item page (85.6 KB) and a 4-message
thread (52.6 KB). Real mail text will compress differently; the ranking should hold, the exact
percentages will not.

## Results on the board

| Codec | Whole frontend (374 KiB raw) | 25-item list (10.7 KB) | 200-item list (85.6 KB) | Thread (52.6 KB) | Peak RSS |
|---|---|---|---|---|---|
| gzip 6 | 39.3%, 165 ms | 39.4%, 2.6 ms | 32.9%, 19.6 ms | 36.3%, 13.6 ms | 12 MiB |
| brotli 5 | 36.6%, 271 ms | 37.4%, 4.7 ms | 30.6%, 27.0 ms | 34.7%, 17.1 ms | 31 MiB |
| brotli 6 | 36.5%, 323 ms | 37.3%, 5.0 ms | 30.5%, 30.7 ms | 34.6%, 19.2 ms | 36 MiB |
| brotli 9 | 36.4%, 1.2 s | 37.4%, 11.7 ms | 30.4%, 45.2 ms | 34.6%, 29.3 ms | |
| **brotli 11** | **34.2%**, **6.6 s** | 32.0%, **133 ms** | 26.6%, **1.02 s** | 30.8%, **525 ms** | 91 MiB |
| zstd fastest | 43.4%, 31 ms | 40.9%, 0.6 ms | 33.7%, 5.0 ms | 40.0%, 3.8 ms | |
| **zstd default** | 41.1%, 59 ms | 39.8%, **1.6 ms** | 32.5%, **9.8 ms** | 37.5%, **7.2 ms** | 12 MiB |
| zstd best | 39.1%, 176 ms | 39.0%, 4.3 ms | 31.4%, 33.0 ms | 35.8%, 24.5 ms | 50 MiB |

Percentages are compressed size over raw size; times are the mean for one pass. The laptop was 8 to
13 times faster than the board across the board (brotli 11 on the whole build: 586 ms there, 6.6 s
here; the 200-item list: 91 ms there, 1.02 s here).

## Findings

- **Dynamic responses: zstd default is the cheapest per byte.** A typical page costs 1.6 ms of one
  core, a large page 9.8 ms. Brotli 5 compresses about 6% smaller but takes about 2.7 times the CPU
  (4.7 ms and 27 ms); gzip 6 sits between them in cost and is the largest of the three.
- **Brotli 11 must never run per request:** 133 ms for a typical page and over a second for a large
  one on this board, with 91 MiB of memory. Brotli 9 is also poor value (12 to 45 ms for almost no
  gain over 5).
- **Static assets: brotli 11 wins and costs nothing at runtime** because it runs once at build
  time on the CI runner, not on the board. It is about 12% smaller than zstd default (34.2% vs
  41.1%), and 5% smaller than zstd best, on text assets. Safari decodes brotli, so it stays the
  static default; zstd and gzip variants cover other clients.
- **The cost of a request is small:** 2 to 5 ms of one A53 core for a typical API page. The
  compression budget is not a constraint at single-operator load, so choose the level on size and
  predictability, not on fear of CPU.
- **Memory is a non-issue** for the levels we would actually use at runtime (12 to 36 MiB peak).

## Decisions

1. **Static:** precompress in CI at brotli 11 plus gzip 9 and zstd (as `PERFORMANCE.md` already
   says); the board does no compression work for assets.
2. **Dynamic preference order becomes zstd, then brotli, then gzip** (it was brotli, zstd, gzip),
   because the S5 spike showed Safari advertises and decodes zstd and zstd is the cheapest
   per-request codec. Levels: zstd default; brotli 4 or 5; gzip 6. Never above brotli 6 on the hot
   path. Negotiation stays driven strictly by `Accept-Encoding`.
3. **Size threshold:** keep a minimum (about 1 KB); everything above it compressed to about 35 to
   40% in these runs.
4. **Hot-path benchmarks** per `PERFORMANCE.md` should use these corpora so a regression shows up.

## Not tested

Concurrent load (several simultaneous compressions plus Polaris and the other services),
streaming/SSE compression behaviour, real mailbox JSON and sanitised HTML bodies, and
`COMPRESS=DEFLATE` toward the IMAP server (a different question, not run).
