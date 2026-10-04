package mailworld_test

import (
	"bytes"
	"fmt"
	"slices"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

func TestSeedObserverSeesEveryDeliveryWithItsServerUID(t *testing.T) {
	t.Parallel()
	var seen []mailworld.Delivery
	w, res := seedWorld(t, mailworld.Minimal(), mailworld.WithObserver(func(d mailworld.Delivery) {
		seen = append(seen, d)
	}))

	if len(seen) != res.Delivered {
		t.Fatalf("observer saw %d deliveries, Delivered = %d", len(seen), res.Delivered)
	}
	harvested := harvest(t, w, res.Accounts)
	byBox := map[string]int{}
	for _, h := range harvested {
		byBox[h.account+"/"+h.mailbox]++
	}
	next := map[string]uint32{}
	for _, d := range seen {
		key := d.Account + "/" + d.Mailbox
		next[key]++
		// A fresh mailbox hands out UIDs 1, 2, 3 in delivery order; the fast
		// seeder relies on the observer reporting the server's own numbers.
		if d.UID != next[key] {
			t.Fatalf("%s: delivery %d has UID %d", key, next[key], d.UID)
		}
		if len(d.Raw) == 0 || d.At.IsZero() {
			t.Fatalf("%s UID %d: empty raw or zero time", key, d.UID)
		}
	}
	for key, n := range byBox {
		if int(next[key]) != n {
			t.Errorf("%s: observed %d, server holds %d", key, next[key], n)
		}
	}
}

func TestMailboxesReportsWhatSyncReadsFromSelect(t *testing.T) {
	t.Parallel()
	w, res := seedWorld(t, mailworld.Minimal())
	acc := w.Account(res.Accounts[0].Address, res.Accounts[0].Password)

	boxes, err := acc.Mailboxes()
	if err != nil {
		t.Fatalf("Mailboxes: %v", err)
	}
	names := map[string]mailworld.MailboxInfo{}
	for _, b := range boxes {
		names[b.Name] = b
		if b.UIDValidity == 0 {
			t.Errorf("%s: zero UIDVALIDITY", b.Name)
		}
	}
	for _, want := range []string{"INBOX", "Archive", "Sent", "Drafts", "Trash", "Junk"} {
		if _, ok := names[want]; !ok {
			t.Errorf("mailbox %s missing from %v", want, boxes)
		}
	}
	modSeq, err := acc.HighestModSeq("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if names["INBOX"].HighestModSeq != modSeq {
		t.Errorf("INBOX modseq = %d, want %d", names["INBOX"].HighestModSeq, modSeq)
	}
}

// serverFlags reads every message's flags without touching a body, which would
// mark it \Seen, keyed by account/mailbox/UID.
func serverFlags(t *testing.T, w *mailworld.World, accounts []mailworld.SeedAccount) map[string][]imap.Flag {
	t.Helper()
	out := map[string][]imap.Flag{}
	for _, acc := range accounts {
		c := dial(t, w, acc.Address, acc.Password)
		boxes, err := c.List("", "*", nil).Collect()
		if err != nil {
			t.Fatal(err)
		}
		for _, box := range boxes {
			sel, err := c.Select(box.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
			if err != nil {
				t.Fatal(err)
			}
			if sel.NumMessages == 0 {
				continue
			}
			msgs, err := c.Fetch(imap.SeqSet{{Start: 1, Stop: sel.NumMessages}}, &imap.FetchOptions{UID: true, Flags: true}).Collect()
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range msgs {
				out[fmt.Sprintf("%s/%s/%d", acc.Address, box.Mailbox, m.UID)] = m.Flags
			}
		}
	}
	return out
}

// A fetch returns the bytes and flags the server holds, so a delivery must
// report those and not the seeder's own copy; otherwise a direct seed stores
// different rows from a sync (the corpus files use bare LF line endings, and
// the server lists flags in no fixed order).
func TestObserverReportsWhatAFetchReturns(t *testing.T) {
	t.Parallel()
	var seen []mailworld.Delivery
	w, res := seedWorld(t, mailworld.Demo(), mailworld.WithObserver(func(d mailworld.Delivery) {
		d.Raw = append([]byte(nil), d.Raw...) // valid only during the callback
		seen = append(seen, d)
	}))
	flags := serverFlags(t, w, res.Accounts) // before harvest, whose body fetch sets \Seen
	got := harvest(t, w, res.Accounts)
	byBox := map[string][]harvested{}
	for _, h := range got {
		key := h.account + "/" + h.mailbox
		byBox[key] = append(byBox[key], h)
	}
	next := map[string]int{}
	for _, d := range seen {
		key := d.Account + "/" + d.Mailbox
		i := next[key]
		next[key]++
		if want := byBox[key][i]; !bytes.Equal(d.Raw, want.raw) {
			t.Fatalf("%s UID %d: delivery reports %d bytes, a fetch returns %d", key, d.UID, len(d.Raw), len(want.raw))
		}
		// As a set: the server lists a message's flags in no fixed order.
		want := slices.Clone(flags[fmt.Sprintf("%s/%d", key, d.UID)])
		got := slices.Clone(d.Flags)
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("%s UID %d: delivery reports flags %v, a fetch returns %v", key, d.UID, d.Flags, want)
		}
	}
}
