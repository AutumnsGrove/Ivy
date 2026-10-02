# S2: go-imap v2 and `imapmemserver`

Run 2026-10-02 with `go-imap/v2 v2.0.0-beta.8`, Go 1.26.1. Code: `spikes/s2-imapmem/` (throwaway).
The probe starts `imapmemserver` in-process, connects with `imapclient` and tries each command Ivy's
sync needs, recording the server's answer.

## Result

| Need | Result |
|---|---|
| LOGIN, SELECT, APPEND, UID FETCH (UID, FLAGS) | works |
| `UID STORE` of `\Seen`, `\Deleted` and an arbitrary keyword (`$ivy-tag`) | accepted |
| `UID MOVE` | works, returns `COPYUID` (new UIDVALIDITY, source and destination UIDs) |
| `UID EXPUNGE` | works |
| IDLE | works; a second connection's APPEND produced `EXISTS` on the idling one |
| **CONDSTORE** (`SELECT (CONDSTORE)`, `FETCH MODSEQ`, `HIGHESTMODSEQ`) | **missing**: `BAD Syntax error` / `Unknown FETCH data item` |
| **QRESYNC** and `CHANGEDSINCE` | **missing**: same `BAD` replies |

Advertised capabilities: `ENABLE IDLE IMAP4rev1 LITERAL- SASL-IR UNSELECT UTF8=ACCEPT`. MOVE and
UID EXPUNGE work even though MOVE and UIDPLUS are not advertised, so the advertised list understates
the server; do not use it to decide what the client may try.

## Decision

- The **client** side is fine: `imapclient` already has `ChangedSince`, `ModSeq` and `HighestModSeq`
  options, so Ivy's sync can use CONDSTORE/QRESYNC when the real server offers them.
- The **server** side is the gap. `imapserver` has no mod-sequence support at all, so `mailworld`
  must add CONDSTORE and QRESYNC itself (a fork or a wrapper around the `imapserver` package's
  command parsing), including per-message mod-sequences, `HIGHESTMODSEQ`, `VANISHED (EARLIER)` and
  `CHANGEDSINCE`.
- **Sequencing:** whether Ivy needs QRESYNC at all depends on S1 (does Purelymail advertise
  CONDSTORE/QRESYNC?). Build that part of `mailworld` only if it does; otherwise `mailworld` should
  match the real server and the sync falls back to UID/flags comparison. Do not invest in the fake
  before S1 answers.
- Everything else Ivy's Milestone 0 needs (APPEND, flags and keywords, MOVE, EXPUNGE, IDLE) is
  already usable from the memory server, which is a sound base for the day-one end-to-end slice.

## Not tested

Keyword persistence across reconnects (the store was accepted, not read back), `PERMANENTFLAGS`
handling, multi-connection races, large literals, and fault injection. `mailworld` needs the last
one regardless, since the memory server has no way to drop connections or delay replies on demand.
