# Ivy — Plan

**Status:** approved 2026-10-02 (`qa-log.md` round 28); product decisions settled through round 31.
Work goes directly on `main` for this phase. Milestone 1 (Read) is nearly done: 2a-2f done, 2g
finishing, 2h next (`../next_steps.md` has the live status; `BUILD-LOG.md` has what each chunk
delivered).

**Doc map:** `PLAN.md` (this: product, features, milestones, risks) · `ARCHITECTURE.md` (how it's
built) · `STANDARDS.md` (engineering standards, TDD workflow) · `STACK.md` (libraries, pure-Go
rule) · `PERFORMANCE.md` (compression, budgets) · `JEV.md` (the cheap decision engine and its
question catalog) · `CI.md` (GitHub Actions plan, public-repo security) · `TESTING.md` (how everything gets tested) · `qa-log.md` (every question and
answer) · `../CLAUDE.md` (rules for agents).

## 1. What Ivy is

A self-hosted web mail client for a single operator. It sits in front of existing IMAP/SMTP
mailboxes (first target: many addresses on one domain, hosted on Purelymail), mirrors them into a local
SQLite database so everything is fast and searchable, and adds a calm interface plus careful LLM
help: it tells you what needs you, tames newsletters, understands receipts, and answers questions
about your mail with cited sources. A single Go binary with an embedded SvelteKit frontend,
bare-metal on a small server (the potato), reached from a phone or desktop over Tailscale.

**Why it exists:** the operator wants a project to build, barely sends mail, mostly receives, and
wants a mail experience that fits the grove.place world. It must also be easy for a stranger to set
up and run for themselves, like Polaris.

**Feel:** calm and quiet. Soft, warm, whitespace, grove-green leaning, nothing shouting for
attention. Fresh design (the old Lattice Ivy UI is reference only).

**Design direction (rounds 16/16b, mockups in `design/canvas/`):** a night walk through a botanical
garden: deep green-black sky with faint stars, Grove's vine pattern tiled behind everything,
frosted-glass chrome over it, and a calm near-opaque reading panel. Lexend for the interface,
Newsreader for message text, Lucide icons, gentle firefly motion (reduced-motion aware). Moonlight
lilac accent. A daytime-garden light theme follows the system setting. Phone: bottom tab bar
(Inbox, Reading, Search, Tags, Settings) and a side panel for accounts. The Reading tab is a
newsletter feed with a daily digest. Smart summaries appear as a quiet firefly-dot chip, never
labelled as AI.

**Non-goals for v1:** calendar-invite RSVP, PGP/S-MIME, CardDAV/CalDAV (all possible later);
multiple human users (out for now, not on the roadmap); push notifications (Apple Mail keeps that
job); Gmail parity; importing old Proton mail (start fresh; revisit as an optional one-off).

## 2. Prerequisites (before building, after planning)

1. ~~Migrate grove.place mail from Forward Email to Purelymail~~ **DONE 2026-10-01** (details in
   `qa-log.md` round 15). Several mailbox users, alias routing rules and a catch-all are set up.
   Inbound and outbound confirmed for the main address in Apple Mail. Still to confirm: auth headers, a Grove (Resend) email passing DMARC
   under `p=reject`, alias/catch-all routing, and send-as from an alias.
2. A throwaway **`dev@`** Purelymail user for tests (**created**; still needs seeding with sample
   mail, TESTING.md section 7). Its credentials go in a git-ignored local `.env`, never in chat or
   the repo (the repo has no `.gitignore` yet; add one before any secret exists on disk).
3. Operator's **OpenRouter key** available for the Jev/chat/vision spikes (ask before using it).

## 3. Features (settled)

**Accounts ("branches" in lore terms, names TBD).** Many accounts from day 1, each its own IMAP
login. A per-account view and a combined "all inboxes" view, with a colored badge per address;
replies default to the address the mail was sent to (identities and signatures per address).
Accounts are customizable in settings: rename, icon, optional photo.

**Reader.** Threaded conversations (local JWZ). Full history mirrored. Sanitized HTML in a sandboxed
frame; remote images blocked by default with a per-sender allow-list (configurable). Phone and
desktop are both first-class layouts. Swipe actions, desktop keyboard shortcuts (with a help
overlay), snooze (local hide-until).

**Triage (Jev + a bigger model; see JEV.md).**
- **Needs attention:** a dedicated view plus inline markers, each with a one-line reason. Cascade:
  Jev flags mail that *could* matter (high recall); only flagged mail goes to a bigger chat model
  that decides and writes the reason.
- **Newsletters:** a separate reading feed, a daily digest, one-click unsubscribe
  (`List-Unsubscribe`, executed server-side with SSRF protection). Future idea: quiet auto-archive
  (must never touch personal correspondence).
- **Receipts and invoices:** auto-extracted fields (vendor, amount, date, renewal), a ledger view,
  renewal reminders, and a plain receipt filter as the baseline.
- **First-class mail types:** contact-form submissions (Reply-To-aware), security/abuse reports
  (the security and abuse addresses: priority, LLM off by default), personal correspondence (protected from any
  automated handling). Automated notifications get generic triage.

**Spam (settled, round 18: Junk rescue only).** The provider filters first:
Purelymail runs SpamAssassin (threshold 5), files suspected spam in the Junk folder, rejects
blocklisted IPs at SMTP, and learns per user from messages you move into and out of Junk (after
~200 examples of each). Ivy mirrors Junk like any folder, shows the spam score from the
`X-Spam-Status` header, and **moving mail to or from Junk in Ivy is an IMAP move, so it trains the
provider's filter**. Jev's only spam job is **Junk rescue** ("this looks real", a quiet chip with a
one-tap Not junk). Jev does not screen the Inbox for spam (an `is_spam` check stays an idea, not
planned). Never auto-delete or auto-move; a wrong guess must cost nothing. See `JEV.md` section 3B and `ARCHITECTURE.md` section 4.

**Search and ask.** One search box: hybrid keyword (FTS5) + meaning (embeddings from OpenRouter by
default, local Ollama optional, each message embedded once), merged into one ranked list, with quick filters (from, account, has
attachment, date, tag). **Talk to Ivy** lives at the top of the Search page (a Search / Ask Ivy
switch): an **agent loop with three read-only tools, `search_mail`, `read_mail` and `think`**,
that writes a plain answer whose claims cite emails it actually read; an account-picker (tap the
accounts to include; LLM-off accounts are locked, not just unselected); step and spend caps; it can
suggest an action but never take one. It shows a short, quiet trace of what it looked at. Details
in `ARCHITECTURE.md` section 6.

**Tags and rules.** Your own tags; automatic rules (if-this-then-that, including fuzzy plain-language
conditions answered by Jev); model-applied system tags (the only autonomous model write: local and
reversible). Smart views from saved searches are out of v1. A **People** view derived from the
mirror (correspondents, recent threads, compose autocomplete).
- **Tag colours (round 19):** a fixed palette of 12 named colours (Sky, Rose, Teal, Coral, Lilac,
  Mint, Gold, Sand, Orchid, Fern, Slate, Berry), each stored with a **night shade and a deeper day
  shade** so tags stay readable in both themes, plus a custom colour. New tag: name, colour, and an
  optional "tag matching mail automatically" that leads to a new rule. Tags you did not make
  ("placed for you": needs you, newsletters, looks real found in Junk) are local and removable.
- **People:** the Tags tab links to People (often in touch, everyone, search) and a person page
  (name, address, which account they write to, Write, All mail, tags, conversations).
- **Rules (round 20):** shown as plain sentences ("When mail is from Cloudflare and looks like a
  receipt, tag it receipts"), each with an on/off switch and a match count. **Rules are written by
  describing them:** you type one free-form sentence and **one structured generation** turns it into
  our exact rule format (plus any new smart checks), once; running a rule never calls the generator
  again. You review a plain restatement and a dry run on your last 200 messages ("5 would be tagged,
  6 more from Cloudflare left alone") and then turn it on; nothing runs before that. A
  detail editor stays for tweaking. **Rule actions are local only: add a tag, show in Reading,
  snooze.** Moving, deleting, forwarding and sending are never a rule action (they always ask).
- **Smart checks:** user-owned classification questions Jev answers per message ("looks like a
  receipt", "a job application"), listed on a Smart checks screen alongside the built-ins, each with
  an editable plain description, a "how sure" setting (Eager / Balanced / Careful), the accounts it
  runs on (locked for LLM-off accounts) and a try-on-recent-mail action. Details in `JEV.md` 3F.

**Failure states (round 20).** Calm, specific screens and banners for: server unreachable, sync
problems (auth failed, unreachable, backfilling) with a **Mirror health** screen, a message or
attachment that failed to load, send failures (too large, rejected, retrying) with the draft kept,
toasts with undo and "will keep trying", smart-feature limits and provider errors in Ask Ivy, and
empty/no-result states. Principle: say what happened, what is safe, and what to do; never lose or
silently drop anything. See `ARCHITECTURE.md` section 9b.

**Compose (milestone 3, after sync and before triage).** Markdown and a rich-text editor (markdown first), per-address signatures,
**undo send with a configurable delay** (setting; default 10 s, 0 = off), drafts in the server's
Drafts folder (visible in Apple Mail), reply-as-the-right-address. **Images and attachments can be
added to outgoing mail** (photos, camera, files; inline images; EXIF stripped by default; optional
photo downscale; size checked against the provider's limit). Replies simply say "Replying to
name@domain.com" (the contact-form Reply-To case works underneath without extra UI).

**Images and attachments.** Attachments mirrored locally and text-extracted (bodies/.ics, digital
PDFs and Office files in pure Go). A **vision model** reads images on opted-in accounts (soon):
automatically only for mail Jev flags, plus an on-demand "read this image" action, with spend
caps and dedupe. OCR (Tesseract) is far-out, only if the operator finds it useful.

**LLM privacy and safety (settled).** Per-account opt-in, off by default. Embeddings are hosted
(OpenRouter) by default and therefore sit behind the same opt-in gate (changed in round 30, which
replaced "embeddings always local"); a local Ollama endpoint is the private option per account.
The model may write local tags automatically; sending, deleting, moving, forwarding and
unsubscribing always need a click. Model output is plain text; citations are DB-verified message
ids; automated calls see one account; ask mixes only operator-selected accounts.

**Settings and config.** Lots of behavior is configurable (an explicit operator wish): behavior in
an in-app settings panel with per-account overrides; secrets in `ivy.yaml`/`.env`. Backup: **once a
day, 15 days kept, older pruned**, of the local `state.db` (the mirror is a separate, never backed
up file) plus any server-deleted ("disabled") messages, to a folder or S3-compatible target (ideally one off the
potato), one-line restore. **Server-deleted mail is disabled, not erased:** hidden everywhere like a
deletion, never purged automatically, restorable (`ARCHITECTURE.md` section 4).

**Stats panel (like Polaris's).** One place to see everything the LLM layer did and cost, viewable
at a glance and drillable to individual calls. The screens (`/settings/spend`, `/settings/spend/calls`)
are built against a mock ledger; chunk 5 supplies the real one (`BUILD-LOG.md`, round 33):
- Totals for today / 7 days / 30 days / all time, broken down by **feature** (needs-me stage 1,
  stage 2, categories, rules, digest, ask, extraction, vision, embeddings), by **account**, by
  **provider/model**, with exact costs from provider responses (`usage.cost`).
- **Per-call log** (the full ledger): time, account, feature, model, tokens, latency, cost, outcome
  (acted / quiet / error / skipped by gate), filterable and exportable; Jev calls show the
  probability vector.
- **Caps and gates:** monthly spend vs caps, and how many calls the gates blocked (opt-in off, cap
  hit, withheld mail).
- **Mirror health:** per-account sync state, last sync, backlog, errors, DB size, embedding queue,
  memory in use.

**In-app help glossary** with plain-English names, as Polaris's help modal does. Lore feature names
(chosen later, checked against `Lattice/docs/philosophy/grove-naming.md`; Canopy and Rings are
already taken there) get an entry in the same change that adds them.

**Naming.** Product name **Ivy**. The old public repo (Grove's earlier mail client) was renamed
`AutumnsGrove/Ivy-legacy` and archived on 2026-10-01 (it had 0 stars/forks and 4 stale issues about
the old TypeScript app); this project now lives at `AutumnsGrove/Ivy`, currently **private**. Making
it public is the operator's call. License **AGPL-3.0**.

## 4. Stack and decisions (details in ARCHITECTURE.md)

Go backend; **pure-Go SQLite** (WAL, FTS5; D1-friendly SQL); SvelteKit (`adapter-static`, Svelte 5,
pnpm) with **pure CSS, no Tailwind** and vendored Grove design tokens; enmime for MIME; go-imap v2
for IMAP (verify the v2 API); bluemonday for sanitizing; embeddings via OpenRouter (Ollama optional); OpenRouter for Jev
(`/systemone`), chat and vision. Deployment: GitHub Actions builds a multi-arch container image
(frontend and Go binary) on merge to main and publishes it to GHCR; the potato only pulls it, via a
host-side update watcher (`ivy update`, also an in-app button). Access control (passkeys / Face ID, password fallback) is later; Tailscale-only for now.
Raw RFC 822 messages are stored so everything derived can be rebuilt and exported.

## 5. Milestones (order settled: read -> sync -> send -> triage)

Reordered 2026-10-03 (`qa-log.md`): send moved ahead of triage so Ivy can replace the operator's
mail client as early as possible; the LLM features come after. Send needs no part of the LLM gate.

Every milestone's exit criteria include the TESTING.md definition of done (unit + integration +
E2E on both viewports + a live check on the dev mailbox/potato).

-1. **Spikes (settled, round 23): run first, before any build implementation** (`SPIKES.md`,
   S1-S10: Purelymail live facts, go-imap v2 and its test server, the potato build, Jev, Safari,
   PDF extraction, compression cost, embeddings, committed-build strategy, disk size). Each ends in
   a written finding in `docs/spikes/` and doc updates. *Exit:* every spike answered; no settled
   decision silently contradicted.
0. **Harness (settled, round 21).** Before any feature: `internal/mailworld` (fake IMAP/SMTP/
   OpenRouter/Ollama/clock with fault injection), the **offline seeded local dev stack
   (`make dev`, `DEV.md`, round 22)**, the `ivy-dev` CLI, the day-one E2E smoke slice
   (boot, `init`, deliver, read, flag, restart; WebKit + Chromium), CI with lint/`-race`/cgo-free
   checks, the OpenAPI + sqlc codegen pipeline, the compression skeleton with its budget tests, and
   `make potato-bench`. *Exit:* smoke slice green in CI and on the potato, benchmarks recorded.
1. **Read.** Multi-account reader (per-account + combined view with badges), threaded, sanitized
   HTML, remote-image policy, account customization, adaptive phone/desktop layouts, settings
   skeleton, stats panel skeleton, seed tool and fakes. *Exit:* browse a seeded and the real
   dev mailbox on phone and desktop; all security/sanitizer tests green.
2. **Sync.** The real mirror: full-history backfill, CONDSTORE/QRESYNC/IDLE, write path + outbox
   (flags, move, archive, delete), tags/rules/snooze, attachments mirrored + tier 0-1 extraction,
   FTS5 + embeddings + hybrid search, People view, backup/restore, `ivy update`. *Exit:* the
   convergence property test passes; Apple Mail and Ivy stay in sync on the dev mailbox; potato
   resource budgets recorded.
3. **Send.** Compose (markdown, then rich text), identities/signatures, undo send, drafts, replies
   (Reply-To aware), SMTP + Sent handling. *Exit:* send-as verified live per address.
4. **Triage.** Jev layer + question registry, the needs-me cascade, categories, newsletters
   (feed, digest, unsubscribe), receipts/ledger/renewals, vision, ask-your-mailbox with the account
   picker, full stats panel and the LLM gate. *Exit:* eval report on a labeled corpus; the safety
   assertions (opt-in, isolation, injection) green; caps work.

## 6. Risks and spikes (each owned by a milestone)

| Risk / spike | Milestone | Notes |
|---|---|---|
| `ANNOTATION` or custom keywords as a server-side home for tags; check `PERMANENTFLAGS` | 2 | CAPABILITY already read live; ANNOTATION advertised |
| ~~Does Purelymail file a copy in Sent, or must Ivy `APPEND`?~~ Answered by S1: it does not, Ivy `APPEND`s. Send-as from a routed alias works (S1c); its exact scope is unprobed | 3 (spike early in 2) | Docs say send-as works; verify live |
| Purelymail connection limits for N accounts (IDLE + work connections) | 2 | Budget RAM/connections on the potato |
| Pure-Go SQLite compile time/RAM on the potato; FTS5 availability | 1 | Build while serving is the squeeze; cache warm |
| Embedding scan cost and memory at 50k-100k messages | 2 | Measured (S8): int8 768d, 73 MiB and 340 ms at 100k on the potato. Embedding itself is 17 s per chunk there, so backfill runs on a faster host (configurable endpoint) |
| Jev `noul`/`score` shapes; accuracy and thresholds per question; per-email cost; injection behavior | 4 | JEV.md section 5; needs the operator's key |
| Vision: do scanned PDFs go straight to a model? default cheap multimodal model | 4 | Pure Go cannot rasterize PDFs |
| HTML sanitization edge cases and tracker coverage | 1 | Fuzz + XSS corpus + browser checks |
| Prompt injection via email into stage 2 / ask / vision | 4 | Gate, tripwire, plain-text output, tests |
| Update flow: digest resolve, CI race, health check, rollback | 2 | `ivy update` tests with a fake registry and watcher |
| Committed frontend build output bloats git history | all | Accepted; CI builds it on merge to main, PRs never touch it (round 24). No compiled Go binary is ever committed or released (round 25) |
| go-imap v2 API vs the v1 snippet seen in the original thread | 1 | Verify before pinning |
| Locally owned state (and disabled mail) lost if the potato's storage dies | 2 | Daily backups of `state.db`, 15 days, off-device target recommended (settled; mirror rebuilt from IMAP) |

## 6b. Decisions from round 23

- **Spikes before Milestone 0**; PWA is not a goal for now (plain Safari over Tailscale; Firefox
  occasionally), so "Can't reach Ivy" is the accepted offline behaviour for v1 and offline reading is
  dropped from the open list. A PWA/push is a possible future.
- **Dev stack defaults to live OpenRouter** (capped, cached), with a two-account `--pair` preset for
  testing sends between accounts (`DEV.md`).

## 6c. Decisions from round 24 (all settled)

- **Disabled, not deleted:** server-deleted messages get a `disabled` flag and are hidden as if
  deleted; never purged automatically; restorable; included in backups.
- **Rolling backups:** (round 30 changed this to once a day, keep 15 days, of `state.db` only;
  floor of 10 newest, prune only after a verified new backup).
- **Disk is not a concern** (256 GB): no storage budget or eviction.
- **Everything is compiled in GitHub Actions** and shipped as a container image (round 29); the
  potato builds nothing (a cold Go build there exhausts swap, spike S3). Nothing compiled is
  committed to the repo.

## 7. Open items for the operator

1. **API design (item 6 of the defaults) wasn't explicitly confirmed:** assumed JSON REST + SSE
   with one typed client module. Veto or approve.
2. **Lore feature names:** deferred. Pick when features exist.
3. **Repo visibility:** `AutumnsGrove/Ivy` is private for now; decide when to make it public.
4. **First spike session:** when you're ready, an Ivy-specific Jev spike with your OpenRouter key
   and a small labeled sample (JEV.md section 5), plus the Purelymail live checks once the
   migration and `dev@` exist.
5. **Review this plan set.** Anything wrong, missing or overbuilt? Then decide: migrate Purelymail
   next, or run the spikes first.
