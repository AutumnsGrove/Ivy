# 5c. Needs-me cascade

## Purpose

Tell the operator which mail needs them, with a one-line reason: a dedicated **Needs attention**
view and inline markers. The feature the operator will feel first; it is also the first real test of
the whole layer, so its evaluation is the template for every classifier.

## Depends on

5a, 5b. **Builds the vetting machinery itself as 5c.0** (the tripwire, the sensitive check and the real
`Vetting`, moved here from 5d by the G1 decision), so withheld mail never reaches stage 2. Uses
`is_automated` from 5d's classifiers; can ship first with `is_automated` alone.

## Where we start

- The **`needs_me` table already exists** (mirror migration 2: `account_id`, `content_key`,
  `verdict`, `reason`, `stage2_model`, `state`, primary key (account, content key)) and nothing
  writes it. `store/inbox.go` left-joins it and flags rows whose `verdict = 'needs'`; `GET /inbox`
  returns `needCount` and a per-row flag, with **no reason** in the API. So the verdict value
  `needs` is already baked into a read query; any new verdict vocabulary must account for it.
- The reader, tags and the "placed for you" tag idea (needs you, newsletters, looks real) exist in
  the design; the tag machinery is real (3b, 3g).
- The fake provider can queue Jev answers and chat replies, so both stages are testable offline.

## Settled

- **Cascade:** Jev `needs_me` (`none`, `maybe`, `likely`) first, high recall; only `maybe` and above
  goes to the chat model, which decides and writes the reason (no tools, structured output). Saves
  cost and shrinks the amount of raw mail the chat model sees (`PLAN.md` 3, `ARCHITECTURE.md` 7).
- **Reason is plain text, never labelled as AI** on the reading surface (brief invariant 5).
- **Companions that feed it:** `urgency` (ordering inside the view), `has_deadline`,
  `asks_question`, and `is_automated`, which protects personal correspondence from every automated
  behaviour (JEV.md 3A).
- **Needs-me never moves, hides or deletes anything:** it is a view, a marker and a sort order
  (invariants 6 and 7). The verdict is a hint the operator can dismiss.
- **One account per call; withheld mail is excluded** (invariants 4 and 8).

## Scope

0. **5c.0 The vetting machinery (moved here from 5d by the G1 decision).** The `injection_tripwire`
   and `sensitive_content` questions, the withheld fact on the content key (a mirror migration), the
   real `llm.Vetting` implementation replacing the gate's refuse-everything default, and the quiet
   withheld marker with its per-message override. Without it the gate refuses stage 2 by design, so it
   comes first; the OTP regex and the rest of 5d's classifiers stay in 5d.
1. **5c.1 Stage 1 question** `needs_me` plus companions in the registry, with instructions written as
   what does not count, then the exceptions; the quiet option is `none`.
2. **5c.2 Stage 2.** The chat-model verdict and reason via `Complete` with a JSON schema, validated;
   an invalid reply is a recorded error and leaves the message unmarked, never a guessed verdict.
3. **5c.3 Storage and API.** Write the existing `needs_me` table (a migration only if columns are
   missing, e.g. dismissal), a list endpoint with a cursor, the reason on the existing inbox rows
   (today only a flag), and the dismiss action (local state).
4. **5c.4 Screens.** The Needs attention view and the inline marker with its reason, replacing any
   mock; the odds sheet shows both stages.
5. **5c.5 Evaluation (G3).** The labeling plan and the eval report: the operator labels about 100
   messages from the dev mailbox; precision and recall per stage at the shipped thresholds.

## Tests and exit

- Stage 1 `none` makes no stage-2 call; `maybe` and `likely` make exactly one; a withheld message
  makes none.
- An invalid stage-2 reply, a provider error and a capped account leave the message unmarked and the
  inbox unchanged.
- Cache: a move, archive or flag never re-runs either stage; editing the instruction does, lazily.
- Cost is asserted per message for the shipped question set and for the worst case (every message
  flagged).
- **Exit:** the view and markers work on the dev stack; the eval report exists with real labels (the
  operator's, so its own pending line); no threshold is called tuned without it.

## Failure paths (STANDARDS 4a)

| Input | Bound | Above it |
|---|---|---|
| Stage-2 input | the clipped state of one message | clipped; overrun recorded `rejected` |
| Flagged messages per tick | stated burst bound | the rest waits |
| Reason text | a short maximum, stripped to plain text | truncated at a word boundary |
| Both stages down | n/a | the inbox is exactly as it is today |

## Decisions (Q&A with the operator, 2026-10-07)

1. **Verdicts: `needs` / `not`.** `needs` stays the flagged value the inbox query already reads; `not`
   is stage 2 saying no. "Waiting on me" stays a thread-level idea in 5e.
2. **Handled or dismissed clears it.** Replying, archiving, deleting or tapping dismiss clears the
   flag for that message, kept locally so it does not return on a re-read. Reading alone does not
   clear it. Dismiss is per message.
3. **Stage 2 uses the registry's default chat model** (overridable per feature in settings). The reason
   is one short plain sentence, may name the sender, and may not quote the message.
4. **A Needs attention view plus an inline marker with its reason;** the inbox keeps its existing
   needs count.
5. **It also places a "placed for you" tag** (the operator's choice, not the recommended marker-only).
   Per `PLAN.md` 3 such tags are **local and removable, never an IMAP keyword.** Removing the tag and
   dismissing the flag are the same action (assumed, confirm at build).
6. **No accuracy target is fixed now.** About 100 of the operator's messages are labelled, precision
   and recall are measured at the shipped threshold, and the operator picks the bar from real misses.
   It ships off by default until then.
7. **Eligible mail is the Inbox only** (5b). A snoozed message keeps its flag when it wakes (assumed).

## The questions as asked

1. **Verdict vocabulary:** the existing inbox query already treats `verdict = 'needs'` as the
   flagged value. Keep `needs` and one "does not" value only, or add a `waiting on me` kind
   (overlaps 5e's `thread_state`)? Adding values means checking every reader of the column.
2. **Stage-2 model and prompt**, and whether the reason may mention the sender or quote any text.
3. **Read and handled mail:** does a message you have read, replied to or archived stop needing you?
   What does dismiss do, and is it per message or per thread?
4. **Where it lives:** its own tab, a filter on the inbox, or both? Badge counts?
5. **Interaction with tags and snooze:** is "needs you" a visible "placed for you" tag (PLAN.md
   mentions one) or only a marker?
6. **Targets:** what precision and recall are good enough to turn it on by default for an account?
7. **Scope:** which folders are eligible (Inbox only), and does a snoozed message re-rank on waking?
