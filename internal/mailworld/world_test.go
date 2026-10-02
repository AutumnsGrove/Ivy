package mailworld_test

import (
	"errors"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"go.uber.org/goleak"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// dial connects a real IMAP client to the fake server, which is the boundary
// every test uses (STANDARDS.md section 2: integration over unit).
func dial(t *testing.T, w *mailworld.World, addr, password string) *imapclient.Client {
	t.Helper()
	c, err := imapclient.DialInsecure(w.IMAPAddr(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Login(addr, password).Wait(); err != nil {
		t.Fatalf("login: %v", err)
	}
	return c
}

func TestDeliverAppearsOverIMAP(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	acc := w.Account("me@grove.test", "secret")

	raw := mailworld.Msg().
		From("sender@example.com").
		To("me@grove.test").
		Subject("hello").
		Text("body").
		Build()
	if uid := acc.Deliver("INBOX", raw); uid == 0 {
		t.Fatalf("Deliver returned uid 0")
	}

	c := dial(t, w, "me@grove.test", "secret")
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("select: %v", err)
	}
	msgs, err := c.Fetch(imap.UIDSetNum(1), &imap.FetchOptions{Envelope: true, UID: true}).Collect()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("fetched %d messages, want 1", len(msgs))
	}
	if got := msgs[0].Envelope.Subject; got != "hello" {
		t.Errorf("subject = %q, want %q", got, "hello")
	}
}

func TestScenarioFlagIsVisibleToClient(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	acc := w.Account("me@grove.test", "secret")
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	if err := acc.Flag("INBOX", uid, imap.FlagSeen); err != nil {
		t.Fatalf("flag: %v", err)
	}

	c := dial(t, w, "me@grove.test", "secret")
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("select: %v", err)
	}
	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{Flags: true}).Collect()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("fetched %d messages, want 1", len(msgs))
	}
	var seen bool
	for _, f := range msgs[0].Flags {
		if f == imap.FlagSeen {
			seen = true
		}
	}
	if !seen {
		t.Errorf("flags = %v, want \\Seen", msgs[0].Flags)
	}
}

func TestScenarioMoveBetweenMailboxes(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	acc := w.Account("me@grove.test", "secret")
	if err := acc.CreateMailbox("Archive"); err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	if err := acc.Move("INBOX", uid, "Archive"); err != nil {
		t.Fatalf("move: %v", err)
	}

	c := dial(t, w, "me@grove.test", "secret")
	if _, err := c.Select("Archive", nil).Wait(); err != nil {
		t.Fatalf("select archive: %v", err)
	}
	inbox, err := c.Status("INBOX", &imap.StatusOptions{NumMessages: true}).Wait()
	if err != nil {
		t.Fatalf("status inbox: %v", err)
	}
	if inbox.NumMessages == nil || *inbox.NumMessages != 0 {
		t.Errorf("INBOX has %v messages, want 0", inbox.NumMessages)
	}
}

func TestScenarioExpungeRemovesMessage(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	acc := w.Account("me@grove.test", "secret")
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	if err := acc.Expunge("INBOX", uid); err != nil {
		t.Fatalf("expunge: %v", err)
	}

	c := dial(t, w, "me@grove.test", "secret")
	st, err := c.Status("INBOX", &imap.StatusOptions{NumMessages: true}).Wait()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.NumMessages == nil || *st.NumMessages != 0 {
		t.Errorf("INBOX has %v messages, want 0", st.NumMessages)
	}
}

func TestBumpUIDValidityChangesUIDs(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	acc := w.Account("me@grove.test", "secret")
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	c := dial(t, w, "me@grove.test", "secret")
	before, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	if err := acc.BumpUIDValidity("INBOX"); err != nil {
		t.Fatalf("bump: %v", err)
	}
	if err := c.Unselect().Wait(); err != nil {
		t.Fatalf("unselect: %v", err)
	}
	after, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("select after bump: %v", err)
	}
	if after.UIDValidity == before.UIDValidity {
		t.Errorf("UIDVALIDITY unchanged at %d, want it to change", after.UIDValidity)
	}
}

func TestFaultDropConnection(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("me@grove.test", "secret")

	w.Fault(mailworld.DropConnection{After: 1})

	c, err := imapclient.DialInsecure(w.IMAPAddr(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if err := c.Login("me@grove.test", "secret").Wait(); err == nil {
		t.Fatalf("login succeeded, want a dropped connection")
	}
}

func TestFaultAuthFail(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	w.Account("me@grove.test", "secret")

	w.Fault(mailworld.AuthFail{})

	c, err := imapclient.DialInsecure(w.IMAPAddr(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	err = c.Login("me@grove.test", "secret").Wait()
	if err == nil {
		t.Fatalf("login succeeded, want auth failure")
	}
	if !isImapNo(err) {
		// The exact error type is not important; a failure is.
		t.Logf("login error (accepted): %v", err)
	}
}

func isImapNo(err error) bool {
	var imapErr *imap.Error
	return errors.As(err, &imapErr) && imapErr.Type == imap.StatusResponseTypeNo
}

func TestBuilderIsDeterministic(t *testing.T) {
	t.Parallel()
	a := mailworld.Msg().From("a@example.com").To("b@example.com").Subject("same").Text("body").Build()
	b := mailworld.Msg().From("a@example.com").To("b@example.com").Subject("same").Text("body").Build()
	if string(a) != string(b) {
		t.Errorf("builder output differs between identical calls")
	}
}

func TestClockAdvances(t *testing.T) {
	t.Parallel()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	start := w.Clock().Now()
	w.Clock().Advance(time.Hour)
	if got := w.Clock().Now().Sub(start); got != time.Hour {
		t.Errorf("advance = %v, want 1h", got)
	}
}
