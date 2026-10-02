package mailworld_test

import (
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

func newWorld(t *testing.T, opts ...mailworld.Option) *mailworld.World {
	t.Helper()
	w, err := mailworld.New(opts...)
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func selectCondStore(t *testing.T, c *imapclient.Client) *imap.SelectData {
	t.Helper()
	data, err := c.Select("INBOX", &imap.SelectOptions{CondStore: true}).Wait()
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	return data
}

// A CONDSTORE-capable world must advertise it, and SELECT ... (CONDSTORE)
// must report a non-zero HIGHESTMODSEQ.
func TestSelectReportsHighestModSeq(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	c := dial(t, w, "me@grove.test", "secret")
	caps, err := c.Capability().Wait()
	if err != nil {
		t.Fatalf("capability: %v", err)
	}
	if !caps.Has(imap.CapCondStore) {
		t.Fatalf("CONDSTORE not advertised, caps = %v", caps)
	}

	data := selectCondStore(t, c)
	if data.HighestModSeq == 0 {
		t.Errorf("HIGHESTMODSEQ = 0, want non-zero")
	}
}

// FETCH MODSEQ returns each message's mod-sequence, and it advances when the
// message changes.
func TestFetchModSeqAdvancesOnChange(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	c := dial(t, w, "me@grove.test", "secret")
	before := selectCondStore(t, c)

	fetch := func() uint64 {
		t.Helper()
		msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{ModSeq: true}).Collect()
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if len(msgs) != 1 {
			t.Fatalf("fetched %d messages, want 1", len(msgs))
		}
		return msgs[0].ModSeq
	}

	first := fetch()
	if first == 0 {
		t.Fatalf("MODSEQ = 0, want non-zero")
	}
	if first != before.HighestModSeq {
		t.Errorf("message MODSEQ %d != HIGHESTMODSEQ %d", first, before.HighestModSeq)
	}

	if err := acc.Flag("INBOX", uid, imap.FlagSeen); err != nil {
		t.Fatalf("flag: %v", err)
	}

	second := fetch()
	if second <= first {
		t.Errorf("MODSEQ after change = %d, want > %d", second, first)
	}
}

// CHANGEDSINCE limits a FETCH to messages whose mod-sequence is newer.
func TestChangedSinceFiltersUnchanged(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	stable := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("stable").Build())
	changing := acc.Deliver("INBOX", mailworld.Msg().From("b@example.com").Subject("changing").Build())

	c := dial(t, w, "me@grove.test", "secret")
	floor := selectCondStore(t, c).HighestModSeq

	if err := acc.Flag("INBOX", changing, imap.FlagFlagged); err != nil {
		t.Fatalf("flag: %v", err)
	}

	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(1), imap.UID(2)), &imap.FetchOptions{
		Flags:        true,
		UID:          true,
		ModSeq:       true,
		ChangedSince: floor,
	}).Collect()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(msgs) != 1 || msgs[0].UID != imap.UID(changing) {
		t.Errorf("CHANGEDSINCE returned %+v, want only UID %d", uidsOf(msgs), changing)
	}
	_ = stable
}

// A QRESYNC SELECT reports messages expunged since the client's mod-sequence
// as VANISHED (EARLIER).
func TestQResyncSelectReportsVanished(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	victim := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("old").Build())
	acc.Deliver("INBOX", mailworld.Msg().From("b@example.com").Subject("new").Build())

	c := dial(t, w, "me@grove.test", "secret")
	if _, err := c.Enable(imap.CapQResync).Wait(); err != nil {
		t.Fatalf("enable QRESYNC: %v", err)
	}
	base := selectCondStore(t, c)

	if err := acc.Expunge("INBOX", victim); err != nil {
		t.Fatalf("expunge: %v", err)
	}

	if err := c.Unselect().Wait(); err != nil {
		t.Fatalf("unselect: %v", err)
	}
	data, err := c.Select("INBOX", &imap.SelectOptions{QResync: &imap.QResyncOptions{
		UIDValidity: base.UIDValidity,
		ModSeq:      base.HighestModSeq,
	}}).Wait()
	if err != nil {
		t.Fatalf("qresync select: %v", err)
	}
	if !data.Vanished.Contains(imap.UID(victim)) {
		t.Errorf("VANISHED = %v, want it to contain UID %d", data.Vanished, victim)
	}
}

// STATUS reports HIGHESTMODSEQ so a sync can poll a folder without selecting it.
func TestStatusReportsHighestModSeq(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	c := dial(t, w, "me@grove.test", "secret")
	st, err := c.Status("INBOX", &imap.StatusOptions{NumMessages: true, HighestModSeq: true}).Wait()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.HighestModSeq == 0 {
		t.Errorf("STATUS HIGHESTMODSEQ = 0, want non-zero")
	}
}

// STORE (UNCHANGEDSINCE) must refuse to overwrite a message that changed after
// the client's floor, leaving its flags untouched.
func TestStoreUnchangedSinceRefusesStale(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	c := dial(t, w, "me@grove.test", "secret")
	floor := selectCondStore(t, c).HighestModSeq

	// Someone else changes the message, advancing its mod-sequence.
	if err := acc.Flag("INBOX", uid, imap.FlagSeen); err != nil {
		t.Fatalf("flag: %v", err)
	}

	// A store pinned to the stale floor must not apply.
	cmd := c.Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagFlagged},
	}, &imap.StoreOptions{UnchangedSince: floor})
	if err := cmd.Close(); err != nil {
		t.Fatalf("stale store: %v", err)
	}

	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{Flags: true}).Collect()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("fetched %d messages, want 1", len(msgs))
	}
	for _, f := range msgs[0].Flags {
		if f == imap.FlagFlagged {
			t.Errorf("stale STORE applied: flags = %v", msgs[0].Flags)
		}
	}
}

// A world built without CONDSTORE must not advertise it, so sync exercises the
// UID/flags fallback path.
func TestWithoutCondStoreFallback(t *testing.T) {
	t.Parallel()
	w := newWorld(t, mailworld.WithoutCondStore())
	w.Account("me@grove.test", "secret")

	c := dial(t, w, "me@grove.test", "secret")
	caps, err := c.Capability().Wait()
	if err != nil {
		t.Fatalf("capability: %v", err)
	}
	if caps.Has(imap.CapCondStore) {
		t.Errorf("CONDSTORE advertised when disabled, caps = %v", caps)
	}
}

// The scenario bot can read a mailbox's current mod-sequence the way sync
// captures a floor before it starts watching for changes.
func TestAccountHighestModSeq(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("x").Build())

	before, err := acc.HighestModSeq("INBOX")
	if err != nil {
		t.Fatalf("highest modseq: %v", err)
	}
	if before == 0 {
		t.Fatalf("HighestModSeq = 0, want non-zero")
	}

	if err := acc.Flag("INBOX", uid, imap.FlagSeen); err != nil {
		t.Fatalf("flag: %v", err)
	}
	after, err := acc.HighestModSeq("INBOX")
	if err != nil {
		t.Fatalf("highest modseq: %v", err)
	}
	if after <= before {
		t.Errorf("HighestModSeq after change = %d, want > %d", after, before)
	}
}

func uidsOf(msgs []*imapclient.FetchMessageBuffer) []imap.UID {
	uids := make([]imap.UID, 0, len(msgs))
	for _, m := range msgs {
		uids = append(uids, m.UID)
	}
	return uids
}
