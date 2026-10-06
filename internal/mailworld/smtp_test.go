package mailworld_test

import (
	"bytes"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

// dialSMTP connects and authenticates a real SMTP client, the boundary these
// tests drive the fake through (STANDARDS.md section 2).
func dialSMTP(t *testing.T, w *mailworld.World, user, pass string) *smtp.Client {
	t.Helper()
	conn, err := net.Dial("tcp", w.SMTPAddr())
	if err != nil {
		t.Fatalf("dial smtp: %v", err)
	}
	c := smtp.NewClient(conn)
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Auth(sasl.NewPlainClient("", user, pass)); err != nil {
		t.Fatalf("smtp auth: %v", err)
	}
	return c
}

// inboxSubjects fetches every subject in a mailbox over real IMAP.
func inboxSubjects(t *testing.T, w *mailworld.World, user, pass, mailbox string) []string {
	t.Helper()
	c := dial(t, w, user, pass)
	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		t.Fatalf("select %s: %v", mailbox, err)
	}
	msgs, err := c.Fetch(imap.UIDSetNum(1), &imap.FetchOptions{Envelope: true}).Collect()
	if err != nil {
		t.Fatalf("fetch %s: %v", mailbox, err)
	}
	var subjects []string
	for _, m := range msgs {
		subjects = append(subjects, m.Envelope.Subject)
	}
	return subjects
}

func TestSMTPDeliversLocallyBetweenAccounts(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("a@grove.test", "secret")
	w.Account("b@grove.test", "secret")

	c := dialSMTP(t, w, "a@grove.test", "secret")
	raw := mailworld.Msg().
		From("a@grove.test").
		To("b@grove.test").
		Subject("hi b").
		Text("hello").
		Build()
	if err := c.SendMail("a@grove.test", []string{"b@grove.test"}, bytes.NewReader(raw)); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := inboxSubjects(t, w, "b@grove.test", "secret", "INBOX")
	if len(got) != 1 || got[0] != "hi b" {
		t.Errorf("b INBOX subjects = %v, want [hi b]", got)
	}
}

func TestSMTPRecordsExternalMailWithoutDelivering(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("a@grove.test", "secret")

	c := dialSMTP(t, w, "a@grove.test", "secret")
	raw := mailworld.Msg().From("a@grove.test").To("outside@example.com").Subject("out").Build()
	if err := c.SendMail("a@grove.test", []string{"outside@example.com"}, bytes.NewReader(raw)); err != nil {
		t.Fatalf("send: %v", err)
	}

	sent := w.Sent()
	if len(sent) != 1 {
		t.Fatalf("recorded %d messages, want 1", len(sent))
	}
	if sent[0].From != "a@grove.test" {
		t.Errorf("From = %q, want a@grove.test", sent[0].From)
	}
	if len(sent[0].To) != 1 || sent[0].To[0] != "outside@example.com" {
		t.Errorf("To = %v, want [outside@example.com]", sent[0].To)
	}
	if string(sent[0].Raw) != string(raw) {
		t.Errorf("recorded raw differs from what was sent")
	}
}

func TestSMTPSentCopyModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mode mailworld.SentCopy
		want int
	}{
		{"client appends", mailworld.SentCopyClient, 0},
		{"server files", mailworld.SentCopyAuto, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, err := mailworld.New()
			if err != nil {
				t.Fatalf("new world: %v", err)
			}
			t.Cleanup(func() { _ = w.Close() })
			a := w.Account("a@grove.test", "secret")
			w.Account("b@grove.test", "secret")
			a.SetSentCopy(tc.mode)
			// A real provider already has a Sent mailbox; create it so this test
			// measures who files the copy, not who creates the folder.
			if err := a.CreateMailbox("Sent"); err != nil {
				t.Fatalf("create Sent: %v", err)
			}

			c := dialSMTP(t, w, "a@grove.test", "secret")
			raw := mailworld.Msg().From("a@grove.test").To("b@grove.test").Subject("s").Build()
			if err := c.SendMail("a@grove.test", []string{"b@grove.test"}, bytes.NewReader(raw)); err != nil {
				t.Fatalf("send: %v", err)
			}

			got := inboxSubjects(t, w, "a@grove.test", "secret", "Sent")
			if len(got) != tc.want {
				t.Errorf("Sent subjects = %v, want %d", got, tc.want)
			}
		})
	}
}

func TestSMTPRejectFaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		code int
		want bool // want a 4xx (transient) rather than a 5xx
	}{
		{"transient", 450, true},
		{"rejected", 550, false},
		{"too large", 552, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, err := mailworld.New()
			if err != nil {
				t.Fatalf("new world: %v", err)
			}
			t.Cleanup(func() { _ = w.Close() })
			w.Account("a@grove.test", "secret")

			w.Fault(mailworld.SMTPReject{Code: tc.code, Message: "nope"})

			c := dialSMTP(t, w, "a@grove.test", "secret")
			raw := mailworld.Msg().From("a@grove.test").To("b@example.com").Subject("x").Build()
			err = c.SendMail("a@grove.test", []string{"b@example.com"}, bytes.NewReader(raw))
			var smtpErr *smtp.SMTPError
			if !errors.As(err, &smtpErr) {
				t.Fatalf("send error = %v, want *smtp.SMTPError", err)
			}
			if smtpErr.Code != tc.code {
				t.Errorf("code = %d, want %d", smtpErr.Code, tc.code)
			}
			if transient := smtpErr.Code >= 400 && smtpErr.Code < 500; transient != tc.want {
				t.Errorf("transient = %v, want %v", transient, tc.want)
			}
			if got := len(w.Sent()); got != 0 {
				t.Errorf("recorded %d messages after rejection, want 0", got)
			}
		})
	}
}

func TestSMTPTransientFaultIsConsumedOnce(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("a@grove.test", "secret")
	w.Fault(mailworld.SMTPReject{Code: 450, Message: "try later"})

	c := dialSMTP(t, w, "a@grove.test", "secret")
	raw := mailworld.Msg().From("a@grove.test").To("b@example.com").Subject("x").Build()
	if err := c.SendMail("a@grove.test", []string{"b@example.com"}, bytes.NewReader(raw)); err == nil {
		t.Fatalf("first send succeeded, want a transient failure")
	}
	c.Reset()
	if err := c.SendMail("a@grove.test", []string{"b@example.com"}, bytes.NewReader(raw)); err != nil {
		t.Fatalf("retry after a one-off transient fault: %v", err)
	}
}

func TestSMTPAuthFailFault(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("a@grove.test", "secret")
	w.Fault(mailworld.SMTPAuthFail{})

	conn, err := net.Dial("tcp", w.SMTPAddr())
	if err != nil {
		t.Fatalf("dial smtp: %v", err)
	}
	defer conn.Close()
	c := smtp.NewClient(conn)
	if err := c.Auth(sasl.NewPlainClient("", "a@grove.test", "secret")); err == nil {
		t.Fatalf("auth succeeded, want failure")
	}
}

func TestSMTPWrongPasswordFails(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("a@grove.test", "secret")

	conn, err := net.Dial("tcp", w.SMTPAddr())
	if err != nil {
		t.Fatalf("dial smtp: %v", err)
	}
	defer conn.Close()
	c := smtp.NewClient(conn)
	if err := c.Auth(sasl.NewPlainClient("", "a@grove.test", "wrong")); err == nil {
		t.Fatalf("auth succeeded with a wrong password")
	}
}

// TestSMTPAdvertisesAndEnforcesSize is the fake's half of the 4a SIZE tests: the
// EHLO line carries the limit and DATA above it is refused with 552, so the real
// client's pre-check and the server's rejection can both be driven.
func TestSMTPAdvertisesAndEnforcesSize(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New(mailworld.WithSMTPSize(1024))
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("a@grove.test", "secret")

	conn, err := net.Dial("tcp", w.SMTPAddr())
	if err != nil {
		t.Fatalf("dial smtp: %v", err)
	}
	defer conn.Close()
	c := smtp.NewClient(conn)
	size, ok := c.MaxMessageSize()
	if !ok || size != 1024 {
		t.Fatalf("MaxMessageSize = %d, %v; want 1024, true", size, ok)
	}
	if err := c.Auth(sasl.NewPlainClient("", "a@grove.test", "secret")); err != nil {
		t.Fatalf("auth: %v", err)
	}

	// Many short CRLF-terminated lines, so the 552 comes from the size limit and
	// not the line-length limit; the fake must refuse the former like a real
	// provider. 13 and 10 are CR and LF.
	line := append(bytes.Repeat([]byte("x"), 60), 13, 10)
	oversize := bytes.Repeat(line, 40)
	err = c.SendMail("a@grove.test", []string{"b@example.com"}, bytes.NewReader(oversize))
	var smtpErr *smtp.SMTPError
	if !errors.As(err, &smtpErr) || smtpErr.Code != 552 {
		t.Fatalf("oversize send error = %v, want an SMTP 552", err)
	}
	if got := len(w.Sent()); got != 0 {
		t.Errorf("recorded %d messages after an oversize send, want 0", got)
	}
}

// TestSMTPRejectRcptRefusesOneAddressAmongSeveral is the fake's half of the
// refused-recipient test: the named address fails at RCPT, the transaction is
// left without a DATA, and nothing is recorded.
func TestSMTPRejectRcptRefusesOneAddressAmongSeveral(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("a@grove.test", "secret")
	w.Fault(mailworld.SMTPRejectRcpt{Address: "bad@example.test", Code: 550, Message: "no such user"})

	c := dialSMTP(t, w, "a@grove.test", "secret")
	raw := mailworld.Msg().From("a@grove.test").To("bad@example.test").Subject("x").Build()
	err = c.SendMail("a@grove.test", []string{"good@example.test", "bad@example.test"}, bytes.NewReader(raw))
	var smtpErr *smtp.SMTPError
	if !errors.As(err, &smtpErr) || smtpErr.Code != 550 {
		t.Fatalf("send error = %v, want SMTP 550 for the bad recipient", err)
	}
	if got := len(w.Sent()); got != 0 {
		t.Errorf("recorded %d messages after a refused recipient, want 0", got)
	}
}

// TestSMTPStallGreetingTimesOut is the fake's stalled-peer fault: the connection
// is accepted and then nothing is written, so the client's command deadline fires.
// Clearing the fault lets the next connection through, which is what a real
// outage-then-recovery test needs.
func TestSMTPStallGreetingTimesOut(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("a@grove.test", "secret")
	w.Fault(mailworld.SMTPStall{Phase: mailworld.SMTPStallGreeting})

	conn, err := net.Dial("tcp", w.SMTPAddr())
	if err != nil {
		t.Fatalf("dial smtp: %v", err)
	}
	defer conn.Close()
	c := smtp.NewClient(conn)
	c.CommandTimeout = 250 * time.Millisecond
	if err := c.Auth(sasl.NewPlainClient("", "a@grove.test", "secret")); err == nil {
		t.Fatalf("auth against a stalled peer succeeded, want a deadline error")
	}

	w.ClearFaults()
	c2 := dialSMTP(t, w, "a@grove.test", "secret")
	raw := mailworld.Msg().From("a@grove.test").To("b@example.com").Subject("after").Build()
	if err := c2.SendMail("a@grove.test", []string{"b@example.com"}, bytes.NewReader(raw)); err != nil {
		t.Fatalf("send after clearing the stall: %v", err)
	}
	if got := len(w.Sent()); got != 1 {
		t.Errorf("recorded %d messages after recovery, want 1", got)
	}
}

// TestSMTPStallDataTimesOutWithoutRecording parks the server after 354, so the
// client's submission deadline fires and no message is recorded: the fake's
// model of the dangerous window where the client cannot know what happened.
func TestSMTPStallDataTimesOutWithoutRecording(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("a@grove.test", "secret")
	w.Account("b@grove.test", "secret")
	w.Fault(mailworld.SMTPStall{Phase: mailworld.SMTPStallData})

	conn, err := net.Dial("tcp", w.SMTPAddr())
	if err != nil {
		t.Fatalf("dial smtp: %v", err)
	}
	defer conn.Close()
	c := smtp.NewClient(conn)
	c.CommandTimeout = 250 * time.Millisecond
	c.SubmissionTimeout = 350 * time.Millisecond
	if err := c.Auth(sasl.NewPlainClient("", "a@grove.test", "secret")); err != nil {
		t.Fatalf("auth: %v", err)
	}
	raw := mailworld.Msg().From("a@grove.test").To("b@grove.test").Subject("lost").Build()
	if err := c.SendMail("a@grove.test", []string{"b@grove.test"}, bytes.NewReader(raw)); err == nil {
		t.Fatalf("send against a data stall succeeded, want a deadline error")
	}
	if got := len(w.Sent()); got != 0 {
		t.Errorf("recorded %d messages after a data stall, want 0", got)
	}
}
