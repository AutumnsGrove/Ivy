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

## Not tested (needs an outbound send or more probing)

Whether SMTP submission files a copy in Sent or Ivy must `APPEND` one; send-as from a routed
alias; whether mail relayed from Resend passes DMARC under `p=reject`; `COMPRESS=DEFLATE` actually
working with go-imap; annotation behaviour; the true connection limit. The first two decide the
send path and are worth one real send between two owned addresses when sending is built.
