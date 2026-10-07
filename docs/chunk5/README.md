# Chunk 5 plans (triage: the LLM layer)

Standing rules, invariants, escalation gates and the definition of done are in
`docs/CHUNK5-BRIEF.md`. **Read it first.** This directory holds one plan per feature so each can be
built, reviewed and, if the operator wants, dropped on its own. Everything here is an extra on top
of a finished backend: a fresh account has none of it on.

## How to read a plan

Every plan has the same sections: **Purpose**, **Depends on**, **Where we start** (what exists in
the code today, so nobody assumes the wrong baseline), **Settled** (only decisions already recorded,
with where), **Scope** (the stages), **Tests and exit**, **Failure paths** (STANDARDS 4a) and **Open
questions**. A plan states only what is settled; every unsettled choice is an open question, to be
asked in batches in the session and recorded in `docs/qa-log.md` before the stage starts. A stage
with an unanswered open question is not ready to build. **All ten plans' questions were answered on
2026-10-07;** each plan's "Decisions" section holds the answers and the original questions follow it
as the audit trail. A few small items are marked "assumed, confirm at build".

## Order and dependencies

```
5a gate, ledger, spend ──┐
                         ├─► 5b the Jev layer ──┬─► 5c needs-me cascade ──┐
                         │                      ├─► 5d classifiers and safety ──┐
                         │                      ├─► 5e summaries and thread help │
                         │                      ├─► 5f newsletters and digest ◄──┤ (uses 5d category)
                         │                      ├─► 5g receipts and ledger ◄─────┤ (uses 5d category)
                         │                      └─► 5j rule compiler, smart checks
                         └─► 5h vision (needs 5d safety flags) ──► 5i Ask Ivy (needs 5d withheld rules, 5e)
```

**Foundation first.** 5a and 5b are the prerequisite for everything, and the stats screens and the
odds sheet are how every later stage is measured. After them the order is a recommendation, not a
rule: 5c is the feature the operator will feel first; 5d's safety flags (withheld mail) must exist
before 5h and 5i; 5j needs 5b's registry and the existing rules engine (3g).

## Status

| Stage | Plan | Status |
|---|---|---|
| 5a | [Gate, ledger and spend](5a-gate-ledger-spend.md) | planned; questions answered 2026-10-07 |
| 5b | [The Jev layer](5b-jev-layer.md) | planned; questions answered 2026-10-07 |
| 5c | [Needs-me cascade](5c-needs-me-cascade.md) | planned; questions answered 2026-10-07 |
| 5d | [Classifiers and safety](5d-classifiers-and-safety.md) | planned; questions answered 2026-10-07 |
| 5e | [Summaries and thread help](5e-summaries-and-thread-help.md) | planned; questions answered 2026-10-07 |
| 5f | [Newsletters and the digest](5f-newsletters-and-digest.md) | planned; questions answered 2026-10-07 |
| 5g | [Receipts and the ledger view](5g-receipts-and-ledger.md) | planned; questions answered 2026-10-07 |
| 5h | [Vision](5h-vision.md) | planned; questions answered 2026-10-07 |
| 5i | [Ask Ivy](5i-ask-ivy.md) | planned; questions answered 2026-10-07 |
| 5j | [Rule compiler and smart checks](5j-rule-compiler-and-smart-checks.md) | planned; questions answered 2026-10-07 |

The overall status line lives in `next_steps.md`; keep this table in step with it.

## What is real and what is mocked today

Checked against the code on 2026-10-07, so each plan starts from the truth:

| Surface | Today |
|---|---|
| `llm.Gate` | **Embeddings only** (`llm/gate.go`, `EmbedRequest`; endpoints `embeddings`, `ollama_embed`). Provider clients are unexported and `llm/arch_test.go` enforces it. |
| Cost ledger | **Real**: `api_calls` and `api_caps` in `state.db` (`store/ledger.go`, migration 6), written by the gate for embeddings. Nothing reads it for the UI yet. |
| Stats panel (`/settings/spend`, `/settings/spend/calls`) | **Mock only.** There is no spend endpoint in `api/openapi.yaml`; the screens run on a mock ledger. |
| `GET /messages/{id}/summary` | **Not an LLM summary**: it is "the header alone, for a failed body". Summaries need their own surface (5e). |
| `GET /reading` | **Real** (the tag-driven Reading feed from 3g); the digest is not generated. |
| `POST /ask`, `/checks` | In the contract, **no gateway handler**; mock-backed in the web client. |
| Fake provider | `internal/mailworld` already fakes `/systemone`, chat and vision with queued answers and a call log (`llm.go`). It has no `score` questions (papercuts N3). |
| Rules (3g) | Real: header conditions, local-only actions, the applier and the ingest pass. The free-form compiler and fuzzy checks are 5j. |
| Search (3f) | Real: FTS5 plus embeddings, hybrid ranking, the embed-once queue. Ask Ivy builds on it. |
| `needs_me` | The **table exists** (mirror migration 2: account, content key, verdict, reason, stage-2 model, state) and **nothing writes it**. The inbox query already left-joins it and flags rows whose `verdict = 'needs'`; `GET /inbox` returns `needCount` and a per-row flag, but no reason. |
| `decisions`, `receipts`, `checks` tables | Not created (`ARCHITECTURE.md` 3 describes the first two). |
| `Authentication-Results` | Parsed and stored (`mime.AuthResults`, `messages.auth_results`, migration 3). |
| `X-Spam-Status`, `List-Id`, `List-Unsubscribe` | **Not parsed or stored anywhere.** 5d and 5f add them (a parse change and a migration each). |
| Outbound fetches | **None.** Ivy blocks remote content rather than fetching it; the only SSRF guard is the connector's host check in `internal/accountsvc`. 5f builds the first guarded fetch client. |
| Server-side image decoding | **None.** `web/src/lib/photo.ts` (EXIF strip, downscale) is client-side only, for outgoing photos. 5h is the first server-side decode of hostile images. |
| Vision, categories, receipts, digest | Nothing built. |
