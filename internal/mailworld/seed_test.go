package mailworld_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

// harvested is one message read back out of the fake IMAP server, the way any
// real client would see it.
type harvested struct {
	account string
	mailbox string
	flags   []imap.Flag
	raw     []byte
}

// harvest reads every message from every mailbox of every seed account through
// a real IMAP client, so assertions run against what a client observes rather
// than the seeder's in-memory intent (STANDARDS.md section 2).
func harvest(t *testing.T, w *mailworld.World, accounts []mailworld.SeedAccount) []harvested {
	t.Helper()
	var out []harvested
	for _, acc := range accounts {
		c := dial(t, w, acc.Address, acc.Password)
		boxes, err := c.List("", "*", nil).Collect()
		if err != nil {
			t.Fatalf("list mailboxes for %s: %v", acc.Address, err)
		}
		for _, box := range boxes {
			sel, err := c.Select(box.Mailbox, nil).Wait()
			if err != nil {
				t.Fatalf("select %s/%s: %v", acc.Address, box.Mailbox, err)
			}
			if sel.NumMessages == 0 {
				continue
			}
			section := &imap.FetchItemBodySection{}
			msgs, err := c.Fetch(
				imap.SeqSet{{Start: 1, Stop: sel.NumMessages}},
				&imap.FetchOptions{UID: true, Flags: true, BodySection: []*imap.FetchItemBodySection{section}},
			).Collect()
			if err != nil {
				t.Fatalf("fetch %s/%s: %v", acc.Address, box.Mailbox, err)
			}
			for _, m := range msgs {
				out = append(out, harvested{
					account: acc.Address,
					mailbox: box.Mailbox,
					flags:   m.Flags,
					raw:     m.FindBodySection(section),
				})
			}
		}
	}
	return out
}

func seedWorld(t *testing.T, profile mailworld.Profile, opts ...mailworld.SeedOption) (*mailworld.World, mailworld.SeedResult) {
	t.Helper()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	res, err := mailworld.Seed(w, profile, opts...)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return w, res
}

func TestSeedEmptyConfiguresAccountsWithoutMail(t *testing.T) {
	t.Parallel()
	w, res := seedWorld(t, mailworld.Empty())
	if len(res.Accounts) == 0 {
		t.Fatal("empty profile configured no accounts")
	}
	if res.Delivered != 0 {
		t.Errorf("delivered %d messages, want 0", res.Delivered)
	}
	if got := harvest(t, w, res.Accounts); len(got) != 0 {
		t.Errorf("harvested %d messages, want 0", len(got))
	}
}

func TestSeedMinimalDeliversAboutFifteenMessages(t *testing.T) {
	t.Parallel()
	w, res := seedWorld(t, mailworld.Minimal())
	if len(res.Accounts) != 1 {
		t.Errorf("minimal has %d accounts, want 1", len(res.Accounts))
	}
	got := harvest(t, w, res.Accounts)
	if res.Delivered != len(got) {
		t.Errorf("Delivered = %d but harvested %d", res.Delivered, len(got))
	}
	if len(got) < 10 || len(got) > 20 {
		t.Errorf("minimal delivered %d messages, want about 15", len(got))
	}
}

func TestSeedDemoIsBelievable(t *testing.T) {
	t.Parallel()
	w, res := seedWorld(t, mailworld.Demo())
	if len(res.Accounts) != 3 {
		t.Errorf("demo has %d accounts, want 3", len(res.Accounts))
	}
	got := harvest(t, w, res.Accounts)
	if res.Delivered != len(got) {
		t.Errorf("Delivered = %d but harvested %d", res.Delivered, len(got))
	}
	if len(got) < 300 || len(got) > 500 {
		t.Fatalf("demo delivered %d messages, want 300-500", len(got))
	}

	// Every shape the demo profile promises must actually appear, or the screens
	// built against it are testing a fiction (DEV.md section 3).
	markers := map[string][]byte{
		"list-unsubscribe": []byte("List-Unsubscribe:"),
		"reply-to":         []byte("Reply-To:"),
		"attachment":       []byte("Content-Disposition: attachment"),
		"inline-image":     []byte("Content-ID:"),
		"xss":              []byte("onerror"),
		"remote-image":     []byte("tracking.example"),
		"non-utf8":         []byte("iso-8859-1"),
		"rtl":              []byte("\u05e9\u05dc\u05d5\u05dd"),
		"emoji":            []byte("\U0001f33f"),
		"calendar":         []byte("text/calendar"),
		"json-ld":          []byte("application/ld+json"),
		"spam-score":       []byte("X-Spam-Score:"),
		"multipart-mixed":  []byte("multipart/mixed"),
	}
	for name, needle := range markers {
		found := false
		for _, m := range got {
			if bytes.Contains(m.raw, needle) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("demo contains no message with %s", name)
		}
	}

	// Threads: at least one message must reference another by Message-ID.
	threaded := 0
	for _, m := range got {
		if bytes.Contains(m.raw, []byte("In-Reply-To:")) || bytes.Contains(m.raw, []byte("References:")) {
			threaded++
		}
	}
	if threaded < 5 {
		t.Errorf("demo has %d threaded messages, want several", threaded)
	}

	// A believable mailbox has some read and some flagged mail.
	var seen, flagged int
	for _, m := range got {
		for _, f := range m.flags {
			switch f {
			case imap.FlagSeen:
				seen++
			case imap.FlagFlagged:
				flagged++
			default:
				// Other flags are not part of this check.
			}
		}
	}
	if seen == 0 {
		t.Error("demo has no seen messages")
	}
	if flagged == 0 {
		t.Error("demo has no flagged messages")
	}
}

func TestSeedIsDeterministic(t *testing.T) {
	t.Parallel()
	_, a := seedWorld(t, mailworld.Demo(), mailworld.WithSeed(7))
	_, b := seedWorld(t, mailworld.Demo(), mailworld.WithSeed(7))
	if a.Hash != b.Hash {
		t.Errorf("same seed produced different data:\n a=%s\n b=%s", a.Hash, b.Hash)
	}
	if a.Delivered != b.Delivered {
		t.Errorf("Delivered differs: %d vs %d", a.Delivered, b.Delivered)
	}
}

func TestSeedDifferentSeedDiffers(t *testing.T) {
	t.Parallel()
	_, a := seedWorld(t, mailworld.Minimal(), mailworld.WithSeed(1))
	_, b := seedWorld(t, mailworld.Minimal(), mailworld.WithSeed(2))
	if a.Hash == b.Hash {
		t.Error("different seeds produced identical data")
	}
}

func TestSeedLargeDeliversRequestedCount(t *testing.T) {
	t.Parallel()
	w, res := seedWorld(t, mailworld.Large(200))
	if res.Delivered < 200 {
		t.Errorf("large delivered %d, want at least 200", res.Delivered)
	}
	got := harvest(t, w, res.Accounts)
	if len(got) < 200 {
		t.Errorf("harvested %d, want at least 200", len(got))
	}
}

func TestSeedUsesOnlyReservedDomains(t *testing.T) {
	t.Parallel()
	w, res := seedWorld(t, mailworld.Demo())
	for _, acc := range res.Accounts {
		if !strings.HasSuffix(acc.Address, ".test") {
			t.Errorf("seed account %q is not under a reserved .test domain", acc.Address)
		}
	}
	forbidden := []string{"@gmail.com", "@yahoo.com", "@hotmail.com", "@outlook.com", "@icloud.com", "@protonmail.com"}
	for _, m := range harvest(t, w, res.Accounts) {
		lower := bytes.ToLower(m.raw)
		for _, bad := range forbidden {
			if bytes.Contains(lower, []byte(bad)) {
				t.Errorf("seeded message in %s contains real-looking address %s", m.mailbox, bad)
			}
		}
	}
}

func TestParseProfile(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"empty", "minimal", "demo", "large"} {
		p, err := mailworld.ParseProfile(name)
		if err != nil {
			t.Fatalf("ParseProfile(%q): %v", name, err)
		}
		if p.Name != name {
			t.Errorf("ParseProfile(%q).Name = %q", name, p.Name)
		}
	}
	if _, err := mailworld.ParseProfile("nope"); err == nil {
		t.Error("ParseProfile(nope) succeeded, want an error")
	}
}
