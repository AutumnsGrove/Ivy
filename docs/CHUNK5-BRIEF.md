# Chunk 5 brief (triage: the LLM layer)

Read this **after** `CLAUDE.md`, `docs/STANDARDS.md` and `next_steps.md`, and before writing any
code. It is the standing instruction for chunk 5, written the way `docs/CHUNK4-BRIEF.md` was for
chunk 4: the decisions already made, the invariants that must hold, the traps earlier work found,
and the points where you must stop and hand the work to Claude. **Read `docs/CHUNK3-BRIEF.md` and
`docs/CHUNK4-BRIEF.md` too**: their invariants (IMAP first, nothing erased, hostile input,
append-only migrations, bounded everything, live checks are the operator's) carry over unchanged
unless this file says otherwise. If this file and another doc disagree, stop (gate T4) and ask.

Chunk 5 is **extras on top of a finished backend.** Ivy already reads, searches, tags, sorts and
sends without a single LLM call; every feature here is optional, per-account opt-in and off by
default. That shapes the whole chunk: an outage, a missing key or a disabled account must leave Ivy
exactly as useful as it is today, never degraded.

The detail lives in one plan file per feature under `docs/chunk5/` (start at
`docs/chunk5/README.md`: the order, the dependency graph and the status table). **Foundation first:
5a (gate, ledger, spend) and 5b (the Jev layer) before any feature plan.** Do not start a stage
before the one it depends on is finished and its checkpoint (section 5) is cleared.

## 1. Invariants (a violation is a bug even if every test is green)

1. **One chokepoint.** Every remote call that can cost money (Jev, chat, vision, embeddings,
   anything new) goes through `llm.Gate`. The provider clients stay unexported; the architecture
   test that fails when another package names a provider endpoint (`llm/arch_test.go`) is extended
   for every new endpoint, never relaxed.
2. **Off means off.** An account with smart features off makes **zero** outbound calls, for every
   feature, including the ones that look harmless (the rule compiler, the dry run, a spike script).
   This is tested per feature with a recording fake that fails the test on any request.
3. **One ledger row per call**, and for a batch one row per input, with the exact cost the provider
   returned (`cost_estimated` only when it reports none). Gate-blocked and failed calls are recorded
   at zero cost so volume is visible. The row and the monthly counter are written in one
   transaction. Only the gate writes the ledger.
4. **One account per automated call.** Automated features see one account's mail at a time. Ask Ivy
   mixes only accounts the operator selected, and an LLM-off account is locked there, not merely
   unselected.
5. **Model output is data, never authority.** Jev's typed answers are probabilities, and free-text
   output (`complete()`, `see()`) is untrusted: validated against a schema, rendered as plain text,
   never as HTML, and never labelled as AI on the reading surface. Citations point at message ids
   the run actually read, verified against the database.
6. **Nothing acts on a probability beyond a local, reversible tag.** Sending, deleting, moving,
   forwarding and unsubscribing always need a click. Rule actions stay local-only (add a tag, show
   in Reading, snooze). Ask Ivy can propose an action as a button; it cannot take one.
7. **A wrong guess costs nothing.** Every question has a quiet default option and a threshold, and
   acts only when its answer is non-quiet **and** clears the threshold. Nothing is hidden, moved or
   deleted by a score: at most a chip, a tag, a sort order.
8. **Hostile mail cannot steer the layer.** State sent to a model is text only, stripped and clipped
   under the context limit with margin (never HTML, never raw headers beyond the chosen few).
   Mail the tripwire or the sensitive check flags is **withheld** from stage 2, ask and vision.
   Security and abuse addresses are LLM-off by default.
9. **Derived results are cached by content key and never recomputed by a move.** A decision is keyed
   by (account, content key, question id, instruction hash, model); an archive, move, flag or tag
   change never re-asks, because regenerating costs money. Editing a question changes its hash and
   re-reads lazily; a backfill is opt-in with a cost estimate and the monthly cap applied.
10. **Nil-client safe.** Jev, the chat model and the vision model are optional. With the client
    absent or failing, triage shows nothing new and the app is otherwise unchanged. Failures are
    structured and visible (a quiet banner, a ledger row), never a thrown error on the reading path.
11. **Everything bounded** (STANDARDS 4a): calls per tick, concurrency, backfill burst, state size
    per call, tool steps and tokens per Ask question, per-message and monthly spend. Each has a
    defined outcome above it, shown, never silent.
12. **Append-only migrations.** The ledger and caps live in `state.db`; derived answers
    (`decisions`, `needs_me`, `receipts`) live in `mirror.db` keyed by content key. Never edit an
    existing migration.
13. **Secrets never leave the process** in logs, errors or API bodies. Mail content never appears in
    logs (STANDARDS), including prompts and model replies; the ledger stores ids, counts and costs,
    not text.

## 2. Decisions that are settled (do not re-litigate; `docs/qa-log.md` and `docs/JEV.md` hold the reasoning)

- **Jev through OpenRouter `/systemone`, model `jev-latest`**, behind a `decide()` interface whose
  fallback is a chat model with a JSON schema. `complete()` and `see()` sit beside it, all behind
  the gate (`docs/JEV.md` 2, `ARCHITECTURE.md` 7).
- **Model yes/no as a `choice` with `yes`/`no` (+ `none`) options.** `noul` and `score` exist but
  their calibration on real mail is unmeasured (S4).
- **One Jev call per message with every enabled question in it.** Question text counts as input
  tokens (about 90 to 110 per question), so the question set dominates the cost of short mail.
- **Cascade:** Jev `needs_me` first pass (high recall) → only flagged mail goes to the chat model
  for the verdict and a one-line plain-text reason, no tools, structured output.
- **Spam: Junk rescue only** (round 18). Jev never screens the Inbox for spam; `is_spam` is an idea,
  not planned. Nothing here ever deletes or moves by itself.
- **Rules are described, not built** (round 20): one structured `complete()` call compiles a
  sentence into a validated rule, once; running a rule never calls the compiler. Review and a dry run
  on the last 200 messages come before a rule is live.
- **Ask Ivy is an agent loop with three read-only tools** (`search_mail`, `read_mail`, `think`),
  step and spend caps, DB-verified citations, a plain-text answer, a quiet trace.
- **Vision is automatic only for mail Jev flags,** plus an on-demand "read this image", with size
  and dimension filters, content-hash dedupe and a monthly cap. OCR is far-out.
- **Structured data before a model:** schema.org JSON-LD in receipts is tried before any extraction
  call.
- **Smart summaries appear as a quiet firefly-dot chip, never labelled as AI** (PLAN.md 1).
- **The stats panel is one place for everything the LLM layer did and cost** (PLAN.md 3): totals by
  feature, account and model; the per-call log; caps and blocked-call counts; mirror health.

The operator settled every plan's open questions on 2026-10-07; each plan's "Decisions" section holds
the answers and `docs/qa-log.md` the record. The ones that shape the whole chunk:

- **Models come from a code-defined registry** (`llm/models.go`, the Polaris pattern), the settings
  panel picks a default chat model with an optional per-feature override, and pricing lives there.
- **Caps are per account plus one global,** work pauses at the cap and resumes next period, and a
  cost estimate is mandatory before any bulk action. In the interface Jev is the **helper decision
  model.**
- **New mail only;** backfill is an opt-in button with an estimate. Eligible mail is the Inbox, plus
  Junk for `junk_rescue` only.
- **Thresholds (0.65 / 0.80 / 0.90) are provisional** until real probability spreads exist.
- **Withheld mail fails closed** and shows a quiet marker; authentication verdicts are trusted only
  from the provider's own `authserv-id`.
- **Needs-me and categories also place local "placed for you" tags** (never IMAP keywords).
- **Renewal reminders are IMAP-appended emails** (never SMTP) plus a ledger list.
- **Ask Ivy has 25 steps, saved deletable history, and no per-question cost ceiling.**
- **Limits (operator): 500 rules, 50 checks, 100 questions per Jev call,** to be spiked beyond 20.

## 3. Traps found by earlier work

- **Instruction wording moves precision a lot** (S4: `needs_me` 0.73 to the 0.9s at a 0.75
  threshold). Write each instruction as what does *not* count, then the exceptions, and tune from
  the odds sheet, not by feel.
- **Real-mail accuracy is unmeasured.** S4 ran on a synthetic corpus; there is no labeled real
  corpus yet. Start every threshold conservative (0.75 to 0.85) and treat the first live weeks as
  the calibration, with the odds sheet as the instrument. A feature is not "tuned" until it has been
  checked on real mail the operator labelled.
- **The fake `/systemone` has no `score` questions** (papercuts N3), and the mock-backed screens
  (`/ask`, `/checks`, spend, reading digest) have E2E tests that fake the network boundary. When a
  screen goes real, its mock route and mock E2E fixtures are replaced in the same change, not left
  to rot (the dead `rules/new/review` route is the cautionary example).
- **Identical `Message-ID`s share a content key** (N8), so a verdict or a decision is shared by
  copies. Accepted for tags; for decisions state it in the plan that relies on it.
- **A long message's state, not the questions, dominates cost,** and a 32k-token overrun is a clean
  `400 max_tokens_exceeded`, not truncation: Ivy must clip itself, deliberately, and test the edge.
- **Beta and proprietary.** Jev can change or vanish. Nothing may depend on it being present, and
  the interface must let the chat-model fallback implement the same questions.
- **The embeddings gate is the only gate that exists** (`llm/gate.go`, `EmbedRequest`, endpoints
  `embeddings` and `ollama_embed`). 5a generalises it; do not build a second gate beside it.
- **Hostile text can skew numbers even when it cannot exfiltrate.** The tripwire is a cheap filter,
  not a guarantee. Design every feature so a skewed probability costs a chip, not an action.
- **Backfill bursts.** The mirror holds full history; the first enable on an account must not queue
  years of mail into the ledger. See each plan's "Backfill" line and the gate's burst bound.
- **The operator's laptop overheats on large profile runs** (memory, 2026-10): measure small and
  extrapolate; potato numbers are the operator's.

## 4. Stage bounds

The stages, the order and the dependencies are in `docs/chunk5/README.md`; each stage's scope,
tests, failure paths and open questions are in its own plan file. Common bounds for every stage:

- **Test first, and watch it fail** (CLAUDE.md), against `internal/mailworld`'s fake OpenRouter,
  never a live provider. The live accuracy checks are separate, operator-run and cost cents.
- **A stage is not done until its mock is gone:** the real endpoint replaces the mock behind
  `web/src/lib/api/http.ts`, and the E2E for that screen runs against the real request shape.
- **Every feature ships dark.** It lands behind the per-account opt-in and a per-feature setting,
  and its default in a fresh account is off.
- **Costs are stated.** Each plan names its cost per message at the expected question set and its
  worst case; the stage's test asserts the cap behaviour, not just the happy path.

## 5. Escalation gates

The same machinery as `docs/CHUNK3-BRIEF.md` section 5: a reviewer starts from `CLAUDE.md`,
`next_steps.md`, this file and `docs/handoffs/`, so every checkpoint and stop is a **committed
file** (`docs/handoffs/<date>-<gate>-<slug>.md`), with a "waiting at G2" line in `next_steps.md` and
one sentence to the operator. The tests are the spec, so a review is of the tests and the design.

| # | When | What you bring |
|---|---|---|
| G1 | 5a: **before generalising the gate** | The half-page design: the `decide`/`complete`/`see` request types, how the single ledger row and counter transaction extends to them, the withheld-mail and ask-selection hooks, and the extended architecture test |
| G2 | 5b: **before the first live Jev call** | The registry format, the cache key, the cost estimate for the shipped question set, and a request to the operator for the OpenRouter key (ask first; it spends money) |
| G3 | 5c: before tuning any threshold | The labeling plan (which ~100 messages the operator labels, how) and the eval report format; no threshold is "tuned" without real labels |
| G4 | 5h: **before choosing any image dependency or sending any image off the device** | What is needed, what stdlib covers, the `STACK.md` entry, the size and dimension limits, and what leaves the device |
| G5 | 5i: **before coding the agent loop** | The injection design: tool-result wrapping, withheld mail invisibility, the step and token caps, the citation check, and the hostile corpus it will be run against |
| G6 | End of chunk | The whole chunk, for Claude's review with the review skill |

**Triggers.** Chunks 3 and 4's T1-T13 apply unchanged. Chunk 5 adds:

- **T14** Any path to a provider endpoint that does not go through `llm.Gate`, or a new endpoint the
  architecture test does not cover.
- **T15** Any outbound call for an account with smart features off, or a call that mixes two
  accounts' mail outside Ask's operator-selected picker.
- **T16** Any model output that causes an action other than a local tag, a chip or a sort order, or
  any model-written text rendered as HTML or labelled as AI on the reading surface.
- **T17** Any path where withheld mail (tripwire, sensitive), a security or abuse address's mail, or
  an LLM-off account's mail reaches stage 2, vision, ask or a summary.
- **T18** Any spend path that is not bounded by the monthly caps, writes no ledger row, or can exceed
  its stated worst case. (Ask Ivy has no per-question cost ceiling by the operator's decision; its
  bound is 25 steps, the per-step token bound and the shared caps.)

**How to stop** is as in chunk 3: a handoff file that makes sense to someone who has seen none of the
session, committed, a line in `next_steps.md`, one message beginning `STOP: this needs Claude:`, and
no other work until Claude has answered.

## 6. Definition of done (every stage)

As chunk 3's section 6. In addition:

- **Off-path proof:** the stage's test shows the feature makes no request with the account off and
  none after the cap is reached.
- **Odds are visible:** anything Jev decides can be inspected on the message's odds sheet.
- **Live checks are the operator's** and their own pending line, never folded into a stage marked
  done: a live run with the operator's key on a dev mailbox, the cost shown on the stats panel
  matching the provider's dashboard, and, for any classifier, the accuracy check on real labelled
  mail. They are recorded in `next_steps.md`.
