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
 |              store (pure-Go SQLite, WAL, FTS5): mirror.db + state.db                    |
 +------+-----------------------+-----------------------------+---------------------+------+
        | IMAP/SMTP (TLS)       | OpenRouter (HTTPS)          | Ollama (optional)   | backup target
        v                       v                             v                     v
   Purelymail (per-user)   Jev / chat / vision /         local embeddings       folder or S3/R2
                           embeddings (default)          (any host)
```

- **Go backend, SQLite, SvelteKit frontend (settled).** Single operator, one container on the potato.
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
  search/            FTS5 queries, chunking, embedding client (OpenRouter default, Ollama optional), hybrid ranking
  extract/           Extractor interface: bodies/.ics, PDF text layer, OOXML; vision hook
  jev/               Jev client (/systemone), question registry, thresholds, cache
  llm/               chat/vision client + THE gate (opt-in, isolation, caps, ledger)
  triage/            needs-me cascade, categories, receipts/ledger, newsletter feed/digest, rules
  compose/           outbound MIME build (enmime builder), identities, signatures
  smtp/              thin go-smtp wrapper: implicit-TLS dial, AUTH PLAIN, EHLO SIZE, RCPT/DATA, deadlines
  events/            the SSE hub: typed hints, bounded per-client queues (no dependency on sync or gateway,
                     so sync can publish and gateway can serve)
  gateway/           HTTP handlers, the /events stream, stats, settings, update endpoints
  update/            ivy update: resolve GHCR digest, signal the host watcher, health check, rollback
  backup/            daily snapshots (15 days) of state.db + disabled blobs, restore
  web/               SvelteKit app; its build is copied into internal/webui/build by
                     `make web-assets`, precompressed there and embedded (go:embed); never
                     committed (a tracked placeholder keeps go build working)
  dev/               fake IMAP/OpenRouter helpers, seed tool, stack launcher
  testdata/          fixtures and corpora
  docs/
```

## 3. Data model (SQLite; append-only migrations, settled lesson from Polaris)

Pragmas: WAL, `synchronous=NORMAL`, `foreign_keys=ON`; sync writes are batched in transactions to
limit flash wear on the potato's SD/eMMC. **Pure-Go driver (settled):** `modernc.org/sqlite` (verify
FTS5 is available, confirmed by spike S3; it is cross-compiled in CI, never on the potato).

**Two SQLite files (settled, round 30).** `mirror.db` is a full mirror of the mailboxes plus
everything derived from them; it is rebuildable from IMAP, so it is **never backed up**. `state.db`
holds the small locally owned state; it is the **only database that is backed up** (spike S10:
about 27 MiB at 100k messages, a snapshot takes about 0.1 s on a laptop). Rows in `state.db`, and
derived rows in `mirror.db`, refer to mail by a stable **content key**, never by a mirror row id,
so a mirror rebuild or a move between folders breaks nothing. **Content key:** SHA-256 of the
lower-cased `Message-ID` header (of the raw header block when that header is missing). It is the
same in every folder and across UID changes; copies sharing a Message-ID within an account are one
message as far as derived data goes. Spike S10 sizes the mirror at about 12 KiB per message plus
about 0.4 MiB per message with an attachment (1.2 to 5.6 GiB at 100k messages), comfortable on the
potato's disk.

Mirror tables (`mirror.db`, rebuildable from IMAP):
- `accounts` (id, address, imap/smtp host+port, username, llm_enabled, vision_enabled, color,
  sort_order, created_at). Password/app-password lives in env/file, never here. The operator's
  name, icon and photo are **not** here: they are typed by the operator, so they live in `state.db`
  (`account_profiles`, below) and `GetAccount`/`ListAccounts` lay them over this row. Older builds
  kept them on this row; `Open` copies any such values into `state.db` once and empties the columns.
  A list reads `HasPhoto`, never the blob, so `/accounts` does not load image bytes.
  `GET /accounts/{id}/photo` streams the photo, and only a sniffed raster image (JPEG/PNG/GIF/WebP,
  not SVG) is ever stored. Renaming and uploading are mutating requests, so they sit behind the
  same-origin `Origin` check (section 8).
- `folders` (id, account_id, name, role[inbox|sent|drafts|trash|archive|junk|other], uidvalidity,
  highestmodseq, last_sync_at)
- `messages` (id, account_id, folder_id, uid, content_key, message_id_hdr, in_reply_to, references, subject,
  from/to/cc/reply_to/delivered_to JSON, date, size, flags JSON, internaldate, auth_results JSON,
  has_attachments, raw_blob, raw_path, body_status, body_text, body_html_sanitized,
  thread_id, snippet, parse_errors JSON, derived_version). Unique (folder_id, uid).
  **Where the raw message lives depends on its size** (limits in `STANDARDS.md` 4a): up to 2 MiB
  it is `raw_blob` in the row; from 2 MiB to 64 MiB it is a file at `raw_path` (relative to the data
  directory, `spool/<folder row id>/<uid>.eml`, mode 0600, written atomically) and `raw_blob` is
  empty, so reading a row never loads the bytes; above 64 MiB it is not downloaded at all and
  `body_status` is `too_large` (the envelope is mirrored and the UI offers "open in webmail").
  A spooled message is parsed by `mime.ParseStream`, which keeps only headers, text bodies and
  small parts in memory and records the rest as `PartInfo`; an attachment is served by
  `mime.CopyPart`, which decodes it from the file straight to the response in a fixed buffer, so
  attachments are never held in memory. The same walk enumerates every attachment and inline
  part with its path, decoded size and a SHA-256 of its decoded bytes (chunk 2f), so sync writes
  the `attachments` rows from the message it has already read, with no extra pass. The spool is
  part of the mirror (rebuildable from IMAP,
  not backed up). A disabled message keeps its file like its row; `sync.SweepSpool` removes only
  files no row owns (crash-leftover temp files, downloads whose row never landed) once they are
  over an hour old. **Derived data is versioned and written atomically (round 32b).** The body
  text, snippet, `has_attachments`, parse errors, `body_status`, `body_html_sanitized` and the
  attachment rows are computed from the raw message by one pipeline (the parser, the sanitizer and
  the part walk), and `SetMessageDerived` writes all of it plus `derived_version` in a single
  transaction, so a crash leaves the previous data and version rather than half of the new. The
  version is `sync.DerivedVersion`; **bump it whenever that pipeline's output changes**, and a
  fingerprint test over a fixed corpus fails if the output moves without a bump. Every row behind
  the current version is re-derived from its raw message (the row's blob, or the spool file
  streamed) by `Fetcher.Rederive`, newest first and at most 200 per sync run, which is how a parser
  or sanitizer fix reaches mail that is already mirrored. A message whose spool file is gone is
  marked `derive_failed_version` and skipped until the version moves on, so it cannot hold a slot in
  every pass; a failure that may clear (permissions, a busy disk) is skipped unmarked and retried. `thread_id` has its own writer
  (`SetMessageThread`/`ReplaceThreads`). `UpsertMessage` writes none of these after the first insert
  and never blanks `raw_blob`/`raw_path` with a row that carries none, so a re-sync or a flag
  refresh cannot erase derived data or the raw message.
- `threads` (id, account_id, root_message_id, subject_norm, last_date, message_count)
- `attachments` (id, message_id, filename, mime, size, content_hash, cid, storage_path).
  `storage_path` is the part path inside the message's own raw bytes (there is no second copy of
  the attachment), `size` is the decoded size, and `content_hash` (SHA-256 of the decoded bytes) is
  the durable identity across messages, folders and mirror rebuilds. `id` is per `(message, part)`
  because the row belongs to one message, so extraction and embeddings key on `content_hash`, never
  on the row id; the reader's public attachment id is the part path (stable, and it survives a
  rebuild).
- `extracted_text` (ref, kind[body|attachment], tier, status, text, derived_version): `ref` is
  the message content key for a body or the attachment content hash, so the same file is read once.
  Every outcome above is recorded, so nothing is retried forever.
- `embeddings` (account_id, ref, kind, chunk_ix, model, dims, scale, norm, vector BLOB; keyed by
  content and model, never by folder or UID), plus the FTS5 `search_index` over
  subject/body/attachment text with `search_docs` mapping a content key to its rowid.

Locally owned state (`state.db`, not on the server and not rebuildable, so backed up, section 9):
- `tags` (id, slug, name, color), `message_tags` (account, content_key, tag_id, source: user | rule
  | model | server), `rules` (conditions JSON incl. fuzzy Jev questions, actions JSON, account
  scope, enabled), `snoozes` (account, content_key, until), `image_allow` (sender/domain),
  `settings` (key/value + per-account overrides), `jev_questions` (user-defined and overrides),
  `account_profiles` (account_id, display_name, icon, photo_blob; written only by
  `SetAccountProfile`/`SetAccountPhoto`, which require the mirror account to exist).
- **Tags live in both places (settled, round 30).** The definition (name, color) and the durable
  membership are local, in `state.db`. Membership is also written to the server as an IMAP
  keyword `$ivy-<slug>` (the slug is ASCII, because a keyword is an IMAP atom and tag names are
  not), IMAP first as for every write. Sync reads keyword changes made by other clients back into
  `message_tags` (source `server`); a keyword that disappears removes the membership. If a
  mailbox is lost or restored, local membership re-applies the keywords. Spike S1 showed Purelymail
  persists custom keywords (`PERMANENTFLAGS \*`); an account whose server does not allow arbitrary
  keywords keeps its tags local-only.
  **How 3e built it (rounds 51 and 52).** A tag is a `flags` outbox op for the keyword, so it has the
  idempotency key, the inverse cancellation and crash recovery of every other flag. *The membership
  follows the server's acknowledgement*: the worker writes it after the keyword is confirmed, never
  before. On a folder without `\*` in `PERMANENTFLAGS` the keyword is dropped from the op, an op that
  was only keywords finishes local-only without a failure, and the membership is written anyway.
  The slug is stored on the tag at creation and never recomputed (a clash gets `-2`, `-3`), so a
  rename cannot orphan a keyword. Read-back works on *transitions* of a row's keyword set, never on
  absence: a keyword that appears adds membership (source `server`), one that disappears removes it
  once no other live copy of the same content key (N8) still carries it, and a slug with no tag, or
  a malformed one, is ignored and left alone. A row that arrives new for mail the operator already
  tagged (a rebuilt mailbox, a new UIDVALIDITY) gets its keywords re-applied through the outbox, if
  the folder keeps them. Deleting a tag queues a keyword clear for every live copy, then removes the
  tag and its memberships, or refuses with `outbox_full` and changes nothing. Limits are in
  `STANDARDS.md` 4a.
  **How 3g built rules, snooze, People and Reading (round 56).** A rule is data: `conditions`
  (`from`, `subject`, `account`, `has_attachment`) and `actions` (`tag`, `reading`, `snooze`) as
  JSON, validated in Go against that closed vocabulary, so running a rule never calls a model;
  fuzzy checks arrive with Jev in chunk 5. The ingest pass in `Settle` evaluates the enabled rules
  scoped to an account over messages it has not seen (`rule_eval`, a rebuildable mirror cache),
  records each match once in `rule_hits`, and applies the local actions: a tag action is a keyword
  write through the outbox, and `reading` is the reserved `reading` tag through the same path; a
  snooze is local state. A rule created after mail arrived reaches it only through the explicit,
  idempotent apply, after a free local dry run. **Snooze** is a local hide-until (`snoozes`): the
  message never leaves the server or the mirror, and the inbox view hides it by loading the active
  keys from state and excluding them in the mirror query. **Reading** is that same reserved tag, so
  later Jev applies it with no second mechanism; the inbox hides membership the same way, and the
  Reading screen is the tag's messages. **People** is derived: `people` in the mirror is one row per
  (address, account) rebuilt from From/To/Cc headers, and the operator's merges of several addresses
  into one person (`person_links` in state) are applied at read time, so a mirror rebuild keeps them
  and the result never depends on account order.
- `outbox` (queued IMAP actions with retry state), `send_queue` (composed message, undo deadline):
  unsent work cannot be rebuilt from IMAP, so it lives here.
- `api_calls`, the **cost ledger (settled, round 30)**: one row per remote API call and, for
  batched calls such as embeddings, **one row per message in the batch** with the call's exact cost
  allocated by token share. Columns: time, provider, endpoint (systemone, chat, vision, embeddings),
  model, feature, account, content key, input and output tokens, exact cost from the response
  (`cost_estimated` is set when a provider reports none), latency, outcome, call id. Gate-blocked
  and failed calls are recorded at zero cost, and local calls (Ollama) at zero cost so volume is
  visible. `api_caps` monthly counters. Only the gate writes it, and nothing that costs money is
  reachable except through the gate. Spike S10: 150,000 rows (every embedded message plus a Jev
  call for half of them) is about 27 MiB.

Derived (`mirror.db`; regenerable, but regenerating costs money, so rows are keyed by content key
and a move, archive, trash or flag change never recomputes them):
- `decisions` (account_id, content_key, question_id, instruction_hash, model, probabilities JSON,
  choice, confidence, cost, created_at), `needs_me` (account_id, content_key, verdict, reason,
  stage2_model, state)
- `people` (one row per (address, account) derived from the From/To/Cc headers; the operator's
  merges live in state.db's `person_links`), `rule_eval` (which messages the ingest rule pass has
  seen), `receipts` (extracted fields, renewal dates)
- `schema_migrations` positional `user_version` in each file (never reorder or edit existing entries).

Vectors (spikes S8 and S10): **embed once.** A message is embedded when it first arrives and is
eligible (the account has smart features on), keyed by (account, content key, chunk, model, dims);
moves, archive, trash, flag and tag changes never embed again, and neither does a UIDVALIDITY
reset, because the key is the message and not its place. Re-embedding happens only when the
content or the chosen model changes, started by the operator with a cost estimate and the monthly
cap applied. The default provider is OpenRouter (`perplexity/pplx-embed-v1-0.6b`, 1024 dimensions, 32k
context; the operator's choice, round 30), with local Ollama optional per account (section 6).
Storage is **int8**. That model returns int8 values natively (every component is a multiple of
1/128, verified), so they are stored exactly as returned, together with each vector's L2 norm
because the vectors are not normalised (norm about 2.8): cosine is the integer dot product divided
by the two norms. Vectors from a provider that returns float32 are quantised with one scale for the
whole set; S8 measured 98.7% top-10 recall against exact float32 at 768 dimensions. At 1024
dimensions the vectors are about 98 MiB per 100k messages plus about 400 KiB of norms (the scan
time is extrapolated, about 450 ms on the potato, not measured). Brute-force cosine in Go, streamed from SQLite in batches (never loaded
whole); embed subject plus the first part of the body per message and per attachment chunk, chunked
by token count with margin, not by characters. Each vector records its model, so a model change is
detected rather than silently mixed.

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
   locally owned state keys on `(account, content key)` so it survives moves and UID changes.
   Two rules settled in round 37:
   - **Sync defers to the outbox.** While a row has a pending outbox op, sync does not overwrite
     that row's flags or folder. The invariant holds for every row without a pending op, and for
     all rows once the outbox is empty. Reads never join the outbox.
   - **Move vs delete.** A UID that vanishes from a folder whose content key appears in another
     folder in the same pass is a **move**: the old row gets `disabled_reason = 'moved'` (kept,
     hidden, not counted as "server removed"), the new row is live, tags follow the content key.
     Only a key found nowhere is disabled as removed.
7. **Write path (outbox, built in 3d).** Every reader action is one row in the `outbox` table
   in `state.db` (migration 5) naming a **postcondition**, not a command: `flags`, `move` or
   `expunge`, keyed by `(content_key, source_folder_id)` (never the content key alone: N8). The op
   row is committed before the API reports the action accepted; the outbox worker then runs the
   IMAP command and only on its ack updates the mirror. The worker is one goroutine per account, on
   its own connection (not the IDLE one), strict FIFO by `seq`, one op in flight. The UID is
   resolved at dispatch (by Message-ID) and its `(uidvalidity, uid)` is committed in the same
   statement that sets the op `in_flight`, before anything goes on the wire. States: `pending`
   (committed, not sent), `in_flight` (may have been sent, ack unknown), and the terminal `done`,
   `failed`, `cancelled`; sync defers to a row while its op is live. An idempotency key unique over
   non-terminal rows makes a double tap one op without blocking a later flag/unflag/flag; a flag and
   its exact inverse collapse to two cancelled rows. A crash between the ack and the DB write is
   recovered by asking the server what happened (re-search by Message-ID, or the stored UID when the
   UIDVALIDITY is unchanged), so a move applies exactly once (gate C4). Bounded: 8 attempts, 24 h,
   500 queued ops, seven-day terminal retention, 5 s→15 min backoff. `move` needs `MOVE` and
   `expunge` needs `UIDPLUS`; without them the op fails rather than emulating. A move/delete is
   confirmed by the UI before enqueue; undo is the inverse op from the toast. The exact design and
   its crash recovery are in `docs/handoffs/2026-10-04-C3-outbox.md`.
   **Disabled, not deleted (settled, round 24):** the mirror may be the only complete copy, so
   "the DB follows the server" means *behaves as if deleted*, never *erased*. A message that
   vanishes from the server (expunged elsewhere, a provider mistake, a UIDVALIDITY reset, or an
   expunge the user did in Ivy) gets `disabled_at` and `disabled_reason` set instead of its row,
   raw blob and derived data being removed. Disabled messages are hidden from every view, count,
   search result, rule, digest and LLM tool (the Ask Ivy tools can never see them), and they are
   **never purged automatically**. If the message reappears on the server (matched by Message-ID +
   content hash) it is re-enabled with its tags intact. Mirror health shows "N hidden because the
   server removed them" with **Restore** and an explicit **Purge forever** action. A mass-disable
   sweep (more than N messages or X% of a folder, including UIDVALIDITY resets) raises a Mirror
   health alert with a one-click Restore; sync continues because disabling is reversible. Scenario
   tests: the fake server empties a mailbox, resets UIDVALIDITY, and restores messages. Disabled
   raw blobs are part of the backups (section 9).
8. **Events:** every DB change fans out through an SSE hub so open clients update live. The
   contract (round 37) is **hints only**: one stream, `/api/v1/events`, small typed events
   (`message.changed`, `folder.changed`, `sync.state`, `outbox.state`, `health.alert`) that say what
   to refetch. No replay and no event ids; after any reconnect the client refetches what it is
   showing. A slow client's queue is bounded and drops hints (the next refetch heals it). In code:
   `events.Hub` never blocks `Publish`; a hint equal to one already queued is coalesced (so a
   backfill's thousand `message.changed` collapse to one), a full queue drops its **oldest** hint
   and counts it, and closing the hub (wired to `http.Server.RegisterOnShutdown`) ends every open
   stream, which would otherwise never go idle for `Shutdown`. Behind any wrapper writer the handler
   needs `Unwrap()` for `http.ResponseController` to flush (`gateway.errorWriter` once lacked it).
   `sync_state` (migration 8 of the mirror) holds per-account status; `store.SetSyncState` keeps
   `last_ok_at` across a failure and clears the error text on recovery.
9. **Spam handling (round 17, proposed):** the provider filters (Purelymail: SpamAssassin, Junk
   folder). Ivy stores the parsed `X-Spam-Status` score/flag on each message, treats the `junk`
   folder as a normal mirrored folder, and implements Mark spam / Not junk as IMAP MOVE to and from
   Junk through the same outbox, because those moves train the provider's per-user filter (it needs
   ~200 examples each way). Jev spam questions are advisory tags only (JEV.md 3B).

## 5. Parsing and rendering

- **Parse:** `jhillyerd/enmime` (settled: import, don't build) -> text, HTML, inlines, attachments,
  charset handling, `Authentication-Results` and threading headers, plus a bounded list of
  non-fatal errors stored on the message. Parsing never aborts a sync: a hostile or malformed
  message keeps its raw blob and its recorded errors (`mime/`, chunk 2c).
- **Sanitize:** `bluemonday` strict policy server-side; strip scripts, forms, event handlers,
  `javascript:`/`data:` abuse, `<base>`, `<meta refresh>`; rewrite `cid:` to local attachment URLs;
  neutralize/remove remote images and CSS URLs unless the sender is allow-listed (configurable);
  strip known tracking pixels even when allowed (configurable).
- **Display:** sandboxed `<iframe sandbox>` with a strict CSP and no script, styled for light/dark,
  with a plain-text view always available. Links open with `rel=noopener noreferrer` and show the
  real destination before leaving. **Chunk 2d finding:** `render/` produces the sanitised HTML and
  the policy, and the reader frames it with `sandbox="allow-same-origin"` and a deny-all CSP. A
  `<meta>` CSP inside a `srcdoc` frame is enforced by WebKit but ignored by Chromium, so the
  cross-browser remote-content assertion uses the body-document endpoint (2f), which carries the
  policy as a response header; the reader frames that endpoint instead of `srcdoc` (2g, done). The
  app's own CSP still needs SvelteKit build-time script hashes and remains open.
- **Threading (chunk 2e):** JWZ from Message-ID/References/In-Reply-To with a normalized-subject
  fallback, implemented in `thread/` as pure logic and stored on each message and in the
  `threads` table. **A thread's stored id is sticky (round 32b):** `thread/` proposes the content
  key of the root, but `store.ReplaceThreads` keeps the existing id carried by a rebuilt thread's
  oldest member (a merge collapses onto the older conversation, a split leaves the id with the half
  holding the oldest message) and mints a new one only for a thread with none, scoped to the
  account as `<account>:<key>` because the same email delivered to two addresses has one content
  key and `threads.id` is a global primary key. So the id survives a move, a UIDVALIDITY reset and
  the arrival of an older root during backfill, and features may key on `thread_id`. The fallback strips only the `Re:` family (`RE[5]:`, `Re: Re:`), not
  `Fwd:`, because a forward is a different subject (JWZ step 5). The pass runs over a whole
  account after each fetch so a reply filed in Archive still joins its inbox root; it reads
  headers only. Disabled messages are excluded, so they never anchor a thread. The subject pass
  is a re-thread of the account, not incremental; chunk 3's steady-state sync narrows it once
  it owns the flag/move updates.
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
- **Building and submitting (chunk 4a, round 61):** `compose/` is a pure builder and `smtp/` the
  only transport. Every header-bound value is validated (a CR, LF or NUL is rejected, never
  stripped), an outgoing addr-spec must be ASCII because Purelymail has no `SMTPUTF8`, and a display
  name or subject that could be read as an RFC 2047 word is force-encoded. The operator's markdown
  is the `text/plain` part exactly as typed and goldmark renders the `text/html` part of a
  `multipart/alternative` (raw HTML stays off; links are narrowed to `http`/`https`/`mailto`). `Bcc`
  is an envelope recipient on the wire and a header only on the Sent copy. `smtp.Submit` opens one
  implicit-TLS connection per call, reads `SIZE` from `EHLO`, and returns a `*SendError` whose `Kind`
  and `Transient` flag the 4b queue branches on; a refused recipient aborts the whole transaction, so
  a send is never partial. **4b** added the `send_queue` (the durable may-have-been-sent point
  before `DATA`, `unconfirmed` for an unknown outcome, the Sent `append`) and **4c** the API: `POST
  /send` builds both copies and commits the row with the undo deadline, `POST /send/{id}/undo`
  cancels a queued row before its server-side deadline and returns the draft, and every change is a
  `send.state` hint. The window is `compose.undo_delay_seconds` (default 10, 0 = off, per account over
  global); the From must be the account's own address until 4e brings identities.
- **Auth results:** parse `Authentication-Results` (SPF/DKIM/DMARC) into a trust signal used by
  the phishing question and the spoofed-sender discount. Only the **topmost** header whose
  `authserv-id` is in the account's `trusted_authserv_ids` is believed, and **only that one
  header**: a later header cannot add a method it omitted. The trusted id is stored beside the
  verdicts (N9, `papercuts.md`). RFC 8601 does not let a consumer interpret the header until the
  operator configures the ids, so an empty list means no verdict, never a forged one. **Purelymail
  adds no SPF/DKIM/DMARC verdicts** (spike S1: its header is only `mail.purelymail.com; auth=pass`),
  so on Purelymail the signal is empty and the spoofed-sender discount stays off; verifying DKIM
  ourselves is the later feature that would change that.

## 6. Search and ask

- **Hybrid (settled):** FTS5 (BM25) + embedding similarity merged with reciprocal rank fusion into
  one list from one search box. **Embeddings come from OpenRouter by default (settled, round 30;
  `perplexity/pplx-embed-v1-0.6b`)**, chosen per account, with a local Ollama endpoint (any host,
  `nomic-embed-text`) as the private option behind the same `Embedder` interface. Why hosted
  first: spike S8 measured the potato at 17 s per email-sized chunk and a 36 s cold model load
  holding about 407 MiB, while OpenRouter embedded about 51 chunks per second and answers a query
  in about 0.3 s, for about $0.15 per 100k messages. Hosted embeddings send message text off the device, so they go through
  the gate (the account must have smart features on, caps apply) and every embedded message is a
  ledger row; an account with smart features off is searched by FTS5 alone, or by local Ollama if
  that is configured for it. Embedding is a low-priority background queue (one job at a time),
  runs once per message (section 3), and at query time embeds only the query. If the provider is
  unreachable, search falls back to FTS5 and says so quietly.
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
- **How 3f built it (rounds 54 and 55).** The index is a standalone FTS5 table `search_index`
  (subject, body, attachment) with `search_docs` mapping each durable `(account, content key)` to
  its rowid, so one document covers a copy in two folders and an identical Message-ID (N8). The
  rowid is `SearchDocID`, a SHA-256 of the identity, so two identical seeds and a full rebuild
  produce byte-identical databases. Hidden mail is excluded at query time by an `EXISTS` on a live
  `messages` row, so a disabled message's index entry can stay for a later restore without ever
  showing (invariant 10). `ReindexContent` rebuilds one key's document after a derivation or an
  extraction. The tokenizer is **`unicode61 remove_diacritics 2`** (round 55: 402 KB and ~0.65 ms
  per query for 5000 docs, against 7.3x the index and 12x the time for `trigram`; folding makes
  `cafe` find `café` and the prefix form works). Tier 0-1 extraction reads an attachment once,
  bounded by input size, output size, page count and a 20 s timeout, records every outcome
  (ok/empty/unsupported/failed/too_large) so it is never retried, and runs in the settle pass after
  derivation, not the fetch. The embeddings gate is `llm.Gate`: the provider clients are unexported,
  an architecture test fails if any other package names a provider endpoint, and one ledger row per
  input plus the monthly counter are written in one transaction. The embed-once queue (`search`)
  keys a vector on `(account, content key or attachment hash, chunk, model, dims)` and sends at most
  `MaxBatchInputs` through the gate at a time; a move, an archive or a UIDVALIDITY reset cannot
  re-embed. Search fuses BM25 with brute-force cosine by reciprocal rank fusion, query embedding
  included, and falls back to keyword-only when the provider is off, capped or down.

## 7. LLM layer

- **Interfaces:** `decide(state, questions) -> answers` (Jev; fallback: chat model + JSON schema),
  `complete(prompt) -> text` (configurable OpenAI-compatible chat model via OpenRouter),
  `see(images, prompt) -> text` (vision-capable model, per-account opt-in).
- **The gate (single chokepoint):** every outbound remote API call (Jev, chat, vision and
  embeddings, and anything else that costs money) goes through one function that enforces: account
  `llm_enabled`/`vision_enabled`, ask-selection rules, withheld-message rules
  (tripwire/sensitive), monthly caps, a ledger row per call (per embedded message for batches)
  with the exact cost from the response (section 3), and timeouts. An
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
  hot-reload. **An account can also be connected from the app (round 59):** its connection details
  go in `state.db` (`account_configs`) and its password in a private file, `data/secrets/<id>` (mode
  0600, outside every backup), read after the environment and `.env`. A restored `state.db` therefore
  brings the accounts back without their passwords, and each shows "Update password". Delayed send (undo send) is a setting (`compose.undo_delay_seconds`; default 10,
  0 = off, with an upper bound), editable globally and per account.

## 9. Deployment, update, backup

- **Container image, built in CI (settled, round 29; supersedes rounds 24 and 25).** The potato
  never compiles anything: spike S3 measured a cold `modernc.org/sqlite` build at about 730 MiB
  peak and exhausted swap beside the live services. A multi-stage `Dockerfile` builds the frontend
  (pinned Node/pnpm, lockfile, precompressed brotli/zstd/gzip) and cross-compiles the Go binary
  (`CGO_ENABLED=0`, `GOARCH=$TARGETARCH`) on the runner, both stages pinned to `$BUILDPLATFORM` so
  no QEMU is needed; only the tiny final stage is per-arch. A GitHub Actions job publishes a
  multi-arch (amd64, arm64) image to GHCR on every push to main, tagged `:latest` and the short
  SHA (rollback by name). The workflow uses only `GITHUB_TOKEN` with `packages: write` and never
  runs on pull requests. Nothing compiled is committed: `internal/webui/build/` is git-ignored
  apart from a tracked placeholder that keeps `go:embed` and `go build` working from a fresh
  checkout.
- **`ivy update` (also an in-app button):** resolve the digest of `:latest` from
  `ghcr.io/autumnsgrove/ivy` on GHCR (waiting out an in-progress `docker-publish.yml` run for main,
  best-effort, before resolving), hand off to a host-side update watcher (the Polaris design, with a
  signal file at `<data_dir>/update-signal/requested`), which pulls that exact digest, recreates the
  container, health-checks it against the image's own HEALTHCHECK, and rolls back to the previous
  image on failure. The watcher runs unprivileged from the checkout as a systemd oneshot
  (`ivy-update.path`/`.timer`); `install.sh` installs its units and the hash-pinned root wrapper
  (`/etc/ivy/watcher-sync-verify.sh`) that is the only thing the update flow may run as root. The
  data directory is a bind-mounted volume, so the config, SQLite files, blobs and backups survive
  image swaps. The build version is `r<git-count>.<short-sha>`, passed as `-ldflags -X`;
  `POST`/`GET /api/v1/update` drive it and a `update.state` SSE hint brings progress to the UI.
- **Backup (settled, rolling policy changed in round 30):** **once a day, keep 15 days, about 15
  backups; prune anything older than 15 days.** (It was twice a day for 30 days; spike S10 showed
  that copying the mirror is far too heavy, so the policy only has to protect the small state
  file.) Each run takes a consistent online snapshot (SQLite `VACUUM INTO`/backup API) of
  **`state.db` only**: tags and their membership, rules, snoozes, settings, allow-lists, check
  definitions, the outbox and send queue, the API cost ledger, accounts without secrets,
  zstd-compressed, verified (`integrity_check` + open test) before it counts. At 100k messages
  that file is about 27 MiB and 15 snapshots about 390 MiB uncompressed. **`mirror.db` is a full
  mirror in its own file and is never backed up**; it is rebuilt from IMAP (about 27 minutes of
  database work per 100k messages on the potato plus the transfer, and re-embedding costs about
  $0.15 per 100k via the default provider). Disabled messages' raw blobs (the only copy of
  server-deleted mail) are written to a **content-addressed, de-duplicated, append-only file
  store** outside both databases at the moment a message becomes disabled, are included in every
  backup, and are not pruned by the 15-day rule. Safety rules for pruning: only after a new backup
  has succeeded and verified, and never below a floor of the 10 newest backups (a broken clock or
  failing backups must not prune everything). Targets: a folder or S3-compatible bucket; **at least
  one target should be off the potato** (a backup on the same card/disk as the DB does not survive
  the device dying) and `ivy doctor` warns if every target shares the DB's disk. One-line restore,
  followed by a mirror rebuild from IMAP. Failures show in Mirror health; the last successful backup
  time is visible in Settings.
- **Resource budget (potato, 1.9 GB RAM, ~800 MB free, swap in use):** Ivy target well under
  100 MB resident at idle; embedding and extraction are serialized background jobs; numbers get
  measured and recorded in `docs/perf.md`, not assumed.

## 9a. Operational gaps found in the round 23 review

- **Disk (settled, round 24): not a constraint.** The potato has 256 GB, so there is no storage
  budget and no raw-blob eviction (disabled messages are kept forever). Still: zstd at rest for
  speed and fewer flash writes, and `ivy doctor` warns on low free space (for example under 10%).
  Spike S10 measured it: about 12 KiB per message plus about 0.4 MiB per message with an
  attachment, so 1.2 to 5.6 GiB for 100k messages (attachments are about 85% of it).
- **SQLite on flash:** power loss and card corruption are real. `PRAGMA integrity_check` in
  `ivy doctor` and at startup after an unclean exit, periodic WAL checkpoints, backups of locally
  owned state verified by a restore test, and a kill -9 / power-loss test of the outbox and sync
  checkpoints with the fake server.
- **Search quality:** **settled in 3f (round 55):** `unicode61 remove_diacritics 2`, measured
  against `trigram` (402 KB / ~0.65 ms vs 2.94 MB / ~7.96 ms for 5000 docs); a trigram index is a
  later add if CJK becomes a need.
- **Time:** all timestamps UTC in the DB, local zone at the edge, with DST and bad-`Date`-header
  tests (clock-skewed mail sorts by internal date).
- **Logs:** journald with size limits so logs don't wear the card; no mail content in logs.
- **Frontend build and delivery:** resolved in section 9 (built inside the CI-published image).

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
