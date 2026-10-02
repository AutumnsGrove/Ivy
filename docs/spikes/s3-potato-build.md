# S3: pure-Go build on the target board

Run 2026-10-02. Code: `spikes/s3-potato-build/` (throwaway). Board: aarch64, 4 cores, 1.9 GB RAM,
~1 GB swap, Go 1.26.5, running other services (about 900 MiB used at idle, swap partly in use).
Program: `modernc.org/sqlite v1.60.1` with an FTS5 table, `CGO_ENABLED=0`, fresh Go caches.
Peak RAM is the summed RSS of the build's process group, sampled every 50 ms.

| Build | Time | Peak build RSS | Lowest MemAvailable | Swap used |
|---|---|---|---|---|
| Cold, default | 154 to 190 s | 707 to 738 MiB | 66 to 83 MiB | all 958 MiB |
| Cold, `GOGC=20 GOMEMLIMIT=300MiB -p 2` | 249 s | 477 MiB | 198 MiB | 796 MiB (not exhausted) |
| Warm, one file changed | 6 s | 30 MiB | 726 MiB | n/a |

- FTS5 works (`porter unicode61` tokenizer); the binary is about 9.7 MB, SQLite 3.53.4.
- A default cold build nearly runs the board out of memory and exhausts swap, with other services
  (DNS, VPN, Docker, Polaris) resident. Hypothesis, not verified: one very large generated package
  (`modernc.org/sqlite`) compiles in a single process, so `-p 1` cannot help.
- Building on the board is workable only when the build cache is warm, or with a memory limit and
  a 4-minute cold build. Either way it competes with the live services.

## Decision (recommended, pending operator confirmation)

Do not compile on the board. Build in CI and ship a container image (the Polaris model:
multi-arch image to GHCR on every push to main, host-side update watcher pulls it). The pure-Go,
`CGO_ENABLED=0` rule stays: it is what makes the cross-build from the CI runner trivial, with no
QEMU for the Go and frontend stages. This supersedes the "target builds the binary" and
"bot-committed frontend build output" decisions, and makes S9 moot. The docs that describe those
(`ARCHITECTURE.md` section 9, `CI.md`, `PLAN.md`, `qa-log.md`, `CLAUDE.md`) need updating once
confirmed.
