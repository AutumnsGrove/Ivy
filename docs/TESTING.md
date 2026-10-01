# Testing strategy

Operator requirement: **test absolutely everything.** This doc turns that into mechanisms that
make untested code hard to merge, in Polaris's live-verification culture. Status: DRAFT (2026-10-01).

## 0. Principles

0. **Test-driven, integration-weighted** (full rules in `STANDARDS.md` sections 1-3). Write the test
   first, watch it fail for the right reason, make it pass, refactor. Most tests drive the real
   core against the **mail world** fake (`internal/mailworld`, driven from Go, Playwright and the
   `ivy-dev` CLI); unit tests are for pure branchy logic only. A thin end-to-end smoke slice (boot,
   `init`, deliver a message, read it, flag it, restart) exists before any feature and grows with
   each milestone. Performance budgets (`PERFORMANCE.md`) are tests too.
1. **A feature is done only when it has all its layers**: unit + integration + (UI) end-to-end +
   one live check against real hardware/mail. "Done" is defined per layer in section 9.
2. **Every bug gets a regression test, and the test must be seen failing without the fix**
   (revert the fix, confirm red, restore). This caught real issues repeatedly in Polaris.
3. **Fakes for plumbing, the real thing for judgment.** Fake IMAP/OpenRouter prove wiring; real
   Purelymail and real Jev catch quirks and calibration problems. Both are required.
4. **Tests encode the safety line.** Privacy and injection rules are asserted, not just documented.
5. **Determinism first.** Injected clocks and ID generators; no sleeps; seeded randomness with the
   seed printed on failure.

## 1. Layers

| Layer | Tool | What it covers |
|---|---|---|
| Unit | `go test`, table-driven | Pure logic: parsing glue, sanitizer, threading, rules engine, spend caps, config, extraction, Jev thresholding/suppression |
| Fuzz | Go native fuzzing, seed corpora in repo | Sanitizer, MIME glue, header parsing, `Authentication-Results`, rule conditions, unsubscribe URL handling |
| Property / model-based | `testing/quick` or `rapid`-style generators | Sync convergence (section 2), JWZ threading invariants |
| Integration | In-memory IMAP + SMTP servers, temp SQLite | Sync engine, write path, outbox, send/undo, backup/restore, migrations |
| API contract | `httptest` + generated types | Every endpoint's request/response vs the typed contract; SSE streams |
| LLM plumbing | Fake OpenRouter (Jev `/systemone` + chat + vision) | Gating, caps, isolation, cascade, injection handling, ledger rows |
| LLM judgment (evals) | Real Jev/chat on a labeled corpus, run on demand | Thresholds, calibration, injection resistance; costs cents, logged in the ledger |
| Frontend unit/component | Vitest + Svelte testing library | Stores, API client, components, keyboard shortcuts |
| End-to-end | Playwright against the real binary + fake IMAP + fake OpenRouter | Phone and desktop viewports, touch swipes, keyboard, offline/reconnect |
| Visual regression | Playwright screenshots, pixel diff | Layout and CSS-scoping regressions (Polaris's browser before/after diff caught a real one) |
| Accessibility | axe in Playwright | Contrast, labels, focus order |
| Live | Real dev@ Purelymail mailbox, real Jev, the potato | Server quirks, resource budgets, update flow |
| Performance | `go test -bench` + budget assertions | Query times, memory ceilings, backfill throughput |

## 2. Sync correctness (the highest-risk code)

The mirror's one invariant: **after sync quiesces, the DB equals the server** (messages, folders,
flags, threads). Everything else is detail.

- **In-memory IMAP server** (go-imap v2's server + memory backend; verify package path) with a
  scripted "other client" that mutates the mailbox the way Apple Mail would: flag, move, delete,
  expunge, append, rename/delete folders, bump UIDVALIDITY.
- **Model-based test:** generate random operation sequences (seeded), apply to the server, run
  sync, assert DB == server. Shrink failing sequences to a minimal reproducer.
- **Scenario tests:** UIDVALIDITY change mid-sync; expunge during fetch; moves across folders;
  duplicate Message-IDs; huge messages; connection dropped mid-FETCH (fault-injecting `net.Conn`
  wrapper); server without CONDSTORE/QRESYNC (fallback path); IDLE timeout and reconnect;
  clock skew; very large mailbox backfill resumes after a crash with no duplicates and no gaps.
- **Write-path tests:** IMAP-first ordering (a failed IMAP call never leaves a DB-only change);
  outbox retries; optimistic UI rollback on server rejection.

## 3. Parsing, rendering, security

- **Corpus (not committed if it contains real mail):** public corpora + enmime/go-message test
  data + hand-built nasty cases (mismatched charsets, nested `message/rfc822`, broken boundaries,
  inline images, 2007-era mailing lists, TNEF, huge headers). Operator's real mail is only ever
  used locally via scrubbed copies.
- **Sanitizer:** a known-XSS payload corpus must produce output with no scripts, event handlers,
  `javascript:` URLs, remote loads, form posts or style-based exfiltration; verified in a real
  browser (Playwright) and by CSP header tests. Fuzz the sanitizer.
- **Remote-content policy:** blocked by default; per-sender allow-list; tracking pixels never load
  with the default; asserted in E2E by watching network requests.
- **SSRF guard (unsubscribe + any server-side fetch):** private/loopback/link-local IPs, redirects
  to private IPs, DNS rebinding, IPv6 tricks, non-HTTP schemes, oversized responses.
- **CSRF/Origin:** same-origin protection must work in dev (Vite proxy) and prod; Polaris once broke
  every mutation in dev here, so a test covers both.
- **Threading:** JWZ invariants (every message in exactly one thread, no cycles, stable under
  re-ordering) + fixtures for subject-fallback edge cases.

## 4. LLM safety and spend (asserted, not trusted)

Using the fake OpenRouter's call log (`/_control/calls`-style, as in Polaris) to assert what Ivy
actually sent:

- **Per-account opt-in:** an LLM-off account produces **zero** outbound calls, through every path
  (triage, digest, ask, vision, backfill).
- **Isolation:** automated calls contain exactly one account's mail; ask calls contain only the
  accounts the operator selected, and locked accounts are never selectable.
- **Cascade:** unflagged mail never reaches stage 2; withheld mail (tripwire/sensitive) reaches
  neither stage 2, ask nor vision.
- **Injection corpus:** hostile emails must not cause any action beyond local tags; model output is
  rendered as plain text; citations resolve only to verified message ids; no model-invented URLs.
- **Talk to Ivy (agent loop):** the tools are read-only (a test enumerates the tool registry and
  fails if one can write); `search_mail`/`read_mail` never return LLM-off, locked or withheld mail
  even when asked by id; the loop stops at the step and token caps and says so; a citation to a
  message the loop did not read is rejected; hostile mail that tries to steer the loop (call another
  tool, claim a fake citation, ask for an action) must not change the tool calls beyond what the
  question needs; every step has a ledger row.
- **Rule compiler:** a fixture of ~30 plain sentences (including ambiguous ones and ones asking for
  forbidden actions like "delete these" or "forward to my other address") against the fake
  OpenRouter: output must validate, reuse existing checks and tags, never contain a non-local action,
  and return a clarification when ambiguous. Hostile output (extra fields, unknown actions, huge
  strings) is rejected by the validator. The dry run's header part makes zero LLM calls; the check
  part is ledgered and capped. A saved rule never triggers a compiler call when it runs.
- **Failure states (fake servers + faults):** the fake IMAP/SMTP servers can inject auth failure,
  dropped connections, timeouts, 4xx and 5xx (including 552 too large), slow bodies and missing
  parts. Assert: no data loss, the outbox keeps queued actions until success, banners and Mirror
  health reflect the real state and clear on recovery, a failed send keeps its draft, and Playwright
  runs each state on phone and desktop (including the browser going offline) with visual baselines.
- **Attachments on send:** size-limit and type rules, EXIF stripped by default, inline `cid:` images
  render in the sent copy, draft round-trip keeps attachments, undo-send releases the temp files.
- **Spend caps:** hitting a cap stops calls and surfaces a clear state; ledger rows match calls 1:1.
- **Architecture test:** only the `llm`/`jev` packages may talk to the network provider; a test fails
  if another package imports an HTTP client for it (the single chokepoint stays single).
- **Evals (real Jev/chat):** labeled corpus (~100+ messages, grows over time) with precision/recall
  per question and a threshold report; run on demand and before changing instructions or models.

## 5. Frontend

- **Component/unit:** stores, API client (against the generated contract), keyboard-shortcut map,
  swipe state machine, account picker (greyed vs locked), settings forms.
- **E2E (Playwright), both form factors:** inbox, combined view with badges, thread reading, compose
  and undo-send (configurable delay), search, ask account-picker, settings, stats panel, snooze,
  tags/rules, swipe gestures via touch emulation, keyboard shortcuts via the help overlay.
- **Visual baselines** per screen at phone and desktop sizes; intentional redesigns update baselines
  in the same commit.
- **Resilience:** server restart/reconnect, SSE drop, offline banner, partial sync states.

## 6. Data and operations

- **Migrations:** append-only (positional `user_version`, per Polaris lesson); a test upgrades a
  snapshot DB from **every** prior schema version to current and checks data survives; a test fails
  if a migration is inserted mid-list or edited.
- **Backup/restore:** snapshot of locally owned state, restore into a fresh instance, mirror
  rebuilds from IMAP, round-trip equality of tags/rules/settings/snooze.
- **`ivy update`:** temp git repos simulate: clean fast-forward; diverged checkout (refuses);
  failed build (old binary keeps running); failed health check (rolls back); in-app button path.
- **Config:** every setting has a default test, an invalid-value test and a hot-reload test.

## 7. Live and performance verification

- **Dev mailbox:** a throwaway `dev@` Purelymail user seeded with fixtures; a `live` build tag runs
  sync, flags, moves, SMTP send-as, Sent/Drafts behavior, CAPABILITY, `PERMANENTFLAGS`,
  `ANNOTATION` against it with credentials from the environment. Never runs in default CI.
- **Potato checks:** RAM ceiling and CPU during backfill + embeddings + update build, with Polaris,
  SearXNG and Ollama running; flash-write volume (WAL, batching). Numbers recorded in
  `docs/perf.md`; budgets become assertions (e.g. inbox list query under N ms at 100k messages,
  resident memory under N MB during sync).
- **Seed tool:** generates large synthetic mailboxes (100k+ messages) for performance and UI tests.

## 7b. Fidelity and browsers (settled, round 21)

- The mail world is the default for every test; the same scenarios run under the `live` tag against
  the real `dev@` mailbox to surface provider quirks (the fake is a model, not the truth).
- Playwright runs **WebKit** (closest to iOS/iPadOS Safari, the primary target) and **Chromium**
  (throttled timing budgets) on every run, and a smaller **Firefox** suite nightly. WebKit on Linux
  approximates but is not iOS Safari: a short manual pass on the real iPhone/iPad is part of each
  milestone's exit, recorded in the PR.
- `make potato-bench`: cross-build for linux/arm64, copy to `ssh potato-remote`, run benchmarks and
  the memory budget, append results to `docs/perf.md`. Manually triggered, never in default CI.

## 8. CI

GitHub Actions: `go vet`, `staticcheck`, `go test -race ./...` (fuzz seed corpora included),
frontend lint/typecheck/Vitest, Playwright E2E (both viewports), build of the embedded frontend
checked against the committed output (a drift check, since the frontend build is committed),
migration-upgrade tests, license header/AGPL check. Coverage floors on the critical packages
(sync, sanitize, llm gate, rules, update). Live and eval suites are manual/nightly, never required
for a merge.

## 9. Definition of done (per change)

- [ ] Unit tests for new logic; table cases include the failure modes, not just the happy path.
- [ ] Integration test through the real component boundary (IMAP/SQLite/HTTP), not only mocks.
- [ ] For UI: Playwright flow on phone and desktop viewports + visual baseline.
- [ ] For anything LLM-touching: fake-OpenRouter plumbing test + safety assertions (section 4);
      eval run if instructions/thresholds/models changed.
- [ ] For bugfixes: regression test verified failing without the fix.
- [ ] Live check against the dev mailbox and/or the potato when it touches sync, send, update,
      deployment or resource use. Record what was checked and the result in the commit/PR.
- [ ] Docs updated (FEATURES/SETUP/glossary entries as they exist).
