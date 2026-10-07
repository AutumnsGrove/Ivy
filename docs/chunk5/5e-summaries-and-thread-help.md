# 5e. Summaries and thread help

## Purpose

Quiet help on long mail and long threads: a **smart summary** chip, "waiting on" lists and follow-up
nudges, a reply tone and identity suggestion that seeds a draft, a translate action, snooze
suggestions, and model-applied local tags from the operator's own tag set. All of it is assistance,
never an action.

## Depends on

5a, 5b. Uses 5d's `is_automated` and withheld rules. The tag suggestions use the existing tag set (3b).

## Where we start

- **There is no summary surface.** `GET /messages/{id}/summary` returns only "the header alone, for a
  failed body" (a `MailSummary` list row); it is not an LLM summary, so this stage must **not** reuse
  that name. Pick a new endpoint and field name (open question 1).
- The firefly-dot chip is in the design direction (PLAN.md 1); the reader and its chips are real.
- Replies already compute recipients, identity and threading (4e); drafts and compose are real (4).

## Settled

- **Summaries appear as a quiet firefly-dot chip, never labelled as AI** (PLAN.md 1); model output
  is plain text, never HTML (brief invariant 5).
- **Only summarise long, multi-party threads:** `worth_summarizing` (no / yes) gates the `complete()`
  call to save spend (JEV.md 3C).
- **Catalog entries** (JEV.md 3C and 3D): `thread_state` (open, waiting_on_me, waiting_on_them,
  resolved), `reply_identity` (one option per account/identity, generated from settings),
  `reply_tone` (brief, warm, formal, firm; seeds the draft prompt, the operator edits freely),
  `language` (offer a translate action via `complete()`), `snooze_suggest` (one-tap suggestion),
  `tag_suggest` (one option per user tag plus none).
- **Model-applied local tags are the one autonomous model write** (local and reversible); everything
  else here proposes and waits for a click. A draft suggestion is never sent, and Send stays the
  only confirmation (chunk 4 invariant 4).
- **Withheld mail is never summarised;** one account per call.

## Scope

1. **5e.1 Summary.** `worth_summarizing` then a `Complete` summary with a schema, stored keyed by
   content key (or thread) and hash of what it summarised; a new read endpoint and the chip.
2. **5e.2 Thread state.** `thread_state` on threads where the operator sent last or is addressed;
   the "Waiting on" lists and a follow-up nudge as an in-app marker (no push: Apple Mail keeps that
   job).
3. **5e.3 Tag suggestion.** `tag_suggest` applying local tags from the operator's set, each
   removable and visibly "placed for you".
4. **5e.4 Reply help.** `reply_identity` and `reply_tone` feeding a draft prompt in compose; a
   translate action on `language`. Each lands as its own small stage and is skippable.
5. **5e.5 Snooze suggestion** as a one-tap chip that calls the existing snooze.

## Tests and exit

- `worth_summarizing` no makes no `Complete` call; yes makes exactly one; the summary is cached and a
  new message in the thread re-summarises lazily, bounded per thread and per day.
- Output is rendered as plain text: a summary containing markup renders as text (hostile corpus).
- A suggestion never changes anything until clicked, except an applied local tag, which is
  removable and ledgered.
- With the account off, the cap reached or the client absent: no chip, no error.
- **Exit:** the chip and the waiting-on list work on the dev stack; the operator's live read of real
  summaries is their own pending line.

## Failure paths (STANDARDS 4a)

| Input | Bound | Above it |
|---|---|---|
| Thread length | the clipped state, newest and oldest messages favoured (open) | clipped, summary says so quietly |
| Summaries per thread per day | a stated ceiling | the last one stands; no re-spend |
| Summary length | short maximum | truncated at a word boundary |
| Translate input | the message's clipped text | refused above the limit, shown |

## Open questions

1. **Name and shape of the summary surface** (a new endpoint, e.g. insight or digest-of-thread; the
   existing `/summary` must keep its meaning) and where the chip appears (reader, list row).
2. **What gets summarised:** a thread, a single long message, a newsletter (5f owns the digest), or
   attachments (5h)? Length threshold for "long"?
3. **Re-summarising rules:** on every new message, or only when the operator opens the thread?
4. **Follow-up nudges:** how long after you sent last, and where they appear without push; is a
   nudge a tag, a list or a banner?
5. **Which of the suggestion features ship** (reply identity/tone, translate, snooze suggest, tag
   suggest) and in what order; each is independent.
6. **Draft assistance:** does the draft prompt write a whole reply or only seed one, and is its
   output inserted into compose automatically (a click) or shown first?
7. **Tag suggestion confidence:** the threshold for applying a tag without a click, given it is the
   one autonomous write.
