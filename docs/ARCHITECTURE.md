# Architecture

Status: DRAFT (2026-10-01). Settled decisions are marked **(settled)**; everything else is the
proposed default and open to veto. Product behavior lives in `PLAN.md`; the Jev design in `JEV.md`;
the testing strategy in `TESTING.md`.

## 1. Shape

```
 phone / desktop browser (SvelteKit SPA, pure CSS, embedded in the binary)
        |  JSON REST + Server-Sent Events (typed client module)
        v
 +--------------------------- ivy (single Go binary, systemd) ---------------------------+
 |  api  |  sync (one worker per account)  |  triage  |  search  |  compose/outbox  |  llm |
 |                          store (pure-Go SQLite, WAL, FTS5)                              |
 +------+-----------------------+-----------------------------+---------------------+------+
        | IMAP/SMTP (TLS)       | Ollama (localhost)          | OpenRouter (HTTPS)  | backup target
        v                       v                             v                     v
   Purelymail (per-user)   nomic-embed-text            Jev / chat / vision      folder or S3/R2
```

- **Go backend, SQLite, SvelteKit frontend (settled).** Single operator, bare-metal on the potato.
- **Mirror model (settled):** the DB follows the IMAP server (deletes, moves, archives included);
  **writes go to IMAP first.** The DB exists for speed, search, tags, rules and triage.

## 2. Repo layout (proposed)

```
ivy/
  main.go            cobra-style CLI: run, init, update, backup, restore, doctor
  cmd/               one file per command; thin HTTP clients for a running instance where possible
  config/            ivy.yaml + env loading, defaults, validation
  store/             SQLite open/migrate, queries per area (messages, tags, rules, usage, settings)
  imap/              thin wrapper over go-imap v2: connect, capabilities, IDLE, QRESYNC helpers
  sync/              per-account sync workers, folder roles, backfill, write path, outbox
  mime/              enmime glue: parse, build, cid map, thread keys, Authentication-Results
  render/            sanitizer (bluemonday), tracker stripping, safe HTML/CSP, plain-text fallback
  thread/            JWZ threading
  search/            FTS5 queries, chunking, embedding client (Ollama/remote), hybrid ranking
  extract/           Extractor interface: bodies/.ics, PDF text layer, OOXML; vision hook
  jev/               Jev client (/systemone), question registry, thresholds, cache
  llm/               chat/vision client + THE gate (opt-in, isolation, caps, ledger)
  triage/            needs-me cascade, categories, receipts/ledger, newsletter feed/digest, rules
  compose/           MIME build (enmime builder), identities, signatures, undo-send queue
  gateway/           HTTP handlers, SSE hub, stats, settings, update endpoints
  update/            ivy update: ff-only pull, build to temp, swap, restart, rollback
  backup/            snapshot of locally owned state, restore
  web/               SvelteKit app; build output committed in web/build and embedded (go:embed)
  dev/               fake IMAP/OpenRouter helpers, seed tool, stack launcher
  testdata/          fixtures and corpora
  docs/
```

## 3. Data model (SQLite; append-only migrations, settled lesson from Polaris)

Pragmas: WAL, `synchronous=NORMAL`, `foreign_keys=ON`; sync writes are batched in transactions to
limit flash wear on the potato's SD/eMMC. **Pure-Go driver (settled):** `modernc.org/sqlite` (verify
FTS5 is available; time `go build` on the real potato and keep the build cache warm).

Mirror tables (rebuildable from IMAP):
- `accounts` (id, address, imap/smtp host+port, username, display_name, icon, photo_blob,
  llm_enabled, vision_enabled, color, sort_order, created_at). Password/app-password lives in env/file, never here.
- `folders` (id, account_id, name, role[inbox|sent|drafts|trash|archive|junk|other], uidvalidity,
  highestmodseq, last_sync_at)
- `messages` (id, account_id, folder_id, uid, message_id_hdr, in_reply_to, references, subject,
  from/to/cc/reply_to/delivered_to JSON, date, size, flags JSON, internaldate, auth_results JSON,
  has_attachments, raw_blob (zstd/gzip, nullable until fetched), body_text, body_html_sanitized,
  thread_id, snippet). Unique (folder_id, uid).
- `threads` (id, account_id, root_message_id, subject_norm, last_date, message_count)
- `attachments` (id, message_id, filename, mime, size, content_hash, cid, storage_path)
- `extracted_text` (attachment_id | message_id, tier, text, status)
- `chunks` / `embeddings` (message_id or attachment_id, chunk_ix, model, dims, vector BLOB), plus an
  FTS5 virtual table over subject/body/attachment text.

Locally owned state (NOT on the server, so backed up, section 9):
- `tags`, `message_tags` (source: user | rule | model), `rules` (conditions JSON incl. fuzzy Jev
  questions, actions JSON, account scope, enabled), `snoozes` (message_id, until), `image_allow`
  (sender/domain), `settings` (key/value + per-account overrides), `jev_questions` (user-defined
  and overrides), account display fields above.

Derived/ledgers:
- `decisions` (message_id, question_id, instruction_hash, model, probabilities JSON, choice,
  confidence, cost, created_at), `needs_me` (message_id, verdict, reason, stage2_model, state)
- `people` (derived view/table from addresses seen), `receipts` (extracted fields, renewal dates)
- `outbox` (queued IMAP actions with retry state), `send_queue` (composed message, undo deadline)
- `usage` ledger: `jev_usage`, `llm_usage` (chat/vision/embeddings), each per call: account,
  feature, model, tokens, exact cost, latency, outcome; `api_caps` monthly counters.
- `schema_migrations` positional `user_version` (never reorder or edit existing entries).

Vectors: 768-dim float32 is 3 KB per vector (about 150 MB per 50k messages). Plan: brute-force
cosine in Go, streamed from SQLite in batches (never loaded whole), with int8 quantization as the
escape hatch (about 4x smaller); benchmark on the potato; embed subject + first N characters per
message and per attachment chunk, not whole threads. Each vector records its model so a model
change re-embeds in the background.

## 4. Sync engine

One goroutine tree per account; connections are limited (one for IDLE on INBOX, one for work) to
respect the potato's RAM and Purelymail's connection tolerance (verify limits live).

1. **Connect + capabilities.** Purelymail advertises IDLE, CONDSTORE, QRESYNC, MOVE, UIDPLUS,
   ESEARCH, SORT, COMPRESS=DEFLATE, ANNOTATION; no SPECIAL-USE, no THREAD (settled facts, read
   live 2026-10-01). Everything optional has a fallback (UID-list diff when QRESYNC is missing) so
   Ivy works with other providers.
2. **Folders.** LIST; roles from LIST attributes when present, else name heuristics
   (Sent/Drafts/Trash/Junk/Archive in a few languages), overridable in settings.
3. **Initial backfill (full history, settled).** Newest to oldest: envelopes + flags first so the
   inbox is usable at once, then bodies/raw in the background; resumable, checkpointed per folder
   by UID range; throttled to cap RAM/CPU/flash writes.
4. **Steady state.** IDLE on INBOX; on notification or a periodic timer, `SELECT ... (QRESYNC ...)`
   per folder to learn changed flags, expunged UIDs, and new UIDs since the stored modseq.
5. **UIDVALIDITY change:** drop that folder's mirror rows and re-sync it (messages are matched by
   Message-ID where possible to preserve tags/snoozes). Tested with the fake server.
6. **Convergence invariant:** after quiescence, DB == server. Conflicts are resolved server-wins;
   locally owned state keys on `(account, Message-ID)` so it survives moves and UID changes.
7. **Write path (settled):** action -> outbox row -> IMAP command (STORE/MOVE/EXPUNGE/APPEND) ->
   on success update the DB; the UI updates optimistically and rolls back on rejection. Outbox
   retries survive restarts and dropped connections.
   **Mirror-loss protection (round 23, proposed):** the mirror may be the only complete copy, so
   "DB follows the server" must not turn a provider mistake or a sync bug into silent data loss.
   (a) Messages that vanish from the server are **soft-deleted** locally (hidden from every view,
   raw blob kept) for a retention window (default 30 days) before purging; (b) a **circuit breaker**
   pauses sync for that folder and raises a Mirror health alert when one sweep would remove more
   than N messages or X% of a folder (including UIDVALIDITY resets), requiring a click; (c) both
   are covered by scenario tests with the fake server emptying a mailbox. Needs operator approval.
8. **Events:** every DB change fans out through an SSE hub so open clients update live.
9. **Spam handling (round 17, proposed):** the provider filters (Purelymail: SpamAssassin, Junk
   folder). Ivy stores the parsed `X-Spam-Status` score/flag on each message, treats the `junk`
   folder as a normal mirrored folder, and implements Mark spam / Not junk as IMAP MOVE to and from
   Junk through the same outbox, because those moves train the provider's per-user filter (it needs
   ~200 examples each way). Jev spam questions are advisory tags only (JEV.md 3B).

## 5. Parsing and rendering

- **Parse:** `jhillyerd/enmime` (settled: import, don't build) -> text, HTML, inlines, attachments,
  charset handling and a list of non-fatal errors that are stored on the message.
- **Sanitize:** `bluemonday` strict policy server-side; strip scripts, forms, event handlers,
  `javascript:`/`data:` abuse, `<base>`, `<meta refresh>`; rewrite `cid:` to local attachment URLs;
  neutralize/remove remote images and CSS URLs unless the sender is allow-listed (configurable);
  strip known tracking pixels even when allowed (configurable).
- **Display:** sandboxed `<iframe sandbox>` with a strict CSP and no script, styled for light/dark,
  with a plain-text view always available. Links open with `rel=noopener noreferrer` and show the
  real destination before leaving.
- **Threading:** JWZ from Message-ID/References/In-Reply-To with a normalized-subject fallback;
  computed on arrival and stored.
- **Composing with attachments and images (settled need, round 18):** the editor can attach
  photos and files and place images inline. **Browser limits:** a web page cannot browse the device
  photo library, so there is no "recent photos" grid. Photos, Camera and Files are buttons that open
  the phone's own picker (`<input type="file">`, with `accept="image/*"` for the photo/camera sheet;
  `capture` would force camera-only, so it is used only on the Camera button); the page receives
  just what the user picks. Desktop adds drag-drop and paste. iOS Safari 17+ may hand back HEIC, so the
  server converts HEIC to JPEG (verify on the real phone). Separately, a **"From your mail"** list
  offers attachments already in the mirror (received or sent), attached by a server-side copy with no
  upload. Uploads go to the server's temp storage and attach to the `send_queue` row;
  enmime's builder emits `multipart/mixed` (+ `multipart/related` with `cid:` for inline images).
  Rules: a total-size limit read from the provider (SMTP `SIZE`; verify Purelymail's live) with a
  clear error before sending, a MIME-type allow/deny list for the dangerous types, **EXIF/location
  stripped from photos by default** (setting), optional downscale of phone photos (Original / Large /
  Medium, default Large), per-attachment remove, the saved draft in the Drafts folder carries its
  attachments, and the copy in Sent keeps them. Reply/forward re-attach the original's attachments
  only when asked.
- **Auth results:** parse `Authentication-Results` (SPF/DKIM/DMARC) into a trust signal used by
  the phishing question and the spoofed-sender discount.

## 6. Search and ask

- **Hybrid (settled):** FTS5 (BM25) + embedding similarity merged with reciprocal rank fusion into
  one list from one search box; embeddings from Ollama `nomic-embed-text` on localhost, remote
  OpenAI-compatible embeddings as an optional provider; embedding is a low-priority background
  queue (one job at a time, shared with other CPU-heavy work).
- **Talk to Ivy (settled, round 18):** reached from the Search page (a Search / Ask Ivy switch at
  the top). It is an **agent loop with three tools**, all read-only and all executed locally:
  `search_mail(query, filters, limit)` (the hybrid search above, restricted to the selected
  accounts), `read_mail(message_id, part?)` (sanitized text of one message, or one attachment's
  extracted text), and `think(thought)` (a scratchpad the model uses to plan; never shown as
  authoritative and never executed). The chat model loops until it answers or hits a cap.
  Guard rails: a max step count and token budget per question, the account picker decides which
  accounts the tools can see (LLM-off accounts are locked, so the tools never return their mail),
  withheld mail (tripwire/sensitive) is invisible to the tools, every model call and tool call is a
  ledger row, and tool results are wrapped as untrusted data. **Citations must point at message ids
  the loop actually read in this run**, verified against the DB (optionally Jev `claim_supported`
  per claim); the final answer renders as plain text. No tool can send, move, delete or tag; a
  proposed action ("archive these") is shown as a button the user must click.
  The old fixed pipeline (retrieve -> `answers_question` filter -> answer) remains as the cheap
  fallback for the one-shot case and as the evaluation baseline.

## 7. LLM layer

- **Interfaces:** `decide(state, questions) -> answers` (Jev; fallback: chat model + JSON schema),
  `complete(prompt) -> text` (configurable OpenAI-compatible chat model via OpenRouter),
  `see(images, prompt) -> text` (vision-capable model, per-account opt-in).
- **The gate (single chokepoint):** every outbound model call goes through one function that
  enforces: account `llm_enabled`/`vision_enabled`, ask-selection rules, withheld-message rules
  (tripwire/sensitive), monthly caps, per-call ledger row with exact cost, and timeouts. An
  architecture test ensures nothing else can reach the provider (TESTING.md section 4).
- **Cascade (settled):** Jev `needs_me` first pass (high recall) -> only flagged mail goes to the
  chat model for the verdict + a short plain-text reason (no tools, structured output).
- **Safety line (settled):** the model may write local tags/ratings automatically; sending,
  deleting, moving, forwarding and unsubscribing always need a click. One account per automated
  call; ask mixes only operator-selected accounts. Unsubscribe runs server-side with an SSRF guard.
- **Vision (settled soon):** attachments and inline `cid:` images only; gates: flagged mail + a
  "read this image" action, size/dimension filters, content-hash dedupe, monthly cap.
- **Extraction tiers:** bodies/.ics (0), digital PDFs + OOXML in pure Go (1), vision model (2),
  OCR far-out (3); structured data (schema.org JSON-LD) is tried before any model call for receipts.

- **Rule compiler (round 20):** `rules/compile` turns one plain-text sentence into a validated rule
  plus any new smart checks with a single structured `complete()` call (see JEV.md 3F). Pipeline:
  gate -> model -> JSON-schema validate -> vocabulary check (local-only actions, known fields, known
  accounts) -> dry run -> user review -> store. Rules and checks are stored as data (`rules`,
  `checks` tables with instruction hash); the engine evaluates header conditions locally and reads
  check answers from the cached Jev results, so running a rule never calls the compiler.

## 8. API, frontend, config

- **API:** JSON REST + SSE; one typed client module in the frontend; contract generated or shared so
  the frontend stays portable. **Item for operator review:** this wasn't explicitly confirmed; it's
  the default assumption. Endpoints include `/api/stats` (see PLAN.md stats panel), `/api/version`,
  update/restart, settings, accounts, search, ask, compose.
- **Frontend (settled):** SvelteKit with `adapter-static`, Svelte 5, **pnpm, pure CSS with custom
  properties, no Tailwind**; a vendored copy of Grove's design tokens as one CSS file; shared
  `--space-*`, `--radius-*`, `--z-*` scales like Polaris (no raw px literals); phone and desktop
  are both first-class (stacked layout with bottom navigation vs multi-pane), swipe gestures,
  keyboard shortcuts with a help overlay, PWA-installable (manifest only; no push), reduced-motion
  and contrast respected. Calm and quiet visual language.
- **Config split (settled):** `ivy.yaml` + `.env` for secrets (credentials, API keys); behavior
  settings in the in-app panel (SQLite) with per-account overrides; non-secret file settings
  hot-reload. Delayed send (undo send) is a setting (`compose.undo_delay_seconds`; default 10,
  0 = off, with an upper bound), editable globally and per account.

## 9. Deployment, update, backup

- **Bare metal (settled).** systemd unit on the potato; the potato builds the Go binary; the
  committed `web/build/` is embedded via `go:embed`. Docker is a maybe-later path.
- **`ivy update` (also an in-app button):** verify the remote, `git fetch` + `merge --ff-only`
  (refuse on a diverged checkout), `go build` to a temp file (peak RAM is a known risk: serving
  continues while building), health-check the new binary, swap, restart via systemd, roll back on a
  failed check. Reports progress to the UI over SSE.
- **Backup (settled):** scheduled snapshot of locally owned state only (not the rebuildable mirror)
  to a configurable target (folder or S3-compatible), one-line restore; restore followed by a
  mirror rebuild from IMAP.
- **Resource budget (potato, 1.9 GB RAM, ~800 MB free, swap in use):** Ivy target well under
  100 MB resident at idle; embedding and extraction are serialized background jobs; numbers get
  measured and recorded in `docs/perf.md`, not assumed.

## 9a. Operational gaps found in the round 23 review

- **Disk budget:** a full-history mirror with raw RFC 822 blobs may not fit the potato's card
  (100k messages at ~75 KB is several GB before compression). Plan: zstd at rest, a configured
  storage budget, `ivy doctor` disk check and alert, and an eviction policy that drops old raw
  blobs (refetched on demand from IMAP) while keeping parsed text. Measured in spike S10.
- **SQLite on flash:** power loss and card corruption are real. `PRAGMA integrity_check` in
  `ivy doctor` and at startup after an unclean exit, periodic WAL checkpoints, backups of locally
  owned state verified by a restore test, and a kill -9 / power-loss test of the outbox and sync
  checkpoints with the fake server.
- **Search quality:** FTS5 tokenizer choice (`unicode61` with diacritics folding, possibly trigram
  for CJK/partial matches) decided with a multilingual test corpus.
- **Time:** all timestamps UTC in the DB, local zone at the edge, with DST and bad-`Date`-header
  tests (clock-skewed mail sorts by internal date).
- **Logs:** journald with size limits so logs don't wear the card; no mail content in logs.
- **Committed frontend build:** PRs that change the UI would conflict on and bloat the committed
  output. Proposal pending spike S9: CI regenerates and commits `web/build` only on merge to main
  (deterministic build, drift check on PRs compares sources, not output).

## 9b. Failure states (round 20)

Things go wrong in four places; each has a distinct, calm state. Every error carries a stable code,
and the UI copy answers three questions: what happened, what is safe, what can I do.

| Where | Cause | State the user sees |
|---|---|---|
| Phone -> Ivy | Server unreachable (Tailscale off, server down) | Full-screen "Can't reach Ivy" with Try again. v1 has no offline reading (open question: cache recent messages read-only in the PWA) |
| Ivy -> provider (sync) | Auth failed (`auth_failed`), host unreachable (`unreachable`), other (`error`), backfilling | Amber banner over the inbox naming the account, "your mail is safe, showing what Ivy has", a Fix action; a dot on the account switcher; **Mirror health** screen with per-account status, progress for backfill, Update password / Try again / View log |
| Message fetch | Body or attachment not downloaded yet or fetch failed | Header and metadata still shown; an inline "This message didn't load" card with Try again and Show what we have; a failing attachment shows its own Retry. Stored as `body_status` so retries back off and survive restarts |
| Send / write path | SMTP 4xx (transient), 5xx (rejected, e.g. too large), outbox IMAP command failed | Transient: automatic retry with a quiet "will keep trying" toast. Permanent: a **Not sent** sheet with the reason, the draft kept in Drafts, and a concrete fix ("Remove X and send"). Message size is checked against the provider's `SIZE` before sending where known |
| LLM | Monthly cap reached, provider not responding, gate refusal | Inline card in Ask Ivy ("Ivy is resting", "Ivy can't answer right now") with Raise the limit / Try again / Search instead; smart features degrade to doing nothing, never to blocking mail |

Data model: per account `sync_state` (status, last_ok_at, last_error_code, last_error_detail,
backfill_done/backfill_total) and per message `body_status`; the SSE hub streams changes so banners
appear and clear live. Reconnection shows a brief "Back online. Catching up..." toast. Undo and
retry affordances (Sending with Undo, Archived with Undo) are toasts with a deadline, backed by the
outbox. Errors never delete anything and never silently drop an action: the outbox keeps it until it
succeeds or the user dismisses it.

## 10. Security summary

Email is hostile input. Layers: server-side sanitization + sandboxed iframe + CSP; remote content
blocked by default; SSRF-guarded server-side fetches; per-account LLM opt-in with a single gate;
typed Jev output, plain-text model output, DB-verified citations; local-only tags are the only
autonomous model write; secrets in env/file, never in the DB or logs; same-origin/CSRF protection
that works in dev and prod; passkey/Face ID (password fallback) access control designed in but
deferred (Tailscale-only for now; WebAuthn needs a secure context, i.e. Tailscale HTTPS, when added).
