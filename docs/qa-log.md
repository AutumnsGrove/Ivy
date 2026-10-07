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
| Accounts | **Multiple, from day 1.** Many addresses on one domain (a personal one plus several role addresses). Needs a per-account view AND a combined "all inboxes" view, like Outlook mobile. |

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
| LLM privacy | **Per-account opt-in**, off by default (the security and abuse addresses can stay local-only). Embeddings stay local either way. |

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

Implications: contact-form mail needs Reply-To-aware replies and threading (reply goes to the visitor, not the form's From); the security and abuse addresses get priority treatment and default to local-only (already the per-account opt-in default); personal correspondence is protected from any automated handling, including the future quiet-auto-archive idea. **Tension found:** ask-your-mailbox over the combined view would put several accounts' mail in one LLM call, contradicting "one account's mail per LLM call" and per-account opt-in. Embeddings/search can span accounts (local); only the LLM answer step is affected. Asked in round 9.

## Round 9 — ask scope, tags/rules, license, interaction (2026-10-01)

| Question | Answer |
|---|---|
| Ask scope across accounts | **A switch mode in the UI:** tap the accounts you want included; non-included accounts are greyed out, so entering the mode is easy. The operator chooses the set per question, which counts as informed consent for mixing those accounts in one ask call. |
| Account customization | **Rename accounts, set their icons, and optionally upload photos**, all in settings (so others can personalize theirs too). |
| Tags and sorting | **Your own tags, automatic rules, model-applied system tags.** (Saved searches as smart views NOT picked.) |
| License | **AGPL-3.0**, like Lattice. |
| Interaction | **Swipe actions, keyboard shortcuts on desktop, snooze.** |

Design consequences: (1) the safety rule is refined to **automated classification stays one account per call; ask-your-mailbox may mix the accounts the operator explicitly selects**, and only accounts with LLM enabled are selectable (the security and abuse addresses default to LLM-off and so appear locked, which is distinct from merely unselected); (2) each account has a display name, icon and optional photo (stored locally, resized server-side); (3) snooze, tags, rules, allow-lists and account display settings are **locally owned state**, so a backup path is mandatory and snooze needs a decision (local hide-until vs a server-side Snoozed folder); (4) AGPL-3.0 means a LICENSE file and headers policy at repo creation.

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
- **Forward Email inventory (screenshots):** a catch-all with no recipients, several aliases
  forwarding to the main user, a few users with small IMAP stores, and one alias pointing at the old
  Ivy's webhook (never set up, abandoned). Correction to an earlier statement: Forward Email DID
  store ~2.5 MB of IMAP mail (operator: nothing useful).
- **Lattice code search:** nothing live receives mail by Worker/webhook; only the old Ivy in
  `_junkdrawer` did. Lattice only sends (Resend) from many `@grove.place` addresses. The old
  `ivy.grove.place` Worker is still deployed (HTTP 200); harmless, removable later.
- **Decisions:** separate users per role address plus a `dev` test user; routing rules send the
  alert/notification aliases to the main user and feedback to the support user; the catch-all goes
  to the support user (variant that excludes real users); the old webhook alias was not recreated;
  "Allow Account Reset" unchecked (the Purelymail login is off-domain).
- **Server facts learned:** Purelymail greylists unknown senders (a bare SMTP RCPT probe got
  `451 4.7.1 greylist`), so a first message from a new sender may arrive minutes late. IMAP 993 and
  SMTP 465/587 reachable. MX priority is 0 (any value works as the only MX).
- **Result:** the main address was added to Apple Mail only (deliberately; the role addresses stay
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

## Round 27 — going public: addresses removed, LICENSE, README, SECURITY (2026-10-01)

Operator asked to remove the role email addresses now (they stay in git history) and for mockups to
use a generic address; and to add a brief LICENSE, README and SECURITY file. Done: addresses
replaced by generic wording in the docs and `CLAUDE.md`, and by `example.com` addresses in the
mockups (`me@`, `hello@`, `support@`, `alerts@`); the Purelymail login identity removed from the
migration notes. `LICENSE` is the standard AGPL-3.0 text (SPDX copy; gnu.org was unreachable from
the sandbox). `SECURITY.md` points to GitHub private vulnerability reporting, which must be switched
on in the repo settings before going public. `dev@` (the throwaway test user) and the
`potato-remote` alias are still mentioned and were left as is.

## Round 28 — plan approved, frontend first (2026-10-02)

Operator: "it's time, let's get to building", with the full mockup set as the guide.
Answers to the kickoff questions:
- **Plan approved; start with the frontend.** SvelteKit shell, tokens and a reusable component
  library against mock data; backend and spikes follow. (This reorders the old "spikes first"
  sequencing for the frontend only; the Go backend is still gated by S1-S3.)
- **API style approved:** JSON REST + SSE with one typed client module. Until `api/openapi.yaml`
  exists, `web/src/lib/api/client.ts` is that module, backed by mock data, and `types.ts` stands in
  for the generated types.
- **First slice:** the whole shell with every screen stubbed (all 32 screens and states from
  `design/canvas/`), then wired up.
- **Workflow:** all tooling allowed; **everything goes directly on main** (overrides the "work on a
  branch" rule in `CLAUDE.md` for this phase), committed in stages.

Built (all tests-first where behaviour exists): token file with night/day themes, accents and a
`data-force` scope for side-by-side previews; ~40 components; phone shell (tab bar, drawer, FAB) and
desktop shell (rail + three panes) sharing the same list and reader components; mock API with
`?scenario=` edge states; Vitest (token guard, stores, citations, components) and Playwright
(WebKit iPhone + Chromium desktop) suites. E2E caught one real bug (failed attachments invisible in
the wide reader). SvelteKit 3 differences recorded in `CLAUDE.md`.

Findings to remember: a stray `/Users/autumn/node_modules` shadows Kit's `cookie@2` at build time
unless `cookie` is a direct devDependency (fixed); `error()` now takes `(status, message, props)`;
`goto` options `keepFocus`/`noScroll` became `reset: false`.

**App icon (2026-10-02):** a lilac ivy leaf with fireflies, generated by the operator from the
proposed prompt. Cut out to a transparent squircle, plus an opaque full-bleed version for iOS;
favicon, touch icon, manifest and in-app logo derived from it (`docs/design/brand/README.md`). It
replaces the Lucide sprout in the rail and welcome screen and SvelteKit's default favicon.

## Round 29 — CI-built container image replaces building on the potato (2026-10-02)

Spikes S2 and S3 ran (`docs/spikes/`). S2: `imapmemserver` covers APPEND, flags and keywords, MOVE,
UID EXPUNGE and IDLE but has no CONDSTORE/QRESYNC, so `mailworld` adds those only if S1 shows the
real server offers them. S3: FTS5 works, but a cold `go build` of `modernc.org/sqlite` on the potato
takes 154 to 190 s, peaks near 730 MiB and exhausts swap beside the live services (a GC-limited
build was 249 s and 477 MiB). That contradicts the round 25 assumption that the potato can build
the binary comfortably.

Operator, shown how the sibling project Polaris ships (multi-arch image to GHCR from Actions, a
host-side update watcher on the target): **do that.** Settled: a multi-stage Dockerfile builds the
frontend and cross-compiles the pure-Go binary in CI, pinned to `$BUILDPLATFORM` (no QEMU for those
stages); publish to GHCR on every push to main (`:latest` and short SHA); `ivy update` resolves the
digest and signals the host watcher, which pulls, health-checks and rolls back. The target compiles
nothing. **Supersedes** round 24 (bot-committed `web/build/`, no ruleset bypass or loop guard any
more, spike S9 moot) and round 25 (the potato builds the Go binary). Docs updated: `ARCHITECTURE.md`
section 9, `CI.md`, `PLAN.md`, `STACK.md`, `TESTING.md`, `SPIKES.md`, `CLAUDE.md`.

Also: spike code now lives under `spikes/<name>/` on main (its own Go module), not on `spike/*`
branches. `.env` (git-ignored) holds the dev mailbox, board alias and OpenRouter key for spikes and
`live` tests.

## Round 30 — spikes S1 to S10 done; embeddings, tags, backups and the cost ledger (2026-10-02)

All the spikes that need no other input have run (`docs/spikes/`). The operator's decisions on
what they showed:

| Topic | Decision |
|---|---|
| S5 | Accepted as done. It ran on an iPad over HTTPS only; the iPhone, plain HTTP and the `image/heic` upload box were not run. |
| Real-mail accuracy for Jev (S4) | Deferred. There is no real mail corpus yet, so this waits until the operator has one to label. |
| Embeddings | **OpenRouter by default** (it is much faster than the potato: about 21 chunks/s against 0.06). Local Ollama stays as an optional provider. **Replaces "embeddings always local".** Default model proposed as `baai/bge-m3` (1024 dimensions, 8k context; a 512-token model fails whole batches). |
| Embed once | A message is embedded once, keyed by a stable content key (SHA-256 of the lower-cased `Message-ID`, or of the header block when absent), so moving to a folder, archive, trash, flag changes and UIDVALIDITY resets never re-embed. Re-embedding only on changed content or a chosen model change, with a cost estimate. |
| Cost tracking | **Every remote API call is tracked per occurrence**: one ledger row per call, and per message for batched calls such as embeddings, with the exact cost from the response. All remote calls go through the one gate; nothing paid is reachable otherwise. |
| Tags | **Both**: locally owned and backed up, and also written as IMAP keywords `$ivy-<slug>`, synced both ways. |
| Backups | **Once a day, 15 days kept** (was twice a day for 30 days), of **`state.db` only**. |
| Databases | **Two files**: `state.db` (local state, backed up) and `mirror.db` (a full mirror, never backed up, rebuilt from IMAP). Asked whether that is feasible: yes (mirror 1.2 to 5.6 GiB at 100k messages; state about 27 MiB at 100k). |

Consequence worth remembering: local state and derived data must refer to mail by the content
key, never by mirror row ids, or a mirror rebuild would orphan them. Docs updated:
`ARCHITECTURE.md` sections 3, 6 and 9, `PLAN.md`, `STACK.md`, `TESTING.md`, `JEV.md`, `SPIKES.md`,
`CLAUDE.md`. Also: the operator asked that the board carry no test artifacts; an audit after the
spikes found none (only the pre-existing `nomic-embed-text` model and unrelated services).

**Embedding model (operator, later the same day):** `perplexity/pplx-embed-v1-0.6b`, replacing the
proposed `baai/bge-m3`: cheaper and a 32k-token context. Tested before adopting (repo prose only):
1024 dimensions; **returns native int8 values** (every component a multiple of 1/128) that are
**not normalised** (L2 norm about 2.8), so vectors are stored as returned with their norm beside
them; 50.7 chunks/s and $0.15 per 100k email-sized chunks (against 18.3/s and $0.46 for `bge-m3`);
repeated text gives an identical vector; 6 of 6 sanity queries had their answer in the top 3, the
same as `bge-m3` (a small test, not a quality ranking); a 28,001-token input was accepted (the
behaviour past 32k was not tested). The synthetic S10 databases (about 600 MB, `.dev/s10`) were
deleted at the operator's request.

## Round 31 — pin go-imap/v2 to a fork for server-side CONDSTORE/QRESYNC (2026-10-02)

Chunk 1b needed mailworld to serve CONDSTORE/QRESYNC so Ivy's sync path can be tested offline.
Reading `go-imap/v2@v2.0.0-beta.8` showed the gap is structural: `imapserver` parses the wire
protocol itself and has no CONDSTORE/QRESYNC cases, and its parser is built on the unexported
`internal/imapwire`, so the module cannot be extended from outside. The client side is better:
it has CONDSTORE (`MODSEQ`, `CHANGEDSINCE`, `HIGHESTMODSEQ`, `UNCHANGEDSINCE`) but no QRESYNC.

Operator first approved vendoring a fork into the repo, then asked why we were copying code in and
whether a third-party module could not be used. Findings: upstream has 40 open PRs and 79 open
issues; **PR #756** (`parisxmas`, May 2026, one commit, +574/−14, mergeable) already implements
server-side CONDSTORE + QRESYNC plus the client pieces and cherry-picks cleanly onto beta.8, while
a competing PR #690 has sat since Jun 2025 and now conflicts. Upstream review is not a reliable
timeline.

**Settled (operator):** do not vendor and do not block on upstream. Fork `emersion/go-imap` to
`github.com/AutumnsGrove/go-imap`, branch `ivy-beta8-condstore`, tag `v2.0.0-beta.8-ivy.N`, and pin
Ivy with a `replace` in `go.mod`. The fork is beta.8 + a cherry-pick of #756 + a modseq backend for
`imapmemserver` (mailworld's `Account.Deliver` mutates the memory store directly, so the backend is
where modseq must live). Retire the fork when #756 lands upstream. Nothing is vendored into this
repo. Recorded in `docs/STACK.md`, `next_steps.md`.

## Spike code removed from the tree (2026-10-02, operator)

All nine spikes had run and their findings are written up in `docs/spikes/`. The spike code (nine
separate Go modules under `spikes/`, 264 KB) is **removed from `main`**; git history is the archive
(`git show a049bfb:spikes/<name>/`, the last commit that has it). Reasons: CodeQL's Go autobuild
walks every `go.mod` and compiled all of them; their PDF and image libraries (used nowhere else)
showed up in the dependency graph and Dependabot alerts once the repo became public; and
`STANDARDS.md` section 1 already says spikes do not live on `main`. Follow-ups that still want the
code (S4 on a real corpus, S10 on a real mailbox, S5 on an iPhone) restore it from that commit or
start a fresh `spike/` branch.

## Audit of chunks 1-2c and the resulting ground rules (2026-10-02, operator)

Before 2d, an independent commit-by-commit audit of everything from `aa0e438` (Go module and store)
through `a049bfb` (2c complete) ran and was merged to `main`. Every defect was reproduced with a
failing test before its fix, in its own commit; the full log is `papercuts.md` (41 numbered findings
plus open N-items) and the method is `.claude/skills/review-deepseek/SKILL.md`. The recurring
pattern - the happy path was well covered while the second attempt, a stalled peer, huge or hostile
input and silent failures were not - became a rule.

**Settled (operator) on top of the fixes:**

- **Single-writer SQLite.** Each database is now a `store.DB{Read, Write}`: a `query_only` read pool
  and exactly one write connection that begins transactions `IMMEDIATE`, so writers queue in Go and
  a read-then-write transaction can never fail with `SQLITE_BUSY`. Replaces the unbounded pooled
  handle that failed 7 of 8 concurrent workers.
- **Raw mail is tiered by size; big messages never sit in memory.** Up to 2 MiB in `raw_blob`; 2-64
  MiB streamed to `spool/<folder id>/<uid>.eml` and parsed by `mime.ParseStream`; above 64 MiB only
  the envelope, with `body_status = too_large` and an "open in webmail" state. Attachments are
  served by `mime.CopyPart` decoding from the file. A disabled message keeps its spool file exactly
  as it keeps its row (nothing is ever erased).
- **Failure paths are first-class.** `STANDARDS.md` section 4a and `CLAUDE.md` non-negotiable 8:
  every input has a documented maximum with a defined outcome above it, nothing blocks without a
  deadline, no failure is silent, and the failure tests are written with the success test. The
  limits table in 4a is the source of truth.

These were folded into `CLAUDE.md`, `docs/STANDARDS.md` (4a), `docs/ARCHITECTURE.md` (section 3),
`docs/CI.md`, `docs/TESTING.md`, `docs/PERFORMANCE.md` (section 1), `docs/STACK.md` and
`next_steps.md`. The spike code was removed in the same pass (entry above).

## N9 resolved: trusting Authentication-Results (2026-10-02)

Spike S1 showed Purelymail adds **no** SPF/DKIM/DMARC verdicts (its inbound header is only
`mail.purelymail.com; auth=pass`), so the parser's "first header carrying each method" rule had no
real header to prefer: a sender could add
`Authentication-Results: bad.example; spf=pass; dkim=pass; dmarc=pass` and switch off the
spoofed-sender discount. RFC 8601 sections 2.5 and 4.1 are explicit that a consumer must not
interpret this header until the operator configures which `authserv-id` is trustworthy.

**Settled (operator):**

- `mime.Parse`/`ParseStream` believe only the **topmost** header whose `authserv-id` is in the
  account's `trusted_authserv_ids`; every other header is ignored, and a later header cannot fill a
  method the trusted one omitted (that would be the same forgery by another route). The trusted id
  is stored with the verdicts; the raw headers are kept for debugging.
- The default is an **empty list, so nothing is trusted**. This is stricter than the audit's first
  suggestion (default to the IMAP host's registrable domain), which would have trusted a forged
  header spelling `purelymail.com`.
- Because Purelymail adds no verdicts, the auth signal on Purelymail is now honestly empty and the
  spoofed-sender discount does not fire. Verifying DKIM (and, from `Received`, DMARC alignment)
  ourselves is the later feature that would make it usable; it is recorded as such in
  `docs/ARCHITECTURE.md` section 5 and `next_steps.md`.

Recorded in `docs/ARCHITECTURE.md` section 5, `papercuts.md` (N9 resolved) and `next_steps.md`.

## Build order changed: send before triage (2026-10-03, operator)

Asked how long until Ivy is usable, the answer was that the sync and send milestones, not triage,
decide when it can replace Apple Mail. **Settled (operator):** the order is now
**read -> sync -> send -> triage**. This supersedes the early "Build order" line ("Read -> sync ->
triage -> send") near the top of this log.

- Send is **Milestone 3 / chunk 4**; triage is **Milestone 4 / chunk 5**. Sync stays Milestone 2 /
  chunk 3, and send still depends on it (outbox, Sent `APPEND`).
- Send needs no LLM gate, so nothing in it waits for triage. The gate, the cost ledger and the
  stats panel move with triage.
- Renumbered references in `docs/PLAN.md` (milestones and risk table), `docs/JEV.md`,
  `docs/DEV.md`, `next_steps.md` and `papercuts.md`. No Go or web code names these chunks.

## Account customization: where the fields live and how writes are guarded (2026-10-03, during 2g)

Chunk 2g made account rename, icon and photo real. Three decisions were taken without a new operator
round, because the schema (2a) and the standards already pointed the way; they are recorded here so
they are not re-litigated.

- **The fields stay on the mirror `accounts` row.** Migration 2 added `display_name`, `icon` and
  `photo_blob` "for account customization", so the store keeps them there and gained targeted
  setters (`SetAccountProfile`, `SetAccountPhoto`) instead of routing them through `UpsertAccount`.
  This is the one local-ownership exception to "owned state lives in `state.db`": accounts are
  config-derived rather than mail-derived, so the content-key rule does not apply, and sync's
  `ensureAccount` returns early and never clobbers the row. The cost, recorded in `next_steps.md`,
  is that a full mirror rebuild would lose the customization; if that path grows a hard rebuild,
  the fields move to `state.db`.
- **A list never loads the photo bytes.** `store.Account` now reports `HasPhoto` (a
  `photo_blob IS NOT NULL AND length(...) > 0` expression) and `GetAccountPhoto` is the only read
  that touches the blob, so `/accounts` cannot inherit a multi-megabyte response from three
  accounts with photos.
- **An upload is untrusted input.** The photo is bounded at 5 MiB, sniffed with
  `http.DetectContentType`, and kept only when the sniff is JPEG, PNG, GIF or WebP. SVG is refused
  even though it is an image, because served same-origin it can carry script. The two limits are in
  `STANDARDS.md` 4a.
- **`Origin` is checked on every mutation.** These are the first mutating endpoints, and with no
  auth a cross-site page open in the operator's browser could otherwise rename an account. A
  non-GET API request whose `Origin` host differs from the request `Host` is answered `403
  forbidden`; an absent `Origin` (a non-browser client, which already had network access) is
  allowed, and reads stay open. `forbidden` joins the stable error codes.

Recorded in `docs/ARCHITECTURE.md` section 3, `docs/STANDARDS.md` 4a, `web/src/lib/api/errors.ts`
and `next_steps.md`.

## Round 32 — cleanup phase: account data home, doc split, review first (2026-10-03, operator)

The operator brought a second model in after 2g to audit and tidy. Baseline first: `make check`,
Vitest (132), svelte-check and Playwright (150 passed, 8 viewport-conditional skips) were all green.
Three decisions, all taken from the recommended options:

- **Account name, icon and photo move to `state.db`.** This supersedes the 2g note that kept them on
  the mirror `accounts` row: a mirror rebuild would erase them and the mirror is never backed up,
  which contradicts non-negotiable 5. Needs a state migration, a one-time copy from the mirror
  columns, and the gateway reading them from `State`. The mirror columns become unused.
- **`next_steps.md` is split three ways.** `next_steps.md` keeps only the Now pointer, one
  deduplicated backlog and operator actions. The finished 1a-2g narratives move verbatim to
  `docs/BUILD-LOG.md`. The audit ground rules fold into `STANDARDS.md` and `CLAUDE.md`.
- **Review before cleanup.** The unreviewed range `6f29d15..HEAD` (2d through account
  customization) is audited with the review-deepseek process first, fixes test-first and logged in
  `papercuts.md`, so the cleaned docs include the findings.

### Round 32b — review findings that needed a decision (2026-10-03, operator)

The review of `6f29d15..HEAD` fixed four defects (#45-#48 in `papercuts.md`) and left six open. Four
needed an operator decision; all took the recommended option. They are implemented as backlog items
(`next_steps.md`), each test first.

- **N15, timestamps:** the API sends RFC 3339 timestamps and the browser formats "Yesterday"/"Mon"
  in the viewer's own zone and locale. The `MailSummary`/`MailMessage` `time` strings become
  timestamps, a contract change. No timezone setting for now.
- **N12, thread identity:** a thread's id is sticky and oldest-wins. When a rebuilt thread contains
  messages that already carry a `thread_id`, it keeps the oldest existing one; a new id is minted
  only for a thread with none; a merge collapses to the oldest. Features may therefore key on
  `thread_id`.
- **N10/N14, derived data:** a `derived_version` column, set last in the same transaction as the
  derived data (sanitised HTML, attachment rows), plus a bounded sync/boot pass that re-derives rows
  behind the current version from the raw message. The sanitizer, parser and attachment walk each
  bump it when their output changes.
- **N11, allowed hosts:** a `allowed_hosts` config key; every API request whose `Host` is not listed
  is rejected. The default is loopback plus the configured listen address, and `ivy init` prompts
  for the Tailscale name. This closes DNS rebinding for reads and writes.

## Round 33 — finishing 2g: settings and stats design (2026-10-03, operator)

Settings (`/settings`, canvas board K) exists with real theme/motion/accent; "Spend and calls" is a
dead row and no stats design exists. Four decisions, three of them the recommended option:

- **Stats panel: the full panel, mock-backed.** Period totals (today, 7 days, 30 days, all time)
  broken down by feature, account and model; month spend against the caps and how many calls the
  gates blocked; and a filterable per-call log screen (`PLAN.md` "Stats panel"). The data sits
  behind the API client as a mock until the ledger exists in chunk 5. It needs a new canvas board
  first (no mockup exists).
- **Desktop settings: the phone list in a centred column** inside the content pane; sub-screens
  (account, health, spend) replace it. No two-pane layout.
- **Decorative rows become real controls, through a mock settings API** (the operator chose this
  over local-only prefs): undo-send delay, remote-images policy, digest time, photo size, strip
  location, reply-as, junk rescue and spam score go through the typed client against a mock
  `/settings`, so adopting `state.db` settings later is a swap behind `client.ts`. Theme, motion and
  accent stay in `localStorage` (they are per device).
- **Account photos are downscaled in the browser** to a small square JPEG before upload (Safari
  decodes HEIC), which settles the 5 MiB/HEIC backlog item; the server limit stays as a backstop.

## Round 34 — starting 2h: what `fast` and `full` populate (2026-10-03, operator)

Reading the code before 2h found that `--mode` was parsed and stored in the snapshot recipe but read
nowhere, that neither `up` path ever ran the sync (so `make dev` served empty databases), and that
`state.db` has no rules, snoozes, ledger or Jev tables yet. Two decisions, both the recommended option:

- **`state.db` seed is only what exists.** The fast seeder writes tags, message tags, settings and
  account profiles. Rules, snoozes, the ledger and Jev rows are added by the chunk that creates each
  table, in the same stage, so the seeder never gets ahead of the schema.
- **`full` is a one-shot sync at startup.** Before serving, `ivy-dev` runs `sync.Fetcher` once per
  account against mailworld, in both the watched and in-process `up` paths (the watched child only
  opens the databases `ivy-dev` filled). `fast` writes the same rows without IMAP by feeding each
  seeded delivery through the same store code the sync uses, so the `full == fast` test compares
  two runs of one code path rather than two implementations.

## Round 35 — `fast` was not fast: the snapshot cache (2026-10-04, operator)

The `fast` seeder and the `full == fast` test are done, and measuring them showed the seeding is not
what the `DEV.md` speed claims need: `demo` took 246 ms full vs 272 ms fast, and `large` (100k) took
3m17s full while fast did not finish in 280 s. Both pay for parsing plus two SQLite transactions per
message. Decision (the recommended option): **cache the built database files** under `.dev/cache/`,
keyed by profile, seed, accounts, both schema versions and `DerivedVersion`, and restore them by file
copy, as `DEV.md` 3 already says. The cache is only written from a data directory that started empty
(so it never holds the operator's edits) and only restored into one, a bad cache falls back to a
build, and the agreement test keeps guarding the build itself.

## Round 36 — splitting chunk 3 into stages (2026-10-04, operator)

Chunk 3 as scoped (backfill, QRESYNC/IDLE, write path + outbox, disabled-not-deleted, tags both
ways, rules/snooze, extraction, FTS5 + embeddings + hybrid search, People, backups, `ivy update`)
bundled six independent subsystems; it is larger than chunks 1 and 2 combined and cannot be one
push, so it is cut the way 1 and 2 were. Four answers settled it:

- **Eight stages 3a-3h** (sync core; disabled-not-deleted; backups; outbox + write path; tags both
  ways; search; rules/snooze/People/reading; the deploy track). 3a may still split at the
  backfill/steady-state line.
- **A minimal embeddings gate in 3f.** The `Embedder` interface, the per-account opt-in, the
  monthly cap and one ledger row per call ship with search rather than with chunk 5, so "nothing
  paid is reachable except through the gate" holds from the first paid path. The full Jev/chat/
  vision gate stays chunk 5.
- **The deploy track is its own track, pulled forward.** The `Dockerfile`, GHCR publish workflow
  and `ivy update` watcher touch no mail code and block nothing, so they are not the tail of the
  sync milestone.
- **Backups run before the outbox.** `outbox`/`send_queue` are the only state that cannot be
  rebuilt from IMAP, so backups land as 3c, ahead of the write path.

The stage table and the constraints are in `next_steps.md` ("The chunk plan"). Still open inside
3a: the backfill/steady-state split, and the N8 threat-model line for duplicate `Message-ID`s.

## Round 38 — 3a groundwork and the C1 harness (2026-10-04, agent; no operator answers)

Decisions made while building, for the operator to veto: the SSE hub lives in its own `events/`
package (so sync can publish without importing the HTTP layer; `STANDARDS.md` and `ARCHITECTURE.md`
updated, and the stale "event ids and `Last-Event-ID`" line corrected to round 37); `sync_state` lives
in `mirror.db` (it describes the connection, so it is rebuildable); the convergence oracle reads the
server, never the model. The three open questions were put to the operator the same day; all took the recommended option:

- **Folder rows of deleted or renamed-away folders:** keep the row and mark it gone (a `gone_at`
  column in an append-only migration), hidden from folder lists. Nothing is erased.
- **Removal reason:** a message the server removed (found nowhere) is disabled with
  `disabled_reason = 'server_removed'`; `'moved'` stays for the round 37 move rule. The mass-disable
  alert counts only `server_removed`. The convergence oracle now requires exactly this string.
- **`error` sync status:** added to the API contract as a fifth `SyncState` value, with its own
  banner copy to design, rather than mapped onto `unreachable` or `syncing`.

## Round 37 — the open design questions before chunk 3 (2026-10-04, operator)

Chunk 3 is the hardest chunk (about 8 of 10; 3a and 3d are 9). Four design calls were open and each
shapes a schema or a contract, so they were settled before any code. All took the recommended option.

- **Move vs delete.** When a UID vanishes from a folder and the same content key appears in another
  folder in the same pass, it is a **move**: the old row is hidden with `disabled_reason = 'moved'`
  (kept, never counted as "server removed"), the new row is live, tags carry over by content key.
  Only keys found nowhere become disabled-as-removed. A move still erases nothing.
- **Pending writes vs sync ("server wins").** **Sync defers to the outbox**: while a row has a
  pending outbox op, sync does not overwrite that row's flags or folder. On success the outbox
  updates the DB; on failure the row rolls back and the next sync converges. "DB == server after
  quiescence" therefore holds for every row without a pending op, and for all rows once the outbox
  is empty. Reads never join the outbox.
- **N8 (identical `Message-ID`s share a content key).** Accept it for tags (harmless labels; a
  message in two folders is one message). **Chunk 5 verdict rows must store a hash of the message's
  normalised headers and body and are ignored on mismatch**, so a copied `Message-ID` inherits no
  verdict. No schema change now.
- **SSE contract: hints only.** One stream, `/api/v1/events`, small typed events (`message.changed`,
  `folder.changed`, `sync.state`, `outbox.state`, `health.alert`) that say what to refetch. No replay
  and no event ids: after any reconnect the client refetches what it is showing.
- **Mass-disable threshold (set by the agent, a tunable constant, not a design call):** a sweep
  that disables more than 50 messages, or more than 20% of a folder that held at least 10, raises the
  Mirror health alert.
- **How chunk 3 is handed over.** The work goes to a faster model (DeepSeek, through pi) with
  `docs/CHUNK3-BRIEF.md` as its standing instructions, and Claude reviews at the end with the
  review skill. Rather than Claude writing the 3a/3d test harness first, the brief carries
  **escalation gates**: fixed checkpoints where the model must stop for review, and objective
  triggers on which it must stop and say "this needs Claude".

## Round 39 — the 3a runner and its schema (2026-10-04, agent; C2 checkpoint)

Made while building the 3a runner (gate C2; the handoff is `docs/handoffs/2026-10-04-C2-runner.md`).
The convergence test passes with the 24 default seeds on both the CONDSTORE and the no-CONDSTORE
variant (640 operations per variant; every operation kind exercised).

- **Message identity now includes UIDVALIDITY.** Migration 9 adds `messages.uidvalidity` and
  `UNIQUE(folder_id, uidvalidity, uid)`, the row id is `hash(folderID, uidvalidity, uid)` and the
  spool path is `spool/<folderID>/<uidvalidity>/<uid>.eml`. A server that rebuilds a mailbox and
  reuses an old UID can no longer collide with the disabled row, its id or its file. The table is
  rebuilt (SQLite cannot drop the old unique in place); `migrate` turns foreign keys off around a
  pending run, restores them and runs `PRAGMA foreign_key_check`, because `attachments` references
  the table being replaced. Nothing is deleted. This is the "keeping new rows and spool files from
  colliding with old disabled ones" schema work the C1 handoff asked for.
- **A rename is per-message moved, not in-place renaming.** The runner creates the live folder row
  for the new name, fetches its messages as new rows, marks the old name's folder row gone and
  disables the old messages with `disabled_reason = 'moved'` (the round 37 rule; the content key is
  still on the server). A deleted folder is marked gone the same way but its messages get
  `server_removed` when nothing on the server still holds them. `gone_at` is revived (cleared) if
  the same name comes back, so one row per name is still enough.
- **`server_removed` is the implemented removal reason** (round 38), and the runner never deletes a
  row: only `DisableMessage` hides it.
- **The fast dev seeder keeps the same identity.** `mailworld.Delivery` carries `UIDValidity`, read
  once per mailbox by the seed observer and passed through `StoreRaw`, so `fast` and `full` still
  agree column-for-column (`TestFastAndFullAgreeForDemo`, now exercising the runner in `full` mode).
- **The one-shot reconciliation is deliberately not yet QRESYNC/IDLE.** Both harness variants take
  the full-account-rescan fallback for now; the snapshot/reconcile split is shaped so a CHANGEDSINCE
  delta and a `VANISHED` set can replace the snapshot later. `sync_state` is also not written by the
  runner yet. These are open 3a items, listed in the C2 handoff.
- **Two existing tests moved for the new implementation, neither weakening the oracle.**
  `TestFetchNewestFirstAndResumes`'s `DropConnection` moves from 6 to 8 commands because the runner
  adds a metadata pre-pass (the resumability and newest-first assertions are unchanged); and the
  harness's stale-flags self-test now uses a deliberately broken `staleFlagSync` instead of relying
  on the chunk 2b fetch being wrong. The C2 break-and-shrink demonstration (38 ops to 3) is in the
  handoff. The oracle, generator and property test are byte-for-byte untouched.

## Round 40 — 3a steady state: sync_state, QRESYNC and IDLE (2026-10-04, agent)

Implementation decisions made while finishing the 3a Go scope after C2; the handoff is
`docs/handoffs/2026-10-04-3a-steady-state.md`.

- **`sync_state` mapping.** The runner writes `syncing` on entry and `ok`/failure on exit. An IMAP
  auth response code is `auth_failed`, a `*net.OpError` is `unreachable`, everything else is
  `error`; the detail is truncated to the store's cap. A failure keeps the previous `last_ok_at`
  (the store coalesces it) and recovery clears the error text.
- **Move vs removal is decided after the account pass.** A QRESYNC delta cannot see the whole
  live set, so rows disabled during the pass stay provisionally `server_removed` and a final
  reclassification flips any whose Message-ID is still live elsewhere to `moved` (round 37). This
  also simplified the full-scan path.
- **A folder's modseq advances only after all its bodies are stored.** Advancing at the snapshot
  made a resume after an interrupted delta skip the messages it never fetched; the test was watched
  failing on the early-advance version.
- **IDLE needs a `UnilateralDataHandler`.** The go-imap client only surfaces unilateral data through
  the handler, so `dial` grew a `dialWith` form and the worker waits on Mailbox/Expunge/Fetch. The
  idle timeout is the periodic fallback (and the test relies on it because the fake can register
  its listener after acknowledging IDLE).
- **`ivy run` owns the workers**, one per configured account, and publishes `sync.state` (and
  `message.changed` when mail was stored) as SSE hints.
- **A configured account gained `insecure` (default false).** It is needed because the real binary
  now connects to the plaintext loopback dev fake; `devstack` sets it and still refuses non-loopback
  hosts. Named for the unsafe thing, per STANDARDS.md 4a.7.
- **Still open:** the frontend `EventSource` client and its Playwright coverage. The Go hub and
  endpoint are done and tested; the browser side was left rather than half-built.

## Round 41 — 3a closed out (2026-10-04, agent)

The loose ends from round 40 were finished in the same session, and the definition-of-done checks
were run.

- **Frontend `EventSource` client.** `web/src/lib/api/events.ts` is the one constructor of
  `EventSource` (per the API-client rule). The server names its events, so it registers a listener
  for each of `message.changed`, `folder.changed`, `sync.state`, `outbox.state` and `health.alert`;
  a malformed frame is ignored. The root layout opens it and calls `invalidateAll()` on every hint
  (the hub coalesces duplicates). The mock E2E fixture answers `/api/v1/events` with a valid empty
  stream so the app connects without a console error, and `web/e2e/events.spec.ts` sends one hint
  and proves the inbox is fetched again. Vitest covers the client with a fake `EventSource`.
- **Checks run green (round 41):** `make check` (drift, gofumpt, vet, staticcheck, Go `-race`,
  `pnpm check`, 209 Vitest), `govulncheck` (local Go 1.26.6, so the 1.26.1 note is stale), the mock
  Playwright suite (242 passed / 10 skipped) and the real-binary smoke slice (8/8). Only the
  operator's real-mailbox live check and the potato numbers remain, plus Claude's fresh-session C2
  review.
- **Benchmarks added:** `sync/bench_test.go` has `BenchmarkSyncBackfill` (200-message cold backfill)
  and `BenchmarkSyncDelta` (steady-state QRESYNC re-sync of an unchanged mailbox), per
  PERFORMANCE.md's small-data rule.
- **3a ends here.** The next gate is C3 (the outbox op states before 3d). 3b's backend can start
  after C2 is cleared; its Restore/Purge and mass-disable screens wait on the C0 canvas board.

## Round 42 — 3b disabled-not-deleted backend (2026-10-04, agent; two operator answers)

3b's Go and API scope, with C2 cleared by the round-41 review and its three return items fixed
(`papercuts.md` #58-#64). The screens are not built: they wait on the C0 canvas board.

- **Q: how broad is Restore?** (operator) **Single plus account bulk.** One message is
  `POST /api/v1/mirror/messages/{id}/restore`; the whole account is
  `POST /api/v1/mirror/accounts/{id}/restore`, which is the one-click answer to a mass-disable alert
  and returns `{restored: n}`. Both are local mirror changes and never touch IMAP. A restored message
  the server still does not hold is simply hidden again by the next completed pass, which is the
  point: disabling is reversible.
- **Q: how broad is Purge-forever?** (operator) **Single only.**
  `DELETE /api/v1/mirror/messages/{id}` erases the row, its attachment rows and its spool file. There
  is no bulk purge, so a 50-message mistake stays 50 deliberate taps. The store refuses a live row
  (`ErrNotDisabled` -> 409), so the reader's ordinary delete can never be wired to the only erasure
  Ivy has, and the handler deletes the row before unlinking the file, so a crash leaves an orphan the
  spool sweep collects rather than a row pointing at a missing file.
- **Mass-disable alert.** A completed pass that hides more than `sync.MassDisableCount` (50), or more
  than `sync.MassDisableFraction` (20%) of a folder that held at least `sync.MassDisableFloor` (10),
  puts the folder in `Result.MassDisabled`; `ivy run` publishes one `health.alert` with code
  `mass_disable` per folder. Only a completed pass alerts, so a `pending_classification` row is never
  counted before it settles (N22).
- **Mirror health** now carries the per-account `hidden` count with its `moved`/`removed`/`pending`
  breakdown, absent when nothing is hidden.
- **N21 closed.** `Fetcher.Fetch` records the final `sync_state` on a `context.WithoutCancel`
  context with a 5 s bound, so a cancelled or timed-out pass settles its own row instead of leaving
  `syncing` until the next start.
- **Not done, on purpose:** the Restore/Purge and mass-disable screens (gate C0); the property test
  that no sync sequence deletes a row already exists (`checkNothingErased` in the convergence
  harness), so 3b only added the scenario and endpoint tests.

## Round 43 — 3c backups and the disabled-blob store (2026-10-04, agent; three operator answers)

3c was scoped from `next_steps.md`, `ARCHITECTURE.md` 9 and `TESTING.md` 6. Three questions were
open because the docs pulled in different directions.

- **Q: does 3c include the content-addressed disabled-blob store, or only the state.db snapshot?**
  (operator) **Include the blob store.** `ARCHITECTURE.md` 9 and `TESTING.md` 6 both put it in the
  backup, and the mirror is never backed up, so without it the one class of mail a mirror rebuild
  cannot bring back would have no off-device copy.
- **Q: which targets?** (operator) **Local folders only** for now. An S3-compatible target is a
  later track and will be a **hand-written S3-style client for Ivy's exact use case, not an
  imported SDK**; `next_steps.md` records that so it is not re-litigated.
- **Q: what does the one-line restore do?** (operator) **In place, server stopped.** It verifies the
  snapshot, moves the current `state.db` aside and writes the snapshot back, refusing while a server
  holds the data-directory lock.

Decisions taken inside that scope (settled policy: once a day, 15 days, floor of 10):

- **The blob store lives at `data/blobs`**, is keyed by the SHA-256 of the message bytes
  (content-addressed), de-duplicates identical mail and is append-only: `Put` never rewrites or
  removes, and the backup mirrors it whole and never prunes it by age. A disabled message's hash is
  recorded on its mirror row (`messages.disabled_blob`, migration 10); `EachDisabledRaw` streams
  hidden rows' bytes for a backup-time reconcile, so a row disabled before the store existed, or a
  copy that failed, is healed on the next run.
- **`VACUUM INTO` + zstd.** The plain snapshot and the compressed archive are both verified
  (`PRAGMA integrity_check`) before anything is written, and each target's copy is verified again
  before that target is pruned. A target that fails is reported and skipped; the others still run.
- **Prune is per target and only after its verified new snapshot**: remove a snapshot when
  `now - at >= 15 days` **and** it is not among the 10 newest. A clock jump or a failed run can
  therefore never empty the target.
- **A data-directory flock (`ivy.lock`)** is held by `ivy run` for its whole life and checked by
  `ivy restore`. It is an `flock`, so a crash leaves no stale lock. This is what makes "server
  stopped" a fact rather than a hope.
- **`ivy backup`** writes one snapshot now (also used by the daily scheduler inside `ivy run`),
  **`ivy restore <snapshot>`** is the one-line restore, and `ivy doctor` warns when every target is
  on the data directory's device. Backups run at `backup.at` (default `03:00` local).
- **Purge does not delete the blob.** The store is append-only and de-duplicated, and one blob can
  back several rows, so `PurgeMessage` keeps the file; this is recorded as an open question in
  `papercuts.md` rather than guessed away.

One bug in the stage was found by the real-binary smoke slice, not the unit tests: the new strict
`backup.at` check broke the dev stack, because `devstack.BuildConfig` built a `Config` without the
field and the spawned `ivy` reloads the YAML it wrote (`papercuts.md` #67). Fixed in `BuildConfig`,
with `TestWrittenConfigReloads` pinning the round trip. Recorded because it is the reason the
backend stage still runs `make smoke`.

## Round 44 — N24: purge erases everywhere (2026-10-04, agent; two operator answers)

N24 was the open design question 3c raised: `PurgeMessage` deleted the row and spool but left the
content-addressed blob, so "Purge forever" was not true once a backup had a copy.

- **Q: what should purge guarantee?** (operator) **Erase everywhere.** The blob is deleted from the
  local store and from every configured backup target, but only when no other hidden row shares the
  bytes (reference counting; N8 means identical mail can share a hash). A shared blob is kept.
- **Q: what about an offline or unmounted target?** (operator) **Durable retry.** The erasure is
  recorded in `state.db` (`pending_blob_deletions`, state migration 3) and the daily backup retries
  it until every target is clean. state.db is backed up, so the record also survives a restore. An
  absent target directory is treated as offline, not clean.

Design notes, so the next reader does not have to reconstruct them:

- **`PurgeMessage` returns `PurgedMessage{RawPath, BlobHash, BlobUnreferenced}`.** It records the
  pending deletion **before** deleting the row, so a crash in between leaves the erasure to be
  retried; if the delete fails the record is cleared again. The last-reference check counts rows
  other than the one being purged.
- **`backup.PurgeBlob`** removes the blob from the local store and every target, clearing the
  record only when the erasure is complete. It refuses to evict bytes another hidden row still
  references and clears the then-stale record. `backup.Run` calls `processPendingBlobDeletions`
  before the snapshot, so a purge retries without being asked and a target that is back online is
  cleaned on the next daily run. `mirrorTree` skips a source blob that vanished (a concurrent
  purge), so a backup never fails over it.
- **The gateway** wires the target list through `WithBackupTargets` (set by `ivy run` from the
  config) and erases immediately after a purge, for the common case where every target is reachable.
  A failure or a pending target is logged, never fatal: the row is already gone and the record is
  durable.
- **Purge is still single-message only** (`qa-log.md` round 42), so this cannot erase a mailbox by
  accident; each erase is a deliberate tap.

## Round 45 — the 3b/3c review's open items (2026-10-04, agent; four operator answers)

The review of 3b and 3c (`papercuts.md` #68-#72) left five open items (N25-N29). All five are closed,
and the test suite no longer leaves the OS short of ports.

- **Q: how should the sync tests stop exhausting macOS ports?** (operator) **Close test connections
  with a reset.** The shortage was the client side (every closed connection parks a temporary port in
  TIME_WAIT for ~30 s, and one run parked ~9,000 of ~16,000), not which ports the fakes listen on, so
  a fixed 30k-40k range would not have helped (it is smaller than the default pool) and cannot clash
  with Polaris either way, since the fakes take OS-picked ports. Loopback test dials now set
  `SO_LINGER` to 0: ~144 sockets linger after a full run instead of 9,340.
- **Q: N25, do moves count toward the mass-disable alert?** (operator) **No, and account restore skips
  them.** The alert counts rows that will settle as `server_removed` (a pending row whose Message-ID is
  live elsewhere is a move); `RestoreAccountDisabled` restores only `server_removed` (and no-reason)
  rows, so it can no longer show a moved message twice. This refines round 42.
- **Q: N29, what happens to a purge's pending erasures on a restore of an older snapshot?**
  (operator) **Keep them outside state.db.** They are now one marker file per blob hash in
  `data/pending-blob-deletions/` (synced before the row is deleted; no locking needed, so the server
  and `ivy backup` can both touch it). State migration 4 drops the old table; erasures recorded in the
  table before this change are not carried over. This supersedes round 44's "recorded in `state.db`".
- **Q: N28, single restore of a pending row?** (operator) **Refuse it.** `RestoreMessage` returns
  `ErrPendingClassification`, the API answers 409 `pending_classification`, matching the contract.
- **N26 (the leaked fake-server goroutine)** was a bug in the go-imap fork: `imapmemserver`'s fetch
  returned on a failed body write without closing the response, so the connection's encoder lock was
  never released. (operator) **Pushed to the fork:** `v2.0.0-beta.8-ivy.3`; the clone now lives at
  `~/Documents/Projects/go-imap` (branch `ivy-beta8-condstore`) for later changes.
- **N27 (missed backup slot).** On start the loop runs a backup at once when any target has no
  snapshot or its newest is a day old (`backup.Manager.Due`), then keeps the daily schedule.

## Round 46 — the outbox design review, gate C3 (2026-10-04, agent; operator answers)

The first C3 draft (`docs/handoffs/2026-10-04-C3-outbox.md`) was reviewed against `ARCHITECTURE.md`
section 4, the round 37 rules and the sync code. Six defects were found and corrected in the design
(`papercuts.md` #79-#84); the four questions the draft left open are settled.

- **Q: what is the reader's "delete"?** (operator) **A `move` to the Trash role.** `expunge` is only
  for emptying Trash, and the API refuses it for any other folder, so a single tap can never erase mail.
- **Q: how long do terminal outbox rows stay?** (operator) **Seven days**, then pruned (the only
  deletion the outbox does, and only of terminal rows).
- **Q: one in-flight op per account, or per folder?** (operator) **Per account.**
- **Q: undo, and CLAUDE.md rule 6 ("nothing moves or deletes without confirmation").** (operator)
  **Moves and deletes need a confirmation modal before the op is enqueued; the undo toast is not a
  confirmation.** Archive, delete, mark spam and not-junk are moves and get the modal; flag and unflag
  change no folder and do not. Emptying Trash gets a stronger modal with the count. **Undo is an
  inverse op** sent after dispatch (no hold-back window), and `cancelled` only means "superseded before
  dispatch". This adds a UI requirement to 3d: a confirm modal on those actions.
- **Reviewer's changes the operator accepted ("keep it all"):**
  - the idempotency key is unique over non-terminal ops only (a repeated action must work);
  - a message is identified by `(content_key, source_folder_id)`, never the content key alone (N8);
  - the resolved `(uidvalidity, uid)` is persisted when the op becomes `in_flight`, and recovery asks
    about that message first, re-resolving by Message-ID only after a UIDVALIDITY change;
  - the mirror update is idempotent and runs before `done`, because `mirror.db` and `state.db` cannot
    share a transaction; the outbox hides the source row through sync's own disable function and never
    writes a destination row;
  - retry is bounded (8 attempts, 24 h, 500 queued ops) and IMAP `NO` is classified by response code;
  - the outbox owns its own connection rather than sharing the sync worker's IDLE connection;
  - `move` needs `MOVE` and `expunge` needs `UIDPLUS`, otherwise the op fails instead of emulating them.

## Round 47 — the C4 review's two open questions (2026-10-04, agent; two operator answers)

The C4 handoff (`docs/handoffs/2026-10-04-C4-outbox-crash.md`) left two questions. Both are settled
before the rest of 3d (the HTTP surface, reader actions and optimistic UI) is built.

- **Q: how wide is the sync-vs-outbox deferral window?** `fetchAll` read `OutboxActiveKeys` once at
  the top of the pass, so a row enqueued mid-pass was not yet deferred. (operator) **Re-check per
  folder.** `reconcileFolder` gets a fresh `OutboxActiveKeys` at the start of each folder, and
  `markGoneFolders` refreshes once before its sweep. It is one extra read query per folder per pass
  on a local database, and it narrows invariant 4's window to a single folder's work without a
  query per row. Two databases still cannot share a transaction, so the outbox's post-ack overwrite
  remains the closed loop; this only makes the gap smaller.
- **Q: where should the C4 crash seam live?** (operator) **Keep the unexported seam.** The
  `OutboxWorker.afterAck` hook dies exactly between the IMAP ack and the DB write, which
  `mailworld.AckThenDrop` cannot guarantee (the client may or may not have read the ack). It stays
  unexported, is set only by the white-box crash test and is nil in production, so it adds no public
  API surface. `AckThenDrop` stays as the server-side half of the race.

## Round 48 — after 3d, next steps (2026-10-04, agent; one operator answer)

3d (outbox + write path) is done and green (C4 crash test, HTTP surface, confirm modal, optimistic
overlay, undo, `ivy run` wiring).

- **Q: what next?** (operator) **Finish the 3d UI leftovers first**: a flag/junk control in the
  reader (which needs a `flagged` field on the read API, since there is none yet), the Empty-Trash
  UI, and a queue/history screen for the outbox `retry`/`dismiss` endpoints. 3e (tags) starts after.

## Round 49 — Empty Trash and the deepseek review (2026-10-05, operator instruction)

- **Q: finish the unfinished Trash handling.** (operator) Done by giving `/inbox` a `folder` role
  parameter and an Empty Trash button that reuses the outbox `expunge` (the dropped one-off endpoint
  stays dropped). See the BUILD-LOG 3d entry.
- **Q: review the other model's work.** (operator) Run the `review-deepseek` skill from commit
  `ea78a63` inclusive to the tip on a `review/` branch; findings go in `papercuts.md`.

## Round 50 — decisions from the deepseek review (2026-10-05, two operator answers)

- **Q: how should Undo of a move work before sync mirrors the arrival (N30)?** (operator) **Mirror via
  COPYUID.** After a MOVE the outbox worker stores the arrived message itself from the server's
  destination UIDs, best effort; `not_synced` remains only as the fallback answer.
- **Q: should connection failures count against an op's 8-attempt cap (N31)?** (operator) **No.** Only
  a server NO to the op counts; dial, login, drop and stall failures back off separately and the 24 h
  age cap bounds them. The STANDARDS limits row says so.

## Round 51 — 3e tags both ways, design questions (2026-10-05, four operator answers)

All four took the recommended option.

- **Q: how is a tag change represented in the outbox?** (operator) **Reuse the `flags` kind.** A
  keyword add or remove is a flag in the same STORE, so it inherits the idempotency key, the
  inverse-cancellation (tag then untag cancels) and crash recovery; there is no new state machine.
- **Q: two names with the same ASCII slug ("Café", "Cafe")?** (operator) **Numeric suffix** (`cafe-2`).
  The slug is stored on the tag at creation and never recomputed, so a rename never rewrites keywords
  on the server.
- **Q: what does deleting a tag do on the server?** (operator) **Remove the keyword everywhere,
  behind the confirm modal**: one flags-remove op per tagged message through the outbox, no second
  path to the server (as for Empty Trash).
- **Q: read-back of a `$ivy-<slug>` keyword with no matching local tag?** (operator) **Ignore it and
  leave the keyword on the server.** Only slugs the operator has defined become membership, so a
  hostile or odd keyword cannot mint tags. Bounds: at most 32 `$ivy-*` keywords considered per
  message and a slug of at most 48 bytes (to be put in the STANDARDS limits table with the code).
- N8 (identical `Message-ID`s share tags) was already settled in round 37, so it was not re-asked.

## Round 52 — 3e, decisions taken while building (2026-10-05, agent; no operator input)

Each follows from a round 51 answer or from the docs; none changes one. The operator can overturn any.

- **The membership follows the server's acknowledgement.** The worker writes `message_tags` after the
  keyword is confirmed, not at enqueue, so "IMAP first" holds for tags too and a refused tag leaves
  nothing behind. On a server without `\*` the op finishes local-only and the membership is written
  at once. The picker's switch and an outbox overlay cover the wait.
- **Read-back uses transitions, not absence.** A keyword that is missing is not "removed": a
  local-only tag, a queued write and an operator's tag on a server that cannot hold it all lack the
  keyword. Only a keyword that was in the mirrored flags and is gone removes membership, and only when
  no other live copy of the content key still carries it.
- **A rebuilt mailbox gets its keywords re-applied** when a row arrives new for mail that is already
  tagged and the folder keeps keywords, through the outbox, best effort (a full outbox is logged).
  Adopting keywords that predate a tag's creation is not done.
- **Untag clears every live copy** that carries the keyword (N8), and always queues the targeted row
  so a local-only tag still loses its membership.
- **Delete refuses instead of half-deleting.** If the keyword clears do not fit in the outbox
  headroom the delete answers 409 `outbox_full` and changes nothing. A tag on more messages than the
  headroom (500 per account) therefore cannot be deleted in one go; the follow-up if it matters is a
  soft delete that a sweeper finishes as the queue drains. A tag-add still queued when its tag is
  deleted is not cancelled: it lands as an unknown slug, which read-back ignores.
- **The contract carries one tag per message** (`tag`, the first by name) plus `tagIds` on the
  reader's message for the picker; tag counts are memberships, so they include hidden mail.

## Round 53 — 3e side findings (2026-10-05, agent)

- The HTTP client read a `204` as an unreadable body, so every successful delete (Dismiss on
  `/settings/outbox`) surfaced `internal_error`. Fixed with a test in `http.test.ts`; the e2e fake
  had hidden it by answering 204 with a body.

## Round 54 — 3f search, the three blocking questions (2026-10-06, operator; three answers)

3f (search: FTS5, tier 0-1 extraction, the `Embedder`, the embeddings gate and ledger, embed-once,
hybrid RRF, `/search`) was reviewed before coding. Two were escalation-gate calls (T7 new
dependency, T4 design), one is the operator's money.

- **PDF extraction (T7):** **adopt `github.com/ledongthuc/pdf`.** Spike S6 chose it (pure Go,
  cgo-free, 5.7 s for 212 pages on the potato, ~15 MiB, ~97% recall); it was never added to
  `STACK.md`, which is why this was asked. It goes in the `STACK.md` dependency table, every call is
  wrapped in `recover` plus a timeout, and `dslipak` stays dropped and `pdfium` is not adopted
  (S6: 31 s start-up, ~300 MiB on the board).
- **Frontend scope:** **wire `/search` for real** in 3f, with Playwright coverage; it is no longer
  mock-backed after this stage.
- **Embeddings monthly cap default:** **$5 per month**, a tunable constant (the operator can change
  it in settings). The measured rate is $0.15 per 100k messages, so ~3.3M messages of headroom.

Consequences folded into the docs with the work: `STACK.md` gains the PDF dependency and the FTS5
tokenizer is a measure-first choice recorded in 3f.

## Round 55 — 3f, the FTS5 tokenizer measured (2026-10-06, agent)

The brief leaves the FTS5 tokenizer as a measure-first choice inside 3f. Measured
`store.BenchmarkFTS5Tokenizers` (synthetic 5000-document corpus, ~60 words each, dev laptop, arm64):

| Tokenizer | Index bytes | Query (`"invoice" "report"`) |
|---|---|---|
| `unicode61 remove_diacritics 2` | 402,085 | ~0.65 ms/op |
| `trigram` | 2,941,903 (7.3x) | ~7.96 ms/op (12x) |

**Chosen: `unicode61 remove_diacritics 2`.** It is far smaller and faster, folds
diacritics (so `cafe` finds `café`, a real need for multilingual mail) and supports the
prefix form the UI uses (`renew*`). Trigram's one advantage, matching substrings of three
or more characters and CJK without word breaks, does not pay for a 7x index and a 12x
slower query on the operator's mostly-English mail; if CJK becomes a need, a second
trigram index can be added beside this one without changing the contract. The choice is
recorded in migration 13 and in `docs/ARCHITECTURE.md` section 6.

## Round 56 — 3g rules, snooze, People and Reading, the design questions (2026-10-06, operator; five answers plus two follow-ups)

3g's scope touches schema in both databases and the write path, so the open calls were settled
before any code. Round 20's rule decisions and round 10's local-hide-until snooze still stand.

- **Frontend scope:** **wire all three screens for real.** Rules, People and Reading stop being
  mock-backed in 3g, including the inbox tag filter. The rule compiler is chunk 5, so 3g ships a
  **manual structured editor** (conditions and actions from the closed vocabulary below); the
  "Describe it" free-form screen stays a labelled preview until the compiler lands.
- **When rules run:** **on ingest, plus an explicit apply-to-existing action.** A rule pass runs in
  `Settle` over messages the pass has not evaluated yet (bounded), and the operator can apply one
  rule to existing mail on demand after a free local dry-run count. No LLM is involved (fuzzy
  conditions are chunk 5).
- **Reading:** **a reserved tag, visible in Tags, and the inbox hides mail that carries it.** There
  is no Reading table: "show in Reading" adds the reserved `reading` tag, and later Jev can apply
  the same tag without a second mechanism (the operator's reason for choosing this). The tag's slug
  is fixed and it cannot be deleted.
- **People:** **one person may have several addresses.** The default is one person per address; the
  operator can merge addresses by hand, and the operator's own account addresses are auto-linked.
  Merge decisions are locally owned (`state.db`), so a mirror rebuild keeps them.
- **Header matching:** **case-insensitive substring only, no regex.** Conditions are matched against
  sender-controlled headers, so a backtracking engine is not worth the ReDoS surface; lengths are
  bounded.

Vocabulary and mechanics taken from `JEV.md` 3F and `PLAN.md`: conditions are `from`, `subject`,
`account` and `has_attachment`; actions are the local-only `add tag`, `show in Reading` and
`snooze`. A tag action goes through the outbox like every other keyword write; `show in Reading` is
the reserved tag through the same path; snooze is local. Conditions are stored as JSON on the rule,
validated on the way in against the closed vocabulary. Every count and bound is in
`STANDARDS.md` 4a with the code.

## Round 57 — 3h deploy track, the host and registry decisions (2026-10-06, operator; three answers)

3h is the deploy track from the round 36 split: a multi-stage `Dockerfile`, a multi-arch GHCR publish
on merge to main, a host-side update watcher with a signal file, and `ivy update` (CLI and in-app)
with SSE progress. Round 29 settled the approach; this round settles the three calls the docs left
open. The operator's answers:

- **Host install automation: full Polaris parity.** An `install.sh` templates the watcher's systemd
  units (`.service`/`.path`/`.timer`), the update flow re-syncs those units, and a hash-pinned
  root-owned wrapper (`/etc/ivy/watcher-sync-verify.sh` plus a sudoers rule) is the only thing the
  watcher may run as root, so an unreviewed commit to the unit-render script cannot gain root. The
  watcher itself runs unprivileged in the docker group; the container never touches the Docker
  socket. This is more surface than the minimal design, chosen deliberately for a reproducible,
  self-updating board.
- **Image reference: hardcoded `ghcr.io/autumnsgrove/ivy`.** Like the model registry, it names what
  the software is, not an operator preference, so it is a constant, not config. Tag `:latest` plus
  the short SHA; rollback is by name or digest.
- **CI race: wait on the Actions run.** `ivy update` first polls the most recent `docker-publish.yml`
  run for `main` and blocks (bounded) while it is queued or in progress before resolving `:latest`
  from GHCR, closing the window where a click right after a merge would silently deliver the
  previous build. `GITHUB_TOKEN` in the environment raises the API rate limit; the wait is
  best-effort and never fails the update on a GitHub API hiccup.

Supporting calls taken while planning (operator may veto): the version string is the Polaris-style
`r<git-count>.<short-sha>` passed as `-ldflags -X main.version=`; the runtime stage is `alpine` with
`ca-certificates` and non-root, and reuses `GET /api/v1/health` for the container healthcheck; the
update signal directory lives inside the bind-mounted data directory (`<data_dir>/update-signal`),
so one volume covers it and the host watcher watches the same path. The core logic lands in a new
`update/` package (already in the `ARCHITECTURE.md` layout) so the CLI and the gateway endpoint share
one tested path; tests use a fake GHCR and a fake GitHub Actions API per `TESTING.md` 6.

## Round 58 — the 3e-3h review's open decisions (2026-10-05, operator; eight answers)

`papercuts.md` N33-N41 held the findings of the second-opinion review that needed a decision. The
operator's answers (recommended option unless noted):

- **N37, Reading/Snoozed/tag views are capped at 200 rows with no paging: keyset paging now.** A
  `cursor` in the Reading and Inbox contract, the same (date, id) scheme the inbox uses, and the
  screens load more. Done before the potato install.
- **N34, a missing `usage.cost` is ledgered at $0 so the cap never trips: estimate from tokens.** A
  small per-model price table; when the provider reports no cost the gate computes one from tokens
  and marks the row estimated, so the cap still works.
- **N33, a document the provider always refuses is retried every pass: tombstone after 5 failures.**
  Failures are counted per document in the ledger; the fifth records the document as skipped.
- **N36/N38/N40, per-settle full-mailbox passes: measure first, then decide.** A small benchmark at 5k
  messages for the operator to run on the board (extrapolate; no 100k run), before any change.
- **N35, the unpaged People list: page it by 100 at a time** (the operator's own wording, not one of
  the offered options). Keyset paging, 100 per page; the minimum-count filter and the per-address
  lookup from the recommendation are not part of this answer.
- **N39, the dev stack never wires search or the embed worker: share one wiring function.** One
  exported function builds the gate, embedders, query embedder and worker from a `config.Config`, used
  by both `ivy run` and `ivy-dev`.
- **N41, a rollback may open a newer schema: refuse a newer database.** `ivy run` exits with a clear
  message when the database is newer than the binary, and the update result says why.
- **First-install runbook: yes, `docs/DEPLOY.md`.** Clone, `install.sh`, `ivy.yaml` and `data/.env`
  templates, `allowed_hosts`, first start, one live update, rollback; linked from the README and the
  doc map.

## Round 59 — in-app account setup, before the first install (2026-10-06, operator; four answers)

The operator would not copy a mailbox password to the board over SSH and wants to type it into the
app as part of setup. `/welcome/account` was a designed screen that only navigated; no endpoint
created an account or stored a secret. The first install waits on building it. Answers:

- **Build in-app account setup before deploying.** The OpenRouter key may still go in `data/.env`;
  the mailbox password may not travel anywhere but the browser.
- **Non-secret account settings live in `state.db`, merged with `ivy.yaml` (recommended).** It matches
  `ARCHITECTURE.md` section 9 ("accounts without secrets" are in `state.db`) and gets backed up;
  accounts in `ivy.yaml` keep working (the dev stack uses them). **The password never enters a
  database**: it is a mode-0600 file under `data/` that no backup touches (`STANDARDS.md` 2 and 8).
- **Purelymail only for now, and no provider presets.** The host and ports are fixed server-side for
  Purelymail. Later, other providers are plain host and port fields with no preset list (operator's
  wording). Arbitrary hosts need the SSRF guard first, so that is not in this stage.
- **The connect endpoint is always open (operator's choice over the recommended first-run-only).**
  Anyone on the tailnet can add an account or replace a password. The tailnet is the access control,
  as for every other endpoint; revisit when auth lands.

## Round 60 — chunk 4 (send) split into stages (2026-10-06, operator; two answers plus three recorded decisions)

After the first install the operator chose to finish the core build-out, chunk 4 at the least, before
touching the live-use issues (#7-#15), and to cut chunk 4 into sub-chunks the way 1, 2 and 3 were, so
it is visible which stages DeepSeek can handle.

- **Split approved as proposed:** eight stages 4a-4h (builder and submit, send queue and Sent copy,
  undo send and the send API, drafts, identities and reply logic, the compose screen, outgoing
  attachments, rich text). The smallest set that can replace Apple Mail is 4a-4f. The stage table is in
  `next_steps.md`; the standing instructions, invariants and gates are `docs/CHUNK4-BRIEF.md`.
- **Suggested owners, by risk rather than by skill:** 4e solo (pure logic); 4a, 4c, 4f with a review of
  the tests; 4d with a gate (it touches the outbox); 4b designed by Claude first and reviewed after
  (gate G2, G3); 4g led by Claude for decoders and dependencies (gate G4). The gates exist so the
  outcome shows what the model can do.
- **A crash after SMTP accepts is never auto-resent (recommended, accepted).** The row becomes
  `unconfirmed` and the operator decides. SMTP cannot be asked what happened, unlike IMAP, and a
  duplicate email to a real person costs more than a retry tap.
- **Markdown renders to HTML at send time** as `multipart/alternative` with a plain-text part. The
  library is **goldmark, pre-approved by the operator** (asked after the split, so DeepSeek would not
  stall on the dependency rule) and entered in `STACK.md`; raw HTML passthrough stays off.
- **The operator will run DeepSeek through 4a-4h in order,** so the planned hand-backs are exactly the
  gates G1-G5 (and any T-trigger); between them 4c-4f should flow without stopping.
- **The undo window is a column on the queue row,** enforced server-side, so a closed tab or a restart
  neither sends early nor loses the message.

## Round 61 — 4a pre-flight, three open design questions (2026-10-06, agent; three answers)

Before writing any code in 4a, three questions the docs did not answer were put to the operator. They
are frozen into the 4a tests at gate G1.

- **A recipient refused among several aborts the whole send.** If the SMTP server refuses one `RCPT`
  (for example a 550 for a mistyped address), Ivy issues `RSET` and reports the refused address; no
  recipient receives the message. Partial delivery is never silent and never surprising: the operator
  fixes the address and sends again. This is the `recipient_refused` outcome in `smtp/`.
- **The `text/plain` part is the operator's markdown exactly as typed.** `multipart/alternative`
  carries the raw compose text as `text/plain` and goldmark's rendering as `text/html`. There is one
  renderer (goldmark, HTML only); a plain-text-only reader sees the markdown source. A stripped plain
  text rendering is deferred.
- **The implicit-TLS SMTP client lives in a new thin `smtp/` package.** `compose/` stays a pure
  builder (`Validate`, `Build`, `Envelope`, easy table tests); `smtp/` owns dial, `AUTH PLAIN`, the
  `EHLO` `SIZE` read, `RCPT`/`DATA` and the deadlines, matching `STACK.md`'s "thin wrapper at the
  boundary". The 4b send queue and the 4a tests both call `smtp.Submit`; `ARCHITECTURE.md` section 2's
  proposed `compose/`-only layout is updated when the package lands.

## Review of the 4a-4c range (2026-10-06)

- **Q: should a send waiting out a retry backoff block the sends behind it (strict FIFO)?** No. The
  operator chose independent rows: a row in its backoff or undo window is skipped, due rows go lowest
  sequence first (`store.NextQueuedSend`). This replaces the strict-FIFO line in the G2 design for the
  send queue only; the IMAP outbox stays strict FIFO.
- **Q: fix the four pre-existing golangci-lint findings at the CI pin in the review branch?** Yes.

## Welcome screen with an account (2026-10-06)

- **Q: what should `/welcome` do when an account already exists (a stale bookmark landed there)?**
  Not an automatic redirect: it shows a "Take me to my inbox" button at the top and turns the main
  button into "Connect another account". With no account it is unchanged. The lookup is a
  convenience, so a failed `/accounts` call falls back to the first-run view instead of an error page.
  The reported "accounts vanished" issue was a bookmark of `/welcome`, not lost data.

## Round 62 — 4d drafts design gate (2026-10-06, agent; four answers)

Before coding 4d (which touches the outbox), the four choices the docs left open were put to the
operator. They are settled into `docs/handoffs/2026-10-06-4d-drafts-design.md`.

- **A draft replace is one dedicated outbox op kind, `draft`.** It appends the new version, then
  expunges the superseded one, in one op. The existing `expunge` op stays Trash-only (widening it
  would let a single tap erase mail); the `append` op cannot be reused because it dedupes by
  Message-ID and would silently skip a reused id. The op locates both copies by `Message-ID`, so it
  needs no mirror row and two autosaves before a sync pass are still correct. UIDPLUS is required to
  expunge precisely.
- **The drafts list merges the mirror's Drafts folder with local not-yet-synced drafts.** The mirror
  is server truth (drafts made in Apple Mail appear); the local head gives immediate feedback for an
  autosave.
- **Two tabs: optimistic version, last write wins.** A save carries the version it began from; the
  later save wins and bumps the version; a stale save is refused `409 draft_conflict` with the newer
  content, so the loser is told.
- **The sent-draft linkage is in 4d, not 4f.** `send_queue` gains the sent draft version's
  Message-ID, and the send worker enqueues its removal from Drafts after the `250`. An undo, a
  cancel or a permanent failure keeps the draft.

Related choices settled in the design file rather than asked: one immutable `drafts` row per saved
version (the head is the highest live version); a fresh injected Message-ID per save; `\Draft` and
no `\Seen`; a missing Drafts folder is a clear error (no auto-create in 4d); resume parses a
server-only draft's stored MIME back into To/Cc/Subject/text without the inbound sanitiser.

## Round 63 — 4e identities and reply logic, the design questions (2026-10-06, operator; four answers)

Before coding 4e, the four open choices were put to the operator. They are settled into
`docs/handoffs/2026-10-06-4e-identities-design.md`.

- **Surface: API + settings editor.** 4e ships the store, the CRUD API, the reply/reply-all/forward
  logic and a small identities and signatures section on the account settings screen. The operator
  can add aliases and run the live send-as check without waiting for the compose screen (4f), which
  still owns the From picker. This is the first time a 4x stage adds a screen; the risk is low and
  the alternative left the identities unreachable from the app.
- **Signature: per identity, plain text, `-- ` separator.** A non-empty signature is appended after
  the body (`\n\n-- \n<signature>`); when markdown is on the whole text is rendered, so the
  signature styles with the message. A global-default-plus-override model was rejected as more state
  for no gain on a single-operator box.
- **Forward: attribution block.** A blank line, `---------- Forwarded message ----------`, then
  `From`/`Date`/`Subject`/`To` and the original plain text. Attachments are not attached unless
  asked (CHUNK4-BRIEF section 3).
- **Aliases: configured only, offer to add.** `From` must be the account's own address or a stored
  identity. When a reply's delivered-to address has no identity, the prefill reports it and the
  screen offers to add it in one tap; an unconfigured address never sends.

Related choices settled in the design file rather than asked: the account's own address is always a
synthetic, non-deletable primary identity merged at read time (no seed migration, because accounts
live in the rebuildable mirror); direct reply goes to `Reply-To` then `From`; reply-all adds the
original To/Cc minus every one of the operator's addresses; `List-Post` and other list semantics stay
out of v1; a reply body is empty and a forward body is the attribution block plus the original text.

## Round 64 — 4f compose screen, the three scope questions (2026-10-06, agent; three answers)

4f has no escalation gate, but three choices the docs left open changed the size of the stage enough
to put them to the operator before any code.

- **Drafts: autosave plus a drafts screen.** The operator chose both halves: the compose screen
autosaves to the server's Drafts folder, and a new `/drafts` screen lists and resumes a draft. The
screen has no canvas design, so it follows the existing list patterns (the same rows as tags/People,
with an empty state) and is reached from the shell's folder list.
- **Autosave cadence: debounced and on leave.** A few seconds after typing stops, and once more when
leaving compose. A send failure leaves the server draft in place, so the Not-sent sheet's "safe in
Drafts" is literally true.
- **Attachments stay a preview; Send is blocked while any are attached.** `SendRequest` has no
attachment field until 4g, so the attach sheet still opens and lists picks (the designed canvas), but
the message cannot be sent with them yet. Nothing is silently dropped.

Related choices settled here rather than asked: undo keeps the canvas flow (the tap leaves compose
and the inbox shows a toast with Undo and the server's countdown; Undo returns the stored draft); a
permanent failure shows the Not-sent sheet over compose; `unconfirmed` is a persistent, plain notice
("This may have been sent. Check Sent.") that never offers a resend; the format bar stays but the four
markdown buttons remain inert until 4h (rich text); two tabs race on the optimistic draft version and
the loser is told.

## Round 65 — 4g attachments and images, the G4 dependency gate (2026-10-06, agent; four answers)

4g is "Claude leads decoders and dependencies", so the four choices that decide whether any new
module is imported were put to the operator before code. They are settled into
`docs/handoffs/2026-10-06-4g-attachments-G4.md`.

- **Browser-first image preparation; no server decoder.** The browser decodes (Safari handles HEIC),
  applies EXIF rotation, downscales and re-encodes via canvas, which strips EXIF/GPS by construction.
  The server only sniffs, bounds and streams bytes, so **no new dependency** is added and the G4/T7
  stop does not trigger. This reuses the account-photo path (`web/src/lib/photo.ts`).
- **The phone hands back JPEG.** The operator reports iOS Safari sends a JPEG through a web photo
  picker, so no HEIC decoder is needed; the browser decode still covers a HEIC from other sources.
- **Limits: 25 MiB per file, 25 MiB total raw, 20 attachments.** Base64 inflation keeps the built
  message under Purelymail's `EHLO SIZE` (~48.8 MiB) and matches the existing SendFailed canvas copy.
- **Inline images are in 4g**, not deferred to 4h: an inline image is a `multipart/related` part with
  `Content-ID: <uploadId@ivy>` and the body references it with `![name](cid:uploadId@ivy)`.

Related choices settled in the design file rather than asked: uploads are content-addressed staging
under `data/uploads/` (not the disabled-mail blob store) with a `state.db` metadata table; resume and
undo re-materialise attachments from the stored MIME, so a swept staging file is invisible to the
operator; "Original" size still re-encodes so location is removed, unless "Remove location" is off.

## Round 66 — 4h rich text, the editor and body-format questions (2026-10-06, agent; five answers)

4h is the last, deferrable chunk 4 stage. The four format buttons are still inert and the docs never
picked an editor, so the choices that decide the whole stage went to the operator before any code.
They are settled here and in `docs/handoffs/2026-10-06-4h-richtext-design.md`.

- **Squire, not TipTap.** The operator first picked TipTap, then opened the door to alternatives
  while the measured cost came back. The measured options were `squire-rte` **16.1 KiB brotli with
  zero dependencies**, Quill core 38.9, Lexical 57, and minimal TipTap (`@tiptap/core` +
  `@tiptap/pm` + nine extensions for four buttons) **98.3 KiB brotli**. Squire is the HTML editor
  built for Fastmail's compose (used by Proton, StartMail, Tutanota, Zoho, Superhuman), so arbitrary
  pasted HTML is its design centre, it avoids `execCommand` entirely, ships types and is actively
  maintained (2.4.9, 2026-09-15). MIT. It is in `STACK.md` as the one runtime exception and loads
  only with the `/compose` route chunk. Building our own (~2–4 KiB) was rejected: it means owning
  selection, paste cleaning, block/whitespace normalisation and the iOS Safari quirks Squire
  already handles.
- **Both editors stay; rich text is the default.** A small mode pill switches a message between
  Markdown and rich text. Existing drafts reopen in the format they were saved with.
- **The mode is fixed once you type.** The pill is only active while the body is empty; after that
  the message's format is settled. This keeps the stored draft JSON exact and avoids an HTML↔Markdown
  converter pair and the round-trip fidelity risk it would carry.
- **`bodyFormat` on the wire, sanitised server-side.** `SendRequest`, `DraftRequest` and
  `DraftResume` gain `bodyFormat: "markdown" | "html"` (`markdown` when absent/`markdown: true`, so
  older clients and stored draft JSON keep working). The operator's HTML is stored verbatim as the
  draft; at build time `compose` narrows it through the existing compose-only `outgoingPolicy` and
  derives the `text/plain` alternative by converting the HTML to readable text. No migration: the
  draft's `compose_json` already carries the request, so the format round-trips with it.
- **A small allow-list paste sanitiser, no DOMPurify.** Squire refuses to load HTML without a
  `sanitizeToDOMFragment`; rather than add a client sanitizer library (STACK says none), we supply
  our own DOM walker over the same tag/attribute set as the outgoing HTML policy, with a hostile
  corpus in Vitest. It protects the editing surface; the authoritative sanitiser remains the
  server's `outgoingPolicy`.

Related choices settled here rather than asked: an HTML draft created in another client (Apple Mail)
resumes as `bodyFormat: html` (the mirror parser prefers the HTML part when one exists) so its
formatting survives; a draft with no usable body stays `markdown`; the four format buttons only act
in rich mode and are disabled in Markdown mode.

## Review of chunk 4d-4h: the open items (2026-10-07)

Operator answers to the six open items from `papercuts.md` (`2cabdd0..4f34628`); each is recorded as resolved
there.

- **A permanent draft failure is a `failed` state (N49).** The worker marks the version, the list shows "Not on
  your mail server yet", and a newer save, a send or a discard settles it. A failed head is never pruned by age
  because it is the only copy of what was typed (a deviation from "pruned after 7 days", flagged to the operator).
- **One lock orders upload staging against blob removal (N46).** A stage holds a read lock; removal takes the write
  lock without waiting and leaves the file to the sweep if a stage is running.
- **Orphan staging files are swept with the other prune steps (N47),** with no age threshold, because the lock
  proves nothing is mid-flight.
- **A retry of unchanged content reuses its save id (N50).** The 409 contract is unchanged.
- **Draft bodies live in a content-addressed store, not in `state.db` (N48).** Not backed up, like uploads.
- **The upload sends the Blob itself, not a buffered copy (N51).** Memory on a real iPhone is the operator's live
  check.
