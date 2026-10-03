package mailworld_test

import (
	"testing"

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
