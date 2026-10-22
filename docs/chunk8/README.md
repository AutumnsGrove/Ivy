# Chunk 8: Ivy that acts

Planned 2026-10-22. Ask Ivy (5i) can read and answer. This chunk lets it **propose changes and carry
them out on a click**, with one undo stack behind both the assistant and the operator's own triage.

The order matters: the undo stack (8a) comes **before** anything acts, and is worth building even if
every later stage is dropped, because it makes plain triage undoable today.

## Purpose

1. Every triage change becomes undoable, from the toast, from a list, and by `Ctrl/⌘+Z` (8a).
2. The assistant's act tools produce **proposals**, never writes (8b).
3. The proposals cover what the operator actually does: the five reversible actions, plus move and
   junk (8c).
4. Volume and irreversibility decide when a second confirmation appears (8d).
5. "Undo that" works, and covers both the assistant's changes and hers, newest first (8e).

## Depends on

**8a: nothing.** It sits on the outbox (3d), tags (3e) and snooze (3g), all done.
**8b-8e: 5i** (the agent loop, its tools and its trace), 5a (the gate and the ledger) and 5c.0
(withheld mail is invisible to the tools). 5e's suggestions are independent of all of it.

## Settled by the operator, 2026-10-22

- **The model never executes anything.** It proposes; a click executes through the existing outbox or
  compose. That is 5i's rule and it does not change.
- **Act tools in the first version:** archive, trash, tag add/remove, snooze, mark read/unread, move to
  a folder, junk/not-junk — the reversible set.
- **The irreversible set is card-only, always:** send, delete forever (purge), unsubscribe, changing a
  rule, changing a setting. A card names the exact target, every time, even with every "skip the
  asking" setting the operator could later turn on.
- **Unchanged from earlier:** writes go to IMAP first; nothing is erased; local state lives in
  `state.db` and is the thing that gets backed up; LLM output is plain text.

## Where we start

| Piece | Today |
|---|---|
| Triage writes | `outbox` rows keyed by **content key + folder**, executed IMAP-first with retry across restarts, an optimistic UI that rolls back, and a toast. **No undo**: only send has one (`send_queue`'s undo deadline, a different mechanism). |
| The toast | `Toaster.svelte` supports a single action per toast, already used by send's Undo. |
| Ask Ivy | Planned in 5i, not built: a loop with `search_mail`, `read_mail`, `think`, an SSE trace, verified citations, and "proposed actions, each one click" as decision 4 — with no design written for it. |
| The gate | `llm.Gate` is real; every call is a ledger row; `llm/arch_test.go` already fails on anything reaching a provider outside `llm/`. |
| Settings | `/settings/smart` has the switch pattern (and, after chunk 6, a reason per switch). |

## The three rules that make acting safe

Written down first, because they decide the design:

1. **Separate tools, and the act tools only write proposals.** `read_mail` cannot write. An act tool
   inserts one `proposal` row and returns its id. Nothing else in the loop touches the database. This
   is testable as an architecture rule, in the spirit of `llm/arch_test.go`.
2. **The proposal's targets are verified, never authored.** The ids come from tool results the loop
   actually read this run (the same rule as citations in 5i), and the server re-checks them against
   `state.db` before rendering. A hostile message can at most make the model *ask*; it cannot choose
   what the click will do beyond the set it read.
3. **Every executed proposal is one undo entry**, and the inverse is an ordinary outbox write. So
   "undo" is not a special path through the code — it is the same path with the opposite op, which is
   why it works across a restart and an offline stretch.

## Scope

### 8a. The undo stack — M

**What.** One entry per user-visible change (a bulk action over 40 messages is **one** entry). The
toast's action becomes "Undo", `Ctrl/⌘+Z` does the same, and Settings gains **Recent changes**: the last
20 entries with what, how many, when, and an Undo button each. An undo of an undo is not offered; a
second undo makes a new entry.

**How.** A `undo` table in `state.db` (the backup-relevant DB), append-only:
`(id, account_id, kind, targets_json, inverse_json, expected_json, created_at, undone_at, expires_at)`.
`kind` is the action (`archive`, `trash`, `tag`, `untag`, `snooze`, `read`, `unread`, `move`, `junk`,
`notjunk`). `targets_json` is the content keys and folder ids; `inverse_json` is the outbox op to
enqueue; `expected_json` is the state the undo assumes (each target still where it was). Executing an
undo is one transaction: verify `expected`, enqueue the inverse in the outbox, stamp `undone_at`. If a
target moved since, the undo is **refused with a reason** naming what changed, and the entry stays.

**Bounds and pruning.** 200 entries or 14 days, whichever comes first, pruned on write. An entry whose
inverse can no longer be verified is marked `stale` rather than deleted, so the list can say "too old".

**Tests.** Undo restores exactly (folder, tags, snooze, flags) for each kind; a bulk undo is one entry
and one transaction; an undo after the message moved is refused and nothing is enqueued; the prune
keeps the newest 200; undo survives a restart (written to `state.db`, not memory); a failed inverse
leaves the entry usable and the toast says so; the toast, the key and the list all go through the same
function.

**Why first.** It pays for itself immediately: today archiving 40 conversations is one click with no
way back.

### 8b. Proposals as data — M

**What.** The loop's act tools create a `proposal` row and return its id; the answer renders a card:
what will happen, how many, the first few subjects, and — in words — the undo that will be available
("Archiving 12 conversations · Undo for 14 days"). Under the volume threshold it is one button;
above it, 8d's second card.

**How.** `proposal(id, conversation_id, account_id, kind, targets_json, count, created_at, expires_at,
state, executed_undo_id)`. `state` is `offered`, `declined`, `expired`, `executed`, `refused`. A
proposal **expires in an hour** and cannot be executed after that: a card from yesterday is a
loaded gun.

**Tests.** An act tool's call inserts exactly one proposal and changes nothing else (assert every table
is unchanged); a proposal whose ids the loop did not read is refused server-side; an expired proposal
cannot execute; declining is recorded; two proposals for the same targets do not double-apply if the
first was executed (the expected-state check catches it).

### 8c. The act tools — M

One tool per command, each mapping onto an existing outbox op, each with a dry-run preview
(`count`, a sample of subjects, and the inverse). `archive`, `trash`, `tag`, `untag`, `snooze`, `read`,
`unread`, `move`, `junk`, `notjunk`. No tool for send, purge, unsubscribe, rules or settings: those
are proposals of a different shape, written by the pipeline rather than by a loop tool (5f's
unsubscribe, 4's send), and they are always cards (8d).

**Caps per proposal:** at most 200 targets (the selection cap, `MAX_SELECTION`, is 200 too); the dry
run is bounded like any list; a query that matches 4 000 messages returns "4 000 matching, this
proposal covers the first 200".

**Tests.** Each tool's reverse is exactly 8a's inverse for the same kind (one test table, both
directions); the cap truncates with a marker; a tool called with an empty result makes no proposal and
returns a plain sentence.

### 8d. Volume and irreversibility — S

**What.** One click under **25** targets (a setting); above it, a second card that states the count,
the account and what cannot be undone, and needs a deliberate press ("Archive 40 conversations").
Irreversible actions are cards with no "don't ask again", no always-allow, and the exact target named
("Send this to Priya Raman and 2 others", "Delete 3 messages forever").

**Why not Penguin Mail's "Always Allow".** They offer it per exact web address and per MCP command,
which is reasonable for a desktop tool with an extension ecosystem. Ivy has none, so an always-allow
here would only ever be used to skip the thing that protects the operator from a bad proposal.

**Tests.** 24 targets is one click and 26 is two; the threshold is read from settings and cannot be
turned off, only raised; an irreversible proposal has no "always" option in the DOM (a frontend test);
a proposal mixing accounts is refused (invariant 4).

### 8e. "Undo that" and the shared record — S

**What.** "Undo that" resolves the newest undo entry created by **this conversation's** executed
proposals, refuses if it was already undone or has gone stale, and says what it undid. The
operator's own actions interleave on the same stack, so "undo that" after her own click undoes her
click — recorded as a decision because it is surprising, and because it is the point.

**How.** `proposal.executed_undo_id` is how the conversation knows its own entries; 8a's table is the
single source. Nothing about the model decides which entry is "that".

**Tests.** "Undo that" with no executed proposal says so; after two proposals it undoes the second;
after the operator's own archive it undoes that; a stale one is refused with the reason; the trace
shows the undo as a tool row with a done/failed mark (5i's folding).

## What the model may never do

Written as the invariant list the tests enforce, and as the table the docs' "what each helper does and
when it asks" page will copy:

- write anything except a `proposal` row;
- act on mail it did not read this run, on a withheld message, or on an account with smart features off;
- choose an approval threshold, skip a card, or create an always-allow;
- touch send, purge, unsubscribe, rules or settings through an act tool;
- run its own undo without an executed proposal to attach it to.

## Tests and exit

Test first and watched failing, `make check`, the frontend token guard, Playwright on both viewports
for the card and the Recent changes list, and the hostile corpus extended with an "archive everything"
message and a message that asks to be un-archived.

**Exit:** on the dev stack, "archive everything from Trail Notes older than a week" produces one card;
the click archives exactly those, the toast offers Undo, the key undoes it, "undo that" works, Recent
changes lists both, and every one of those states has a test. The operator's live run on her own
mailbox is her own pending line.

## Failure paths (`STANDARDS.md` 4a)

| Input | Bound | Above it |
|---|---|---|
| Targets per proposal | 200 | truncated with "first 200 of N", stated in the card |
| Proposals per conversation | a stated cap | further act calls return "make a new conversation" |
| Proposal lifetime | 1 hour | expired; the card says so and cannot execute |
| Undo entries | 200 or 14 days | pruned; a stale entry is listed, not silently dropped |
| Undo of a changed target | the expected-state check | refused, naming what moved; the entry stays |
| Model failure mid-loop | 5i's step and token caps | no proposal, a plain sentence, nothing executed |
| Two executes of one proposal | state machine + expected-state check | the second is refused, one undo entry exists |

## Open questions (ask before 8a and before 8c)

1. **The threshold:** 25 is Penguin Mail's number, not ours. Per proposal or per action? Configurable
   only upward, or also downward to 1 (card for everything)?
2. **Undo depth:** 200 / 14 days enough, and should a bulk entry be **partially** undoable (undo 3 of
   the 40) or all-or-nothing? All-or-nothing is simpler and is the plan.
3. **Proposal lifetime:** one hour, or until the conversation closes, or until Ivy stops? One hour.
4. **"Undo that" scope:** may it undo the operator's own last action, as Penguin Mail's does? The plan
   says yes — confirm, because it is surprising the first time.
5. **Where the assistant's cards live:** in the answer (the plan), in a dedicated "Waiting for you"
   list that survives navigation, or both? The second is friendlier on a phone.
6. **Do proposals count against the spend caps?** The proposal itself is free; the model calls that
   produced it are already ledgered. Confirm nothing extra is needed.
