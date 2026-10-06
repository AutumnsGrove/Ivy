# G1 — the 4a corpus and the SMTP failure tests are written and red (2026-10-06)

Checkpoint **G1** of `docs/CHUNK4-BRIEF.md` section 5: the header-injection corpus and the SMTP
failure tests exist and fail for the right reason, **before the builder and the transport are
implemented**. Nothing in `compose/` or `smtp/` does any work yet; the only implemented code is the
fake's own test infrastructure in `internal/mailworld/`. Waiting for an explicit "go".

Last commit before this note: `d703629` (`Pre-approve goldmark for outgoing markdown`). The stage is
4a (builder and submit). The three pre-flight questions the docs did not answer were answered by the
operator and recorded in `docs/qa-log.md` round 61.

## What is already implemented and green (the fake's side)

The mail world had no SIZE and no way to model a stalled SMTP peer or a single refused recipient, so
3 tests of the fake itself were added first (they must be trustworthy before the real tests mean
anything):

- `internal/mailworld/smtp.go`
  - `WithSMTPSize(bytes int64)` / `DefaultSMTPSize` (48 MiB, near Purelymail's live ~48.8 MiB) — the
    fake advertises `SIZE` in EHLO and enforces it (go-smtp answers `552`).
  - `SMTPStall{Phase}` with `SMTPStallGreeting` (accept, then never write) and `SMTPStallData`
    (answer 354, then never read or reply). A `stallConn` releases when the world closes.
  - `SMTPRejectRcpt{Address, Code, Message}` — one named RCPT fails, the rest of the transaction is
    untouched.
- `internal/mailworld/smtp_test.go` — `TestSMTPAdvertisesAndEnforcesSize`,
  `TestSMTPRejectRcptRefusesOneAddressAmongSeveral`, `TestSMTPStallGreetingTimesOut`,
  `TestSMTPStallDataTimesOutWithoutRecording`.

```
$ CGO_ENABLED=1 go test -race -count=1 ./internal/mailworld/
ok  	github.com/AutumnsGrove/Ivy/internal/mailworld
```

## The red: what the tests now freeze

### `compose/` — the pure builder (`compose/compose.go` is types + stubs)

The package is a pure function of its input: every value that reaches a header is validated and a CR,
LF or NUL is **rejected**, never stripped. `Build` returns `(raw []byte, Envelope, error)`; `Validate`
pre-checks without rendering. Limits live as constants: `MaxRecipients` 100, `MaxSubjectBytes` 998,
`MaxBodyBytes` 1 MiB, `MaxReferences` 20, `MaxMessageIDBytes` 320.

**The corpus** is `TestBuildRejectsInjectedHeaders` in `compose/compose_test.go`: 22 cases, each a
hostile value placed in one field, each asserted to return a `*compose.ValidationError` naming that
field.

| class | cases |
|---|---|
| CR / LF / CRLF / NUL in the subject | 5 (including a value that fences a fake body, `ok\r\n\r\nbody`) |
| subject over its bound | 1 (999 bytes) |
| display name with CR / LF / CRLF / NUL | 4 (From and To) |
| non-ASCII or malformed addr-spec | 3 (`mü@…`, `a@exämple…`, `a b@…`) |
| In-Reply-To / References not a canonical `<msg-id>` or carrying CRLF | 4 |
| Message-ID carrying CRLF, not angled, or over its bound | 3 |
| too many recipients / body over its bound | 2 |

**The accepted half** is `TestBuildAcceptsAndSafelyEncodes`: a subject that literally looks like an
RFC 2047 encoded word (`=?utf-8?Q?hi?=`) must decode back to that literal, not to `hi`; a unicode
subject and display name must round-trip; a display name that *contains* an address or names a header
must stay a name (the real address unchanged, no extra header); duplicate recipients must collapse in
the envelope. `TestBuildBccStaysOnTheEnvelopeOnly` pins invariant 7 (no `Bcc` header on the wire, the
recipient still in the envelope, the header only on the Sent copy). `TestBuildMarkdownRendersSafeHTML`
pins round 61 and goldmark's contract: the `text/plain` part is the raw markdown, the HTML part is
`multipart/alternative`, raw HTML never passes through, and no link scheme outside
`http`/`https`/`mailto` is emitted. `TestBuildPlainTextHasNoHTMLPart` and
`TestValidateRejectsBadAddresses` cover the plain case and the pre-check.

**The oracle** is `assertSafeHeaders` (same file). For a successfully built message it computes
safety from the bytes alone:

1. every `\n` must be preceded by `\r` (no bare LF, so a peer unfolds nothing unexpected);
2. the bytes must parse with `net/mail.ReadMessage`;
3. every header name must be in the allowed set (`From`, `To`, `Cc`, `Bcc`, `Reply-To`, `Subject`,
   `Date`, `Message-Id`, `Mime-Version`, `Content-Type`, `Content-Transfer-Encoding`, `In-Reply-To`,
   `References`) — anything else means a hostile value forged a header;
4. each allowed header appears **exactly once** — the duplicate-header check;
5. no header value still contains `\r`, `\n` or `\x00`;
6. the message has a CRLF header/body separator.

Per-case assertions sit on top (decoded subject equals the input, parsed From addr-spec equals the
real address, envelope recipient order and de-duplication, `<strong>` present, `javascript:` absent,
`<script>` absent).

### `smtp/` — the implicit-TLS transport (`smtp/smtp.go` is types + stubs)

`smtp.New(WithTimeout(dial, command, data))`, `Submit(ctx, Account, Envelope, io.Reader, size int64)`
returning a `*SendError{Kind, Transient, Recipient, Code, Err}`. Kinds:
`unreachable`, `timeout`, `canceled`, `auth_failed`, `recipient_refused`, `too_large`, `rejected`,
`transient`. Production deadlines: dial/TLS 15 s, command 30 s, DATA 2 min.

`smtp/smtp_test.go`, all against `mailworld` over a real socket:

| test | asserts |
|---|---|
| `TestSubmitDeliversAndRecordsTheEnvelope` | success; the fake recorded exactly the submitted bytes, From and To |
| `TestSubmitClassifies4xxAsTransient` | `450` → `transient`, `Transient=true`, code kept, nothing recorded |
| `TestSubmitClassifies5xxAsPermanent` | `550` → `rejected`, `Transient=false` |
| `TestSubmitAuthFailureIsPermanent` | `SMTPAuthFail` → `auth_failed` |
| `TestSubmitRefusedRecipientAbortsTheWholeSend` | one bad RCPT among two → `recipient_refused`, names the address, **zero** messages recorded |
| `TestSubmitRetryAfterATransientFailure` | a one-off 4xx retried on a fresh connection sends exactly once |
| `TestSubmitTooLargeIsRefusedBeforeSending` | over the advertised SIZE → `too_large` (552), nothing reaches DATA |
| `TestSubmitServerSizeRejectionIsPermanent` | size unknown, server 552 → `too_large` |
| `TestSubmitUnreachableIsTransient` | closed port → `unreachable`, transient |
| `TestSubmitStalledPeerTimesOut` | greeting stall → `timeout`, transient, nothing recorded |
| `TestSubmitCancellationReturnsPromptly` | cancel during a stall returns inside 2 s as `canceled` |
| `TestSubmitAllErrorsAreSendErrors` | every failure is a `*SendError` with a `Kind` |

## The failing output

```
$ CGO_ENABLED=1 go test -race -count=1 ./compose/
--- FAIL: TestBuildBccStaysOnTheEnvelopeOnly (0.00s)
    compose_test.go: Build: compose: not implemented
--- FAIL: TestBuildAcceptsAndSafelyEncodes (0.00s)
    --- FAIL: .../subject_that_looks_like_an_encoded_word (0.00s)
        compose_test.go: Build: compose: not implemented
--- FAIL: TestBuildRejectsInjectedHeaders (0.00s)
    --- FAIL: .../subject_CRLF (0.00s)
        compose_test.go: Build error = compose: not implemented, want a *compose.ValidationError naming "subject"
... (32 FAIL lines: every corpus case, the markdown renderer, the plain path, the pre-check)
FAIL

$ CGO_ENABLED=1 go test -race -count=1 ./smtp/
--- FAIL: TestSubmitDeliversAndRecordsTheEnvelope (0.00s)
    smtp_test.go: Submit: smtp: not implemented
--- FAIL: TestSubmitRefusedRecipientAbortsTheWholeSend (0.00s)
    smtp_test.go: Submit error = smtp: not implemented, want a *smtp.SendError with kind "recipient_refused"
--- FAIL: TestSubmitStalledPeerTimesOut (0.00s)
    smtp_test.go: Submit error = smtp: not implemented, want a *smtp.SendError with kind "timeout"
... (12 FAIL lines)
FAIL
```

Every failure is the unbuilt feature (`compose: not implemented` / `smtp: not implemented`), never a
compile error or a missing fixture. `go build ./...`, `go vet` on the three packages and `gofumpt -l`
are clean.

## The decisions this freezes, and what a reviewer should confirm

Settled by the operator in round 61 (`docs/qa-log.md`):

1. **A refused recipient aborts the whole send** (`RSET`, report the address). The wire copy is never
   partial.
2. **`text/plain` is the operator's raw markdown** exactly as typed; goldmark renders only the
   `text/html` part of a `multipart/alternative`.
3. **The SMTP client is a new thin `smtp/` package**; `compose/` stays a pure builder.

Chosen here for review (propose, do not re-litigate lightly):

- the **bounds** above (subject 998, recipients 100, body 1 MiB, references 20, id 320 bytes) and the
  SMTP deadlines (15 s / 30 s / 2 min);
- the **`Kind` taxonomy** and `Transient` flag (the 4b queue branches on them);
- `Build` returning the envelope alongside the bytes, and a `Validate` for the pre-check;
- `Submit` taking `size int64` (`< 0` means unknown, so the provider's own 552 is the backstop).

## What happens on "go"

1. Implement `compose.Validate` (address parsing with `net/mail`, ASCII-only addr-specs, message-id
   and CR/LF/NUL validation) and `compose.Build` (enmime builder with injected Message-ID/Date,
   `Bcc` per invariant 7, force-encoding of `=?…?=` values, goldmark for markdown with links limited
   to `http`/`https`/`mailto`). Add goldmark to `go.mod` and record its version in the BUILD-LOG.
2. Implement `smtp.Submit` (own `net.Dialer` + `tls.Client`, `AUTH PLAIN`, EHLO SIZE read, SIZE
   pre-check, per-RCPT classification, `RSET` on refusal, `context.AfterFunc` to close on
   cancellation, command/DATA deadlines).
3. Make both suites green, then `make check`, `go test -race ./...`, `gofumpt`, `go vet`,
   `staticcheck`, `golangci-lint`; add the new limits to the `STANDARDS.md` 4a table; update
   `ARCHITECTURE.md` section 2 (the `smtp/` line) and section 5 (outgoing markdown); add the
   BUILD-LOG entry; fold round 61 and this handoff into `next_steps.md`.
4. Nothing is wired to the API or `ivy run` yet; 4a is a package and its tests (CHUNK4-BRIEF 4a).

## Waiting

**Waiting at G1: see `docs/handoffs/2026-10-06-G1-compose-smtp-tests.md`.** No further changes until
the operator says "go".

**Operator update (2026-10-06):** continue without a stop. This checkpoint is recorded for review but
does not gate; the work proceeds in the same small commits and the handoff is read later.

## Outcome (2026-10-06): 4a is done

The operator said continue past G1. Both suites are now green and committed: `d08120d` (compose) and
`54913ce` (smtp), on the G1 commit `19082f6`. `make check` (whole `-race` suite, gofumpt, vet,
staticcheck, svelte-check, 279 Vitest) is green. The new bounds are in the `STANDARDS.md` 4a table;
`ARCHITECTURE.md` sections 2 and 5, `STACK.md` (goldmark v1.8.6), `BUILD-LOG.md` and `next_steps.md`
are updated. Two small implementation refinements to the frozen tests were made honestly and are in
the diffs: the `text/plain` part is compared after MIME's line handling, and the recorded SMTP bytes
allow the final CRLF that a line-oriented DATA adds. No stop-trigger fired.
