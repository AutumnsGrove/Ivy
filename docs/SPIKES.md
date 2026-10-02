# Spikes (before Milestone 0)

Status: DRAFT (2026-10-01), ordering settled in round 23: **spikes run first, before any build
implementation.** A spike answers one question with evidence, in throwaway code under
`spikes/<name>/` (its own Go module, committed to main per the operator's instruction for this
phase, never imported by real code), and ends in a written finding in `docs/spikes/<name>.md` (what was tried,
what happened, numbers, decision, what to change in the docs). Planning docs are updated from
findings before Milestone 0 starts. Spike code is not TDD; the real implementation is redone
test-first afterward.

**Needs from the operator:** the OpenRouter key (S4, ask before using; goes in git-ignored `.env`),
the `dev@` Purelymail credentials (S1), `ssh potato-remote` (S3, S7, S8), and the iPhone/iPad for
S5. Spend cap for S4: a few cents (`JEV.md` section 5).

| # | Question | Method | Pass looks like | Changes if it fails |
|---|---|---|---|---|
| S1 (done; Resend DMARC taken on the operator's report, send-as scope not fully probed, `docs/spikes/s1-purelymail.md`) | What does Purelymail really do? | Live checks as `dev@`: CAPABILITY, `PERMANENTFLAGS`, ANNOTATION/keywords for tags, does SMTP file a copy in Sent or must we APPEND, send-as from routed aliases, max message size (SMTP `SIZE`), connection limits for N accounts, `COMPRESS=DEFLATE`, auth headers/DMARC on a Grove (Resend) mail | Each fact recorded with the command that proved it | Tag storage, write path, send path, connection budget |
| S2 (done, `docs/spikes/s2-imapmem.md`) | Can go-imap v2 (beta) and its `imapmemserver` carry our sync and tests? | Small client: SELECT with CONDSTORE/QRESYNC, IDLE, MOVE, UID EXPUNGE against `imapmemserver`; list what the memory server lacks | QRESYNC/CONDSTORE/MOVE/IDLE work, or a bounded list of what `mailworld` must add | Fork/extend the memory server, or write our own fake |
| S3 (done, `docs/spikes/s3-potato-build.md`) | Does the pure-Go stack build and run on the potato? | Go toolchain on the potato; build `modernc.org/sqlite` + FTS5 hello world; time and peak RAM of `go build` with other services running | Build fits in RAM (or a documented swap/cross-build path), FTS5 present | `ivy update` strategy (build on potato vs ship binaries) |
| S4 (done on synthetic mail, `docs/spikes/s4-jev.md`; real-mail accuracy still open) | Is Jev usable for Ivy's questions? | `JEV.md` section 5 spike on a small labeled sample; also the `noul`/`score` shapes, per-email cost, injection behaviour | Accuracy and cost within `JEV.md` targets | Question catalog, cascade design, fallback model |
| S5 (iPad over HTTPS done, `docs/spikes/s5-safari.md`; iPhone and HTTP open) | How does Safari behave? | On the real iPhone/iPad: `Accept-Encoding` sent (is zstd there?), sandboxed iframe + CSP rendering, file picker output (HEIC or JPEG), plain HTTP vs `tailscale serve` HTTPS on the tailnet | Facts recorded per device/iOS version | Compression order, HEIC handling, transport default |
| S6 (done for PDF, `docs/spikes/s6-pdf.md`; OOXML not run) | Is pure-Go PDF/OOXML text extraction good enough? | Try 2-3 libraries on a handful of real-shaped receipts and invoices | Usable text for most digital PDFs | Extraction tiers; vision for the rest |
| S7 (done, `docs/spikes/s7-compression.md`) | What does compression cost on the potato? | Brotli/zstd/gzip at several levels over the real SvelteKit-sized assets and sample API JSON; CPU time and ratio | A dynamic level that costs little CPU, and a build-time strategy | Where precompression happens (`PERFORMANCE.md`) |
| S8 | Embedding throughput and memory on the potato | Ollama `nomic-embed-text` on 1k messages, alongside Polaris/SearXNG; brute-force cosine over 100k synthetic vectors | Rates and RAM recorded; search < budget | Chunking, int8 quantisation, backfill pacing |
| S9 (moot, round 29) | ~~Does the bot-commit frontend flow work?~~ Replaced by the CI-built container image (Polaris model); the first image publish is a Milestone 0 check in `CI.md` section 8 | n/a | n/a | n/a |
| S10 | How big will the DB and backups get? (disk is not a constraint, round 24) | Project DB growth from real mailbox size via IMAP `STATUS`/`RFC822.SIZE`; zstd a sample of raw blobs; time a `VACUUM INTO` snapshot on the potato | Sizes and backup duration recorded | Backup scheduling and the state-vs-blobs split |

Order: S1, S2 and S3 first (they gate the architecture), then S4 to S10 in any order; S4, S5 and S1
need the operator present or their keys. A spike that contradicts a settled decision stops and asks.
