# G2: before the first live Jev call (chunk 5, stage 5b)

Status: **waiting at G2.** Everything below is built and green against the fake provider. No live
call has been made and none can happen by accident: the shipped question file is empty, `classify`
ships dark, and the only question that exists is a dev-only one in `ivy-dev`. This file is the
gate the brief asks for (`docs/CHUNK5-BRIEF.md` section 5): the registry format, the cache key, the
cost estimate, and the request for the operator's go-ahead and key.

## 1. What is built

| Stage | What | Where |
|---|---|---|
| 5b.1 | Question registry: strict YAML, merged with the operator's edits by id, validated as one set | `jev/registry.go`, `jev/builtin.yaml` |
| 5b.2 | State builder: chosen headers + body without quotes, clipped, no markup | `jev/state.go` |
| 5b.3 | `Engine.Decide` (one gate call per message), the `decisions` cache, `Evaluate` (threshold, quiet option, suppression), `GET /messages/{id}/odds` | `jev/engine.go`, `jev/evaluate.go`, `store/decisions.go`, `gateway/odds.go` |
| 5b.4 | Background worker: arrival watermark, bounded batch and concurrency, backoff, cap pause, backfill estimate and start | `jev/worker.go`, `store/classify.go` |
| 5b.5 | Odds sheet in the reader's More menu (phone and desktop) | `web/src/lib/components/mail/OddsSheet.svelte` |

## 2. Registry format

A question is data (`jev/builtin.yaml` for shipped ones; the operator's edits are laid over them by
id and will live in `state.db` once 5j adds authoring):

```yaml
questions:
  - id: needs_me                      # lowercase, digits, _ and :; 64 at most
    instructions: "Does this need a reply from the owner? Newsletters do not count."
    criteria:                         # the option set; 2 to 255 options
      none: "nothing is asked of the owner"
      maybe: "a person may expect an answer"
      likely: "a person clearly expects an answer"
    quiet_option: none                # the answer when nothing fires; never acted on
    threshold: 0.8                    # (0, 1]; probability and confidence must both reach it
    scope: [acct-a]                   # optional: accounts; absent means all
    folders: [inbox, junk]            # optional: absent means inbox only
    enabled: false                    # optional: absent means on
    suppresses: [urgency]             # optional: held back when this one fires
    feature: junk_rescue              # optional: a Jev feature whose switch must also be on
```

Unknown keys are refused. At most 100 questions (the operator's limit); the quality and latency
numbers only go to about 20 (S4), so anything past that needs its own spike first.

## 3. The cache key

`(account, content key, question id, instruction hash, model slug)`, stored with the full
probability vector and the call's cost share (`decisions` in `mirror.db`, migration 17).

- The **instruction hash covers the instructions and the option set, nothing else.** Threshold,
  scope, folders, the switch, the quiet option and suppression are tuning, so changing them never
  re-asks (tested). Editing the wording or an option does, for that question only, and the old answer
  is kept beside the new one.
- A move, archive, flag or tag change never re-asks: the content key does not move.
- Refused and empty inputs are recorded in `decision_misses` (a provider 400, a malformed answer, a
  question left out, nothing to read) so they are not paid for twice.
- Verdicts (does it fire, is it suppressed) are computed on read from the stored odds, so the odds
  sheet can be tuned without a new call.
- The price of a rebuilt `mirror.db` is re-asking; the monthly cap bounds that.

## 4. Cost estimate

From `Gate.Estimate`, which prices the worst case and rounds up to a cent. Jev is $0.042 per million
input tokens and free output. A question costs about 110 tokens, so for short mail the question set
dominates.

| Job | Worst case |
|---|---|
| One call, 9 questions, a 1.5 KB state | under $0.01 (about $0.0001 in practice, S4) |
| One call at the 96 KiB limit | under $0.01 |
| 1,000 messages, 9 questions, 1.5 KB each | $0.06 |
| 1,000 messages, 18 questions, 4 KB each | $0.13 |

The shipped set is empty, so production spend today is **$0**. The proposed live run is one dev
question over about 20 messages of the dev mailbox: well under one cent. The global cap stays $10
and the OpenRouter key's own cap should match.

## 5. Safety properties, and where each is tested

- Off means off: account not opted in, `classify` off, or a cap of zero each make zero requests and
  write no refusal rows (`jev/engine_test.go` `TestOffMeansOff`, `jev/worker_test.go`
  `TestOffMeansOffForTheWorker`; `llm.Gate.CanRun` is a read that writes no ledger row).
- New mail only: the first pass with the feature on sets a per-account watermark and reads nothing;
  turning it off forgets it (`TestOnlyMailThatArrivesAfterTurningItOnIsClassified`,
  `TestTurningItBackOnStartsFromNowNotFromTheOldWatermark`). A question shipped later does not reach
  back over earlier mail (`TestNoQuestionsMeansNoWorkAndNothingReachesBack`).
- Backfill is an explicit estimate then an explicit watermark move; the caps still apply while it
  runs (`TestBackfillIsOptInPricedAndBounded`, `TestBackfillStopsAtTheCap`). **There is no backfill
  button yet**; see section 7.
- Model output is data: an answer outside a question's own options is thrown away and recorded
  (`TestAnAnswerOutsideTheOptionsIsRejectedAndRecorded`, with a validator mutation check done by hand).
- A hostile message cannot add a header line, break the call's structure or yield anything but a
  stored, validated answer (`TestInjectionCorpusCannotChangeTheShapeOfACall`, `jev/state_test.go`).
- Nothing acts: `jev` cannot import `smtp`, `send`, `compose`, `sync` or `gateway`
  (`jev/arch_test.go`); `llm/arch_test.go` still guards every provider endpoint.

## 6. The decision to confirm, and an assumption to confirm

1. **Assumed, please confirm:** every shared Jev call runs under one gate feature, `classify`
   (its switch, its ledger rows, its caps). A question may name a feature of its own (for example
   `junk_rescue`) and is then left out of the call unless that switch is also on. The alternative is
   one call per feature, which breaks "one call per message" and multiplies the question-text cost.
   The cost of this choice: the stats panel attributes a shared call to `classify`, not to each
   question; the per-question split is on the odds sheet.
2. `classify` has no label, so it does **not** appear on the Smart features screen yet (by design:
   a switch that does nothing in production would only confuse). 5c gives `needs_me` its label and
   switch. For the live check the switch is turned on through the API (section 7).

## 7. What the operator does for the live check

1. Put the key in `.env` as `OPENROUTER_API_KEY` (git-ignored). The dev stack reads it and, per
   `docs/DEV.md` section 5, defaults to `--llm live` behind a $1 monthly dev cap (`--llm-cap`) and a
   record/replay cache in `.dev/llmcache/`; use `--llm-fresh` for the check so the cache does not
   hide a real call from the provider's dashboard.
2. Start the dev stack, turn on smart features for the dev account (Settings, the account), then the
   `classify` switch (no screen yet, so through the API):
   `curl -X PATCH localhost:<port>/api/v1/smart -H 'content-type: application/json' -d '{"accounts":{"<id>":{"features":{"classify":true}}}}'`
   The first pass only records the watermark.
3. Bring in a few new messages with `ivy-dev deliver` (`docs/DEV.md` section 1; seeded mail is older
   than the watermark and is deliberately not read), wait up to the worker's 30 second interval,
   open one, **More, Show the odds**, and compare the cost on Settings, Spend with the provider's
   dashboard. Record it in `next_steps.md`. (I have not run this against the real provider; the
   exact `deliver` arguments are in `ivy-dev deliver --help`.)
4. Not built, and not needed for this check: the backfill button (an API and a settings control
   over `Worker.EstimateBackfill` / `StartBackfill`), the re-read of a message when its question was
   edited (lazy re-read happens only when the worker next considers it, which for already-classified
   mail is never until backfill), and the queue depth on Mirror health.

## 8. Waiting for

The operator's go-ahead on the live run and on the `classify` assumption in section 6.
