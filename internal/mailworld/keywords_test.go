package mailworld_test

import (
	"slices"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

func hasWildcard(flags []imap.Flag) bool { return slices.Contains(flags, imap.FlagWildcard) }

// A default world behaves like Purelymail (spike S1): PERMANENTFLAGS carries \*,
// so a client may set a custom keyword and it persists.
func TestDefaultWorldPersistsCustomKeywords(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	c := dial(t, w, "me@grove.test", "secret")
	data, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if !hasWildcard(data.PermanentFlags) {
		t.Fatalf("PERMANENTFLAGS = %v, want \\* among them", data.PermanentFlags)
	}
	if err := acc.Flag("INBOX", uid, "$ivy-work"); err != nil {
		t.Fatalf("flag: %v", err)
	}
	msgs, err := acc.Messages("INBOX")
	if err != nil || len(msgs) != 1 || !slices.Contains(flagNames(msgs[0].Flags), "$ivy-work") {
		t.Fatalf("messages = %+v, %v; want the keyword to persist", msgs, err)
	}
}

// WithoutKeywords models a server that only keeps the system flags: it does not
// advertise \*, and a STORE of a keyword is refused with NO, which is what Ivy's
// local-only tag fallback has to survive.
func TestWithoutKeywordsAdvertisesNoWildcardAndRefusesKeywords(t *testing.T) {
	t.Parallel()
	w := newWorld(t, mailworld.WithoutKeywords())
	acc := w.Account("me@grove.test", "secret")
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	c := dial(t, w, "me@grove.test", "secret")
	data, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if hasWildcard(data.PermanentFlags) {
		t.Errorf("PERMANENTFLAGS = %v, want no \\*", data.PermanentFlags)
	}

	if err := acc.Flag("INBOX", uid, "$ivy-work"); err == nil {
		t.Error("storing a keyword succeeded; want it refused")
	}
	// A system flag still works, and the refused keyword left nothing behind.
	if err := acc.Flag("INBOX", uid, imap.FlagSeen); err != nil {
		t.Fatalf("storing \\Seen: %v", err)
	}
	msgs, err := acc.Messages("INBOX")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("messages = %+v, %v", msgs, err)
	}
	if got, want := flagNames(msgs[0].Flags), []string{`\seen`}; !slices.Equal(got, want) {
		t.Errorf("flags = %v, want %v", got, want)
	}
}
