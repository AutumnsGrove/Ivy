# S4: is Jev usable for Ivy's questions?

Run 2026-10-02 through OpenRouter's `/systemone` with `jev-latest` (resolved to
`typesafe/jev-1.13-20260917`). Code and a 40-email hand-labelled synthetic corpus:
`spikes/s4-jev/`. Total spend about $0.007 against a hard cap of $0.05 (cost read from
`usage.cost`). Raw results were written to the git-ignored `.dev/`.

**Honest limits of this evidence:** the corpus is synthetic, short (about 100 tokens per email) and
written by the same person who labelled it; the labels for a few emails are arguable. It is enough
to compare designs and settle shapes and costs. It is not a measured accuracy for real mail; that
needs the operator-labelled real sample in `JEV.md` section 5, item 3, which was not done (real
mail was not sent to a third party for a spike).

## Verified shapes

- **Three question types exist:** `choice`, `noul`, `score` (the server's validation error lists
  them). Errors are structured 400s that name the failing path.
- **`choice`:** as in `JEV.md`. Answer `{choice, probabilities, confidence}`.
- **`noul`:** `{type, instructions}` only (a `criteria` string or record is rejected). Answer
  `{type: "noul", noul: p}` where `p` is the **probability the statement is true**: 0.02 on a
  thank-you note, 0.99 on clear requests. One number, no confidence field.
- **`score`:** `criteria` is an **ordered array** of levels (strings, or `{name, description}`).
  Answer `{score, legend, probabilities, confidence}` where `probabilities` is over level indices
  and **`score` is the expected level index**, the sum of index times probability (checked: 0.56
  for p=[0.44, 0.56, 0]; 1.49 for p=[0, 0.51, 0.49]). It is not normalised to 0 to 1.
- **Not validated:** whether `noul` and `score` are as well calibrated as `choice` on real mail
  (five answers each were inspected, all sensible).

## Results (9 questions per call, 40 emails)

| Question | Result |
|---|---|
| `is_automated` | 40/40 |
| `injection_tripwire` | 40/40: all 5 injected emails flagged, and a benign "ask your assistant to check my calendar" email was correctly not flagged |
| `category` | 35/40. Misses are taxonomy gaps, not noise: a phishing email and a fake invoice landed in `notification`/`receipt`, a failed-payment notice in `notification`, and a landlord email scored `receipt` at 0.53. Do not rely on `category` to detect scams; that is `phishing_risk`'s job |
| `phishing_risk` | all 3 real phishing emails caught at every threshold. False positives fall with the threshold: 10 at p>=0.5, 3 at 0.75 (an SEO spam form message, a "we miss you" mailer, and an email telling the AI to leak mail, arguably all fair), 2 at 0.85, 1 at 0.95. Use 0.85 to 0.95 for a visible warning |
| `needs_me`, first wording | 24/40 exact. Flags bulk mail as `likely` with high confidence (sales, invoices, a webinar). At p>=0.75: precision 0.73, recall 0.94 |
| `needs_me`, sharper wording | 31/40 exact. At p>=0.75: **precision 1.00, recall 1.00** (17 of 17, 0 false). At 0.85 recall falls to 0.82 |

**Wording is the dominant lever.** The sharper `needs_me` text told Jev what does not count (bulk
mail, receipts, routine alerts) and named the exceptions that do (a failed payment or build, a
legal deadline). The caveat: I wrote that wording after reading the first run's misses on the same
40 emails, so the 1.00/1.00 is optimistic. It shows wording moves precision from 0.73 to the 0.9s;
it does not prove 1.00 on unseen mail. Keep the v1 vs v2 pair in the question YAML history and
re-measure on a held-out or real sample before trusting a threshold.

**Injection:** the tripwire is the strongest result. The injected instructions did not visibly
flip the classification answers: two injected emails stayed `needs_me=none` (0.77, 0.91); a third
(an injected sale email) scored `likely` at 0.91, but a near-identical un-injected sale email also
scored `likely` at 0.83 under the same wording, so that error is the wording's, not the
injection's. Six injected samples is a small corpus; hostile text can still skew numbers.

## Cost, latency, scale

- **Cost per email, 9 questions:** average 1,164 input tokens and $0.000049 on this short corpus
  (about $0.49 per 10,000 emails). A single `needs_me` call was about 460 tokens and $0.000019.
- **`JEV.md`'s "cost is about the state tokens" is wrong for short mail.** The question text counts
  as input: each question adds roughly 90 to 110 tokens, and the marginal cost is about $0.000004
  to $0.000005 per question (1, 3, 9, 18 questions: 370, 631, 1,160, 2,013 input tokens). For long
  emails (thousands of tokens) the state dominates and the question set is cheap by comparison.
  Keep instructions and criteria terse.
- **Latency is flat in the question count:** 220 to 330 ms for 1 to 18 questions; single-call
  p50 216 ms, p90 326 ms, max 459 ms at 4 concurrent.
- **No throttling at 30 concurrent calls:** all 200, p50 457 ms, p90 1.24 s, max 1.31 s.
- **Account identifiers appear in error bodies** (a `user_id`). Never log or ledger raw error
  bodies; keep only the structured fields.

## Decisions

1. **Use `choice` for the Tier A catalog** (validated here); consider `noul` for binary questions
   (`has_deadline`, `asks_question`, `is_automated`) and `score` for ordered scales (`urgency`)
   once their calibration is checked on real mail. Until then `JEV.md`'s "yes/no as a choice"
   fallback stands.
2. **Ship the sharper `needs_me` wording** as the starting instruction, with threshold 0.75, and
   write every question's instructions as "what does not count, then the exceptions".
3. **Stage 1 of the cascade is viable:** `needs_me` at 0.75 plus the tripwire gives a cheap, quiet
   front line; precision on real mail still has to be measured.
4. **Do not put scam detection in `category`;** keep the separate `phishing_risk` with a high
   threshold for banners.
5. **Cost budget:** plan with $0.00005 per short email for the full triage set and a monthly cap
   far above a normal mailbox; a 10,000-message backfill is about fifty cents.

## Not done

Operator-labelled real sample (100 messages); a held-out synthetic set; calibration curves;
messages near the 32k-token limit; non-English mail; the rule-compiler accuracy spike
(`JEV.md` section 3F, a chat-model task, not Jev); the other tiers' questions.
