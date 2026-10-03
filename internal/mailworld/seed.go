package mailworld

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"hash"
	"math/rand"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
)

// SeedPassword is the IMAP/SMTP password every seeded account uses. It is a
// dev-only constant, never a real credential (DEV.md section 6).
const SeedPassword = "ivy-dev"

// defaultSeed is fixed so a profile without an explicit seed always produces
// the same mailbox.
const defaultSeed int64 = 1

// corpus holds hand-written, reviewable raw messages (DEV.md section 3) that
// carry shapes a builder call cannot express cleanly: already-encoded charsets,
// RTL text and hostile HTML.
//
//go:embed testdata/corpus/*.eml
var corpus embed.FS

// SeedAccount is one account the seeder configured, with the password the dev
// config must use to log in.
type SeedAccount struct {
	Address  string
	Password string
}

// SeedResult reports what a seed run created. Hash is a stable digest of every
// delivered message, so tests and snapshots can assert determinism (DEV.md
// section 8).
type SeedResult struct {
	Accounts  []SeedAccount
	Delivered int
	Hash      string
}

// Profile is a deterministic mailbox shape. Construct one with Empty, Minimal,
// Demo or Large.
type Profile struct {
	Name     string
	generate func(*seeder) error
}

// SeedOption customises a seed run.
type SeedOption func(*seedConfig)

type seedConfig struct {
	seed    int64
	observe func(Delivery)
}

// Delivery is one seeded message as the server stored it, reported to a
// WithObserver callback. Raw is only valid during the callback.
type Delivery struct {
	Account string
	Mailbox string
	UID     uint32
	Raw     []byte
	Flags   []imap.Flag
	At      time.Time
}

// WithObserver streams each delivery to fn as it is made, so the fast dev
// seeder can write the same messages to the databases without holding a large
// profile in memory or going back through IMAP.
func WithObserver(fn func(Delivery)) SeedOption {
	return func(c *seedConfig) { c.observe = fn }
}

// WithSeed picks the pseudo-random stream; the same seed and profile always
// produce byte-identical data.
func WithSeed(seed int64) SeedOption {
	return func(c *seedConfig) { c.seed = seed }
}

// Seed generates a profile into the world's mailboxes. It delivers through the
// same in-process APPEND path tests use, so the result is visible to a real
// IMAP client with normal UIDs and flags.
func Seed(w *World, p Profile, opts ...SeedOption) (SeedResult, error) {
	if p.generate == nil {
		return SeedResult{}, fmt.Errorf("mailworld: profile %q has no generator", p.Name)
	}
	cfg := seedConfig{seed: defaultSeed}
	for _, opt := range opts {
		opt(&cfg)
	}
	s := &seeder{
		w:    w,
		rnd:  rand.New(rand.NewSource(cfg.seed)), //nolint:gosec // G404: seeded on purpose so a profile is byte-reproducible
		hash: sha256.New(),

		observe: cfg.observe,
	}
	if err := p.generate(s); err != nil {
		return SeedResult{}, fmt.Errorf("seed %s: %w", p.Name, err)
	}
	return SeedResult{
		Accounts:  s.accounts,
		Delivered: s.delivered,
		Hash:      hex.EncodeToString(s.hash.Sum(nil)),
	}, nil
}

// ParseProfile maps a CLI/profile name to its Profile. "large" gets the default
// 100k size; pass Large(n) for a different one.
func ParseProfile(name string) (Profile, error) {
	switch name {
	case "empty":
		return Empty(), nil
	case "minimal":
		return Minimal(), nil
	case "demo":
		return Demo(), nil
	case "large":
		return Large(100_000), nil
	default:
		return Profile{}, fmt.Errorf("mailworld: unknown profile %q", name)
	}
}

// Empty configures accounts and their standard mailboxes but delivers nothing,
// for first-run and empty-state screens.
func Empty() Profile {
	return Profile{Name: "empty", generate: func(s *seeder) error {
		acc := s.account("ivy@grove.test")
		s.standardMailboxes(acc)
		return nil
	}}
}

// Minimal is the fastest believable inbox: about fifteen messages, one account.
func Minimal() Profile {
	return Profile{Name: "minimal", generate: func(s *seeder) error {
		acc := s.account("ivy@grove.test")
		s.standardMailboxes(acc)
		base := time.Date(2025, 12, 1, 9, 0, 0, 0, time.UTC)
		for i := 0; i < 15; i++ {
			at := base.Add(time.Duration(i) * 7 * time.Hour)
			switch i % 5 {
			case 0:
				s.newsletter(acc, "weekly@example.com", "Garden Weekly", i, at)
			case 1:
				s.receipt(acc, i, at)
			default:
				s.plainMessage(acc, "friend@example.com", "Catching up", "", at, i)
			}
		}
		return nil
	}}
}

// Demo is the everyday profile: several hundred messages across three
// addresses covering every shape the frontend screens must survive.
func Demo() Profile {
	return Profile{Name: "demo", generate: func(s *seeder) error {
		a := s.account("ivy-a@grove.test")
		b := s.account("ivy-b@grove.test")
		c := s.account("ivy-c@grove.test")
		accounts := []*Account{a, b, c}
		for _, acc := range accounts {
			s.standardMailboxes(acc)
		}

		base := time.Date(2025, 10, 1, 8, 0, 0, 0, time.UTC)
		s.corpusMessage(a, "INBOX", "xss.html.eml", nil, base.Add(2*time.Hour))
		s.corpusMessage(a, "INBOX", "rtl-emoji.eml", []imap.Flag{imap.FlagSeen}, base.Add(5*time.Hour))
		s.corpusMessage(a, "INBOX", "charset-latin1.eml", []imap.Flag{imap.FlagSeen}, base.Add(9*time.Hour))

		// The schedule is sized so threads push the total to roughly 450
		// messages, comfortably inside the demo profile's 300-500 promise.
		const count = 270
		for i := 0; i < count; i++ {
			acc := accounts[i%len(accounts)]
			other := accounts[(i+1)%len(accounts)]
			at := base.Add(time.Duration(i)*19*time.Hour + time.Duration(i%4)*7*time.Minute)
			switch i % 15 {
			case 0:
				s.personalThread(acc, other.Address(), "Weekend plans", i, 3+i%3, at)
			case 1:
				s.newsletter(acc, "weekly@example.com", "Garden Weekly", i, at)
			case 2:
				s.receipt(acc, i, at)
			case 3:
				s.invoice(acc, i, at)
			case 4:
				s.contactForm(acc, i, at)
			case 5:
				s.securityReport(acc, i, at)
			case 6:
				s.calendarInvite(acc, i, at)
			case 7:
				s.junkSpam(acc, i, at)
			case 8:
				s.attachmentSampler(acc, i, at)
			case 9:
				s.inlineImage(acc, i, at)
			case 10:
				s.remoteImage(acc, i, at)
			case 11:
				s.plainMessage(acc, "colleague@example.com", "Quick question about the sync", "", at, i)
			case 12:
				s.hugeBody(acc, i, at)
			case 13:
				s.personalThread(acc, "friend@example.com", "Re: holiday photos", i, 8, at)
			case 14:
				s.plainMessage(acc, "notifications@example.com", "Your export is ready", "", at, i)
			}
		}
		return nil
	}}
}

// Large generates a synthetic mailbox for performance and scroll work. Bodies
// vary in size with a realistic long tail; no threads are built so the loop
// stays cheap.
func Large(messages int) Profile {
	return Profile{Name: "large", generate: func(s *seeder) error {
		acc := s.account("ivy@grove.test")
		s.standardMailboxes(acc)
		base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i < messages; i++ {
			size := 400 + s.rnd.Intn(24_000)
			body := strings.Repeat("lorem ipsum dolor sit amet consectetur ", size/36+1)
			at := base.Add(time.Duration(i) * 13 * time.Minute)
			raw := Msg().
				From(fmt.Sprintf("sender-%d@example.com", i%500)).
				To(acc.Address()).
				Subject(fmt.Sprintf("Message %d", i)).
				Date(at).
				MessageID(fmt.Sprintf("<large-%d@grove.test>", i)).
				Text(body).
				Build()
			var flags []imap.Flag
			if i%3 == 0 {
				flags = append(flags, imap.FlagSeen)
			}
			s.deliver(acc, "INBOX", raw, flags, at)
		}
		return nil
	}}
}

// seeder carries the deterministic state of one Seed run.
type seeder struct {
	w         *World
	rnd       *rand.Rand
	hash      hash.Hash
	accounts  []SeedAccount
	delivered int
	observe   func(Delivery)
}

func (s *seeder) account(address string) *Account {
	acc := s.w.Account(address, SeedPassword)
	s.accounts = append(s.accounts, SeedAccount{Address: address, Password: SeedPassword})
	return acc
}

func (s *seeder) standardMailboxes(acc *Account) {
	for _, name := range []string{"Archive", "Sent", "Drafts", "Trash", "Junk"} {
		_ = acc.CreateMailbox(name)
	}
}

func (s *seeder) deliver(acc *Account, mailbox string, raw []byte, flags []imap.Flag, at time.Time) {
	data, err := acc.user.Append(mailbox, newLiteral(raw), &imap.AppendOptions{Flags: flags, Time: at})
	if err != nil {
		panic("mailworld: seed append to " + mailbox + ": " + err.Error())
	}
	s.delivered++
	if s.observe != nil {
		s.observe(Delivery{Account: acc.Address(), Mailbox: mailbox, UID: uint32(data.UID), Raw: raw, Flags: flags, At: at})
	}
	writeSeedField(s.hash, acc.Address())
	writeSeedField(s.hash, mailbox)
	writeSeedField(s.hash, string(raw))
	for _, f := range flags {
		writeSeedField(s.hash, string(f))
	}
}

func writeSeedField(h hash.Hash, value string) {
	fmt.Fprintf(h, "%d:", len(value))
	h.Write([]byte(value))
}

func (s *seeder) corpusMessage(acc *Account, mailbox, name string, flags []imap.Flag, at time.Time) {
	raw, err := corpus.ReadFile("testdata/corpus/" + name)
	if err != nil {
		panic("mailworld: missing corpus " + name + ": " + err.Error())
	}
	s.deliver(acc, mailbox, raw, flags, at)
}

// --- content generators -----------------------------------------------------

func (s *seeder) plainMessage(acc *Account, from, subject, mailbox string, at time.Time, i int) {
	if mailbox == "" {
		mailbox = "INBOX"
	}
	phrases := []string{
		"Nothing urgent, just keeping the thread alive.",
		"No rush at all, whenever you get a moment.",
		"Just keeping you posted on where things stand.",
		"Talk soon, I hope.",
	}
	raw := Msg().
		From(from).
		To(acc.Address()).
		Subject(subject).
		Date(at).
		MessageID(fmt.Sprintf("<plain-%d-%d@grove.test>", i, len(subject))).
		Text("Hi,\n\n" + phrases[s.rnd.Intn(len(phrases))] + "\n\n— sent from a test fixture\n").
		Build()
	var flags []imap.Flag
	if i%2 == 0 {
		flags = append(flags, imap.FlagSeen)
	}
	s.deliver(acc, mailbox, raw, flags, at)
}

func (s *seeder) personalThread(acc *Account, other, subject string, i, n int, at time.Time) {
	refs := ""
	for m := 0; m < n; m++ {
		id := fmt.Sprintf("<thread-%d-%d@grove.test>", i, m)
		from, to, mailbox := other, acc.Address(), "INBOX"
		if m%2 == 1 {
			from, to, mailbox = acc.Address(), other, "Sent"
		}
		b := Msg().
			From(from).
			To(to).
			Subject(subject).
			Date(at.Add(time.Duration(m) * 41 * time.Minute)).
			MessageID(id)
		if refs != "" {
			b.Header("In-Reply-To", refs)
			b.Header("References", refs)
		}
		body := "Sounds good to me. Let me check and get back to you.\n\nOn the road,\n" + shortName(from)
		if m == 0 {
			body = "Hey!\n\nAre we still on for this weekend? I can bring the food if you bring the coffee.\n"
		}
		b.Text(body)
		flags := []imap.Flag{}
		if m%2 == 0 {
			flags = append(flags, imap.FlagSeen)
		}
		if m == 0 {
			flags = append(flags, imap.FlagFlagged)
		}
		s.deliver(acc, mailbox, b.Build(), flags, at.Add(time.Duration(m)*41*time.Minute))
		refs = strings.TrimSpace(refs + " " + id)
	}
}

func (s *seeder) newsletter(acc *Account, from, title string, i int, at time.Time) {
	html := fmt.Sprintf(`<html><body><h1>%s</h1>
<p>This week in the garden: seed starting, pruning and a note on soil.</p>
<a href="https://example.com/issues/%d">Read online</a>
</body></html>`, title, i)
	raw := Msg().
		From(from).
		To(acc.Address()).
		Subject(fmt.Sprintf("%s #%d", title, i)).
		Date(at).
		MessageID(fmt.Sprintf("<news-%d@grove.test>", i)).
		Header("List-Unsubscribe", fmt.Sprintf("<https://example.com/u/%d>, <mailto:unsub@example.com>", i)).
		Header("List-Id", "Garden Weekly <weekly.example.com>").
		HTML(html).
		Text(title + " #" + fmt.Sprint(i) + "\n\nThis week in the garden.\n").
		Build()
	s.deliver(acc, "INBOX", raw, []imap.Flag{imap.FlagSeen}, at)
}

func (s *seeder) receipt(acc *Account, i int, at time.Time) {
	html := fmt.Sprintf(`<html><body>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Order","orderNumber":"%d","price":"12.40","priceCurrency":"EUR"}</script>
<h1>Your receipt</h1><p>Thanks for your order. Total: 12.40 EUR</p>
</body></html>`, 10_000+i)
	raw := Msg().
		From("receipts@example.com").
		To(acc.Address()).
		Subject(fmt.Sprintf("Receipt for order %d", 10_000+i)).
		Date(at).
		MessageID(fmt.Sprintf("<receipt-%d@grove.test>", i)).
		HTML(html).
		Text(fmt.Sprintf("Thanks for your order %d. Total: 12.40 EUR\n", 10_000+i)).
		Build()
	s.deliver(acc, "INBOX", raw, []imap.Flag{imap.FlagSeen}, at)
}

func (s *seeder) invoice(acc *Account, i int, at time.Time) {
	raw := Msg().
		From("billing@example.com").
		To(acc.Address()).
		Subject(fmt.Sprintf("Invoice %d due in 14 days", 4_200+i)).
		Date(at).
		MessageID(fmt.Sprintf("<invoice-%d@grove.test>", i)).
		Text("Please find your invoice attached.\n").
		Attach(fmt.Sprintf("invoice-%d.pdf", 4_200+i), "application/pdf",
			append([]byte("%PDF-1.4\n"), deterministicBytes(2048, i)...)).
		Build()
	s.deliver(acc, "INBOX", raw, nil, at)
}

func (s *seeder) contactForm(acc *Account, i int, at time.Time) {
	raw := Msg().
		From("website@example.com").
		To(acc.Address()).
		Subject("New message from the contact form").
		Date(at).
		MessageID(fmt.Sprintf("<contact-%d@grove.test>", i)).
		Header("Reply-To", fmt.Sprintf("visitor-%d@example.com", i)).
		Text("Someone wrote through the contact form. Reply directly to reach them.\n").
		Build()
	s.deliver(acc, "INBOX", raw, nil, at)
}

func (s *seeder) securityReport(acc *Account, i int, at time.Time) {
	raw := Msg().
		From("security-noreply@example.com").
		To(acc.Address()).
		Subject("Security alert: new sign-in").
		Date(at).
		MessageID(fmt.Sprintf("<security-%d@grove.test>", i)).
		Header("Authentication-Results", "example.com; spf=pass smtp.mailfrom=example.com; dkim=pass; dmarc=pass").
		Text("We noticed a new sign-in to your account. If this was you, no action is needed.\n").
		Build()
	s.deliver(acc, "INBOX", raw, []imap.Flag{imap.FlagSeen}, at)
}

func (s *seeder) calendarInvite(acc *Account, i int, at time.Time) {
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\n" +
		fmt.Sprintf("UID:%d@grove.test\r\nDTSTART:20251120T100000Z\r\nSUMMARY:Standup\r\n", i) +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
	raw := Msg().
		From("organiser@example.com").
		To(acc.Address()).
		Subject("Invitation: team standup").
		Date(at).
		MessageID(fmt.Sprintf("<invite-%d@grove.test>", i)).
		Text("You are invited to the team standup.\n").
		Attach("invite.ics", "text/calendar; method=REQUEST", []byte(ics)).
		Build()
	s.deliver(acc, "INBOX", raw, nil, at)
}

func (s *seeder) junkSpam(acc *Account, i int, at time.Time) {
	raw := Msg().
		From("winner@spam.example").
		To(acc.Address()).
		Subject("You have won a free cruise!!!").
		Date(at).
		MessageID(fmt.Sprintf("<spam-%d@grove.test>", i)).
		Header("X-Spam-Score", "9.4").
		Header("X-Spam-Status", "Yes, score=9.4").
		HTML(`<html><body><p>CLICK NOW to claim your prize.</p><img src="https://tracking.example/pixel.gif" width="1" height="1"></body></html>`).
		Build()
	s.deliver(acc, "Junk", raw, nil, at)
}

// attachmentSampler produces a message per attachment type so the UI and
// extractors meet real content types.
func (s *seeder) attachmentSampler(acc *Account, i int, at time.Time) {
	types := []struct {
		name string
		ct   string
	}{
		{"photo.png", "image/png"},
		{"scan.jpg", "image/jpeg"},
		{"notes.txt", "text/plain"},
		{"data.csv", "text/csv"},
		{"archive.zip", "application/zip"},
		{"report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	}
	t := types[i%len(types)]
	raw := Msg().
		From("files@example.com").
		To(acc.Address()).
		Subject("Attached: "+t.name).
		Date(at).
		MessageID(fmt.Sprintf("<attach-%d@grove.test>", i)).
		Text("See the attached file.\n").
		Attach(t.name, t.ct, deterministicBytes(1024, i)).
		Build()
	s.deliver(acc, "INBOX", raw, nil, at)
}

func (s *seeder) inlineImage(acc *Account, i int, at time.Time) {
	html := `<html><body><p>Here is the chart:</p><img src="cid:chart.png" alt="chart"></body></html>`
	raw := Msg().
		From("analyst@example.com").
		To(acc.Address()).
		Subject("Chart for review").
		Date(at).
		MessageID(fmt.Sprintf("<inline-%d@grove.test>", i)).
		HTML(html).
		Text("Here is the chart.\n").
		Inline("chart.png", "image/png", deterministicBytes(512, i)).
		Build()
	s.deliver(acc, "INBOX", raw, []imap.Flag{imap.FlagSeen}, at)
}

func (s *seeder) remoteImage(acc *Account, i int, at time.Time) {
	html := fmt.Sprintf(`<html><body><p>Read the full story.</p><img src="https://tracking.example/pixel.gif?u=%d" width="1" height="1"></body></html>`, i)
	raw := Msg().
		From("digest@example.com").
		To(acc.Address()).
		Subject("Story of the day").
		Date(at).
		MessageID(fmt.Sprintf("<remote-%d@grove.test>", i)).
		HTML(html).
		Build()
	s.deliver(acc, "INBOX", raw, nil, at)
}

func (s *seeder) hugeBody(acc *Account, i int, at time.Time) {
	body := strings.Repeat("This is a long paragraph used to test scrolling and rendering. ", 400)
	raw := Msg().
		From("verbose@example.com").
		To(acc.Address()).
		Subject("A very long message").
		Date(at).
		MessageID(fmt.Sprintf("<huge-%d@grove.test>", i)).
		Text(body).
		Build()
	s.deliver(acc, "INBOX", raw, nil, at)
}

// deterministicBytes returns n bytes derived from seed, so attachments are
// byte-identical across runs but do not look like text.
func deterministicBytes(n, seed int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte((i*31 + seed*7) % 251) //nolint:gosec // G115: reduced mod 251, always fits a byte
	}
	return out
}

func shortName(address string) string {
	if i := strings.IndexByte(address, '@'); i > 0 {
		return address[:i]
	}
	return address
}
