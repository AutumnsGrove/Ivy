package mailworld

import (
	"bytes"
	"cmp"
	"slices"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// Account is one fake mailbox. Its methods play "the other mail client": they
// mutate the server through a real IMAP connection so tests exercise the same
// path a second client would take.
type Account struct {
	world    *World
	user     *imapmemserver.User
	address  string
	password string
	sentCopy SentCopy
}

// Address is the account's email address (also its IMAP/SMTP username).
func (a *Account) Address() string { return a.address }

// Deliver appends raw to mailbox and returns its new UID. It panics if the
// mailbox does not exist, which is a test setup bug rather than a scenario.
func (a *Account) Deliver(mailbox string, raw []byte) uint32 {
	uid, err := a.Append(mailbox, raw)
	if err != nil {
		panic("mailworld: deliver to " + mailbox + ": " + err.Error())
	}
	return uid
}

// Append is Deliver for callers whose mailbox name comes from an operator
// rather than from test setup, so a typo is an error instead of a panic.
func (a *Account) Append(mailbox string, raw []byte) (uint32, error) {
	data, err := a.user.Append(mailbox, newLiteral(raw), &imap.AppendOptions{Time: a.world.Clock().Now()})
	if err != nil {
		return 0, err
	}
	return uint32(data.UID), nil
}

// CreateMailbox creates a mailbox, e.g. "Archive" or "Sent".
func (a *Account) CreateMailbox(name string) error {
	return a.user.Create(name, nil)
}

// Flag adds flags to a message in mailbox.
func (a *Account) Flag(mailbox string, uid uint32, flags ...imap.Flag) error {
	return a.withClient(func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, nil).Wait(); err != nil {
			return err
		}
		return c.Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{
			Op: imap.StoreFlagsAdd, Silent: true, Flags: flags,
		}, nil).Close()
	})
}

// Move moves a message from mailbox to dest.
func (a *Account) Move(mailbox string, uid uint32, dest string) error {
	return a.withClient(func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, nil).Wait(); err != nil {
			return err
		}
		_, err := c.Move(imap.UIDSetNum(imap.UID(uid)), dest).Wait()
		return err
	})
}

// Expunge flags a message \Deleted and then removes it with UID EXPUNGE.
func (a *Account) Expunge(mailbox string, uid uint32) error {
	set := imap.UIDSetNum(imap.UID(uid))
	return a.withClient(func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, nil).Wait(); err != nil {
			return err
		}
		if err := c.Store(set, &imap.StoreFlags{
			Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted},
		}, nil).Close(); err != nil {
			return err
		}
		return c.UIDExpunge(set).Close()
	})
}

// BumpUIDValidity deletes and recreates a mailbox, as a provider does when it
// rebuilds one: every UID and UIDVALIDITY changes and the messages are gone.
func (a *Account) BumpUIDValidity(mailbox string) error {
	if err := a.user.Delete(mailbox); err != nil {
		return err
	}
	return a.user.Create(mailbox, nil)
}

// HighestModSeq returns a mailbox's current HIGHESTMODSEQ, read the way the
// sync code captures a floor before watching for changes.
func (a *Account) HighestModSeq(mailbox string) (uint64, error) {
	var modSeq uint64
	err := a.withClient(func(c *imapclient.Client) error {
		data, err := c.Select(mailbox, &imap.SelectOptions{CondStore: true}).Wait()
		if err != nil {
			return err
		}
		modSeq = data.HighestModSeq
		return nil
	})
	return modSeq, err
}

// Status returns the message and unseen counts for a mailbox.
func (a *Account) Status(mailbox string) (num, unseen uint32, err error) {
	err = a.withClient(func(c *imapclient.Client) error {
		st, err := c.Status(mailbox, &imap.StatusOptions{NumMessages: true, NumUnseen: true}).Wait()
		if err != nil {
			return err
		}
		if st.NumMessages != nil {
			num = *st.NumMessages
		}
		if st.NumUnseen != nil {
			unseen = *st.NumUnseen
		}
		return nil
	})
	return num, unseen, err
}

func (a *Account) withClient(fn func(*imapclient.Client) error) error {
	c, err := imapclient.DialInsecure(a.world.IMAPAddr(), nil)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Login(a.address, a.password).Wait(); err != nil {
		return err
	}
	return fn(c)
}

// literal adapts a byte slice to imap.LiteralReader for in-process APPEND.
type literal struct{ *bytes.Reader }

func newLiteral(b []byte) literal { return literal{bytes.NewReader(b)} }

func (l literal) Size() int64 { return int64(l.Len()) }

// MailboxInfo is what a client learns about one mailbox from LIST and SELECT.
type MailboxInfo struct {
	Name          string
	Attrs         []imap.MailboxAttr
	UIDValidity   uint32
	UIDNext       uint32
	HighestModSeq uint64
}

// Mailboxes lists every mailbox with the values sync records, selected read-only
// exactly as the sync does so a direct seed and a real sync see the same numbers.
func (a *Account) Mailboxes() ([]MailboxInfo, error) {
	var out []MailboxInfo
	err := a.withClient(func(c *imapclient.Client) error {
		list, err := c.List("", "*", nil).Collect()
		if err != nil {
			return err
		}
		for _, mb := range list {
			sel, err := c.Select(mb.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
			if err != nil {
				return err
			}
			out = append(out, MailboxInfo{
				Name:          mb.Mailbox,
				Attrs:         mb.Attrs,
				UIDValidity:   sel.UIDValidity,
				UIDNext:       uint32(sel.UIDNext),
				HighestModSeq: sel.HighestModSeq,
			})
		}
		return nil
	})
	return out, err
}

// ServerMessage is one message as a client sees it, read independently of Ivy.
type ServerMessage struct {
	UID       uint32
	Flags     []imap.Flag
	MessageID string
}

// Messages returns every message in mailbox, oldest UID first. It is an
// independent read through a fresh IMAP connection, so a test can check its own
// model of the server against what a client would really learn.
func (a *Account) Messages(mailbox string) ([]ServerMessage, error) {
	var out []ServerMessage
	err := a.withClient(func(c *imapclient.Client) error {
		sel, err := c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			return err
		}
		if sel.NumMessages == 0 {
			return nil
		}
		// Stop 0 is "*": every UID from 1 up.
		all := imap.UIDSet{imap.UIDRange{Start: 1, Stop: 0}}
		bufs, err := c.Fetch(all, &imap.FetchOptions{UID: true, Flags: true, Envelope: true}).Collect()
		if err != nil {
			return err
		}
		for _, b := range bufs {
			m := ServerMessage{UID: uint32(b.UID), Flags: b.Flags}
			if b.Envelope != nil {
				m.MessageID = wrapMessageID(b.Envelope.MessageID)
			}
			out = append(out, m)
		}
		return nil
	})
	slices.SortFunc(out, func(x, y ServerMessage) int { return cmp.Compare(x.UID, y.UID) })
	return out, err
}

// wrapMessageID restores the angle brackets ENVELOPE strips.
func wrapMessageID(id string) string {
	if id == "" || (strings.HasPrefix(id, "<") && strings.HasSuffix(id, ">")) {
		return id
	}
	return "<" + id + ">"
}

// Unflag removes flags from a message in mailbox. Removing a flag that is not
// set is not an error, as on a real server.
func (a *Account) Unflag(mailbox string, uid uint32, flags ...imap.Flag) error {
	return a.withClient(func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, nil).Wait(); err != nil {
			return err
		}
		return c.Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{
			Op: imap.StoreFlagsDel, Silent: true, Flags: flags,
		}, nil).Close()
	})
}

// RenameMailbox renames a mailbox, keeping its messages, UIDs and UIDVALIDITY
// (RFC 3501 leaves that to the server; the in-memory one keeps them).
func (a *Account) RenameMailbox(oldName, newName string) error {
	return a.user.Rename(oldName, newName, nil)
}

// DeleteMailbox deletes a mailbox and every message in it.
func (a *Account) DeleteMailbox(name string) error {
	return a.user.Delete(name)
}
