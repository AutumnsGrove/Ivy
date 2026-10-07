# 5j. Rule compiler and smart checks

## Purpose

Rules written by **describing** them: one free-form sentence becomes a validated rule (and any new
smart checks) in one generation. Smart checks are user-owned classification questions Jev answers per
message ("looks like a receipt"), so a rule can say "from Cloudflare **and** looks like a receipt".

## Depends on

5a, 5b (the registry stores checks as questions). The rules engine, applier and editor are real (3g).

## Where we start

- `rules/` has the engine and applier; header conditions and local-only actions (add tag, show in
  Reading, snooze) are real, with a rule editor, dry-run-style match counts and the ingest pass.
- `rules/new/review` is a dead mock route (the compiler is the missing piece); `/checks` and
  `/checks/{id}` are in the contract with no gateway handler, mock-backed in the web client.
- `docs/JEV.md` 3F is the detailed design; spikes for compiler accuracy are not yet run.

## Settled (JEV.md 3F, PLAN.md 3, `ARCHITECTURE.md` 7)

- **One structured `complete()` call** with a JSON schema, through the gate and ledger, compiles the
  sentence once at authoring time; running a rule never calls the compiler or regenerates anything.
- **Compiler input:** the sentence, the existing checks and tags (so it reuses them instead of
  inventing duplicates), the allowed condition and action vocabulary, and the account list. **No mail
  content.** The output is untrusted: validated against the schema and rejected if it uses anything
  outside the vocabulary.
- **Output:** `conditions[]` (from, subject, account, has_attachment, `check` references),
  `new_checks[]` (id, plain instructions with what counts and what does not, options, suggested
  threshold), `actions[]` restricted to **local-only: add tag, show in Reading, snooze**; or a single
  `clarify` question (answering it is the only time a rule costs a second call).
- **Review before live:** a plain-sentence restatement and a **dry run on the last 200 messages**;
  the header-only part is free and local, the check part reads a sample with Jev, shows an estimate
  and is ledgered. Nothing runs until the operator turns the rule on.
- **Stored form:** generated check instructions are editable text with a hash; the cache key is
  (message, check id, instruction hash, model); editing re-reads lazily and may offer a backfill with
  an estimate and the cap.
- **Scope and gates:** a check runs only on accounts with smart features on (locked elsewhere); the
  compiler call needs at least one opted-in account; tuning is by threshold (Eager / Balanced /
  Careful) using the odds sheet, since Jev cannot be trained per user.
- **Rule actions are never move, delete, forward or send** (brief invariant 6).

## Scope

1. **5j.1 Checks as data.** The `checks` table and API (`/checks`), instruction hash, per-account
   scope, threshold presets, joining the 5b registry; the Smart checks screen with a try-on-recent-mail
   action, replacing the mock.
2. **5j.2 Rule conditions on checks.** The engine reads cached check answers; a check that has not
   been evaluated yet is "unknown", which never matches (and is shown in the dry run).
3. **5j.3 The compiler.** The `rules/compile` pipeline: gate → model → schema validation →
   vocabulary check (known fields, known accounts, local-only actions) → dry run → review → store.
4. **5j.4 Dry run.** The last 200 messages, header-only free, check part sampled with Jev, with an
   estimate shown first.
5. **5j.5 Clarify flow and the review screen,** replacing the dead `rules/new/review` mock route.
6. **5j.6 Compiler spike.** Accuracy on about 30 hand-written sentences (valid-schema rate, reuse of
   existing checks, local-only actions); results in `docs/spikes/`.

## Tests and exit

- A compiled rule using a move or delete action, an unknown field or an unknown account is rejected
  with a clear reason; the compiler fed hostile sentence text cannot produce a non-local action.
- Compiling an account-off setup makes no request; the compiler sends no mail text (a fake asserts
  the request body contains none).
- Editing a check changes its hash and re-reads lazily; running a rule never calls `Complete`.
- Dry run numbers match what applying the rule would then do (property over a fixture mailbox).
- **Exit:** a sentence becomes a working rule end to end on the dev stack; compiler accuracy is
  measured by the spike on hand-written sentences (cents), and real-mail check accuracy is the
  operator's own check.

## Failure paths (STANDARDS 4a)

| Input | Bound | Above it |
|---|---|---|
| Sentence length | a maximum | refused with a message |
| Clarify rounds | safety stop at 10 | the detail editor opens with what was understood |
| Rules, checks, questions per Jev call | 500 rules, 50 checks, 100 questions per call (operator); question tokens count against the 32k context | refused at creation with a clear message |
| Dry-run sample | 200 messages, a cost estimate shown first | stops at the cap |
| Compiler reply | schema-validated, size-bounded | invalid → recorded error, nothing stored |
| Backfill after an edit | opt-in with an estimate, capped | stops at the cap |

## Decisions (Q&A with the operator, 2026-10-07)

1. **Conditions:** from address or domain, subject contains, has attachment, account and folder, and
   smart check references. Actions stay local-only (add a tag, show in Reading, snooze).
2. **Several rules from one sentence are allowed,** each reviewed and switched on separately.
3. **Clarify: unlimited back and forth in practice, with a safety stop at 10 rounds** (the operator
   accepted the stop), after which the detail editor opens with what was understood.
4. **Built-ins are visible and tunable** on the Smart checks screen, marked built-in, with your edits
   stored apart from shipped defaults; your own checks share the list.
5. **Editing a check re-reads lazily;** a "Re-read recent mail" button offers a backfill with an
   estimate. Rules using it keep working on old answers, and mail not yet re-read is "unknown", which
   never matches.
6. **A rule that references a check is allowed on an account with smart features off,** inactive there
   ("needs smart features"); nothing is ever sent to a model for that account.
7. **Limits (operator): 500 rules, 50 checks and 100 questions per Jev call.** Context budget and the
   measured range are the build notes in 5b decision 9: the question text counts against the 32k
   context, and a spike at 50 and 100 questions runs before the larger sets are relied on.
8. **The mock rules and checks fixtures are removed** when the real endpoints land; the dev stack seeds
   real rows instead (assumed; confirm at build).

## The questions as asked

1. **Compiler model and prompt,** and how hard the schema is (one rule per sentence, or several?).
2. **Condition vocabulary:** exactly which fields and operators (from domain vs address, subject
   contains, has attachment, account, age?); and **limits** on rules and checks per account.
3. **Clarify UX:** how a clarifying question is asked and answered, and a cap on rounds.
4. **Edited-check semantics:** does an edit backfill old mail by default (no; it offers it) and does a
   rule that depends on a re-read check pause meanwhile?
5. **Checks on LLM-off accounts:** shown locked; may a rule with a check still be created and sit
   inactive for that account?
6. **Sharing built-ins:** are the 5c to 5g questions visible and editable on the Smart checks screen
   next to the operator's own, or hidden?
7. **Migration from mock:** the existing mock rules and checks fixtures: preserved as seed data for
   the dev stack or removed?
