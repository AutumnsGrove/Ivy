# Chunk 6: the craft pass

A small, droppable set of presentation and affordance work, planned 2026-10-22 after reading Penguin
Mail (`penguin-mail-recon.md` beside this file). It adds **no subsystem**: no calendar, no scheduler,
no plugin layer, no new provider. Every stage is a screen or a strip or a sentence.

The reason it exists: chunk 5 has taught Ivy a lot of things to *know*. This chunk makes the knowing
visible without adding a single new model call.

## Purpose

1. Make the list answer "how much of what?" without a trip to the drawer (6a).
2. Put the one action a mailing list has — get off it — where the mail is (6b, with 5f).
3. Make the facts behind a message one tap away instead of one sheet away (6c).
4. Stop two screens from being quietly dishonest: rules that never say when they run (6d), switches
   that never say why they are off (6e).
5. Optionally: make a selection look like a selection on a desktop (6f).

## Depends on

Nothing hard. 6b wants 5f's unsubscribe action to finish it (it ships the strip first, the button
later). 6c is cheaper after 5d.3, which is done (the auth verdict and spam score are already sorted).
6d's second half wants 5j if we say where a rule came from.

## Where we start

Checked against the code on 2026-10-22, so a stage does not assume the wrong baseline:

| Surface | Today |
|---|---|
| Inbox header | `web/src/lib/components/mail/DesktopInbox.svelte` and `PhoneInbox.svelte`: a heading, a one-line subtitle (`N need you · N unread`), a `Select` button and the `Search or ask` link. **No facet row exists anywhere.** |
| `GET /inbox` | Returns `items`, `nextCursor`, `needCount`, `unreadCount`, and takes `account`, `folder`, `tag`, `cursor`. No attachment or flagged count. |
| Selection | Real on both viewports: `Selection`, `BulkBar` (inline above the list on desktop, floating over the tab bar on phone), `MAX_SELECTION`, keyboard `Ctrl/⌘+A`. On desktop the reader keeps showing the first message while a selection is live. |
| Reader header | `MessageView.svelte`: subject, sender row, a flag, a `SenderSheet` (the auth verdict and spam score live there), attachments, Reply/Forward. No inline headers toggle. |
| `List-Id`, `List-Unsubscribe` | **Not parsed or stored** (`chunk5/README.md`). 5f adds both for the reading feed and unsubscribe. |
| Rules | `/rules` lists them as plain sentences with a switch and a match count; `rule-review` shows a dry run. Nothing says *when* they run or what happens while Ivy is off. |
| Smart settings | `/settings/smart` is a set of switches, several "ships dark" (Categories, Junk rescue, Summaries, Phishing warnings, Contact form triage). The reason a switch is off and unclickable is prose in `next_steps.md`, not on the screen. |

## The feasibility screen

The operator asked for a screen first, on two axes: does it work from a phone over Tailscale, and is
it cheap. Results, which shaped the ranking:

| Candidate | Phone | Cost | Verdict |
|---|---|---|---|
| Facet row above the list | works (a scrolling chip row, one thumb) | M: one aggregate query, one component | **in, first** |
| Mailing-list strip + unsubscribe | works, and unsubscribe is exactly a phone job | M: a parse, a migration, one strip | **in** |
| Inline headers `Details` | works (expands in place, no sheet) | S: existing data | **in** |
| Rule provenance sentence | works | S: copy | **in** |
| Settings rows that say why | works | S: copy + a guard test | **in** |
| Selection summary in the reader pane | **irrelevant on a phone** (the floating bar already covers it) | S | **last, optional** |
| Split Send / Send Later | works | **L**: a scheduler, resumption across restarts, a view | **out** (backlog) |
| Calendar views, invite RSVP | — | L | **out** (settled) |
| MCP servers, skills, plugin sandbox | — | XL | **out** (wrong shape for one operator) |

## Stages, in build order

Sizes are the chunk-vocabulary: S is one sitting, M is a day with tests, L is a subsystem.

### 6a. The facets above the list — M

**What.** One row under the heading, before the messages: `All 16 · Unread 4 · With files 3 · Flagged 1 ·
Need you 2`. One tap narrows the list to that facet; the tap again (or `All`) widens it. Counts are
of the *base* view (the same account, folder or tag, with no facet applied), so they do not shuffle
under the finger.

**How.** `GET /inbox` gains a `facets` object computed over the same base filters — `unread`,
`attachment`, `flagged`, `need` — in one aggregate query alongside the page query, with `?facet=`
selecting one. The web side is one component used by both viewports, `aria-pressed`, reachable by
keyboard, added to the shortcuts overlay, and read by the phone's one-handed layout.

**Rules for the row.** A facet with a count of zero is hidden unless it is the active one, so the row
stays short on a phone and an active facet can always be released. `All` is always present. The row
scrolls horizontally and never wraps.

**Tests.** The counts equal the number of rows the same facet returns (property test over the seeded
dev mailbox); a facet composes with a tag view and an account view; the aggregate is one query (assert
by statement count) and stays inside its byte budget; a sparse mailbox renders a two-chip row without
layout shift; Playwright on phone and desktop; a benchmark on the potato for the aggregate over the
largest folder.

**Failure paths.** A count that disagrees with the list is a bug the test catches, never a spinner. If
the aggregate is missing from an older payload, the row renders without counts rather than with zeros.

### 6b. The mailing-list strip — S-M (with 5f)

**What.** A quiet one-line strip above the subject when a message came from a list: `From a mailing
list` and, once 5f lands, `Unsubscribe`. The same strip 5f's feed explains a skipped mail with.

**How.** Parse and store `List-Id` and `List-Unsubscribe` (`mime/`, one migration, the same place
`Authentication-Results` already lives). The strip is presentational; the button is 5f's. A message
with only `List-Id` still gets the strip — it says where the mail came from, which is true — but no
button.

**Tests.** Both headers survive a hostile corpus (folding, a bare `<mailto:>`, no `List-Id`); a
personal message never gets the strip; the strip renders in both themes and on a phone width; the
word "Unsubscribe" appears only when an action exists.

**Failure paths.** `List-Unsubscribe` is sender-controlled, so it is rendered as text and never as a
link; the URL is only ever used server-side by 5f's guarded fetch client, never by the browser.

### 6c. Inline `Details` in the reader — S

**What.** The sender row gets a `Details` toggle that expands in place: `To`, `Cc`, `Reply-To` when it
differs from `From`, the full date, the address it was sent to (which identity), the auth verdict
("Passed SPF, DKIM and DMARC at purelymail.com"), the spam score where there is one, and the mailbox
it lives in. Collapsed by default, one line tall.

**How.** Existing data only — `mime.AuthResults` is stored, 5d.3 already renders its sentence in the
sender sheet. This lifts the same facts into the header rather than replacing the sheet (the sheet
keeps the person's history, which the header does not need).

**Tests.** A message with no auth results renders no verdict line (never "failed"); a long Cc list
truncates with a count and expands; keyboard focus order is sane; the phone layout grows without
shifting the subject.

### 6d. Rules that say when they run — S

**What.** One sentence above the rules list: *"Ivy runs these as mail arrives. While Ivy is off, mail
waits, and rules run on the next sync."* Each rule keeps its switch and match count; a rule 5j wrote
from a sentence gets a quiet "from your sentence" note, and its plain sentence is what is shown, never
the compiled form.

**Why it matters.** The rules screen currently implies a server-side rule engine. Ivy's is local, and
the difference is the operator's to know — it is also why `List-Unsubscribe` needs a server-side fetch
(5f) while a rule cannot move mail.

**Tests.** Copy only, but the sentence is asserted in the component test so a refactor cannot drop it;
the note appears only for rules with a compiler source.

### 6e. Switches that say why they are off — S

**What.** A sweep of `/settings/smart` (and any other dark switch): every switch that is off, or
unclickable, carries its one-line reason in the row it governs — "Needs Smart features", "Off until
you look over your mail once", "Waits for the Jev layer". The reason appears in the row, not in a
tooltip and not in a paragraph at the bottom.

**How.** A `reason` on the settings descriptor the screen already iterates, so it is data, not markup.

**Tests.** A guard test walks the settings descriptors and fails on any switch that is off with no
reason (the same shape as the token guard: a rule the codebase cannot drift away from); the reason
renders on a phone without clipping; no switch is disabled without one.

### 6f. The reader pane as the selection surface — S, optional

**What.** On desktop and tablet, when a selection is live, the reader pane shows what is selected and
the same actions in large form ("3 conversations selected · Esc clears the selection"), instead of
continuing to show the first message. On phone nothing changes: the floating bar is already the right
answer.

**Why it is last.** It solves a smaller confusion than the five stages above, and the operator's own
phone already has the better pattern. Drop it without regret if it does not feel worth a stage.

**Tests.** Selection on/off restores the previously open message; the action set is the same objects
`BulkBar` uses (one source of truth, not a second list); Escape and the back gesture both clear it.

## Direction only: the assistant that acts

Not built here, and not scheduled. Recorded because Penguin Mail shows it can be done safely, and
because it changes how 5i's proposals should be shaped later.

The model: **the assistant executes only through the same command layer the buttons use**, so its
actions land on the **same undo stack** as the operator's own triage, and "undo that" covers both.
Three rules make that safe:

1. **Read-only tools stay read-only.** Act tools are separate, one per command, each with an approval
   card naming exactly what it will do.
2. **Volume is the trigger, not the category.** Small reversible actions run without a card; anything
   bulk (their number: more than 25 conversations to the Trash) or irreversible always asks. For Ivy,
   the irreversible list also includes unsubscribing (5f), purge and send.
3. **The undo stack is the backstop**, so the wrong click costs one keystroke. Without it, acting stays
   off — which is where 5i is today, deliberately.

That direction needs a **triage undo stack** first, which does not exist: Ivy's actions are
outbox writes with a toast, and only send has an undo. Until it does, this stays a paragraph.

## Amendments to chunk 5 plans

Two plans should absorb what the screenshots taught, both documentation-only now:

- **5i (Ask Ivy).** The trace folds to one line per step with a done/failed mark; the answer is grouped
  under short subheadings with numbered claims; a line at the bottom says what it is doing now; a `+`
  opens a new conversation and the history is reachable; the proposed actions render as one-click cards
  in the answer, each driven by the existing outbox or compose.
- **5f (Newsletters).** The unsubscribe surface is 6b's strip, not a chip: the action belongs where the
  message is, and the same strip explains a skipped newsletter in the feed.

Applied in the same commit as this plan.

## Tests and exit

Per stage, test first and watched failing, `make check`, the frontend token guard, `pnpm check`, and
`pnpm exec playwright test` on both viewports (`e2e/screens.spec.ts` covers the routes; 6a, 6b, 6c and
6d add named states). The definition of done is `TESTING.md` 9 and `STANDARDS.md` 10, unchanged.

**Exit for the chunk:** on the dev stack, a would-be operator can narrow today's mail to "with files",
open a message from a list and leave it, read who actually sent it, read why a rule runs when it does,
read why a switch is off — and every one of those is a screen the E2E suite visits.

## Failure paths (`STANDARDS.md` 4a)

| Input | Bound | Above it |
|---|---|---|
| Facet aggregate | one query over the current folder page's base filter; the same paging bound as the list | the row renders without counts, never a spinner |
| Facet row width | one scrolling line, never wrapping | it scrolls; a count above 999 renders as `999+` |
| `List-Unsubscribe` | header length bounded like every other header; contents stored as text | stored truncated, never used as a URL |
| `Details` expansion | field count fixed; Cc/To lists clipped with a count | "and 12 more" |
| Selection | `MAX_SELECTION` as today | the existing toast says so |

## Open questions (ask before each stage starts)

1. **The facet set.** Unread, with files, flagged, need you — enough? Is a fifth ("from people")
   worth it, and should "need you" survive once 5c is on?
2. **Does a facet persist?** Remembered per view across navigation, or reset every time the list opens?
   The second is simpler; the first is kinder on a phone.
3. **Strip wording**, and whether `List-Id` alone is enough to show it.
4. **`Details` scope:** the fields listed above, and whether it should eventually absorb the sender
   sheet rather than sit beside it.
5. **6f at all?** And if yes, does the reader pane keep the message visible on a tablet wide enough for
   three panes?
