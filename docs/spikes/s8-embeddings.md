# S8: embedding throughput and memory on the board

Run 2026-10-02. Code: `spikes/s8-embed/` (throwaway; removed from the tree, recover it with
`git show a049bfb:spikes/s8-embed/`). Model: Ollama `nomic-embed-text`
(768 dimensions), the same digest (`0a109f422b47`) on the board and on the dev laptop. The board
was running its normal services. Text: chunks of the repo's own docs (prose, not mail), about 350
tokens (1,500 characters) for throughput and about 120 tokens (500 characters) for the recall test.

## Embedding speed

| | Board (Cortex-A53, 4 cores, Ollama CPU) | Dev laptop |
|---|---|---|
| Cold first call (model load plus one embed) | **36.5 s** | 0.7 s |
| One 350-token chunk, batch of 1 | **17.4 s** (0.06 chunks/s) | 80 ms (12.5/s) |
| 1,000 such messages | about **4.8 hours** | about 1.3 minutes |
| Memory while loaded | the `llama-server` child process held about **407 MiB** (the `ollama` front end only 25 MiB) | n/a |

- `llama-server` is Ollama's own inference process, started as a child of `ollama`; there is one
  Ollama, not two servers. (My first memory sampler matched only the front end's name and missed
  it; the 407 MiB figure comes from the process list.)
- **The slowness is the hardware, not a misconfiguration.** The Cortex-A53 has plain NEON but no
  fp16 arithmetic and no int8 dot-product instructions, ran at its full 1.5 GHz at 40 degrees C,
  and a 350-token chunk of a 137M-parameter model is roughly 100 GFLOP, which is 8 to 17 s at the
  board's sustained 6 to 12 GFLOP/s. All four cores were saturated while it ran.
- The batch of 8 and batch of 32 runs on the board were cut off by my time limit after the
  first batch size; the laptop showed no gain from batching (76 to 102 ms per chunk).
- After the test I stopped the model (`ollama stop`); available memory returned to about 920 MiB.

## Search over stored vectors

Recall is the overlap of the top 10 with exact float32 cosine, over 344 real embeddings and 100
query vectors (each query's own vector excluded). The pure-Go scan is single-threaded.

| Representation | Recall@10 | 100k vectors: memory | Board: top-10 over 100k | Laptop |
|---|---|---|---|---|
| float32, 768 dims | 1.000 (exact) | 293 MiB | about 350 ms (projected from 20k: 70 ms) | about 85 ms |
| **int8, 768 dims** | **0.987** | **73 MiB** | **340 ms** | 27 ms |
| int8, 256 dims | 0.769 | 24 MiB | 116 ms | 10 ms |
| float32 512 / 256 dims | 0.863 / 0.766 | | | |

- **int8 at full width loses almost nothing (98.7%) and uses a quarter of the memory.** Quantising
  needs one scale for the whole set taken from the data's largest component; a fixed 127 scale
  would crush unit-vector components (about 0.04) to a few integer levels.
- **int8 is not faster on this CPU** (340 ms vs about 350 ms): with no dot-product instructions
  the win is memory only. Truncating dimensions costs real accuracy, so do not do it.
- The corpus is 344 chunks from one project's docs, so neighbours are unusually close together;
  this probably understates recall on varied mail, but that is untested.

## Decisions

1. **Store and search int8, 768 dimensions**, in memory: 73 MiB at 100k messages, 340 ms for a
   full scan on the board, which fits the `PERFORMANCE.md` budgets (hybrid search under 800 ms,
   resident memory under 250 MB; float32 would not fit the memory budget). Keep the model name,
   digest and dimension beside the vectors so a model change triggers a re-embed. Narrow the scan
   with FTS5 and filters first where a query allows it.
2. **The board cannot backfill embeddings.** At 17 s per chunk it is only suitable for a trickle
   of new mail in the background. Make the embedding endpoint a configurable base URL so the
   backfill (and optionally everything) runs on a fast machine on the tailnet, such as the
   operator's laptop: same model digest, so the vectors are interchangeable. The settled
   "Ollama behind an interface" decision stands; what changes is that the host is a setting.
3. **Run the model only in bursts:** it holds about 407 MiB and takes 36 s to load on the board.
   Use a short `keep_alive` (or unload after each batch) and skip embedding while memory is low.
4. **Levers to try before accepting 17 s per message:** embedding only the subject plus the first
   100 to 150 tokens (about 3 to 4 times faster, quality untested), or a smaller embedding model
   (untested). Neither was run.

## Hosted alternative: OpenRouter embeddings (operator suggestion, tested the same day)

The operator pointed out that embeddings need not be local. OpenRouter's `/embeddings` endpoint
lists 33 embedding models (prices per token; for scale `baai/bge-base-en-v1.5` is $0.005 per
million tokens, `baai/bge-m3` $0.01, `voyageai/voyage-4-lite` $0.02). Only repo prose was sent
(no mail), from the dev laptop over the internet, one run each:

| | `bge-base-en-v1.5` | **`bge-m3`** | `voyage-4-lite` |
|---|---|---|---|
| Dimensions | 768 | 1024 | 1024 |
| Context | **512 tokens** | 8,194 | 32,000 |
| One short query | 0.30 s | 0.38 s | 0.23 s |
| 32 short chunks in one call | 0.80 s | 0.81 s | 0.71 s |
| 128 email-sized chunks (about 420 tokens each) | **invalid**, see below | **6.1 s => 20.9 chunks/s**, $0.000539 | not run |
| Cost per 100k such chunks | | **about $0.42** | |

- **A 512-token model fails the whole batch if any one chunk exceeds the limit.** One 1,500-character
  chunk came to 513 tokens and the API rejected all 32 chunks in its batch (3 of 4 batches failed),
  so that run's throughput is discarded. Chunk by token count with margin, never by characters,
  or choose a long-context model.
- **Against the board:** `bge-m3` embedded email-sized chunks about 360 times faster than the
  board (20.9/s vs 0.06/s): 1,000 messages in about 48 s instead of 4.8 hours, about $0.004.
  A search-time query embeds in 0.2 to 0.4 s, with no 407 MiB model and no 36 s cold start on
  the board, which is the other thing the local route costs.
- **Size effect:** 1024 dimensions at int8 is 98 MiB per 100k messages, and the scan scales to
  about 450 ms on the board (340 ms at 768), still inside the 800 ms hybrid-search budget.
- **Cost is not the issue;** privacy is. This sends message text to a third party, which is a
  different line from the settled "embeddings always local" (`PLAN.md`). It would sit behind the
  existing gate (per-account opt-in, caps, ledger) like the other hosted calls, but a mailbox
  backfill sends far more old mail than triage of new mail does.
- Also: vectors are tied to the model, so store the model id with each vector; a switch means
  re-embedding, which at these prices is cents, so lock-in is cheap.
- Caveats: one run each from the laptop, not the board; no quality comparison against `nomic`
  (different models, different spaces); provider data-retention terms were not reviewed.

**Decision (operator, round 30):** embeddings sit behind a provider interface with two
implementations, OpenRouter (the default) and a local Ollama endpoint (any host, configurable,
the private option), chosen per account. This replaces "embeddings always local" in the settled
decisions. Two rules came with it: **each message is embedded once** (keyed by content, so moves,
archive and trash never re-embed), and **every embedding is tracked per occurrence** in the cost
ledger like every other remote API call. Details are in `ARCHITECTURE.md` sections 3 and 6.

## Model chosen: `perplexity/pplx-embed-v1-0.6b` (operator, same day)

The operator picked this model over `bge-m3` for being cheaper with a 32k-token context. It was
tested against `bge-m3` on the repo's 118 doc chunks (about 1,500 characters each) before being
adopted, from the dev laptop over the internet:

| | `pplx-embed-v1-0.6b` | `bge-m3` |
|---|---|---|
| Speed, 118 chunks in batches of 32 | **50.7 chunks/s** (2.3 s) | 18.3 chunks/s (6.4 s) |
| Tokens per chunk, cost per 100k chunks | 378, **$0.15** | 457, $0.46 |
| Dimensions | 1024 | 1024 |
| Output | **native int8**: every component a multiple of 1/128 (first vector's largest is 0.36, so it uses about 46 of 127 levels) | float, normalised (norm 1.00) |
| Normalised | **no**, L2 norm 2.84 | yes |
| Same text twice | identical vector | not identical |
| Retrieval sanity (6 queries, answer in top 3) | 6/6 | 6/6 |
| Long input | 7,001 and 28,001 tokens both accepted | 8,192 limit, clean HTTP 400 |
| One short query | 0.28 s | 0.38 s |

Consequences: store the returned integers as they are (no quantisation step, no loss relative to
what the provider sends) and keep each vector's norm beside it, since cosine needs it; identical
output for identical input is useful for tests and for the embed-once rule. Caveats: six
sanity queries on one corpus is not a quality ranking; behaviour beyond 32k tokens was not tested
(chunk by token count with margin regardless); one run from the laptop, not the board; the model
returned about 46 of 127 int8 levels on the sample vector, so effective precision is lower than
8 bits, which a recall test against float32 neighbours would need to quantify (not run).

## Not tested

Batch sizes 8 and 32 on the board, throughput under concurrent load, recall on varied real mail,
the shorter-text and smaller-model levers, and `COMPRESS`ed or streamed vector storage.
