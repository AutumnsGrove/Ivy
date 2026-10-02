# S8: embedding throughput and memory on the board

Run 2026-10-02. Code: `spikes/s8-embed/` (throwaway). Model: Ollama `nomic-embed-text`
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

## Not tested

Batch sizes 8 and 32 on the board, throughput under concurrent load, recall on varied real mail,
the shorter-text and smaller-model levers, and `COMPRESS`ed or streamed vector storage.
