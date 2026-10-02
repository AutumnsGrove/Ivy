# S1: what Purelymail really does

Run 2026-10-02 against a dedicated dev mailbox on `imap.purelymail.com:993` and
`smtp.purelymail.com:465` (implicit TLS). Code: `spikes/s1-purelymail/` (throwaway; reads
credentials from the git-ignored `.env`, prints none, sends no mail, and removes its one scratch
folder afterwards). One real message from another provider had been delivered to the INBOX for
the header check.

## Facts

| Question | Result |
|---|---|
| IMAP capabilities | `ANNOTATION AUTH=PLAIN CHILDREN COMPRESS=DEFLATE CONDSTORE ENABLE ESEARCH ESORT I18NLEVEL=1 IDLE IMAP4rev1 LITERAL+ MOVE NAMESPACE QRESYNC RIGHTS= SASL-IR SEARCHRES SORT UIDPLUS UNSELECT WITHIN` (identical before and after login) |
| CONDSTORE and QRESYNC | **advertised**; `SELECT (CONDSTORE)` returned `HIGHESTMODSEQ` |
| MOVE, UIDPLUS (`APPENDUID`), IDLE, UNSELECT | advertised and `APPENDUID` returned |
| Not offered | SPECIAL-USE (capability), LIST-EXTENDED, LIST-STATUS, METADATA, OBJECTID, SAVEDATE, NOTIFY, THREAD, `X-GM-EXT-1` |
| Special folders | `LIST` still carries `\Archive \Drafts \Junk \Sent \Trash` attributes, delimiter `.`; six mailboxes in a fresh account. Roles can be read from attributes even though SPECIAL-USE is not advertised. |
| Tags as keywords | `PERMANENTFLAGS` includes `\*`. Three custom keywords (`$ivy-tag`, `ivytag-receipts`, `$label1`) were stored and **read back after UNSELECT and re-SELECT**. Keywords are a sound tag store. |
| Connections | 13 simultaneous authenticated IMAP connections accepted (1 plus 12); the real ceiling is higher and was not probed further. |
| `COMPRESS=DEFLATE` | advertised; not exercised |
| SMTP | `SIZE 51200000` (about 48.8 MiB), `AUTH LOGIN PLAIN`, `8BITMIME`, `PIPELINING`; no `SMTPUTF8`, `CHUNKING`, `DSN` or `REQUIRETLS`. `AUTH PLAIN` works. |
| Auth headers on inbound mail | `Authentication-Results` holds only `<host>; auth=pass` (the submitter's login). **No SPF, DKIM or DMARC verdicts are added**; the original `Dkim-Signature` and `Return-Path` are preserved. |

## Decisions and what changes

- **QRESYNC is available, so the sync design may use it** (`ARCHITECTURE.md`): CONDSTORE/QRESYNC
  incremental sync with a UID/flags comparison as the fallback when UIDVALIDITY changes. It also
  settles S2's open question: `mailworld` **must implement CONDSTORE and QRESYNC**, because the
  `imapmemserver` fake lacks them (`s2-imapmem.md`).
- **Tags are IMAP keywords.** No ANNOTATION dependency needed, which also keeps tags portable.
  Keyword names should be a documented prefix (the probe used `$ivy-` and `ivytag-`; both worked),
  and `mailworld` should honour `PERMANENTFLAGS \*`.
- **No `LIST-STATUS`:** per-folder counts need a separate `STATUS` per mailbox.
- **No provider-side authentication verdicts.** Showing "failed DMARC" style badges would mean
  verifying DKIM and SPF ourselves; plan for none initially and treat it as a later feature.
- **Attachment/send size ceiling** is about 48.8 MiB at the SMTP layer; the compose screen should
  enforce a slightly smaller limit so the failure appears before sending.
- **Roles from `LIST` attributes**, with a name-based fallback; `mailworld` should emit the same
  attributes and the `.` delimiter.

## Send and download (S1b, `spikes/s1-purelymail/send/`)

- One real message, authorised by the operator, was submitted over SMTP (465, `AUTH PLAIN`,
  envelope sender equal to the mailbox) to an address on another domain. SMTP accepted it and the
  operator confirmed it arrived.
- **SMTP submission does not file a copy in Sent.** The Sent folder (found by its `\Sent`
  attribute) stayed at 0 messages for 10 s after the send. **Ivy must `APPEND` its own copy to
  Sent after a successful submission** (and flag it `\Seen`), which fits the outbox model in
  `ARCHITECTURE.md`: submit, then APPEND, and treat a failed APPEND as retryable without resending.
- **Download works for the full range of sizes seen:** five messages (3.5 KB and 1.7 KB text,
  a 132 KB photo, a 19.8 MB attachment) fetched whole over one connection with byte counts
  matching the server's `RFC822.SIZE`. The 19.8 MB message was fetched into memory by the spike;
  the real sync must stream large bodies to disk, per the bounded-data rule.
- Every message arrived flagged `\Recent` only (unseen), as expected for a fresh mailbox.
- **A trap in go-imap v2:** `imap.SeqSetNum(1, n)` is the two messages 1 and n, not the range;
  use `SeqSet.AddRange`. Worth a lint note when writing the real client.

- **Send to a routed alias:** a second message, addressed to a routed alias on the operator's
  domain, was accepted by SMTP; the Sent folder again stayed empty (the no-Sent-copy result held
  twice). The alias routes to a different user, so arrival is not visible from the dev mailbox;
  the operator confirmed it arrived, so **alias routing works** for mail submitted by the dev user.

## Resend and DMARC: skipped

Not tested here. The operator reports that Grove's Resend mail was exercised recently under the
domain's `p=reject` policy and delivered fine, so this is accepted on that report and is not
independently verified by the spike.

## Send-as (S1c)

Logged in as the dev user, a message with a routed alias as both the envelope sender and the
`From:` header was **accepted by SMTP, delivered, and displayed as from the alias** (operator
confirmed). No credentials for the alias were needed: SMTP does not tie `From:` to the login, so
the provider's policy decides, and Purelymail allowed it.

- **Why it was allowed is not established.** It may be because the alias routes to a user in the
  account, or because any user may send as any address on the domain. Telling these apart needs a
  send from an address with no routing rule (not done).
- **Implications:** Ivy identities are plain `From:` values, with `From` chosen from a stored list
  of known identities and never accepted free-form from the client. A mailbox password can
  therefore send as other addresses on the domain, so credentials for any mailbox on that domain,
  including the dev one, are as sensitive as production credentials (never in CI secrets, logs or
  committed files).

## Not tested

Whether send-as is limited to routed aliases or open to any address on the domain;
`COMPRESS=DEFLATE` actually working with go-imap; annotation behaviour; the true connection limit.
