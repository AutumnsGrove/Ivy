# Chunk 3 brief (sync, write path, search)

Read this **after** `CLAUDE.md`, `docs/STANDARDS.md` and `next_steps.md`, and before writing any
code. It is the standing instruction for chunk 3: every decision already made, every invariant that
must hold, the traps earlier work found, and the points where you must stop and hand the work to
Claude. `docs/TESTING.md` sections 2 and 6 are the test spec; this file adds to them, it does not
replace them. If this file and another doc disagree, stop (gate T4) and ask.

Chunk 3 is the hardest chunk (about 8 of 10; 3a and 3d are 9). The plan is the stage table in
`next_steps.md`. Work directly on main, one stage at a time, in the order 3a, 3b, 3c, 3d, 3e, 3f, 3g
(3h is an independent track and may be done at any point). Do not start a stage before the one it
depends on is finished and its checkpoint (below) is cleared.

## 1. Invariants (a violation is a bug even if every test is green)

1. **Nothing is ever erased.** A message that leaves the server is **disabled** (`disabled_at`,
   `disabled_reason`), never deleted. Its row, raw blob **and spool file** stay. Nothing purges
   without the explicit Purge action. A property test must assert "no sync sequence ever deletes a
   message row". `SweepSpool` must never take the file of a disabled row.
2. **Writes go to IMAP first.** action -> outbox row -> IMAP command -> only then the DB. A failed or
   unconfirmed IMAP call never leaves a DB-only change. No code path may write to the server without
   an outbox row, or mark an outbox row done before the server acknowledged it.
3. **After quiescence, DB == server**, for every row without a pending outbox op, and for all rows
   once the outbox is empty. Server wins on conflict.
4. **Sync defers to the outbox (decided, round 37).** While a row has a pending outbox op, sync does
   not overwrite that row's flags or folder. On success the outbox updates the DB; on failure the
   row rolls back and the next sync converges. Reads never join the outbox.
5. **Move vs delete (decided, round 37).** A UID that vanishes from a folder whose content key
   appears in another folder in the same pass is a **move**: the old row is disabled with
   `disabled_reason = 'moved'` (kept, hidden, **not** counted as "server removed"), the new row is
   live, tags follow the content key. Only a key found nowhere is disabled as removed.
6. **Locally owned state lives in `state.db` and is keyed by `(account_id, content_key)`**, never by
   a row id or UID. The mirror (`mirror.db`) is rebuildable from IMAP; `state.db` is not.
7. **Content key is the SHA-256 of the normalised `Message-ID` only** (the header block is used only
   when the header is missing). Identical Message-IDs therefore share a key, by design: a message in
   two folders is one message. Do not assume the header block is part of it.
8. **Email is hostile input.** Every size, count, depth and time has a documented maximum and a
   defined outcome above it (`docs/STANDARDS.md` 4a). Nothing blocks without a deadline or context.
   Nothing sized by the sender is held whole in memory. No failure is silent.
9. **Append-only migrations.** Never edit or reorder an existing migration; add a new one. A test
   already fails if you do.
10. **Disabled messages are invisible** to every view, count, search, rule, digest and LLM tool.

## 2. Decisions that are settled (do not re-litigate; `docs/qa-log.md` has the reasoning)

- **SSE is hints only (round 37).** One stream, `/api/v1/events`; small typed events
  `message.changed`, `folder.changed`, `sync.state`, `outbox.state`, `health.alert` that say what to
  refetch. No replay, no event ids; after any reconnect the client refetches what it is showing. A
  slow client's queue is bounded and drops hints. The frontend client lives inside
  `web/src/lib/api/` (no `fetch`/`EventSource` elsewhere) and is covered by Playwright.
- **N8 (round 37).** Shared content keys are accepted for tags. Chunk 5 verdict rows will bind to a
  body hash; nothing in chunk 3 writes verdicts.
- **Mass-disable alert:** a sweep that disables more than 50 messages, or more than 20% of a folder
  that held at least 10, raises a `health.alert`. Constants, easy to change. Sync continues, because
  disabling is reversible.
- **Backfill is full history, newest first**, envelopes and flags first, bodies after, resumable and
  checkpointed per folder by UID range, throttled for the potato (~800 MB RAM free).
- **Tags** are kept locally and as IMAP keywords `$ivy-<slug>` (proved on Purelymail, spike S1:
  `PERMANENTFLAGS \*`); server read-back has source `server`; a local-only fallback exists for
  servers without arbitrary keywords.
- **Vectors:** int8, 768 dimensions, stored and scanned in memory (spike S8: 73 MiB and about
  340 ms for 100k on the potato). Do not truncate dimensions.
- **Embeddings gate (3f):** the only paid path in chunk 3. The `Embedder` interface, the per-account
  opt-in, the monthly cap and one ledger row per call (exact cost) all exist before the first paid
  call. OpenRouter by default, Ollama optional. Nothing paid is reachable except through the gate.
- **Backups:** daily `state.db` `VACUUM INTO` + zstd + verify; 15 days kept with a floor of 10;
  prune only after a verified new backup; at least one target off the potato; `ivy doctor` warns if
  all targets share the DB's disk. The mirror is not backed up. 3c lands before 3d.
- **FTS5 tokenizer** is a measure-first choice inside 3f (`unicode61` with diacritics folding vs
  trigram); record what you measured.

## 3. Traps found by earlier work

- **The shared store path.** `sync.StoreRaw`, `RecordFolder`, `EnsureAccount` and `Settle` are used
  by both the IMAP fetch and the dev fast seeder. New fetch logic goes through them. The test
  `TestFastAndFullAgreeForDemo` (`internal/devstack`) compares every table of a real sync with a
  direct seed; if you change what a sync writes and that test fails, fix the divergence, do not
  loosen the test.
- **Flags are stored as a sorted set** (`store.marshalFlags`); a server lists them in any order.
- **CRLF:** a FETCH returns CRLF bytes; test mail built with bare LF is normalised by the seeder.
- **Bump `sync.DerivedVersion`** whenever `mime`, `render` or the part walk changes output. A
  fingerprint test fails if you forget.
- **Thread ids are sticky and account-scoped** (oldest wins); features may key on `thread_id`.
- **`TrustedAuthservIDs`** must be copied from `config.Account` into `sync.Account` by the real
  runner (the dev harness already does). Purelymail adds no auth verdicts, so the signal is empty.
- **A UIDVALIDITY change** must disable-and-rebuild the old validity's rows (never leave them
  mirrored, never erase them), matching by content key so tags survive.
- **Feed new parsers and walks malformed real-world input**, not only well-formed mail. One bad
  base64 attachment once cost a whole message while every test was green.
- **The dev stack syncs once at startup** (`devstack.Populate`). The 3a runner extends this; `make
  dev`, `make smoke` (asserts a seeded inbox) and the agreement test must keep passing.
- **Ports:** Ivy's default is **8418** (`config.DefaultListen`). 8787 is held by wrangler on the
  operator's machine. Vite is 5173, the Playwright mock server 4173.
- **Never run the 100k `large` profile** (the operator's laptop overheated). Use `demo`, or
  `mailworld.Large(5000)`, and extrapolate, saying so. `.dev/cache` holds built databases and
  doubles disk use.
- **No python or sed scripts for file changes** (`CLAUDE.md`); use the editor tools. `CGO_ENABLED=0`
  for builds; `make test` runs `-race` with cgo on.
- **`mailworld` can** deliver, flag, move, expunge, bump UIDVALIDITY, advance a fake clock, run
  without CONDSTORE (`WithoutCondStore`), and inject faults (`Unreachable`, `AuthFail`, `FailFetch`,
  `Latency`, `DropConnection`, `SMTPReject`, `LLMDown`, `LLMCapReached`). Extend it when a test
  needs more (for example a VANISHED/expunge storm or a mid-FETCH drop); extending the fake is part
  of the work.

## 4. Stage bounds

For each stage: the scope is in `next_steps.md`; below is what must also be true.

- **3a Sync core.** One goroutine tree per account with an owner and a cancel path; two connections
  at most (IDLE on INBOX, one for work). Resumable backfill with no duplicates and no gaps after a
  crash. `QRESYNC`/`CHANGEDSINCE` with the UID/flags fallback for a server without it, tested with
  `WithoutCondStore`. IDLE timeout and reconnect with backoff and jitter. UIDVALIDITY change.
  Tests (all in `TESTING.md` 2): the model-based convergence test with seeded sequences and
  shrinking, plus the scenario list. The generator's operation set must include move, flag, expunge,
  append, folder rename/delete and UIDVALIDITY bump. Rows with a pending outbox op are excluded from
  the equality (invariant 4). `sync_state` is a new append-only migration. The SSE hub per section 2.
- **3b Disabled-not-deleted.** Everything in invariants 1, 5 and 10. Scenario tests: the server
  empties a mailbox, resets UIDVALIDITY, restores messages (rows, blobs and spool files survive,
  re-enable with tags intact). Restore and Purge-forever endpoints; Mirror health shows the count.
  The Restore/Purge and mass-disable screens are **not designed** (gate C0 below): build the
  backend and the API first.
- **3c Backups.** `TESTING.md` 6 spec with a fake clock. Prune only after a verified backup. A
  corrupt snapshot is detected. Restore round-trips from any kept snapshot.
- **3d Outbox and write path.** An outbox op is a small state machine whose durable points are
  written down before coding (gate C3). Idempotent retries across restarts and dropped connections.
  The crash window between "server acknowledged" and "DB updated" must recover without a duplicate
  or a lost write; a MOVE changes the UID, so the op must not rely on the old one afterwards.
  Optimistic UI with rollback on rejection. Reader actions: archive, delete, flag, junk move
  (Mark spam / Not junk are IMAP moves through the outbox). Nothing sends, deletes or moves without
  an explicit confirmation from the UI.
- **3e Tags both ways.** `$ivy-<slug>` written through the outbox; server read-back; membership by
  content key; the local-only fallback.
- **3f Search.** FTS5 over visible messages only; tier 0 and 1 text extraction bounded by size and
  time; the `Embedder` interface and the embeddings gate; embed each message once (a test asserts
  the call count); hybrid reciprocal-rank fusion; `/search`. Disabled messages never appear.
- **3g Rules, snooze, People, reading.** Rules are data; header conditions evaluate locally; actions
  go through the outbox. Fuzzy (Jev) conditions stay in chunk 5. Snoozes live in `state.db`.
- **3h Deploy track.** Independent. `Dockerfile`, multi-arch GHCR publish on main, host-side update
  watcher, in-app Update. Touches no mail code. No secrets in PR workflows, no self-hosted runners
  (`docs/CI.md`).

## 5. Escalation gates

You are fast, and this chunk punishes a confident wrong answer. These are a floor, not a ceiling:
**if you are unsure whether to stop, stop.**

**Claude reviews in a separate, fresh session**, not in the one you are working in, so nothing may
live only in chat. A reviewer starts from `CLAUDE.md`, `next_steps.md`, this file and whatever is in
`docs/handoffs/`. Every checkpoint and every stop is therefore a **committed file**, and
`next_steps.md` must say which checkpoint the work is waiting at.

**Fixed checkpoints.** At each, stop, write `docs/handoffs/<date>-<checkpoint>-<slug>.md` with the
summary below, commit it, add a line to `next_steps.md` ("waiting at C1: see docs/handoffs/..."),
tell the operator in one sentence, and wait for an explicit "go". The tests are the spec, so the
review is of the tests and the design, not the code.

| # | When | What you bring |
|---|---|---|
| C0 | Before any 3b frontend work | The Restore/Purge and mass-disable screens need a canvas board; ask the operator |
| C1 | 3a: the convergence test is written and seen failing for the right reason, **before the runner is implemented** | The operation set, the generator, the oracle (what "expected mirror" is computed from) and the failing output |
| C2 | 3a: the convergence test passes | The seeds and sequence count run, the operation mix, and a demonstration that the test fails and **shrinks** when you deliberately break the code |
| C3 | 3d: **before coding the outbox** | A half page: the op states, which step is durable, the idempotency key, and exactly what happens on a crash after the IMAP ack and before the DB write |
| C4 | 3d: the crash-window test passes | The failure-injection test (kill between ack and DB write, repeated) and its output |
| C5 | End of chunk | The whole chunk, for Claude's review with the review skill |

**Triggers.** If any of these happens, **stop immediately**, make no further changes, and tell the
operator in one sentence that starts with `STOP: this needs Claude:`.

- **T1** The same test still fails after two genuinely different fix attempts, or the only way
  forward you can see is to change, loosen or delete a test or the oracle.
- **T2** A change that could let any code path delete, overwrite or erase a message row, a raw blob,
  a spool file or `state.db`.
- **T3** A need to edit an existing migration or change what an existing column means.
- **T4** A question the docs and `qa-log.md` do not answer, or two docs that disagree. Ask; do not
  guess a design decision.
- **T5** A `-race` failure you cannot explain, or a goroutine you cannot give an owner and a cancel
  path.
- **T6** Any path where a server write could happen without an outbox row, or where an outbox row
  could be marked done before the server acknowledged it.
- **T7** A new dependency not in `docs/STACK.md`, anything needing cgo, or a change to a hostile-input
  parser beyond what the stage requires.
- **T8** You cannot make a test fail first (you cannot reproduce the problem), or you want to write
  implementation before its test.
- **T9** The working tree contains changes you did not make.
- **T10** The `TestFastAndFullAgreeForDemo`, `make smoke` or the model-based test goes red and you
  cannot say within one attempt why.

**How to stop.** Write `docs/handoffs/<date>-<stage>-<slug>.md` with: what you were doing, the last
commit, the failing test or situation, the question, what you tried, and your best guess. It must
make sense to someone who has seen none of your session: name the files, the test names and the
exact command that shows the problem. Commit it, add the waiting line to `next_steps.md`, then send
the one-line message. Do not continue with other work while stopped. When Claude has answered, the
answer is appended to the same handoff file and recorded in `docs/qa-log.md` if it is a decision.

## 6. Definition of done (every stage)

Test first and seen failing for the right reason, then the minimum code, then refactor. Integration
over unit: real core against `mailworld`, no mocks of our own packages. Failure paths are
first-class: the second attempt, cancellation, a stalled peer and hostile or huge input are tested
beside the success case. Then: `make check`, `go test -race ./...`, `govulncheck` clean, `pnpm check`,
`pnpm test` and `pnpm exec playwright test` where a screen changed, benchmarks for hot paths
(`benchstat`; measure on small data, never the 100k profile), docs folded in (`ARCHITECTURE.md`,
`TESTING.md`, `openapi.yaml`, a `docs/BUILD-LOG.md` entry), every Q&A answer recorded in
`docs/qa-log.md`, and `next_steps.md` updated and committed **with the work**. Commit at each stage
rather than batching; present tense, first line under 50 characters, the body says why. Never
commit secrets, real mail or `.env`. Do not open a PR.
