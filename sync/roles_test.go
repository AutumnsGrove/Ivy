package sync_test

import (
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// RoleFor resolves the folder role real providers announce two ways: the
// SPECIAL-USE LIST attribute when present (rare on Purelymail) and the mailbox
// name heuristic otherwise (ARCHITECTURE.md 4).
func TestRoleFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		attrs []imap.MailboxAttr
		want  string
	}{
		{"INBOX", nil, store.RoleInbox},
		{"inbox", nil, store.RoleInbox},
		{"Inbox/Sub", []imap.MailboxAttr{imap.MailboxAttrHasChildren}, store.RoleOther},
		{"Sent", nil, store.RoleSent},
		{"Sent Items", nil, store.RoleSent},
		{"Sent Messages", nil, store.RoleSent},
		{"Gesendet", nil, store.RoleSent},
		{"Drafts", nil, store.RoleDrafts},
		{"Draft", nil, store.RoleDrafts},
		{"Trash", nil, store.RoleTrash},
		{"Deleted Items", nil, store.RoleTrash},
		{"Bin", nil, store.RoleTrash},
		{"Archive", nil, store.RoleArchive},
		{"Archives", nil, store.RoleArchive},
		{"All Mail", nil, store.RoleArchive},
		{"Junk", nil, store.RoleJunk},
		{"Spam", nil, store.RoleJunk},
		{"Bulk Mail", nil, store.RoleJunk},
		{"Projects", nil, store.RoleOther},
		{"Projects/Sent", nil, store.RoleSent},
		{"Projects/Spam", nil, store.RoleJunk},
		{"", nil, store.RoleOther},
		// SPECIAL-USE attributes win over the name.
		{"Misc", []imap.MailboxAttr{imap.MailboxAttrSent}, store.RoleSent},
		{"Sent", []imap.MailboxAttr{imap.MailboxAttrJunk}, store.RoleJunk},
		{"All Mail", []imap.MailboxAttr{imap.MailboxAttrAll}, store.RoleArchive},
		{"Flagged", []imap.MailboxAttr{imap.MailboxAttrFlagged}, store.RoleOther},
		// A noselect container is still classified by name.
		{"Sent", []imap.MailboxAttr{imap.MailboxAttrNoSelect}, store.RoleSent},
	}
	for _, tc := range cases {
		if got := ivysync.RoleFor(tc.name, tc.attrs); got != tc.want {
			t.Errorf("RoleFor(%q, %v) = %q, want %q", tc.name, tc.attrs, got, tc.want)
		}
	}
}
