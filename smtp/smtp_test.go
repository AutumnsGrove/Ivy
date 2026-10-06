package smtp_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jhillyerd/enmime"

	"github.com/AutumnsGrove/Ivy/compose"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/smtp"
)

// testSubmitter uses short deadlines so a stalled or unreachable peer is proved
// in milliseconds rather than the production 15 s / 30 s / 2 min.
func testSubmitter() *smtp.Submitter {
	return smtp.New(smtp.WithTimeout(400*time.Millisecond, 400*time.Millisecond, 500*time.Millisecond))
}

func newWorld(t *testing.T, size int64) *mailworld.World {
	t.Helper()
	var opts []mailworld.Option
	if size > 0 {
		opts = append(opts, mailworld.WithSMTPSize(size))
	}
	w, err := mailworld.New(opts...)
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func accountFor(t *testing.T, w *mailworld.World, user string) smtp.Account {
	t.Helper()
	host, portStr, err := net.SplitHostPort(w.SMTPAddr())
	if err != nil {
		t.Fatalf("split %q: %v", w.SMTPAddr(), err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	return smtp.Account{Host: host, Port: port, Username: user, Password: "secret", Insecure: true}
}

func rawMessage(id, to string) []byte {
	return mailworld.Msg().
		From("autumn@grove.test").To(to).
		Subject("hello from Ivy").
		Text("hi there").
		MessageID(id).
		Date(time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)).
		Build()
}

// oversizedInboxMessage is longer than a small advertised SIZE but uses short
// CRLF lines, so it is refused for its size and not its line length. 13 and 10
// are CR and LF.
func oversizedInboxMessage() []byte {
	line := append(bytes.Repeat([]byte("x"), 60), 13, 10)
	return mailworld.Msg().
		From("autumn@grove.test").To("mara@grove.test").
		Subject("big").
		Text(string(bytes.Repeat(line, 40))).
		Build()
}

func assertKind(t *testing.T, err error, want smtp.Kind, transient bool) *smtp.SendError {
	t.Helper()
	var se *smtp.SendError
	if !errors.As(err, &se) {
		t.Fatalf("Submit error = %v, want a *smtp.SendError with kind %q", err, want)
	}
	if se.Kind != want {
		t.Errorf("Kind = %q, want %q", se.Kind, want)
	}
	if se.Transient != transient {
		t.Errorf("Transient = %v, want %v", se.Transient, transient)
	}
	return se
}

func TestSubmitDeliversAndRecordsTheEnvelope(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 0)
	w.Account("autumn@grove.test", "secret")
	w.Account("mara@grove.test", "secret")
	acct := accountFor(t, w, "autumn@grove.test")
	raw := rawMessage("<smtp-deliver-1@example.test>", "mara@grove.test")
	env := smtp.Envelope{From: "autumn@grove.test", To: []string{"mara@grove.test"}}

	if err := testSubmitter().Submit(t.Context(), acct, env, bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	sent := w.Sent()
	if len(sent) != 1 {
		t.Fatalf("recorded %d messages, want 1", len(sent))
	}
	if sent[0].From != "autumn@grove.test" {
		t.Errorf("From = %q, want autumn@grove.test", sent[0].From)
	}
	if len(sent[0].To) != 1 || sent[0].To[0] != "mara@grove.test" {
		t.Errorf("To = %v, want [mara@grove.test]", sent[0].To)
	}
	if !bytes.Equal(sent[0].Raw, raw) {
		// SMTP DATA is line-oriented: a final CRLF is added when the message does
		// not end on a line boundary, which a message body need not.
		if !bytes.Equal(bytes.TrimSuffix(sent[0].Raw, []byte("\r\n")), raw) {
			t.Errorf("recorded bytes differ from what was submitted")
		}
	}
}

func TestSubmitClassifies4xxAsTransient(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 0)
	w.Account("autumn@grove.test", "secret")
	w.Fault(mailworld.SMTPReject{Code: 450, Message: "later"})
	acct := accountFor(t, w, "autumn@grove.test")
	raw := rawMessage("<smtp-4xx@example.test>", "mara@grove.test")

	err := testSubmitter().Submit(t.Context(), acct, smtp.Envelope{From: "autumn@grove.test", To: []string{"mara@grove.test"}}, bytes.NewReader(raw), int64(len(raw)))
	se := assertKind(t, err, smtp.KindTransient, true)
	if se.Code != 450 {
		t.Errorf("Code = %d, want 450", se.Code)
	}
	if got := len(w.Sent()); got != 0 {
		t.Errorf("recorded %d messages after a 4xx, want 0", got)
	}
}

func TestSubmitClassifies5xxAsPermanent(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 0)
	w.Account("autumn@grove.test", "secret")
	w.Fault(mailworld.SMTPReject{Code: 550, Message: "nope"})
	acct := accountFor(t, w, "autumn@grove.test")
	raw := rawMessage("<smtp-5xx@example.test>", "mara@grove.test")

	err := testSubmitter().Submit(t.Context(), acct, smtp.Envelope{From: "autumn@grove.test", To: []string{"mara@grove.test"}}, bytes.NewReader(raw), int64(len(raw)))
	assertKind(t, err, smtp.KindRejected, false)
	if got := len(w.Sent()); got != 0 {
		t.Errorf("recorded %d messages after a 5xx, want 0", got)
	}
}

func TestSubmitAuthFailureIsPermanent(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 0)
	w.Account("autumn@grove.test", "secret")
	w.Fault(mailworld.SMTPAuthFail{})
	acct := accountFor(t, w, "autumn@grove.test")
	raw := rawMessage("<smtp-auth@example.test>", "mara@grove.test")

	err := testSubmitter().Submit(t.Context(), acct, smtp.Envelope{From: "autumn@grove.test", To: []string{"mara@grove.test"}}, bytes.NewReader(raw), int64(len(raw)))
	assertKind(t, err, smtp.KindAuthFailed, false)
}

// TestSubmitRefusedRecipientAbortsTheWholeSend pins the round 61 decision: one
// refused RCPT among several means nobody gets the message, and the error names
// the address so the operator can fix it.
func TestSubmitRefusedRecipientAbortsTheWholeSend(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 0)
	w.Account("autumn@grove.test", "secret")
	w.Fault(mailworld.SMTPRejectRcpt{Address: "bad@example.test", Code: 550, Message: "no such user"})
	acct := accountFor(t, w, "autumn@grove.test")
	raw := rawMessage("<smtp-bad-rcpt@example.test>", "bad@example.test")
	env := smtp.Envelope{From: "autumn@grove.test", To: []string{"good@example.test", "bad@example.test"}}

	err := testSubmitter().Submit(t.Context(), acct, env, bytes.NewReader(raw), int64(len(raw)))
	se := assertKind(t, err, smtp.KindRecipientRefused, false)
	if se.Recipient != "bad@example.test" {
		t.Errorf("Recipient = %q, want bad@example.test", se.Recipient)
	}
	if got := len(w.Sent()); got != 0 {
		t.Errorf("recorded %d messages after a refused recipient, want 0: the send must not go out partially", got)
	}
}

// TestSubmitRetryAfterATransientFailure is the second-attempt path: a one-off
// 4xx is retried on a fresh connection and succeeds.
func TestSubmitRetryAfterATransientFailure(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 0)
	w.Account("autumn@grove.test", "secret")
	w.Fault(mailworld.SMTPReject{Code: 450, Message: "try again"})
	acct := accountFor(t, w, "autumn@grove.test")
	raw := rawMessage("<smtp-retry@example.test>", "mara@grove.test")
	env := smtp.Envelope{From: "autumn@grove.test", To: []string{"mara@grove.test"}}
	submitter := testSubmitter()

	if err := submitter.Submit(t.Context(), acct, env, bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("first submit succeeded, want a transient failure")
	}
	if err := submitter.Submit(t.Context(), acct, env, bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatalf("retry after a one-off transient failure: %v", err)
	}
	if got := len(w.Sent()); got != 1 {
		t.Errorf("recorded %d messages, want exactly 1 after the retry", got)
	}
}

func TestSubmitTooLargeIsRefusedBeforeSending(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1024)
	w.Account("autumn@grove.test", "secret")
	acct := accountFor(t, w, "autumn@grove.test")
	raw := oversizedInboxMessage()
	if int64(len(raw)) <= w.SMTPMaxBytes() {
		t.Fatalf("test message is %d bytes, not over the advertised %d", len(raw), w.SMTPMaxBytes())
	}

	err := testSubmitter().Submit(t.Context(), acct, smtp.Envelope{From: "autumn@grove.test", To: []string{"mara@grove.test"}}, bytes.NewReader(raw), int64(len(raw)))
	se := assertKind(t, err, smtp.KindTooLarge, false)
	if se.Code != 552 {
		t.Errorf("Code = %d, want the provider's 552", se.Code)
	}
	if got := len(w.Sent()); got != 0 {
		t.Errorf("recorded %d messages, want 0: an oversize message is never handed to DATA", got)
	}
}

// TestSubmitServerSizeRejectionIsPermanent is the backstop for an unknown size:
// the provider refuses the transaction with 552 at DATA.
func TestSubmitServerSizeRejectionIsPermanent(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 1024)
	w.Account("autumn@grove.test", "secret")
	acct := accountFor(t, w, "autumn@grove.test")
	raw := oversizedInboxMessage()

	err := testSubmitter().Submit(t.Context(), acct, smtp.Envelope{From: "autumn@grove.test", To: []string{"mara@grove.test"}}, bytes.NewReader(raw), -1)
	assertKind(t, err, smtp.KindTooLarge, false)
	if got := len(w.Sent()); got != 0 {
		t.Errorf("recorded %d messages, want 0", got)
	}
}

func TestSubmitUnreachableIsTransient(t *testing.T) {
	t.Parallel()
	// A port that is bound and immediately released refuses connections.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()

	acct := smtp.Account{Host: "127.0.0.1", Port: addr.Port, Username: "a@b.test", Password: "x", Insecure: true}
	raw := rawMessage("<smtp-unreachable@example.test>", "mara@grove.test")
	err = testSubmitter().Submit(t.Context(), acct, smtp.Envelope{From: "a@b.test", To: []string{"mara@grove.test"}}, bytes.NewReader(raw), int64(len(raw)))
	assertKind(t, err, smtp.KindUnreachable, true)
}

func TestSubmitStalledPeerTimesOut(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 0)
	w.Account("autumn@grove.test", "secret")
	w.Fault(mailworld.SMTPStall{Phase: mailworld.SMTPStallGreeting})
	acct := accountFor(t, w, "autumn@grove.test")
	raw := rawMessage("<smtp-stall@example.test>", "mara@grove.test")

	err := testSubmitter().Submit(t.Context(), acct, smtp.Envelope{From: "autumn@grove.test", To: []string{"mara@grove.test"}}, bytes.NewReader(raw), int64(len(raw)))
	assertKind(t, err, smtp.KindTimeout, true)
	if got := len(w.Sent()); got != 0 {
		t.Errorf("recorded %d messages against a stalled peer, want 0", got)
	}
}

// TestSubmitCancellationReturnsPromptly proves a caller that gives up is not
// held by a stalled peer: cancelling the context ends Submit well before the
// command deadline, and nothing is recorded.
func TestSubmitCancellationReturnsPromptly(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 0)
	w.Account("autumn@grove.test", "secret")
	w.Fault(mailworld.SMTPStall{Phase: mailworld.SMTPStallGreeting})
	acct := accountFor(t, w, "autumn@grove.test")
	raw := rawMessage("<smtp-cancel@example.test>", "mara@grove.test")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- testSubmitter().Submit(ctx, acct, smtp.Envelope{From: "autumn@grove.test", To: []string{"mara@grove.test"}}, bytes.NewReader(raw), int64(len(raw)))
	}()
	// Let the send reach the stalled peer, then leave. The bounded sleep only
	// starts the cancellation; the assertion is on the result and the deadline.
	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assertKind(t, err, smtp.KindCanceled, false)
	case <-time.After(2 * time.Second):
		t.Fatal("Submit did not return after cancellation: a stalled peer can hang the caller")
	}
	if got := len(w.Sent()); got != 0 {
		t.Errorf("recorded %d messages after cancellation, want 0", got)
	}
}

// TestSubmitComposedMessageRoundTrips is the thin 4a slice: compose builds a
// real message, the transport submits it, and the fake's recorded bytes parse
// back to what was composed. It is the seam a later stage wires to the queue.
func TestSubmitComposedMessageRoundTrips(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 0)
	w.Account("autumn@grove.test", "secret")
	w.Account("mara@grove.test", "secret")
	acct := accountFor(t, w, "autumn@grove.test")

	m := compose.Message{
		From:      compose.Address{Name: "Autumn", Address: "autumn@grove.test"},
		To:        []compose.Address{{Name: "Mara", Address: "mara@grove.test"}},
		Bcc:       []compose.Address{{Address: "quiet@grove.test"}},
		Subject:   "Réunion 😀",
		Text:      "**Hi** Mara",
		Markdown:  true,
		MessageID: "<round-trip@example.test>",
		Date:      time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
	}
	raw, env, err := compose.Build(m)
	if err != nil {
		t.Fatalf("compose.Build: %v", err)
	}
	if err := testSubmitter().Submit(t.Context(), acct, smtp.Envelope{From: env.From, To: env.To}, bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	sent := w.Sent()
	if len(sent) != 1 {
		t.Fatalf("recorded %d messages, want 1", len(sent))
	}
	if len(sent[0].To) != 2 || sent[0].To[1] != "quiet@grove.test" {
		t.Errorf("envelope To = %v, want the Bcc recipient present", sent[0].To)
	}
	parsed, err := enmime.ReadEnvelope(bytes.NewReader(sent[0].Raw))
	if err != nil {
		t.Fatalf("parse recorded message: %v", err)
	}
	if got := parsed.GetHeader("Subject"); got != "Réunion 😀" {
		t.Errorf("Subject = %q, want the unicode subject", got)
	}
	if got := parsed.GetHeader("Bcc"); got != "" {
		t.Errorf("wire message carries a Bcc header %q, want none", got)
	}
	if !strings.Contains(parsed.HTML, "<strong>Hi</strong>") {
		t.Errorf("HTML part did not render markdown: %q", parsed.HTML)
	}
}

// TestSubmitAllErrorsAreSendErrors guards the contract the 4b queue depends on:
// every failure carries a Kind and a Transient verdict, so the queue never has
// to guess what an error means.
func TestSubmitAllErrorsAreSendErrors(t *testing.T) {
	t.Parallel()
	w := newWorld(t, 0)
	w.Account("autumn@grove.test", "secret")
	w.Fault(mailworld.SMTPReject{Code: 421, Message: "unavailable"})
	acct := accountFor(t, w, "autumn@grove.test")
	raw := rawMessage("<smtp-contract@example.test>", "mara@grove.test")

	err := testSubmitter().Submit(t.Context(), acct, smtp.Envelope{From: "autumn@grove.test", To: []string{"mara@grove.test"}}, bytes.NewReader(raw), int64(len(raw)))
	var se *smtp.SendError
	if !errors.As(err, &se) {
		t.Fatalf("Submit error = %v (%T), want a *smtp.SendError", err, err)
	}
	if se.Kind == "" {
		t.Errorf("SendError has no Kind")
	}
}
