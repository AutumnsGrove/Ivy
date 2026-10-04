package mailworld_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

func msgWithID(id string) []byte {
	return mailworld.Msg().From("a@example.com").Subject("s " + id).MessageID(id).Build()
}

func flagNames(flags []imap.Flag) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = strings.ToLower(string(f))
	}
	slices.Sort(out)
	return out
}

// Messages is the fake's own independent read of a mailbox: what any client
// would learn, with no Ivy code between the server and the answer. The sync
// convergence test uses it to check its model of the server.
func TestMessagesReportsUIDsFlagsAndMessageIDs(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	u1 := acc.Deliver("INBOX", msgWithID("<one@sync.test>"))
	u2 := acc.Deliver("INBOX", msgWithID("<two@sync.test>"))
	if err := acc.Flag("INBOX", u2, imap.FlagSeen, "$ivy-work"); err != nil {
		t.Fatalf("flag: %v", err)
	}

	got, err := acc.Messages("INBOX")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(got) != 2 || got[0].UID != u1 || got[1].UID != u2 {
		t.Fatalf("messages = %+v, want UIDs %d and %d in order", got, u1, u2)
	}
	if got[0].MessageID != "<one@sync.test>" || got[1].MessageID != "<two@sync.test>" {
		t.Errorf("message ids = %q, %q", got[0].MessageID, got[1].MessageID)
	}
	if len(got[0].Flags) != 0 {
		t.Errorf("flags of an untouched message = %v, want none", got[0].Flags)
	}
	if want := []string{`$ivy-work`, `\seen`}; !slices.Equal(flagNames(got[1].Flags), want) {
		t.Errorf("flags = %v, want %v", flagNames(got[1].Flags), want)
	}

	if _, err := acc.Messages("Nope"); err == nil {
		t.Error("Messages of a mailbox that does not exist did not fail")
	}
	empty, err := acc.Messages("INBOX")
	if err != nil || len(empty) != 2 {
		t.Errorf("second read: %v, %d messages", err, len(empty))
	}
}

func TestMessagesOfAnEmptyMailboxIsEmptyNotAnError(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	got, err := acc.Messages("INBOX")
	if err != nil || len(got) != 0 {
		t.Errorf("empty INBOX: %v, %+v", err, got)
	}
}

func TestUnflagRemovesOnlyTheNamedFlags(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	uid := acc.Deliver("INBOX", msgWithID("<one@sync.test>"))
	if err := acc.Flag("INBOX", uid, imap.FlagSeen, imap.FlagFlagged, "$ivy-work"); err != nil {
		t.Fatalf("flag: %v", err)
	}
	if err := acc.Unflag("INBOX", uid, imap.FlagFlagged, "$ivy-work"); err != nil {
		t.Fatalf("Unflag: %v", err)
	}
	got, err := acc.Messages("INBOX")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if want := []string{`\seen`}; !slices.Equal(flagNames(got[0].Flags), want) {
		t.Errorf("flags = %v, want %v", flagNames(got[0].Flags), want)
	}
	if err := acc.Unflag("INBOX", uid, imap.FlagFlagged); err != nil {
		t.Errorf("removing a flag that is not set must be a no-op, got %v", err)
	}
}

func TestRenameKeepsMessagesUIDsAndValidity(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	if err := acc.CreateMailbox("Old"); err != nil {
		t.Fatalf("create: %v", err)
	}
	u1 := acc.Deliver("Old", msgWithID("<one@sync.test>"))
	u2 := acc.Deliver("Old", msgWithID("<two@sync.test>"))
	before := mailbox(t, acc, "Old")

	if err := acc.RenameMailbox("Old", "New"); err != nil {
		t.Fatalf("RenameMailbox: %v", err)
	}
	after := mailbox(t, acc, "New")
	if after.UIDValidity != before.UIDValidity || after.UIDNext != before.UIDNext {
		t.Errorf("rename changed validity/uidnext: %+v -> %+v", before, after)
	}
	got, err := acc.Messages("New")
	if err != nil || len(got) != 2 || got[0].UID != u1 || got[1].UID != u2 {
		t.Errorf("renamed mailbox holds %+v (%v), want UIDs %d and %d", got, err, u1, u2)
	}
	for _, mb := range mustMailboxes(t, acc) {
		if mb.Name == "Old" {
			t.Error("the old name is still listed")
		}
	}
	if err := acc.RenameMailbox("Old", "Again"); err == nil {
		t.Error("renaming a mailbox that no longer exists did not fail")
	}
	if err := acc.RenameMailbox("New", "INBOX"); err == nil {
		t.Error("renaming onto an existing mailbox did not fail")
	}
}

func TestDeleteMailboxRemovesItAndItsMessages(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	if err := acc.CreateMailbox("Gone"); err != nil {
		t.Fatalf("create: %v", err)
	}
	acc.Deliver("Gone", msgWithID("<one@sync.test>"))
	if err := acc.DeleteMailbox("Gone"); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	for _, mb := range mustMailboxes(t, acc) {
		if mb.Name == "Gone" {
			t.Error("deleted mailbox is still listed")
		}
	}
	if err := acc.DeleteMailbox("Gone"); err == nil {
		t.Error("deleting a mailbox twice did not fail")
	}
}

func mustMailboxes(t *testing.T, acc *mailworld.Account) []mailworld.MailboxInfo {
	t.Helper()
	list, err := acc.Mailboxes()
	if err != nil {
		t.Fatalf("Mailboxes: %v", err)
	}
	return list
}

func mailbox(t *testing.T, acc *mailworld.Account, name string) mailworld.MailboxInfo {
	t.Helper()
	for _, mb := range mustMailboxes(t, acc) {
		if mb.Name == name {
			return mb
		}
	}
	t.Fatalf("mailbox %q not listed", name)
	return mailworld.MailboxInfo{}
}
