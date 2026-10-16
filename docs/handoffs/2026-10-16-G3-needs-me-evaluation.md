# G3: the needs-me labelling plan and report format (2026-10-16)

Gate G3 (`docs/CHUNK5-BRIEF.md`): before any threshold is called tuned, say which messages the operator labels,
how, and what the report looks like. No threshold is "tuned" without real labels.

## Which messages

Every message the Jev worker has classified for an account (Inbox and Junk, one per content key). The first run
is the operator's own mailbox, so a few dozen messages: she labels all of them, there is no sampling. A bigger
mailbox later is sampled by the same tool (`--limit`, newest first) and the report says how many were labelled.

## How she labels

`ivy eval sheet` writes `<data_dir>/eval/needs-me-labels.csv`: `id, date, from, subject, preview, needs_me`.
The `needs_me` cell is blank. She fills `y` or `n`:

- **y** means this message needed something from her: a reply, a decision, a form, a payment, a deadline.
- **n** means it did not, however interesting it was (newsletters, receipts nothing is due on, notifications).

The sheet carries **no Ivy answer**, so she labels blind. A blank cell is "not labelled yet" and is left out of
every number. Re-running the command keeps her labels and appends messages classified since. The file lives in the
data directory and never in the repo: it names real mail.

## The report

`ivy eval report` prints Markdown to the terminal and changes nothing, calls no provider and costs $0.

1. **Coverage.** Labelled, labelled `y`, not yet labelled, classified but not in the sheet.
2. **Stage one** at the shipped threshold: true and false positives and negatives, precision, recall. Stage one
   over-includes on purpose (0.65), so recall matters more than precision here.
3. **Threshold sweep for stage one**, 0.30 to 0.95: flagged count, precision, recall. Read from stored
   probabilities with the registry's own firing rule, so it needs no new call.
4. **The cascade as she sees it**: what ends up in Needs attention (stage two says `needs`) against her labels.
5. **Stage two alone**: of the messages stage one flagged, how many it kept and how many it dropped that she
   labelled `y`, and how many `y` messages never reached it.
6. **Every disagreement**: false positives and false negatives with sender, subject, the stage-one probabilities
   and stage two's reason, so a wrong call can be read and the wording fixed.

A rate is shown as `n/a` when its denominator is zero, never as 0 or 100. With a few dozen messages every figure
is rough; the report says so, and the bar the operator picks is a choice, not a measurement.

## What it does not do

It does not change a threshold. That stays a decision (`jev.tuning`), made by the operator after reading this.
