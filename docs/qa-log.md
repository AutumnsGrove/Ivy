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

## Round 14 — commit, push and the repo name (2026-10-01)

Operator asked to commit and push, and chose "archive the old repo and reuse the name" with an
explicit go-ahead. Executed:
1. Committed locally (`da60973`).
2. GitHub makes archived repos read-only and the histories are unrelated, so "archive then push to
   the same name" is impossible without a destructive force-push. Instead: renamed
   `AutumnsGrove/Ivy` -> **`AutumnsGrove/Ivy-legacy`**, archived it (0 stars, 0 forks, no open PRs;
   4 open issues about the old TypeScript app are now frozen read-only), then created a fresh
   **`AutumnsGrove/Ivy`** and pushed `main`.
3. The new repo is **private** (visibility wasn't specified; private -> public is a one-way-safe
   change, the reverse is not). Lattice's docs link to the `AutumnsGrove/Ivy` URL, which now points at
   this project.

## Round 15 — the Purelymail migration (2026-10-01)

Executed with the operator in Cloudflare DNS and Purelymail's dashboard; checked with `dig`.

- **DNS before:** MX -> Forward Email (priorities 10/20); only root TXT was Forward Email's
  verification; no SPF, no DMARC; Grove's Resend records (`resend._domainkey`, `send.grove.place`
  MX/SPF via SES) on separate names.
- **DNS after (verified):** MX `0 mailserver.purelymail.com`; SPF `v=spf1 include:_spf.purelymail.com ~all`;
  Purelymail ownership TXT; DKIM CNAMEs `purelymail1/2/3._domainkey`; `_dmarc` CNAME ->
  `dmarcroot.purelymail.com` (**p=reject**); autodiscover SRV. Forward Email records removed;
  Resend records untouched.
- **Forward Email inventory (screenshots):** catch-all (no recipients), alerts->autumn, autumn (IMAP,
  1.51 MB), dmca (IMAP, 472 KB, no forward), feedback->hello, github->autumn, hello (IMAP, 356 KB, no
  forward, so NOT forwarded to autumn), legal->autumn, security->autumn (+IMAP), triage->
  `https://ivy.grove.place/api/webhook/incoming` (old Ivy, never set up, abandoned). Correction to
  an earlier statement: Forward Email DID store ~2.5 MB of IMAP mail (operator: nothing useful).
- **Lattice code search:** nothing live receives mail by Worker/webhook; only the old Ivy in
  `_junkdrawer` did. Lattice only sends (Resend) from many `@grove.place` addresses. The old
  `ivy.grove.place` Worker is still deployed (HTTP 200); harmless, removable later.
- **Decisions:** users autumn, hello, dmca, security, dev; routing alerts/github/legal -> autumn,
  feedback -> hello; catch-all -> hello (variant that excludes real users); `triage@` not recreated;
  "Allow Account Reset" unchecked (Purelymail login is `autumnsgrove@purelymail.com`, off-domain).
- **Server facts learned:** Purelymail greylists unknown senders (a bare SMTP RCPT probe got
  `451 4.7.1 greylist`), so a first message from a new sender may arrive minutes late. IMAP 993 and
  SMTP 465/587 reachable. MX priority is 0 (any value works as the only MX).
- **Result:** `autumn@grove.place` added to Apple Mail only (deliberately; hello/dmca/security stay
  webmail-only until Ivy). Inbound from pm.me and outbound to pm.me both confirmed working.
- **Not yet confirmed:** spf/dkim/dmarc=pass in headers; a Grove-sent (Resend) email passing DMARC
  under `p=reject`; alias and catch-all routing; send-as from an alias.

Next session candidates: the confirmations above; a `.gitignore` (with `.env`) and `dev@`
credentials in a local `.env`; the live IMAP spike on `dev@` (PERMANENTFLAGS, ANNOTATION, folder
roles, Sent behavior); the Ivy-specific Jev spike (needs the operator's OpenRouter key; ask first);
the operator's review of the plan docs and the unconfirmed API-design default.

## Round 16 — design direction (2026-10-01)

Operator wants design ironed out before more backend work. Looked at Lattice's Prism tokens
(`libs/prism`, `docs/design-system/COLORS.md`): Grove green `#16a34a` (grove-600), cream/bark
neutrals, Lexend as the house font, glass tokens, Lucide via `@lucide/svelte`. Grove's dark mode is
warm bark-brown and Midnight Bloom is violet; neither is a night sky, so Ivy gets its own variant.
Fresh design, vendoring only what helps.

**Concept (operator's words, paraphrased):** a nighttime walk through a botanical garden. You feel
the plant life around you and see the stars above. Calm over clever. SVG background, glassy
icons, Lucide for now (Phosphor or other later if needed).

| Question | Answer |
|---|---|
| Night base | **Deep green-black** (`#060b10` sky fading to `#0c1f18`), starlight cream text. |
| Light mode | **Yes: a "daytime garden" counterpart**, following the system setting. Doubles design and baseline work (noted). |
| Glass vs scene | **Atmosphere around, calm reading surface:** garden shows through frosted chrome (lists, nav); the message body sits on a near-opaque panel. |
| Motion | **Gently alive:** firefly drift, twinkle, maybe a shooting star on inbox zero. Must honour `prefers-reduced-motion` and pause when hidden. Watch battery and the potato-class phone budget. |
| Phone nav | Undecided. Operator likes both the **bottom tab bar + account chips** and the **drawer**; both mocked (A and E) to compare. |
| List density | **Airy 3-line cards** (sender, subject, one-line preview; account dot, unread glow, tags). |
| Type | **Lexend for UI + Newsreader (serif) for reading.** Self-hosted, embedded. |
| Accent | Undecided: mocked **luminous green**, **firefly amber** and **moonlight lilac** (A, B, C). Leaning green by default. |

Mockups live in `docs/design/canvas/` (Design canvas source: `project/*.dc.html` plus
`project/assets/*.svg`), published as the "Ivy Design Mockups" artifact. Boards: A night/green/tabs,
B amber, C lilac, D daytime, E drawer nav, F reading a message, G tokens and type, H desktop
three-pane. Accessibility notes: accent text colours are chosen to pass on both themes; per-account
badge colours must also differ in lightness, not hue alone (to revisit).

### Round 16b — operator feedback on the first mockups (2026-10-01)

Operator reviewed the canvas. Decisions:

- **Background:** the glasshouse/foliage scene is out (its lit panes and hard ground band read as a
  strange box). Use **Grove's vine tile** from Lattice (`libs/engine/.../nature/VineBackground.svelte`,
  450x450, repeated endlessly, used behind every Grove page) over the night sky. Vendored as
  `assets/vines.svg` (AGPL-3.0 both sides); stacked 3x at night, 2x by day instead of a second
  artwork. A sparse starfield fades out in the top ~380px.
- **Nav:** **option A (bottom tab bar)** wins; the drawer variant is dropped. A **side panel is kept
  for account switching** (plus folders and tags), opened from an account button in the header, so
  the account-chip row is gone.
- **Accent:** **moonlight lilac** (`#c4b5fd` night, `#6d28d9` day). Reason: the chrome already has a
  lot of green, and lilac contrasts. Amber/green dropped.
- **Chunkiness:** operator likes it (it keeps focus on content), but chrome was slimmed anyway
  (card radius 18, tighter padding, 58px tab bar, no chip row). Reassess once they see it.
- **Light mode:** was "mostly ok", but the background looked dark and contrast was off. Rebuilt on a
  cream-meadow gradient with a bark text ramp (`#2a2014` / `#54442f`), glass 72% white, vines on top.
  The background colour is now set per theme in CSS so it can't fall back to the dark base.
- **Smart chip:** keep the chip at the top of the email but **no "Jev"/AI label, no AI symbol**.
  It is now a shimmering lilac-and-gold chip with a pulsing firefly dot and one italic serif line.
  The "needs you" pill uses the same firefly dot instead of an icon. Principle: the LLM layer should
  feel like a little magic, never a labelled AI feature. (The settings/stats ledger can still say
  where calls went; this is about the reading surface.)
- **New: Reading feed** (board E): the newsletter mode, with Today / This week / Saved, a one-line
  digest chip, serif titles with read time, Read / Save, and one-click Unsubscribe.

Open for the next round: compose, search/ask, settings, empty states, the first-run `init` look,
and whether the feed gets its own accent or the digest chip moves to the Inbox too.

Earlier open list (partly answered above): pick accent, pick nav, review the garden SVG art style, decide how the
"needs you" and Jev read-out are labelled (currently "Ivy's read · Jev"), compose and settings
screens, empty/inbox-zero scene, and whether the day theme needs its own accent.

## Round 17 — spam (2026-10-01)

Operator asked whether spam filtering should run through Jev on inbound, then asked me to research
Purelymail first. **Findings** (docs only, not hands-on): Purelymail runs SpamAssassin (threshold 5,
not adjustable), files flagged mail in Junk, rejects only blocklisted IPs at SMTP, greylists
suspicious senders, adds `X-Spam-Status`, and learns per user from mail moved into/out of Junk
after ~200 examples of each. Full notes in `JEV.md` ("Spam: what the provider already does").

**Proposed (operator has not yet picked inbox vs Junk vs both):** provider first, local signals,
then Jev as a second layer. Start with **`junk_rescue`** on the Junk folder (a "looks real" chip and
one-tap Not junk), optionally `is_spam` on Inbox mail later (tag + soft fold only). Mark spam / Not
junk are IMAP moves so they train the provider. Never auto-delete or auto-move. Docs updated:
`PLAN.md` (Spam paragraph), `JEV.md` (catalog + research), `ARCHITECTURE.md` (sync step 9).

Also this round: operator asked for mockups of **Talk to Ivy**, **compose**, **settings**,
**empty / inbox zero**, and the **first-run** look (see round 17b below).

### Round 17b — more mockups (2026-10-01)

New boards on the canvas (`docs/design/canvas/`): **F Talk to Ivy** (ask-your-mailbox: an "Ivy can
look in" account row with LLM-off accounts shown locked, a user bubble, a serif answer with numbered
citation chips and the source emails listed under it, suggestion chips, input bar; the only marker
is a small firefly dot, no AI badge), **G Compose** (reply as the address it was sent to, Reply-To
note for contact-form mail, serif editor sheet, formatting bar, "Sends after 10 s, undo any time",
"Saved to Drafts"), **H Inbox zero** ("All caught up", a moon, a slow shooting star and more
fireflies, a pointer to new Reading issues), **I First run welcome**, **J First run connect an
account** (provider auto-detect, app password, Smart features off by default, one-time full-mailbox
read), **K Settings** (accounts with rename/icon/photo, per-account Smart features, theme / motion /
accent, undo-send, remote images, digest time, Junk rescue and spam score toggles, spend and calls,
backup, mirror health, update button). Placeholders ("[version]") mark values we don't have yet.

Not decided yet: whether Talk to Ivy is its own screen (as drawn) or reachable from the search tab;
the first-run flow beyond two screens (progress while the mailbox is read, the `ivy init` terminal
side); settings information architecture on desktop.

## Round 18 — decisions after the second mockup pass (2026-10-01)

Operator feedback: the overall design "looks really good"; the settings panel is liked (it leaves a
lot of room to extend). Decisions:

- **Spam: Junk rescue only.** Jev does not screen the Inbox. (`is_spam` stays an idea.)
- **Talk to Ivy lives inside the Search page**, behind a Search / Ask Ivy switch at the top, not as
  its own tab. It is an **agent loop with tools: `search_mail`, `read_mail`, and `think`** (all
  read-only). Documented in `ARCHITECTURE.md` section 6, `PLAN.md` and `TESTING.md` section 4
  (read-only tool registry test, locked/withheld mail invisible to tools, step/token caps,
  citations only to mail the loop read). The mockup shows a short quiet trace of what it did
  (searched, read, a thinking line) with no AI badge.
- **Reply note simplified:** drop the "goes to Mara's address, not the form" explanation. Compose just
  says "Replying to name@domain.com". The Reply-To handling stays underneath.
- **Images and attachments on outgoing mail are required.** Compose gets an attach button, inline
  image button and attachment thumbnails/rows, a bottom sheet (Photos, Camera, Files, recent
  photos, photo size, location removed), and the message view shows attachments (image thumbnails,
  file rows with download). Rules recorded in `ARCHITECTURE.md` section 5 (size limit from the
  provider, type rules, EXIF stripped by default, optional downscale, drafts keep attachments).
- **New boards:** Search (switch, field, quick filters, a doorway to Ask Ivy, highlighted keyword
  hits, a "similar in meaning" hit) and Tags (your tags with counts, "Placed for you" local tags
  with the firefly dot, Rules and People links). Settings gained photo size and location-removal rows.

**Follow-up (same day):** the gold-and-lilac "Ask Ivy" doorway card in Search was disliked as
strange; removed. The Ask Ivy switch segment now uses an icon (Lucide message-circle) plus text, like
Search, and the in-results link is a plain quiet row. **"Recent photos" is not possible from a web
page:** Safari/PWAs cannot list the photo library, only open the native picker and receive what is
chosen (sources: web search on iOS Safari file inputs; HEIC conversion in Safari 17+ is a known
quirk). The attach sheet now shows Photos / Camera / Files (native pickers) and **"From your mail"**
(attachments already in the mirror, attached server-side).

Open: provider's real max message size (check SMTP `SIZE` live); whether tag colours are free-choice
or from the palette; how the agent trace behaves while it is still working (streaming state not
mocked yet); People and Rules screens.

## Round 19 — tags, people, rules (2026-10-01)

Operator confirmed the tag-colour, tag-creation, People and Rules mockups (offered after the
attachments discussion). New boards: **O New tag** (sheet: name, 12-colour grid, custom colour,
"tag matching mail automatically"), **P Tag colours** (edit screen with a Night and Day preview and a
named palette), **Q People**, **R A person**, **S Rules**, **T New rule**. Decisions recorded in
`PLAN.md` ("Tags and rules"): palette entries carry a night and a day shade; rules read as plain
sentences; a rule may use an "is about…" phrase answered by Jev with a "would have matched N of the
last 200" preview; **rule actions are local only (tag, show in Reading, snooze)**, consistent with the
safety line (moving, deleting, sending always ask).

Mockup content (names, counts such as "Matched 12 times") is placeholder. Open: how a rule that
uses an "is about" phrase behaves on LLM-off accounts (proposal: those conditions are unavailable and
shown locked, like the Ask Ivy account picker); People merging when one person has several
addresses; desktop versions of Tags, People and Rules.

## Round 20 — smart checks, rule authoring and failure states (2026-10-01)

Operator asked for **smart rules**: a way to add classification options for Jev to check against.
Motivating case: not everything from Cloudflare is a receipt (Dev Day announcements etc.), so a
header rule is too blunt; the rule needs "from Cloudflare AND looks like a receipt", which adds a
classifier step. Operator then proposed the authoring model: **free-form text** ("I want emails from
Cloudflare that look like receipts tagged as such") compiled by an agent **in one generation** into
our exact Jev-ready format, so the user just types and the rule is properly made in the background,
without wasting generations. Rules stay local-only (confirmed).

Decisions: documented in `JEV.md` 3F (compiler contract: no email content as input, reuse existing
checks and tags, schema + vocabulary validation, local-only actions, one optional clarifying
question as the only extra call, dry run on the last 200 messages, edit text = new hash), `PLAN.md`
(Rules and Smart checks), `ARCHITECTURE.md` (rule compiler in the LLM layer) and `TESTING.md`
(compiler fixture and failure-state fault injection). New mockups: **Rules · Describe it**,
**Rules · Check it**, **Smart checks** (list), **Smart check detail**; Rule details now uses a check
chip instead of a free-text "is about" box.

**Edge states requested ("handle all of that"):** mocked **Can't reach Ivy**, **Sync error** banner
(account can't sign in) and the account-switcher dot, **Mirror health** (per-account status,
backfill progress, Update password / Try again / View log), **Message failed to load** (plus a
failed attachment), **Send failed** (too large; draft kept; one-tap fix), **Toasts** (sending with
undo, sent, archived with undo, will retry, not sent, back online), **Ask Ivy limits and errors**
(monthly cap reached, provider not responding), **No results**. Error taxonomy and data model in
`ARCHITECTURE.md` 9b.

Open: offline reading in the PWA (v1 says no; do we want a read-only cache of recent mail?);
what a rule does when its check's account is LLM-off (proposal: the rule is paused and shown
locked); whether the compiler runs on the same model as `complete()` or a cheaper one; how far the
backfill option for a new check should go by default (proposal: new mail only, with an explicit
"also apply to existing mail" showing a cost estimate).

Notes on round 5: the second-stage model sees raw email text, so it's the higher-risk stage: no tools, structured output (`needs_me` bool + short reason), reason rendered as plain text. Configurability needs a deliberate home (config file vs in-app settings with per-account overrides), asked in round 6.

## Round 21 — standards, stack, test philosophy, performance (2026-10-01)

Operator asked for a full-picture review and a standards baseline before implementation. Stated
requirements: **strict TDD** (write tests first, watch them fail, implement, watch them pass);
**mostly integration tests** over unit tests, **a few end-to-end tests from the very start**, using
**full mocks**, including a proper mock of the whole email system, **drivable from a command line**
(a CLI, rarely used since the real target is a remote deployment); **speed tests** to optimise
quality; **compression from day one** (Polaris got slow on large data-heavy responses); **modern
standards and pure-Go libraries, no C**; access from Safari on iPhone and iPad (sometimes Firefox)
over Tailscale; **no auth model yet, frictionless**; **rewrite `CLAUDE.md`** as general code
guidelines.

Written: `STANDARDS.md` (TDD workflow, test shape, mail world + `ivy-dev` CLI, Go/frontend/API
standards, access/exposure), `STACK.md` (libraries, pure-Go rule, open verifications),
`PERFORMANCE.md` (compression plan, budgets, measurement loop); rewrote `CLAUDE.md`; added TDD and
integration-first principles to `TESTING.md`. Target hardware confirmed from the vendor page: Le
Potato AML-S905X-CC, quad Cortex-A53 1.5 GHz, 2 GB DDR3.

Interpretation notes (confirm): "implementation tests" read as **integration** tests; "drive
cycles" read as **repeatable speed/benchmark cycles**.

Open questions asked this round are recorded with their answers below once given.

**Answers (round 21):** API contract = **OpenAPI spec first**, codegen for Go and TS types, CI drift
check. SQL layer = **sqlc** (build-time, committed output). Transport = **HTTPS via
`tailscale serve`**. Speed tests = **both**: product benchmarks with budgets and a budget on our own
test/build loop (`PERFORMANCE.md` 3b). This closes `PLAN.md` section 7 item 1 (API design confirmed).

**Answers (round 21, second batch):** **Milestone 0 (harness first)** added to `PLAN.md` section 5.
Fake mail world is the default, the same scenarios run live against `dev@` for quirks. Potato
benchmarks are scripted over `ssh potato-remote` (`make potato-bench`), manually triggered.
Playwright: WebKit + Chromium every run, Firefox nightly, plus a manual real-device pass per
milestone (`TESTING.md` 7b).

Still open for a later round: lore names; repo visibility; whether to run the Jev/Purelymail spikes
before or after Milestone 0; offline PWA reading; a `.gitignore` before any secret exists.

## Round 22 — local dev stack (2026-10-01)

Operator: we need a way to start a local dev stack so we can iterate without being connected to a
real remote mailbox, with pre-filled data via a seeded database. Written as `DEV.md` (one command,
`make dev`; mailworld + Ivy + Vite HMR; deterministic seed profiles empty/minimal/demo/large;
`full` mode syncs over real IMAP, `fast` mode seeds SQLite through the real store code; snapshots
for instant reset; named states for every edge-case screen; safety rails that refuse real hosts and
real `.env`). Added to Milestone 0. Added a `.gitignore` (`.env`, `.dev/`, SQLite files) before any
secret exists. Interpretation: "iterate on our own documents" read as iterating on the app/UI
against realistic mail; say so if it meant something else.

## Round 23 — spikes first, live LLM in dev, two-account pair, review (2026-10-01, operator on mobile)

Decisions: **spikes run before Milestone 0** and before any build implementation (`SPIKES.md`,
S1-S10). **"Can't reach Ivy" is fine** for v1; the operator will use plain Safari (iPhone/iPad,
sometimes Firefox), not a PWA, maybe later; so `tailscale serve` HTTPS is recommended but optional
(revises the round 21 transport answer). `.gitignore` added (done). **Dev stack:** seeded database
with **live OpenRouter as the default** (`--llm live`, key from `.env`, capped, record/replay cache;
fake only by flag or when no key is found; tests/CI always use the fake). **Demo mode gets a
two-account `--pair`** with local delivery in mailworld so the operator can test sending between
accounts (a standing integration/E2E test from Milestone 4). Work moved onto branch
`docs/standards-baseline` (it had been a detached HEAD) so it can become a PR; no PR opened yet.

Adversarial gap review (done, folded into the docs): mirror-loss protection (soft-delete window and
mass-deletion circuit breaker), disk budget on the potato's card, SQLite/power-loss safety, FTS
tokenizer and time-zone tests, log wear, no public exposure or Funnel and the tailnet-wide access
implication of no-auth, secret file permissions, and how the committed frontend build is produced
without merge churn. Items needing the operator's yes: soft-delete + breaker, disk eviction policy,
committed-build strategy.

## Round 24 — disabled messages, rolling backups, disk, CI-built frontend (2026-10-01)

Operator answers to the round 23 gap review:
- **Server-deleted messages are disabled, not deleted:** a flag hides them as if deleted; the row and
  raw message stay. (Interpreted as: never purged automatically, restorable, re-enabled if the
  message reappears, excluded from views/search/LLM tools, included in backups; explicit "Purge
  forever" is the only way out. The mass-disable case is an alert with one-click Restore, not a sync
  stop, since the action is reversible.)
- **Rolling backups:** twice a day, 30 days total (about 60), prune anything older than 30 days.
  Added safety rules: prune only after a verified new backup, floor of the 10 newest, at least one
  target off the potato recommended (proposal; veto if unwanted). Disabled-message blobs are kept
  de-duplicated in the backup target rather than as 60 copies (proposal).
- **Disk is not a concern** (potato has 256 GB): no storage budget or raw-blob eviction.
- **Frontend artifacts are generated in GitHub Actions**, preferably. Context: rebuilding on the
  potato is not realistic; with Polaris, builds were instant on the dev machine and committed, then
  moved to Docker built on a GitHub runner in about 2.5-3 minutes. Written as: CI builds on merge to
  main and the bot commits `web/build/`; PRs never touch it. Open sub-option (decide after spike
  S3): CI also publishes a cgo-free arm64 binary so the potato builds nothing.

## Round 25 — the potato builds the Go binary (2026-10-01)

Operator: the potato is strong enough to build the Go binary in a reasonable time; no binary should be
built in CI and committed or released (a waste of storage). **Withdrew the round 24 "CI-built arm64
binary" option.** Settled: the potato runs `git pull` and `go build` (the frontend assets still come
from the Actions bot commit); spike S3 only confirms build time and peak RAM.

## Round 26 — CI plan, repo going public (2026-10-01)

Operator approved the proposed CI and said the repository will be made public, so the plan accounts
for it. Written as `CI.md`: Phase A now (secret scan + link check), full Phase B at Milestone 0;
required checks (go, nocgo, drift, web, e2e, guard, deps, codeql), the main-merge frontend bot
commit, nightly Firefox/fuzz/audit jobs, manual `live` and `evals` in protected environments, and a
public-repo security section (no `pull_request_target`, least-privilege permissions, SHA-pinned
actions, never a self-hosted runner such as the potato, no secrets in PR runs). A before-public
checklist: **no LICENSE file exists yet**, full-history secret scan, review docs for personal detail
(role email addresses, `potato-remote` alias, the candid `qa-log.md`), repo settings, and the
mockups. Public repos get free Actions minutes, so the earlier minutes concern is moot. The
workflow files themselves are not written yet.
