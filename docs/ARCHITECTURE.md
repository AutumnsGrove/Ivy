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
8. **Events:** every DB change fans out through an SSE hub so open clients update live.

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
- **Auth results:** parse `Authentication-Results` (SPF/DKIM/DMARC) into a trust signal used by
  the phishing question and the spoofed-sender discount.

## 6. Search and ask

- **Hybrid (settled):** FTS5 (BM25) + embedding similarity merged with reciprocal rank fusion into
  one list from one search box; embeddings from Ollama `nomic-embed-text` on localhost, remote
  OpenAI-compatible embeddings as an optional provider; embedding is a low-priority background
  queue (one job at a time, shared with other CPU-heavy work).
- **Ask-your-mailbox (settled behavior):** retrieve (local) -> optional Jev `answers_question` filter
  (JEV.md E) -> chat model writes the answer with citations to message ids -> verify citations
  against the DB (and optionally Jev `claim_supported`) -> render as plain text. Account-picker
  controls which accounts are included; LLM-off accounts are locked.

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

## 10. Security summary

Email is hostile input. Layers: server-side sanitization + sandboxed iframe + CSP; remote content
blocked by default; SSRF-guarded server-side fetches; per-account LLM opt-in with a single gate;
typed Jev output, plain-text model output, DB-verified citations; local-only tags are the only
autonomous model write; secrets in env/file, never in the DB or logs; same-origin/CSRF protection
that works in dev and prod; passkey/Face ID (password fallback) access control designed in but
deferred (Tailscale-only for now; WebAuthn needs a secure context, i.e. Tailscale HTTPS, when added).
