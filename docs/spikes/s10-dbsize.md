# S10: how big will the database and its backups get?

Run 2026-10-02. Code: `spikes/s10-dbsize/` (throwaway; removed from the tree, recover it with
`git show a049bfb:spikes/s10-dbsize/`), run on a dev laptop and on the board
(on its eMMC, not `/tmp`, which is RAM-backed there). It builds a **synthetic** mailbox of 10,000
messages in a real SQLite file with the planned shape: mirror rows and indexes, the raw RFC 822
message zstd-compressed, body text, an FTS5 index, one int8 768-dimension embedding per message,
and tags. No real mail was used, and the real mailbox size is unknown here (the dev mailbox holds
5 messages), so read the results as a cost per message to scale by the real counts.

Assumptions baked into the model: body text median 3 KB (log-normal), 60% of non-attachment mail
also carries an HTML part, 12% of messages carry one attachment (median 150 KB, log-normal, mean
about 290 KB, stored base64 in the raw message and made of random bytes, so **incompressible**).
Text comes from this repo's docs, which probably compresses better than real mail.

## Results (10,000 messages)

| | With 12% attachments | Text only |
|---|---|---|
| Raw RFC 822 total | 534 MiB | 74 MiB |
| zstd'd raw blobs | 482 MiB (90% of raw) | 26 MiB (35%) |
| **Database file** | **573 MiB, 58.7 KiB per message** | **119 MiB, 12.2 KiB per message** |
| Share by object | raw blobs 85%, body text 8.5%, FTS 4.1%, embeddings 1.4% | body text 42%, raw blobs 27%, FTS 20%, embeddings 6.6%, mirror rows 2.4% |
| Snapshot zstd'd | 491 MiB (86%) | 48 MiB (41%) |

Attachments are about 85% of the database. They are about 387 KiB per attachment-bearing message
in this model (including base64 overhead), against about 12 KiB for everything else.

**Rule of thumb:** database size is about 12 KiB times messages, plus about 0.4 MiB times
messages with attachments. At 100k messages: about 1.2 GiB text-only, or about 5.6 GiB with 12%
carrying attachments like these. The board has 189 GB free, so size itself is not the problem.

## Timings on the board (laptop for comparison)

| | Board | Laptop |
|---|---|---|
| Building the 10k-message DB (compress, 5 inserts and FTS per message) | **2 min 44 s, 61 msgs/s** | 6 s, 1,763/s |
| `VACUUM INTO` a 572 MiB snapshot | **76 s** | 1.1 s |
| zstd of that snapshot, streamed | **31.5 s** | 0.7 s |
| FTS5 match (1,709 hits) | 20.7 ms | 3.1 ms |
| List page, 50 newest | 5.2 ms | 0.65 ms |
| Peak process memory during the whole run | **155 MiB** (lowest system MemAvailable 730 MiB) | |

The board is about 25 to 100 times slower than the laptop for write-heavy work, but the read
queries that serve the UI are comfortably inside the `PERFORMANCE.md` budgets (FTS under
100 ms). A 100k-message backfill is about 27 minutes of database work on this board, on top of
the IMAP transfer.

## What this means for backups

The plan backs up only locally owned state and not the mirror (`ARCHITECTURE.md` section 9).
These numbers make that **a hard requirement**:
- A full-database snapshot takes about 108 s of heavy I/O on the board (`VACUUM INTO` plus
  compression), twice a day.
- Keeping 60 such snapshots (30 days, twice daily) of a 10k-message, 12%-attachment mailbox
  would be about 29 GiB, and over 280 GiB at 100k messages, more than the board's 189 GB disk,
  plus constant eMMC wear.
- **Recommendation:** keep the locally owned state (settings, rules, snoozes, allow-lists, check
  definitions, and tags if they stay local) in its **own small SQLite file**, separate from the
  rebuildable mirror file. Then the snapshot is kilobytes to a few megabytes and takes a moment,
  and the mirror can be rebuilt from IMAP. If everything lives in one file, `VACUUM INTO` copies
  the mirror every time. (The docs do not currently say whether these are one file or two.)
  Separately, disabled messages' raw blobs are the only copy of server-deleted mail and need
  their own append-only store, as already planned.

## Decision and the follow-up measurement (operator, round 30)

The operator chose: **one backup a day, kept 15 days, of a separate local-state file**, with the
mirror in its own file as a full mirror that is never backed up. That is feasible:
- **Mirror file:** about 12 KiB per message plus about 0.4 MiB per attachment-bearing message,
  so 1.2 to 5.6 GiB at 100k messages on a 189 GB disk. It is rebuilt from IMAP if lost (about 27
  minutes of database work per 100k messages here, plus the transfer).
- **State file, measured afterwards** (60 tags, tag membership for 30% of messages, 150 rules,
  3,000 snoozes, 800 allow-list rows, and a cost-ledger row per embedded message plus a Jev call
  for half the messages): **3.0 MiB at 10k messages and 26.7 MiB at 100k** (150,000 ledger rows
  dominate). `VACUUM INTO` takes 12 ms and 103 ms on the laptop respectively (expect a few seconds
  on the potato), and 15 daily snapshots total about 44 MiB and 389 MiB uncompressed.
- Local state must refer to mail by a stable content key and not by mirror row ids, or a mirror
  rebuild would orphan tags, snoozes and the ledger (`ARCHITECTURE.md` section 3).
- Caveat: the keys in this model are random 32-byte hashes (as real content keys would be), so
  they do not compress; the ledger rows are an assumption about call volume.

## Other notes

- The FTS table here is **contentless** (`content=''`), which keeps the index small (4% of the
  database) but cannot return stored text or highlights; the body is stored once in `body_text`
  and the FTS index is rebuilt from it. Check that this matches the search UI's needs before
  committing to it.
- Attachment bytes inside the raw blob dominate. If attachments are kept as separate files
  (as `attachments.storage_path` in the schema sketch implies), the database stays near the
  text-only figure and the files grow on disk; the totals are the same, the backup and vacuum
  cost is not.

## Not tested

A real mailbox size distribution (the model's attachment rate and sizes are assumptions), real
mail text compressibility, incremental backup via SQLite's backup API instead of `VACUUM INTO`,
database growth from re-embedding and tag churn over months, and WAL size under sustained sync.
