# 5a. Gate, ledger and spend

## Purpose

Make the one chokepoint that already guards embeddings guard **every** remote call (Jev, chat,
vision), make the cost ledger readable, and replace the mock behind the stats panel with the real
thing. Nothing in chunk 5 may reach a provider except through this.

## Depends on

Nothing. Everything else in chunk 5 depends on this.

## Where we start

- `llm/gate.go`: `Gate` with one request type, `EmbedRequest`, and one method, `Embed`. It enforces
  the per-account opt-in (`ErrNotEnabled`), the monthly cap (`ErrCapReached`), batch bounds
  (`MaxBatchInputs` 64, `MaxInputBytes` 64 KiB, `ErrTooLarge`), and writes one ledger row per input
  plus the monthly counter in one transaction (`record`). Outcomes: ok, error, refused, rejected.
- `store/ledger.go` and `api_calls` / `api_caps` (state migration 6): time, provider, endpoint, model,
  feature, account, content key, tokens, exact cost, `cost_estimated`, latency, outcome, call id.
- `llm/embedder.go`: the unexported OpenRouter and Ollama clients; `llm/arch_test.go` fails if any
  other package names a provider endpoint.
- The web stats screens exist and run on a mock ledger. The contract has no spend endpoint.

## Settled

- **The gate is the single chokepoint** and enforces: account `llm_enabled` / `vision_enabled`,
  ask-selection rules, withheld-message rules, monthly caps, a ledger row per call (per input for a
  batch) with the exact cost, and timeouts (`ARCHITECTURE.md` 7).
- **Ledger rules** (round 30): gate-blocked and failed calls at zero cost, local calls at zero cost
  so volume is visible, `cost_estimated` only when the provider reports none. Only the gate writes it.
  150,000 rows is about 27 MiB (S10).
- **Stats panel contents** (PLAN.md 3): totals for today / 7 days / 30 days / all time by feature,
  account and provider/model; a per-call log, filterable and exportable, Jev calls showing the
  probability vector; monthly spend against caps and how many calls the gates blocked; mirror
  health (per-account sync state, last sync, backlog, errors, DB size, embedding queue, memory).
- **Per-account opt-in, off by default;** LLM-off accounts make zero outbound calls (brief
  invariant 2). Vision has its own switch.
- **The model may write local tags automatically; nothing else** (brief invariant 6).

## Scope

1. **5a.1 Generalise the gate (G1 first).** Request types for `Decide` (Jev), `Complete` (chat) and
   `See` (vision) beside `Embed`; endpoint constants `systemone`, `chat`, `vision`; one policy check
   shared by all (opt-in, cap, withheld, ask-selection) so a new feature cannot forget one; the
   unexported provider clients for `/systemone`, chat and vision; the architecture test extended to
   every new endpoint. One ledger row per call and per input, in the same transaction as the counter.
2. **5a.2 Spend API.** Add to `api/openapi.yaml` and regenerate: totals by window and breakdown, the
   per-call log (filters, cursor paging, export), caps and blocked-call counts. The gateway reads
   `api_calls` / `api_caps`; the web client moves from the mock to `http.ts`; the mock spend route
   and its E2E fixtures are removed in the same change.
3. **5a.3 Caps and settings.** Monthly caps and the feature switches in the settings panel (the
   structure is an open question), a cost estimate helper the later backfills reuse, and the
   mirror-health additions the panel lists (embedding queue, memory).

## Tests and exit

- A recording fake provider fails the test on **any** request with an account off, per request type.
- Cap reached: refused at zero cost, recorded, distinguishable from an error; the next month resets.
- The ledger row(s) and counter commit together (kill between them leaves neither or both).
- Property: every total on the stats panel equals the sum of the ledger rows it summarises.
- The architecture test fails if a new package names `/systemone`, a chat or vision endpoint.
- **Exit:** the stats screens run on real data from the dev stack; the mock ledger is gone.

## Failure paths (STANDARDS 4a)

| Input | Bound | Above it |
|---|---|---|
| State per `Decide` call | under the 32k-token context with margin, clipped by the caller | `400 max_tokens_exceeded` is `rejected`, recorded at zero cost, never retried |
| Calls per tick, concurrency, backfill burst | stated per feature, enforced by the gate | queued, not dropped; shown on the stats panel |
| Provider timeout / stall | a deadline on every call, cancellable by context | `error`, ledgered at zero cost, retried with backoff by the caller |
| Spend | monthly cap per account (and global; open) | refused, recorded, surfaced as a quiet banner |
| Ledger read | paged, never unbounded | cursor |

## Open questions

1. **Cap structure:** per account, global, per feature, or a mix? What is the default for a new
   account, and is there a per-message spend ceiling as well as a monthly one?
2. **At the cap:** do queued features wait until next month, drop, or ask? What does the operator see?
3. **Where the OpenRouter key lives** (the mailbox passwords moved to `data/secrets` in round 59):
   the same store, `ivy.yaml`, or `.env`? One key for Jev, chat, vision and embeddings (JEV.md says
   one key)?
4. **Model configuration:** is the chat model one global setting or per feature (stage 2, digest,
   summaries, ask, extraction, compiler)? Which default ids?
5. **Ledger retention:** keep forever (about 27 MiB per 150k rows) or prune old rows into monthly
   totals? Export format (CSV, JSON)?
6. **Privacy of the call log:** it stores ids, counts and costs, never text; should the per-call
   view also show the *question set* sent, and Jev's probability vector (PLAN.md says yes)?
7. **Estimates:** is a stated cost estimate before any backfill or dry run mandatory (the plan
   assumes yes), and how conservative should it be?
