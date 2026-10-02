package sync

import (
	"strings"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/store"
)

// specialUse maps the RFC 6154 LIST attributes to Ivy's folder roles.
var specialUse = map[imap.MailboxAttr]string{
	imap.MailboxAttrSent:    store.RoleSent,
	imap.MailboxAttrDrafts:  store.RoleDrafts,
	imap.MailboxAttrTrash:   store.RoleTrash,
	imap.MailboxAttrJunk:    store.RoleJunk,
	imap.MailboxAttrArchive: store.RoleArchive,
	imap.MailboxAttrAll:     store.RoleArchive,
}

// nameRoles matches the common mailbox names across the languages we care
// about. Purelymail does not advertise SPECIAL-USE (spike S1), so the name is
// usually all we have; settings can override a wrong guess later.
var nameRoles = map[string]string{
	"sent":                 store.RoleSent,
	"sent items":           store.RoleSent,
	"sent mail":            store.RoleSent,
	"sent messages":        store.RoleSent,
	"gesendet":             store.RoleSent,
	"envoyes":              store.RoleSent,
	"enviados":             store.RoleSent,
	"draft":                store.RoleDrafts,
	"drafts":               store.RoleDrafts,
	"entwurfe":             store.RoleDrafts,
	"brouillons":           store.RoleDrafts,
	"borradores":           store.RoleDrafts,
	"trash":                store.RoleTrash,
	"deleted items":        store.RoleTrash,
	"deleted messages":     store.RoleTrash,
	"bin":                  store.RoleTrash,
	"papierkorb":           store.RoleTrash,
	"corbeille":            store.RoleTrash,
	"papelera":             store.RoleTrash,
	"archive":              store.RoleArchive,
	"archives":             store.RoleArchive,
	"all mail":             store.RoleArchive,
	"archiv":               store.RoleArchive,
	"junk":                 store.RoleJunk,
	"spam":                 store.RoleJunk,
	"bulk mail":            store.RoleJunk,
	"unwanted":             store.RoleJunk,
	"courrier indesirable": store.RoleJunk,
}

// RoleFor resolves a mailbox's role from its LIST attributes and name. An
// announced SPECIAL-USE attribute wins; otherwise the last path segment is
// matched against the name heuristics; INBOX is recognised case-insensitively.
func RoleFor(name string, attrs []imap.MailboxAttr) string {
	for _, attr := range attrs {
		if role, ok := specialUse[attr]; ok {
			return role
		}
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "inbox" {
		return store.RoleInbox
	}
	if i := strings.LastIndexAny(lower, "/."); i >= 0 {
		lower = lower[i+1:]
	}
	if role, ok := nameRoles[lower]; ok {
		return role
	}
	return store.RoleOther
}
