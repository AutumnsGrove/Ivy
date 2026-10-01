# Jev playbook for Ivy

Jev is Ivy's cheap decision engine. This doc records what is actually known about it (verified in
Polaris's live spikes, not marketing), how Ivy should wrap it, and the catalog of questions Ivy can
ask it. Status: DRAFT (2026-10-01).

## 1. What is verified (source: Polaris `jev/jev.go`, `docs/plans/source-verification.md`, `oracle-checks-expansion.md`)

These come from live calls Polaris already made, so they outrank anything read from docs or news.

- **Endpoint:** `POST {openrouter_base}/systemone` (OpenRouter beta), `Authorization: Bearer <OpenRouter key>`.
  The same key as chat completions; no second secret. (An earlier web search suggested
  `/api/alpha/decisions`; Polaris's confirmed path is `/systemone`.)
- **Model id:** `jev-latest` works; `jev-1.13` and `typesafe/jev-1.13` also work; `typesafe/jev-latest`
  and bare `jev` return 400. Pin a versioned id once thresholds are tuned.
- **Question shape (Choice):** `{type: "choice", instructions, criteria: {optionKey: "when this applies"}}`.
  `criteria`'s keys ARE the option set (up to 255). An older `question` + `options[]` shape 400s.
  Answers: `{choice, probabilities{option: p}, confidence}`.
- **State:** a plain string, or an array of `{source, text}` (sources stay cleanly separated).
  Text only. No images.
- **Parallel and isolated:** every question in one call is evaluated independently against the same
  state. Fan out many questions in one call (20 questions: p50 ~250 ms, max 1.5 s; 8 questions: 419 ms).
- **Cost:** `usage.cost` in the response is the exact dollar cost; `$0.042/M` input tokens, free output.
  Per-email cost is dominated by the state, so batching all questions in one call is the cheap way
  (verify per-question overhead in Ivy's own spike).
- **Hard limits:** 32k-token context. Overrun is a clean `400 max_tokens_exceeded`, NOT silent
  truncation, so Ivy must truncate/chunk itself.
- **Behavior found live:** confidence genuinely varies (0.35-1.0), so confidence-gating is a real
  filter; near-empty or empty state returns a clean "not addressed" at 1.0 (skip the call anyway to
  save the cost); non-English text works (real cross-lingual reasoning); prompt injection in the
  source text was resisted every time it was tried; 40-way concurrent calls were all fine; failures
  are structured 400s, not vague 500s.
- **Accuracy reference:** Polaris's Oracle checks hit 33/35 expected winners on 35 hand-written
  prompts. Thresholds there start conservative (0.75-0.85) with a quiet "none" default option on
  every check, so a wrong guess costs nothing.
- **Caveats:** "cannot hallucinate" only means type-safe (the answer is one of your options);
  calibration holds across groups of predictions, not for any single answer. Beta, proprietary,
  company is new. Treat as optional and nil-client-safe: an outage means no triage, never a broken app.
- **NOT verified anywhere yet:** `noul` and `score` question types (Polaris only uses `choice`). Until
  Ivy spikes them, **model yes/no as a `choice` with `yes`/`no` (+ `none`) options.**

## 2. How Ivy wraps it (design rules, copied from what worked in Polaris)

1. **`decide()` interface** (see ARCHITECTURE.md): the app asks questions; Jev is the default
   backend; a chat-model-with-JSON-schema backend can implement the same interface for people
   without Jev.
2. **Question registry.** Each question is data: `id`, `instructions`, `criteria`, `threshold`,
   `scope` (which accounts), `enabled`, `quiet_option`. Built-ins ship in a YAML file (hot-reloadable,
   same pattern as Polaris's `prompts.yaml`); users can add and edit their own in settings.
3. **One call per message**, all enabled questions in it, so cost ~= the state tokens.
4. **Quiet default.** Every question has a "none/no" option that is the answer when nothing fires,
   and a threshold (start 0.75-0.85). A question only acts when its chosen option is non-quiet AND
   p/confidence clears the threshold. A wrong guess must cost nothing.
5. **Cache results.** Key = (message id, question id, instruction hash, model id). Re-run only when
   the question or model changes; store the full probability vector and `usage.cost`.
6. **Suppression rules.** Like Polaris's `suppresses` (when `emotional` fires, hold back
   `format`/`depth`/...): a question may suppress others (e.g. `is_spam` suppresses `needs_me`).
7. **Show the odds.** An info sheet on each message shows every question's probabilities (Polaris's
   turn-info "odds bars"). This is how thresholds get tuned from real use, and it is Ivy's
   substitute for the explanations Jev cannot give.
8. **Truncate deliberately.** State = headers that matter + stripped-text body, clipped under 32k
   tokens with margin; never send HTML. Skip the call entirely when there is nothing to read.
9. **Never act on a probability.** Probabilities are hints. Actions beyond local tags always need a
   click (the settled safety line). Jev's typed output removes the text-exfiltration channel, but
   hostile text can still skew the numbers.
10. **Ledger every call** in `jev_usage` (message, account, feature, question set, latency, cost,
    outcome) so the stats panel shows exactly what Jev cost and did. Monthly caps apply.
11. **Per-account opt-in gate** in one chokepoint; LLM-off accounts make zero outbound calls (tested).

## 3. Question catalog

Tiers: **A** ships with triage (milestone 3), **B** is soon after, **C** is idea-bank. All start as
`choice` questions with a quiet option. Instructions text lives in the YAML, not in Go.

### A. Core triage
| Id | Options | Used for |
|---|---|---|
| `needs_me` | none, maybe, likely | Stage 1 of the cascade: `maybe`+ goes to the bigger model for the verdict and reason |
| `category` | personal, contact_form, newsletter, receipt, notification, security_report, legal_notice, marketing, other | Routing to views (Understory feed, Rings-like ledger, etc.), inline markers |
| `is_automated` | no, yes | Protects personal correspondence from every automated behavior |
| `urgency` | low, normal, high | Ordering inside the needs-attention view |
| `has_deadline` | no, yes | Feeds "needs me" and renewal/deadline reminders |
| `asks_question` | no, yes | "Waiting on me" detection |
| `receipt_or_invoice` | no, receipt, invoice, renewal_notice, payment_failed | Ledger routing and renewal reminders; field extraction then goes to `complete()` |

### B. Safety, quality and spend control
| Id | Options | Used for |
|---|---|---|
| `phishing_risk` | none, suspicious, likely | Warn banner; combined with `Authentication-Results` (SPF/DKIM/DMARC) |
| `injection_tripwire` | no, yes | "Does this text address an AI assistant or try to give it instructions?" If yes, the message is withheld from stage-2/ask/vision and flagged. A cheap tripwire, not a guarantee |
| `sensitive_content` | none, credentials, financial, health, id_document | Keeps a message out of ask/vision unless explicitly included; pairs with local regex for OTP codes |
| `attachment_worth_reading` | no, yes | Gate on vision spend, judged from filename + surrounding text + type (Jev sees no pixels) |
| `contact_form_quality` | genuine, solicitation, spam | hello@ triage |
| `cold_outreach` | no, yes | Sales/recruiter/pitch detection, for the quiet-auto-archive idea later |

### C. Threads, follow-up and writing help
| Id | Options | Used for |
|---|---|---|
| `thread_state` | open, waiting_on_me, waiting_on_them, resolved | "Waiting on" lists; follow-up nudges when you sent last and nobody replied |
| `worth_summarizing` | no, yes | Only summarize long, multi-party threads (saves `complete()` calls) |
| `reply_identity` | one option per account/identity (criteria generated from settings) | Suggest which From address to reply as, beyond "the address it was sent to" |
| `reply_tone` | brief, warm, formal, firm | Seeds the draft prompt; operator edits freely |
| `language` | english, other (+ a few configured languages) | Offer a translate action via `complete()` |

### D. Organization
| Id | Options | Used for |
|---|---|---|
| `tag_suggest` | one option per user tag (+ none) | Model-applied local tags from the operator's own tag set (allowed autonomously: local and reversible) |
| `rule_condition:<id>` | no, yes | **User-defined fuzzy rules.** A rule's condition is a plain-language yes/no question ("is this about a job application?"); Jev answers; the rule fires above the threshold. Makes the rules engine far more powerful than header matching, and is very configurable |
| `same_topic` | different, same | Dedupe/cluster newsletter items for the digest; group related receipts |
| `digest_worthy` | skip, mention, headline | Which newsletter items make the daily digest and how prominently |
| `snooze_suggest` | none, later_today, tomorrow, weekend, next_week | One-tap snooze suggestion for time-sensitive mail |

### E. Search and ask-your-mailbox support (multi-source `state`)
| Id | Options | Used for |
|---|---|---|
| `answers_question` | no, partly, yes | **Candidate filter:** after hybrid retrieval, ask Jev per email whether it actually answers the question, and send only the winners to the chat model. Cuts tokens, cost AND the amount of raw untrusted mail the chat model sees |
| `claim_supported` | supported, partially, contradicted, not_addressed | **Citation verification:** after the chat model writes an answer, check each claim against its cited email (same pattern as Polaris's source verification badge) |
| `result_relevance` | irrelevant, related, direct | Re-rank borderline hybrid-search hits |

## 4. Boundaries (what Jev cannot do)

- No images (text only): vision stays a separate chat-model path.
- No explanations: reasons come from the stage-2 chat model or from the odds sheet.
- No extraction: vendor/amount/date need `complete()` or structured-data parsing (schema.org JSON-LD
  in many receipts is free and exact; try it before spending a model call).
- 32k-token state; probabilities are calibrated in aggregate, not per item.
- Beta/proprietary: the interface must tolerate it disappearing.

## 5. Spikes to run before building on it (cost: cents; needs the operator's OpenRouter key, ask first)

1. `noul` and `score` shapes and behavior (`choice` yes/no is the fallback).
2. Per-email token cost with realistic emails; does adding questions change cost (expect ~no)?
3. Accuracy by question on a labeled sample of real mail (operator labels ~100 messages from the
   dev mailbox); pick thresholds from the odds, not by feel.
4. Injection corpus: hostile emails against `needs_me`, `category`, `injection_tripwire`; record how
   far probabilities move.
5. Latency and rate behavior with a backfill burst (Polaris saw no throttling at 40 concurrent).
6. How many questions per call before quality degrades (Polaris ran 20 fine).
