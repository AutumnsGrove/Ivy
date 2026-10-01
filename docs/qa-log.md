# Q&A log

Running record of questions asked and answers given during planning. Newest round last. Settled
answers get folded into `PLAN.md` / `CLAUDE.md`.

## Round 0 — pre-planning conversation (2026-10-01, in the Polaris repo)

| Topic | Decision |
|---|---|
| Name | Ivy (grove.place lore). Astral names (Columba, Heliograph, Lagrange, Oort...) discarded. |
| Repo | New standalone folder + git repo, `~/Documents/Projects/ivy`. |
| Backend language | Go. TypeScript/Node rejected: Grove's hosted backend would differ anyway (webhook-in + D1), and Go fits the potato's RAM. |
| Database | SQLite. Postgres considered (wanted to try it, pgvector is nice) but D1 = SQLite and the future Grove path favors it. |
| Sync model | Mirror: DB follows IMAP incl. deletes/moves/archives; DB exists for speed, tagging, sorting. Writes go to IMAP first. |
| Embeddings | Ollama `nomic-embed-text` on the potato by default; optional remote provider for others. |
| Frontend | SvelteKit; portable via one API client module. Grove tokens may be vendored; fresh design. |
| Old Ivy (Lattice) | Reference only. Operator doesn't remember its UI or triage/digest idea; starting from new ideas. |
| Grove hosted version | Far off / maybe never. Not a design constraint beyond keeping SQLite + a portable frontend. |

## Round 1 — product basics (2026-10-01)

| Question | Answer |
|---|---|
| Biggest pain points in mail today | Newsletters/noise, not knowing what needs me, receipts/invoices. (Finding old mail was NOT picked, so search is a supporting feature, not the headline.) |
| Device | Both equally: phone and desktop are first-class layouts. |
| Sending in v1 | Reply and compose are in v1 (not read-only first). |
| Accounts | **Multiple, from day 1.** Many `@grove.place` addresses (autumn@, hello@, security@, dmca@, ...). Needs a per-account view AND a combined "all inboxes" view, like Outlook mobile. |

Implications noted: multi-account + compose + two first-class layouts makes v1 big; milestones must
sequence it (reader -> sync -> compose) rather than cut it. Whether the addresses are separate
Purelymail users or aliases routing into fewer mailboxes decides whether "account" means an IMAP
login or a recipient filter (open, asked in round 2).

## Round 2 — accounts, newsletters, LLM privacy (2026-10-01)

| Question | Answer |
|---|---|
| How are the grove.place addresses set up? | **Not set up yet.** Currently all in Forward Email; operator will migrate to Purelymail later today. Likely **separate users**. Operator wants help with the migration after planning, before building. |
| Combined-view address display | Colored badge per address; replies default to the address the mail was sent to. |
| Newsletters | Separate reading feed, daily digest, one-click unsubscribe (List-Unsubscribe). **Quiet auto-archive = future idea**, not v1. |
| LLM privacy | **Per-account opt-in**, off by default (security@/dmca@ can stay local-only). Embeddings stay local either way. |

### Research: Purelymail multi-user (answered live, 2026-10-01)

- **Many users on many custom domains, no extra charge.** Pricing is infrastructure-based (storage/usage), not per-user: "Multiple domains and users at no extra charge." (purelymail.com, /docs/features)
- **Routing rules** can send an address to a user, an external address, or several targets; exact > longest-prefix > catch-all, and **a routing rule beats a user account** with the same address. (/docs/routing)
- **Send-as works:** replying as the alias a mail was sent to is supported; webmail auto-fills it, third-party clients need an "Identity". (/docs/faq) Implication: Ivy needs identities and must send via SMTP as the right From address (still to verify live).
- **Limits:** ~3000 external messages/day, 300 at once; support can raise them for a legitimate need. (/docs/faq)
- **Server capabilities, read live via `CAPABILITY` on imap.purelymail.com:993:**
  `IMAP4rev1 LITERAL+ CHILDREN I18NLEVEL=1 NAMESPACE IDLE ENABLE CONDSTORE QRESYNC ANNOTATION AUTH=PLAIN SASL-IR RIGHTS= WITHIN ESEARCH ESORT SEARCHRES SORT MOVE UIDPLUS UNSELECT COMPRESS=DEFLATE`
  - **CONDSTORE + QRESYNC + IDLE + MOVE are all supported** -> efficient mirror sync (flag changes, expunges and moves since a modseq) is available. Spike resolved: yes.
  - **Not advertised:** `SPECIAL-USE` (find Trash/Archive/Sent by name heuristics or LIST attributes), `THREAD` (we thread locally), `NOTIFY`, `LIST-EXTENDED`.
  - `ANNOTATION` (RFC 5257) is advertised: possible server-side home for tags, to investigate.
  - Plain port 993 TLS, `AUTH=PLAIN`; app password required when 2FA is on.

## Round 3 — features and LLM (2026-10-01)

| Question | Answer |
|---|---|
| "What needs me" | **Both:** a dedicated "needs attention" view (each item with a one-line reason) plus inline markers in the normal inbox. |
| Receipts/invoices | **All four:** auto-extract fields (vendor, amount, date, renewal date), a ledger view, renewal reminders, and a plain "receipt" filter/tag as the baseline. |
| Prompt-injection posture | **Needs discussing** (discussed in conversation, see round 4 for the decision). |
| Hosted model | **OpenRouter, configurable** (key + model name in config, cheap default). Implemented against an OpenAI-compatible API, so other endpoints work too. |

## Round 4 — safety line, feel, access, threads (2026-10-01)

| Question | Answer |
|---|---|
| LLM safety line | **Local tags auto, everything else confirmed.** The model may write local tags/ratings (reversible, never leaves the box). Sending, deleting, moving, forwarding, unsubscribing always need a click. LLM output rendered as plain text (no auto-loaded images/links); citations are DB-verified message IDs, never model-invented URLs; one account's mail per LLM call; unsubscribe executed server-side with SSRF protection; spoofed senders discounted via `Authentication-Results`. |
| Design feel | **Calm and quiet:** soft, warm, whitespace, grove-green leaning, nothing shouting for attention. |
| Access control | **Later.** Tailscale-only is fine for now. When added it must be frictionless: **passkeys / Face ID first, password as fallback.** Not a v1 blocker, but don't design it out. |
| Conversations | **Threaded** (JWZ locally, since the server has no THREAD). |

### Operator input mid-round: the Jev model for classification

Operator: use **Jev 1.13** on OpenRouter for classification. Looked up (OpenRouter docs + web search):
- **What it is:** TypeSafe's "structured decision model" (`typesafe/jev-1.13`, alias `~typesafe/jev-latest`). Not a chat model: you send `state` (text, max 32k tokens) plus typed `questions` and get typed answers with probabilities. Question types: `noul` (probability of yes), `choice` (selected option + per-option probabilities + confidence), `score` (probability-weighted position on an ordered scale). Multiple questions per call, no documented question limit.
- **Endpoint:** `POST https://openrouter.ai/api/alpha/decisions` (**alpha**), or the TypeSafe-compatible `/api/v1/systemone`. Not chat completions. OpenRouter key only; hidden from the default `/models` list (`?output_modalities=decisions`).
- **Cost/speed:** $0.042 per M input tokens, output free; P50 latency ~0.22s per OpenRouter, ~0.33s in a third-party test. (A 50k-email backfill at ~1k tokens each is roughly $2.)
- **Caveats:** proprietary, **no reasoning traces or explanations returned**, alpha API (may change), 32k context. Some figures are from third-party sites; verify by spike with a real call.

Consequences recorded in PLAN.md: (1) Ivy's LLM layer is **two interfaces**, `decide()` (Jev by default) and `complete()` (configurable chat model); (2) the earlier promise of a "one-line reason" on every needs-me item can't come from Jev; resolved in round 5 by a Jev-then-bigger-LLM cascade; (3) field extraction (vendor/amount/date) still needs a chat model or structured-data parsing, Jev only decides; (4) Jev's typed output can't carry exfiltration text, a structural safety win, though probabilities can still be skewed by hostile content; (5) strangers without Jev need a chat-model-with-JSON-schema fallback behind the same `decide()` interface.

## Round 5 — reasons, history, images, notifications (2026-10-01)

| Question | Answer |
|---|---|
| Needs-me reasons | Operator proposal, adopted: a **two-stage cascade**. Jev is the cheap first pass that flags mail that *could* be worth investigating (tuned for high recall); only flagged mail is handed to a bigger LLM, which makes the final needs-me call and writes the reason. Reasons derive from the checks that fired plus the second pass; no reason call on unflagged mail. |
| History depth | **Everything** (full mirror, so search and the ledger cover all time). Sync order still open (headers first so the inbox is usable at once is an implementation detail). |
| Remote images / trackers | **Blocked by default, allow per sender, configurable in settings.** |
| Notifications | **None in v1** (Apple Mail keeps that job). |
| General principle | **"I want a lot to be configurable."** Many behaviors should be settings with sane defaults. |

## Round 6 — config, compose, attachments, naming (2026-10-01)

| Question | Answer |
|---|---|
| Settings home | **In-app settings panel (behavior, stored in SQLite) + file/env for secrets** (credentials, API keys), same split as Polaris. Per-account overrides. |
| Compose | **All four:** plain text + markdown, a rich-text editor, signatures per address, undo send. Cost note: two editors is the biggest compose item; sequence markdown first and rich text as a later milestone, both still in v1 scope. |
| Attachments | **Mirror and index them:** store locally with the mirror, extract text from PDFs/docs so search and the ledger can use them. Needs a disk-budget answer for the potato. |
| Feature names | **Yes, grove lore names**, plus a plain-English glossary (like Polaris's help modal). Names to be chosen from Grove's world. |

Potato storage check (read-only): `/` is a 233G flash card (`mmcblk0p1`), 41G used, **190G free**, so attachment mirroring is not disk-constrained; the real constraints are RAM and flash wear (use SQLite WAL, batch sync writes).

Lore naming (proposals, avoiding names Lattice already uses: Meadow, Clearing, Terrarium, Amber, Heartwood, Passage, Loom, Zephyr, Warden, Foliage, Gossamer, Shutter, Forage, Vista, Plant, Seedling, Fern, Nook, Solarium): needs-attention **Tendril**, unified inbox **Canopy**, each address **Branch**, newsletter feed **Understory**, daily digest **Dewfall**, receipts/renewals ledger **Rings**, ask-your-mailbox **Taproot**, sync engine/status **Roots**, new message **Sprout**. Approval pending in round 7.

## Round 7 — names, old mail, testing, milestones (2026-10-01)

| Question | Answer |
|---|---|
| Lore names | **Pick names later.** Use plain names in the plan. The authoritative list of existing Grove names is `Lattice/docs/philosophy/grove-naming.md` (1,223 lines; also `docs/museum/glossary.md`, `docs/philosophy/naming-research/internal-names-research.md`): check it before choosing. |
| Old Proton mail | **Start fresh.** Import is "a great idea, not sold yet"; Proton history is mostly junk. May revisit as an optional one-off later. |
| Dev/test setup | **Throwaway dev mailbox + fake IMAP server.** A dedicated dev@ Purelymail user seeded with sample mail for live checks, plus an in-memory IMAP server in Go tests for sync edge cases (UIDVALIDITY changes, expunges, moves). |
| Build order | **Read -> sync -> triage -> send.** Compose comes last, since the operator barely sends mail. |

Collision check of my earlier lore proposals against Grove's naming doc: **Canopy is taken** (Grove's opt-in directory) and **Rings is taken** (Grove's analytics). **Roots, Branch, Understory** appear only as ordinary words, not claimed feature names. **Tendril, Dewfall, Taproot, Sprout** are unused. Since names are deferred, nothing is decided; just don't reuse Canopy/Rings.

Discovery: Grove's naming doc defines Lattice's Ivy as "Email for Grove... zero-knowledge encryption, we can't read your mail" and lists its repository as **`AutumnsGrove/Ivy`**. That repo exists on GitHub (public, "Mail client for Grove.place", last push 2026-02-11, not archived). The new self-hosted Ivy is a different product with a different privacy posture (self-hosted, operator-run, optional hosted LLM), so the shared name needs a deliberate decision (round 8).

## Round 8 — repo, mail types, search, ask (2026-10-01)

| Question | Answer |
|---|---|
| Old `AutumnsGrove/Ivy` repo | **Archive it, reuse the name.** Timing: do it when the new Ivy is ready to publish (archiving is reversible but outward-facing; not done yet, needs an explicit go-ahead from the operator when the time comes). The new project stays local until then. |
| First-class mail types | **Contact-form submissions, security/abuse reports, personal correspondence.** (Automated notifications NOT picked as first-class; they fall to generic triage.) |
| Search | **Hybrid keyword + meaning:** FTS5 for exact words, embeddings for meaning, merged into one ranked list from one search box. |
| Ask-your-mailbox | **Written answers with cited emails**, every claim linking to real messages (Polaris's "sourcing is the product"). |

Implications: contact-form mail needs Reply-To-aware replies and threading (reply goes to the visitor, not the form's From); security@/dmca@ get priority treatment and default to local-only (already the per-account opt-in default); personal correspondence is protected from any automated handling, including the future quiet-auto-archive idea. **Tension found:** ask-your-mailbox over the combined view would put several accounts' mail in one LLM call, contradicting "one account's mail per LLM call" and per-account opt-in. Embeddings/search can span accounts (local); only the LLM answer step is affected. Asked in round 9.

## Round 9 — ask scope, tags/rules, license, interaction (2026-10-01)

| Question | Answer |
|---|---|
| Ask scope across accounts | **A switch mode in the UI:** tap the accounts you want included; non-included accounts are greyed out, so entering the mode is easy. The operator chooses the set per question, which counts as informed consent for mixing those accounts in one ask call. |
| Account customization | **Rename accounts, set their icons, and optionally upload photos**, all in settings (so others can personalize theirs too). |
| Tags and sorting | **Your own tags, automatic rules, model-applied system tags.** (Saved searches as smart views NOT picked.) |
| License | **AGPL-3.0**, like Lattice. |
| Interaction | **Swipe actions, keyboard shortcuts on desktop, snooze.** |

Design consequences: (1) the safety rule is refined to **automated classification stays one account per call; ask-your-mailbox may mix the accounts the operator explicitly selects**, and only accounts with LLM enabled are selectable (security@/dmca@ default to LLM-off and so appear locked, which is distinct from merely unselected); (2) each account has a display name, icon and optional photo (stored locally, resized server-side); (3) snooze, tags, rules, allow-lists and account display settings are **locally owned state**, so a backup path is mandatory and snooze needs a decision (local hide-until vs a server-side Snoozed folder); (4) AGPL-3.0 means a LICENSE file and headers policy at repo creation.

## Round 10 — backup, snooze, extraction, deployment (2026-10-01)

| Question | Answer |
|---|---|
| Backup | **Scheduled snapshot to a configurable target** (local folder or S3-compatible like R2): locally owned state only, not the rebuildable mail mirror; one-line restore. |
| Snooze | **Local hide-until.** (Apple Mail will still show the message in the inbox; accepted.) |
| Attachment text extraction | **Open: operator wants to discuss.** Raised: images may hold important text, so is OCR needed; what tools exist; and can MIME parsing be imported rather than built? (Discussion below, decision in round 11.) |
| Deployment | **Bare metal, simple.** The potato compiles the Go binary; the **frontend is NOT compiled on the target: its build output is bundled in the repo and embedded into the binary via go:embed** (how Polaris used to work). An **in-app "update" settings button that follows an `ivy update` command.** Docker is a maybe-later migration path, explicitly not sold on yet. |

Library check (GitHub, 2026-10-01): `jhillyerd/enmime` (MIT, 524 stars, pushed today) is a high-level MIME decoder; `emersion/go-message` (MIT, last push 2025-02) is the lower-level streaming parser; `emersion/go-imap` (MIT, 2.35k stars, pushed 2026-09-17); `danlock/gogosseract` (Tesseract in WASM via wazero, no CGo, Apache-2.0, 156 stars, last push 2025-06); `otiai10/gosseract` (CGo wrapper for libtesseract, MIT, 3.1k stars); `ledongthuc/pdf` (BSD-3, pushed 2026-09); `pdfcpu/pdfcpu` (Apache-2.0, 8.9k stars); `sajari/docconv` (MIT, last push 2024-07, shells out to external tools).

Build-on-target risks flagged (to spike on the real potato, not assume): the SQLite driver choice matters (pure-Go modernc is slow and memory-hungry to compile on a 2GB board; CGO mattn needs gcc on the potato but caches well); compile peak RAM competes with Polaris/SearXNG/Ollama (swap already in use), so `ivy update` should build to a temp file and only then restart; committing the frontend build output bloats history (acceptable "for now", per the operator); the update button runs `git pull --ff-only` + `go build` from a remote, so it should verify the remote and refuse on a diverged checkout, like Polaris's watcher.

## Round 11 — OCR and vision (2026-10-01)

| Question | Answer |
|---|---|
| OCR (Tesseract) | **Far-out plan**, built only if/when the operator decides it's useful. |
| Vision-capable chat model for images | **Yes: per-account opt-in, and soon.** Operator thinks this may be the easier path and cover most image-text cases before any OCR model is considered. |
| MIME | Import, don't build: **`jhillyerd/enmime`**. Ours: sanitization (bluemonday), cid: rewriting, threading, tracker handling. |

Consequences: the vision model becomes the **primary** image-text path (not a fallback behind OCR), so the cascade gate that OCR confidence would have provided is gone and cost/privacy needs another gate (asked in round 12). Only attachments and inline `cid:` images are eligible (never remote-URL images, consistent with blocking remote content). Candidate cost controls: size/dimension thresholds, dedupe by content hash (signature logos repeat), a configurable monthly spend cap (like Polaris's api_usage caps), and a per-message "read this image" on-demand action. Open spikes: whether the chosen vision model/OpenRouter route accepts PDFs directly (scanned PDFs can't be rasterized in pure Go), and which cheap multimodal model to default to (configurable `vision_model`).

## Round 12 — vision gate, drafts, people, non-goals (2026-10-01)

| Question | Answer |
|---|---|
| Vision gate | **Flagged mail only, plus on-demand:** auto-read attachments on mail Jev flags as receipt/needs-me/contact/security (opted-in accounts only), and a "read this image" button anywhere else; the monthly spend cap applies. |
| Drafts | **Server Drafts folder** (IMAP APPEND), so the same draft opens in Apple Mail. Accepts the known cross-client draft messiness. |
| People | **Yes, derived from the mirror:** everyone you correspond with, recent threads, recipient autocomplete in compose. No separate address book. |
| Non-goals for v1 | **Calendar invite RSVP, PGP/S-MIME, CardDAV/CalDAV, multiple human users.** Operator note: all of these could be in scope for later versions **except multiple human users, which is out for now** (not on the roadmap). |

## Round 13 — operator review of the technical defaults (2026-10-01)

The 11 proposed defaults were put to the operator. Answers (using the numbering of that list):

| # | Default | Answer |
|---|---|---|
| 1 | Keep raw RFC 822 messages | Yes |
| 2 | Sync design (IDLE + QRESYNC sweep, headers-first backfill) | Yes |
| 3 | IMAP-first writes + optimistic UI + outbox | Yes |
| 4 | SQLite | Yes, **definitely pure Go** (so `modernc.org/sqlite`; compile cost on the potato gets measured, not a reason to switch) |
| 5 | HTML rendering (server sanitize + sandboxed iframe + CSP) | Yes |
| 6 | API: JSON REST + SSE, one typed client module | **Not addressed**; assumed accepted, flagged in PLAN.md section 7 |
| 7 | Frontend (SvelteKit, adapter-static, Svelte 5, pnpm, vendored tokens) | Yes, **pure CSS, no Tailwind** |
| 8 | Config (ivy.yaml + .env; behavior in app) | Yes |
| 9 | Sending (enmime builder, SMTP, undo send) | Yes; **delayed send must be configurable in settings** (setting `compose.undo_delay_seconds`) |
| 10 | Cost tracking + ledger | Yes; **a stats panel like Polaris's** with a log of everything, easily viewable |
| 11 | Testing | **STRONG testing: test absolutely everything.** -> `docs/TESTING.md` |
| new | "Consider other ways to use Jev; it's our cheap option" | -> `docs/JEV.md` question catalog |

**Corrections discovered while writing the docs** (Polaris already ships a Jev integration and has
live spike write-ups):
- Jev's confirmed OpenRouter path is **`{base}/systemone`**, model **`jev-latest`** (also `jev-1.13`,
  `typesafe/jev-1.13`), NOT the `/api/alpha/decisions` path found by web search. Source: Polaris
  `jev/jev.go`, `docs/plans/source-verification.md`.
- Only `choice` questions are proven (`instructions` + `criteria`, criteria keys are the options);
  `noul`/`score` are untested, so yes/no is modeled as a `choice` until spiked.
- Real measurements to reuse: 20 questions in one call p50 ~250 ms; 40-way concurrency fine;
  hard 400 on context overrun (no silent truncation, so Ivy must truncate); empty state is handled
  cleanly; prompt injection in source text was resisted; Polaris Oracle checks 33/35 on 35 prompts.
- Polaris already has a `jev_usage` ledger, `aux_usage`, and a Stats struct/panel to model the Ivy
  stats panel on; `dev/fakeopenrouter` already speaks `/systemone` and can be reused or copied.

Documents written this round: rewritten `PLAN.md`, new `ARCHITECTURE.md`, `TESTING.md`, `JEV.md`.

Notes on round 5: the second-stage model sees raw email text, so it's the higher-risk stage: no tools, structured output (`needs_me` bool + short reason), reason rendered as plain text. Configurability needs a deliberate home (config file vs in-app settings with per-account overrides), asked in round 6.
