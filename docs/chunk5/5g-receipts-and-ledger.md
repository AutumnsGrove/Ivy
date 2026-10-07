# 5g. Receipts and the ledger view

## Purpose

Understand receipts and invoices: extract vendor, amount, date and renewal, show them in a **ledger
view**, and remind the operator of renewals. A plain receipt filter is the baseline that works with
no model at all.

## Depends on

5a, 5b; `category` and `receipt_or_invoice` (5d). Attachment text (tier 1 extraction, 3f) feeds it.

## Where we start

- No `receipts` table exists (`ARCHITECTURE.md` 3 describes `receipts` with extracted fields and
  renewal dates in `mirror.db`).
- Tier 0 and tier 1 extraction is real (bodies, `.ics`, digital PDFs, OOXML in pure Go) and runs in
  the settle pass (3f). schema.org JSON-LD in an HTML body is not parsed yet.
- Tags and rules are real, so "tag receipts" works with a header-only rule today.

## Settled

- **Receipts and invoices:** auto-extracted fields (vendor, amount, date, renewal), a ledger view,
  renewal reminders, and a plain receipt filter as the baseline (PLAN.md 3).
- **Structured data before a model:** schema.org JSON-LD in many receipts is free and exact; try it
  before spending a `complete()` call (JEV.md 4, `ARCHITECTURE.md` 7). Jev does not extract; it only
  routes (`receipt_or_invoice`: no, receipt, invoice, renewal_notice, payment_failed).
- **Extraction tiers:** bodies/.ics (0), digital PDFs and OOXML in pure Go (1), vision (2, 5h), OCR
  far-out (3).
- **Model output is validated against a schema and treated as untrusted** (invariant 5); a wrong
  extraction must cost a wrong cell, never an action. A renewal reminder is a local reminder, not a
  send.
- **Withheld and LLM-off mail is not extracted by a model;** it still gets the baseline filter.

## Scope

1. **5g.1 Baseline.** The non-model receipt filter and the ledger view over tagged receipts, so the
   screen is useful with the LLM off.
2. **5g.2 JSON-LD.** Parse schema.org `Order`/`Invoice` data from the sanitised HTML body (bounded,
   never executing anything); store extracted fields with their source (`jsonld`).
3. **5g.3 Model extraction.** For routed receipts without usable JSON-LD, one `Complete` with a JSON
   schema over the clipped body and any attachment text; validate, store with source `model`.
4. **5g.4 `receipts` table and API.** Migration, a ledger list endpoint (cursor, filters by vendor,
   account, month), and operator corrections stored as locally owned state.
5. **5g.5 Renewal reminders.** A local list and marker for upcoming renewals (no push).
6. **5g.6 The ledger screen** with totals by month and vendor and an export.

## Tests and exit

- JSON-LD fixtures (valid, partial, hostile, huge): fields extracted or cleanly absent, never a crash
  or a guess.
- An invalid or schema-violating model reply stores nothing and records the error.
- Currency and amount parsing table-driven (separators, symbols, negative refunds).
- Account off: the baseline filter and ledger still work, with zero provider requests.
- **Exit:** the ledger shows real extracted receipts on the dev stack; accuracy on real receipts is
  the operator's own check.

## Failure paths (STANDARDS 4a)

| Input | Bound | Above it |
|---|---|---|
| JSON-LD block | size and nesting depth | skipped, recorded |
| Extraction input | clipped body plus bounded attachment text | clipped |
| Extracted text fields | length maxima | truncated, never trusted as markup |
| Reminders | a stated horizon and count | the nearest first; the rest shown on request |

## Open questions

1. **Fields and types:** amount as integer minor units plus currency code? Which fields are
   required before a row is shown (vendor, amount, date)?
2. **Corrections:** where are operator edits stored (state.db, so they survive a mirror rebuild) and
   do they override later re-extraction?
3. **Duplicates:** the same receipt in two accounts or a resend; how are they linked, and does a
   shared content key (N8) already cover the identical case?
4. **Renewal reminders:** how far ahead, and where they appear without push (a list, a banner, a
   snoozed item that wakes)?
5. **Ledger screen scope:** totals by month and vendor, tags, export format (CSV?), and whether it is
   per account or combined (a combined *view* of already-extracted rows is fine; extraction is per
   account).
6. **`payment_failed` and `renewal_notice`:** do they raise needs-me (5c) or only the ledger?
7. **Backfill:** do we extract historic receipts (cost estimate and the cap) or only new mail?
