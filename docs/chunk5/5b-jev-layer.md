# 5b. The Jev layer

## Purpose

`decide()`: the app asks a set of typed questions about a message and gets answers with
probabilities. This stage builds the question registry, the state builder, the cache and the odds
sheet, and a worker that runs it. It ships **no user-visible feature of its own**; 5c to 5j are all
questions in this registry.

## Depends on

5a (the gate and its `Decide` request type).

## Where we start

- Nothing of the layer exists in Go. `docs/JEV.md` is the verified playbook (endpoint, shapes,
  limits, costs) and the question catalog; `docs/spikes/s4-jev.md` has the S4 numbers.
- `internal/mailworld` fakes `/systemone` with queued answers (`QueueJev`) and records calls; it has
  no `score` questions (N3).
- The embed-once queue in `search/` is the pattern for a low-priority background worker keyed by
  content key.
- `sync/` has a settle pass after derivation where the ingest rule pass already runs.

## Settled

- **Endpoint and model:** `POST {openrouter_base}/systemone`, model `jev-latest` (pin a versioned id
  once thresholds are tuned); same key as chat (JEV.md 1).
- **Question shape:** `choice` with `criteria` as the option set; answer `{choice, probabilities,
  confidence}`. Model yes/no as `choice` with `yes`/`no`/`none` until `noul`/`score` are calibrated.
- **Parallel, isolated, one call per message** with every enabled question in it; question text
  counts as input tokens.
- **Registry** (JEV.md 2): each question is data: `id`, `instructions`, `criteria`, `threshold`,
  `scope` (accounts), `enabled`, `quiet_option`, and what it `suppresses`. Built-ins ship as a YAML
  file (hot-reloadable); users add and edit their own in settings (5j).
- **Quiet default and threshold:** a question acts only when its answer is non-quiet **and**
  probability and confidence clear the threshold. Thresholds start conservative (0.75 to 0.85).
- **Cache** by (message, question id, instruction hash, model id) with the full probability vector
  and `usage.cost`; `decisions` in `mirror.db` keyed by content key; a move or archive never re-asks.
- **Truncate deliberately:** headers that matter plus stripped text, clipped under 32k tokens with
  margin; never HTML; skip the call when there is nothing to read. Overrun is a clean 400.
- **Suppression rules** between questions (JEV.md 2.6); **show the odds** on a per-message sheet
  (2.7); **never act on a probability** (2.9); **ledger every call**; **nil-client safe**.
- **Instruction style:** write what does *not* count, then the exceptions (S4).

## Scope

1. **5b.1 Registry.** Load and validate the YAML (reject unknown keys, like the config loader), the
   instruction hash, per-account scope, the built-in set empty at first (5c to 5g add theirs), hot
   reload without a restart.
2. **5b.2 State builder.** A pure function from a stored message to the clipped text state; the
   header selection, quoted-reply handling and attachment text are open questions; a table-driven
   test over hostile and huge inputs including exactly-at-the-limit.
3. **5b.3 Decide + cache.** Call through the gate, parse and validate the answer against the
   registry (an answer outside the option set is an error, not a guess), write `decisions`
   (migration), apply suppression, expose the odds over a new `GET /messages/{id}/odds`.
4. **5b.4 The worker.** A background queue after settle: eligibility, bounded concurrency, a burst
   bound, backoff on errors, a pause when the cap is hit, and a **backfill that is opt-in with a
   cost estimate**. A fresh enable on an account does not queue history.
5. **5b.5 Odds sheet UI.** The info sheet on a message listing each question's probabilities, where
   thresholds get tuned (JEV.md 2.7).

## Tests and exit

- Against the fake provider: one call per message however many questions; an invalid answer is
  rejected and recorded; the cache key changes with the instruction text or model id and with
  nothing else (a move, archive, flag or tag change never re-asks).
- Off-path: the account off, the cap reached and the client absent each make zero requests and leave
  triage empty without an error on the reading path.
- Clipping: a message at, one over and far over the limit; the exact clip never splits a UTF-8
  sequence; nothing HTML reaches the state.
- Injection corpus (S4 item 4): hostile text can move probabilities but never produces an action,
  and the recorded numbers are asserted loosely (shape, not value).
- **Exit (G2 first):** the registry, state builder, cache and odds sheet work end to end with a
  trivial built-in question on the dev stack; the operator's live run costs cents and matches the
  provider's dashboard.

## Failure paths (STANDARDS 4a)

| Input | Bound | Above it |
|---|---|---|
| State per call | under 32k tokens with margin | clipped; a 400 overrun is recorded `rejected`, no retry |
| Questions per call | tested to 20 (Polaris, S4 to 18) | the registry refuses more than a stated maximum at load |
| Queue depth, concurrency, burst | stated, enforced | the rest waits; shown on the stats panel |
| Backfill | opt-in, estimate shown, cap applies | stops at the cap, resumes next period |
| A hostile or empty message | skipped when nothing to read | no call, no row beyond a skip record |

## Open questions

1. **When it runs:** only on arrival, or also a background pass over recent mail? Which mail is
   eligible: Inbox only? Junk (for `junk_rescue` only), Sent, Drafts, Archive excluded?
2. **Backfill policy:** how far back is "recent" by default, and what does the opt-in offer (30
   days, 90 days, all)?
3. **State composition:** which headers (From, To, Subject, Date, List-Id, Authentication-Results
   verdicts?); are quoted replies stripped; is extracted attachment text included (and clipped how)?
4. **Registry location:** embedded default plus an override file under `data/`, or settings only?
   How are user-edited questions stored (state.db) and versioned?
5. **Threshold presets:** the numbers behind Eager, Balanced and Careful (PLAN.md names them).
6. **Fallback backend:** build the chat-model implementation of `decide()` now, or only keep the
   interface open for it?
7. **Model pinning:** when do we pin a versioned Jev id, and is the id a setting?
8. **Odds sheet audience:** always available, or behind a developer toggle?
