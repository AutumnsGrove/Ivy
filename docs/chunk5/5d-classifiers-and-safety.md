# 5d. Classifiers and safety

## Purpose

The core classifier questions (category, automated, urgency, deadline, asking a question), the
safety questions that decide what must **not** reach a model (injection tripwire, sensitive
content), the warnings (phishing risk), and Junk rescue. This plan also makes the first-class mail
types real: contact-form submissions, security and abuse reports, and protected personal mail.

## Depends on

5a, 5b. 5h and 5i depend on this plan's **withheld-mail** rules.

## Where we start

- Mail is mirrored and Junk is a normal folder; moving to or from Junk is already an IMAP move that
  trains the provider (3b writes). `Authentication-Results` is parsed and stored
  (`mime.AuthResults`, `messages.auth_results`, migration 3), but which `authserv-id` to trust is an
  open design decision from the first review.
- **`X-Spam-Status` is not parsed or stored anywhere** (checked 2026-10-07). Showing the score needs
  a parse change in `mime/` and a mirror migration, so it is part of 5d.3, not a given.
- The `rules` package and tag machinery are real; "placed for you" tags exist as a design.

## Settled

- **Catalog tiers** (JEV.md 3): **A** `category`, `is_automated`, `urgency`, `has_deadline`,
  `asks_question`; **B** `phishing_risk`, `injection_tripwire`, `sensitive_content`,
  `attachment_worth_reading`, `junk_rescue`, `contact_form_quality`, `cold_outreach`. Options and
  uses are in JEV.md; instruction text lives in the YAML, not in Go.
- **Junk rescue only** (round 18): `junk_rescue` runs only on mail the provider put in Junk;
  `looks_real` at a high threshold shows a quiet chip and a one-tap **Not junk**, which is an IMAP
  move; it never moves anything itself. `is_spam` on the Inbox is **not planned**. Highest value:
  contact forms and legal or abuse mail lost in Junk.
- **Local signals first:** show the spam score from `X-Spam-Status` and the SPF/DKIM/DMARC verdicts
  without any model; skip the call when auth already fails hard.
- **Withheld mail:** a message the tripwire or the sensitive check flags is withheld from stage 2,
  ask and vision, and flagged. A cheap tripwire, not a guarantee (JEV.md 3B). OTP codes also get a
  local regex.
- **First-class mail types** (PLAN.md 3): contact-form submissions (Reply-To-aware, already handled
  by the reply logic), security and abuse reports (priority, **LLM off by default**), personal
  correspondence (protected from any automated handling); other automated mail gets generic triage.
- **A wrong guess costs nothing;** no question deletes, moves or hides anything.

## Scope

1. **5d.1 Tier A questions** with instructions, thresholds and the odds visible; `category` drives
   routing to the Reading feed (5f) and the ledger (5g) and the inline markers.
2. **5d.2 Withheld-mail plumbing.** A `withheld` fact on the content key, written by the tripwire and
   sensitive check, read by the gate's policy check so stage 2, vision and ask cannot see the message.
   This is built **before** any feature that sends mail text to a chat or vision model.
3. **5d.3 Local signals.** Parse and store `X-Spam-Status` (a `mime/` change and a mirror migration,
   with the usual rederive path for existing mail), then show the spam score and the auth verdicts
   on the message, no model.
4. **5d.4 Junk rescue.** The question on Junk-folder mail only, the chip, and Not junk wired to the
   existing move; tested that it never moves anything itself.
5. **5d.5 Mail types.** Security and abuse addresses marked LLM-off by default; the contact-form and
   personal-correspondence protections made explicit and tested.
6. **5d.6 Tier B warnings** (`phishing_risk` banner, `contact_form_quality`, `cold_outreach`) as
   time allows; each is its own small stage.

## Tests and exit

- Withheld mail never appears in any gate-checked request body (a fake that asserts on the body).
- A hostile corpus against `injection_tripwire`, `category` and `needs_me`: probabilities may move,
  actions never happen; recorded loosely.
- `junk_rescue` runs only on Junk; Not junk is one IMAP move through the outbox; nothing else moves.
- A security or abuse address makes zero calls unless the operator turns it on for that address.
- Personal correspondence (`is_automated` no, a known correspondent) is never touched by an
  automated behaviour such as a quiet fold.
- **Exit:** the classifiers and markers run on the dev stack; the accuracy check on labelled real mail
  is the operator's own pending line.

## Failure paths (STANDARDS 4a)

| Input | Bound | Above it |
|---|---|---|
| Junk volume | stated per-tick burst, cap applies, skip rules (auth fails hard, spam score high) | the rest waits; Junk is the highest-volume folder |
| Tripwire on a huge message | clipped state | clipped; fail-closed: if the tripwire cannot run, the message is **withheld** from later stages |
| Provider down | n/a | no chips; the app is as today; withheld defaults to closed |

## Open questions

1. **Which `authserv-id` do we trust** for `Authentication-Results` (the provider's, only)? This is
   the open design decision from the first review; the phishing banner depends on it.
2. **Fail-closed or open** when the tripwire or the sensitive check cannot run (provider down, cap
   reached): is the message withheld from later stages until it has been checked? The plan assumes
   closed.
3. **Final option sets and instruction text** for each question, and the per-question default
   threshold (Eager, Balanced, Careful).
4. **Which addresses are "security and abuse"** and how the operator marks them (a setting per
   address?).
5. **What the operator sees when mail is withheld:** a quiet marker, an explanation, or nothing?
6. **Tier B order:** which of `phishing_risk`, `contact_form_quality`, `cold_outreach` ship, and
   `cold_outreach`'s relationship to the "quiet auto-archive" idea (a deferred feature that must
   never touch personal mail).
7. **`category` to tag:** does a category place a visible "placed for you" tag, or only route views?
