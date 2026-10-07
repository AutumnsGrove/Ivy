package compose

import (
	"strings"
	"testing"
	"time"
)

var (
	aliceIdentity = IdentityRef{Address: "hello@example.test", DisplayName: "Autumn", Signature: "— Autumn"}
	aliasIdentity = IdentityRef{Address: "feedback@example.test", DisplayName: "Grove Feedback", Signature: "— The Grove"}
)

func addr(address string) Address { return Address{Address: address} }

func named(name, address string) Address { return Address{Name: name, Address: address} }

func addresses(in []Address) []string {
	out := make([]string, len(in))
	for i, a := range in {
		out[i] = a.Address
	}
	return out
}

func incoming() Incoming {
	return Incoming{
		From:       []Address{named("Mara", "mara@example.com")},
		To:         []Address{addr("hello@example.test")},
		Cc:         []Address{addr("friend@example.com"), addr("hello@example.test")},
		Subject:    "Moving my blog over",
		Date:       time.Date(2026, 10, 6, 9, 12, 0, 0, time.UTC),
		MessageID:  "<mara-1@example.com>",
		References: []string{"<root@example.com>"},
		Text:       "Hi Autumn,\n\nCan you help?",
	}
}

// A reply goes to Reply-To when one is set, and to From when it is not.
func TestReplyTargetsReplyToThenFrom(t *testing.T) {
	t.Parallel()

	withReplyTo := incoming()
	withReplyTo.ReplyTo = []Address{addr("visitor@example.com")}
	got := Reply(withReplyTo, []IdentityRef{aliceIdentity}, aliceIdentity, false)
	if len(got.To) != 1 || got.To[0].Address != "visitor@example.com" {
		t.Fatalf("to = %v, want the Reply-To address", addresses(got.To))
	}
	if got.ReplyTarget != "visitor@example.com" {
		t.Errorf("reply target = %q, want the Reply-To", got.ReplyTarget)
	}

	plain := Reply(incoming(), []IdentityRef{aliceIdentity}, aliceIdentity, false)
	if len(plain.To) != 1 || plain.To[0].Address != "mara@example.com" {
		t.Fatalf("to = %v, want the From address", addresses(plain.To))
	}
	if len(plain.Cc) != 0 {
		t.Errorf("cc = %v, want none for a direct reply", addresses(plain.Cc))
	}
}

// The identity the reply is sent as is the address the mail was delivered to.
func TestReplySendsAsTheDeliveredIdentity(t *testing.T) {
	t.Parallel()

	orig := incoming()
	orig.DeliveredTo = []Address{addr("feedback@example.test")}
	got := Reply(orig, []IdentityRef{aliceIdentity, aliasIdentity}, aliceIdentity, false)
	if got.From.Address != "feedback@example.test" || got.From.Name != "Grove Feedback" {
		t.Fatalf("from = %+v, want the delivered alias identity", got.From)
	}
	if got.MissingIdentity != "" {
		t.Errorf("missing = %q, want empty when the alias is configured", got.MissingIdentity)
	}
	if !strings.Contains(got.Text, "— The Grove") {
		t.Errorf("text = %q, want the alias signature applied", got.Text)
	}
}

// With no matching identity, the account default is used, and an unconfigured
// delivered-to address is reported so the screen can offer to add it.
func TestReplyFallsBackAndReportsMissingIdentity(t *testing.T) {
	t.Parallel()

	orig := incoming()
	orig.DeliveredTo = []Address{addr("catchall@example.test")}
	got := Reply(orig, []IdentityRef{aliceIdentity}, aliceIdentity, false)
	if got.From.Address != "hello@example.test" {
		t.Fatalf("from = %q, want the account default", got.From.Address)
	}
	if got.MissingIdentity != "catchall@example.test" {
		t.Errorf("missing = %q, want the unconfigured delivered-to address", got.MissingIdentity)
	}
}

// Reply-all keeps the original To and Cc, minus every one of the operator's own
// addresses and anyone already in To, de-duplicated case-insensitively.
func TestReplyAllKeepsTheOthers(t *testing.T) {
	t.Parallel()

	got := Reply(incoming(), []IdentityRef{aliceIdentity}, aliceIdentity, true)
	if len(got.To) != 1 || got.To[0].Address != "mara@example.com" {
		t.Fatalf("to = %v, want only the sender", addresses(got.To))
	}
	if len(got.Cc) != 1 || got.Cc[0].Address != "friend@example.com" {
		t.Fatalf("cc = %v, want the other Cc and not hello@", addresses(got.Cc))
	}
}

// A subject already carrying a reply prefix is not prefixed again.
func TestReplySubjectDoesNotDoublePrefix(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Moving my blog over": "Re: Moving my blog over",
		"Re: Already":         "Re: Already",
		"RE[3]: Threaded":     "RE[3]: Threaded",
		"re: lowercase":       "re: lowercase",
		"":                    "",
	}
	for in, want := range cases {
		orig := incoming()
		orig.Subject = in
		if got := Reply(orig, nil, aliceIdentity, false).Subject; got != want {
			t.Errorf("reply subject %q = %q, want %q", in, got, want)
		}
	}
}

// References gains the original Message-ID, keeping the nearest ancestors when
// the chain is already at the maximum the builder allows.
func TestReplyReferencesAppendAndBound(t *testing.T) {
	t.Parallel()

	orig := incoming()
	got := Reply(orig, nil, aliceIdentity, false)
	if got.InReplyTo != "<mara-1@example.com>" {
		t.Errorf("in-reply-to = %q, want the original Message-ID", got.InReplyTo)
	}
	if len(got.References) != 2 || got.References[1] != "<mara-1@example.com>" {
		t.Fatalf("references = %v, want the original chain plus the Message-ID", got.References)
	}

	deep := incoming()
	deep.References = make([]string, MaxReferences)
	for i := range deep.References {
		deep.References[i] = "<r" + string(rune('a'+i)) + "@example.com>"
	}
	bounded := Reply(deep, nil, aliceIdentity, false)
	if len(bounded.References) != MaxReferences {
		t.Fatalf("references = %d, want the maximum %d", len(bounded.References), MaxReferences)
	}
	// The newest ancestor and the new Message-ID are the last two.
	if bounded.References[MaxReferences-1] != "<mara-1@example.com>" {
		t.Errorf("last reference = %q, want the new Message-ID", bounded.References[MaxReferences-1])
	}
	if bounded.References[0] == deep.References[0] {
		t.Errorf("references kept the oldest id, want the chain trimmed from the head")
	}
}

// Odd and hostile header sets are handled with a defined outcome, never a
// panic: several From mailboxes use the first, and an empty From/Reply-To leaves
// no recipient.
func TestReplyHandlesOddHeaders(t *testing.T) {
	t.Parallel()

	twoFroms := incoming()
	twoFroms.From = []Address{addr("first@example.com"), addr("second@example.com")}
	if got := Reply(twoFroms, nil, aliceIdentity, false); len(got.To) != 1 || got.To[0].Address != "first@example.com" {
		t.Errorf("two Froms = %v, want the first mailbox", addresses(got.To))
	}

	noSender := incoming()
	noSender.From, noSender.ReplyTo = nil, nil
	if got := Reply(noSender, nil, aliceIdentity, false); len(got.To) != 0 {
		t.Errorf("no sender = %v, want no recipient", addresses(got.To))
	}

	emptyAddresses := incoming()
	emptyAddresses.From = []Address{addr("")}
	emptyAddresses.To = []Address{addr("")}
	emptyAddresses.Cc = nil
	if got := Reply(emptyAddresses, nil, aliceIdentity, true); len(got.To) != 0 || len(got.Cc) != 0 {
		t.Errorf("empty addresses = to %v cc %v, want both empty", addresses(got.To), addresses(got.Cc))
	}
}

// A forward fills an attribution block and leaves the recipients to the
// operator. It is not a reply, so no threading headers are set.
func TestForwardFillsAttributionBlock(t *testing.T) {
	t.Parallel()

	got := Forward(incoming(), []IdentityRef{aliceIdentity, aliasIdentity}, aliceIdentity)
	if got.Subject != "Fwd: Moving my blog over" {
		t.Errorf("subject = %q, want a Fwd: prefix", got.Subject)
	}
	if len(got.To) != 0 || len(got.Cc) != 0 {
		t.Errorf("to/cc = %v/%v, want both empty", addresses(got.To), addresses(got.Cc))
	}
	if got.InReplyTo != "" || len(got.References) != 0 {
		t.Errorf("threading = %q/%v, want a forward to start a new thread", got.InReplyTo, got.References)
	}
	for _, want := range []string{
		"---------- Forwarded message ----------",
		"From: Mara <mara@example.com>",
		"Subject: Moving my blog over",
		"To: hello@example.test",
		"Hi Autumn,",
	} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("forward text missing %q:\n%s", want, got.Text)
		}
	}
	if !strings.Contains(got.Text, "Tue, 6 Oct 2026 09:12 +0000") {
		t.Errorf("forward text missing the date:\n%s", got.Text)
	}
}

func TestForwardSubjectDoesNotDoublePrefix(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Moving my blog over": "Fwd: Moving my blog over",
		"Fwd: Already":        "Fwd: Already",
		"FW: Shouty":          "FW: Shouty",
		"":                    "",
	}
	for in, want := range cases {
		orig := incoming()
		orig.Subject = in
		if got := Forward(orig, nil, aliceIdentity).Subject; got != want {
			t.Errorf("forward subject %q = %q, want %q", in, got, want)
		}
	}
}

// The forward body quotes the original sender, not the reply target, and a
// delivered alias still selects the From.
func TestForwardSendsAsTheDeliveredIdentity(t *testing.T) {
	t.Parallel()

	orig := incoming()
	orig.DeliveredTo = []Address{addr("feedback@example.test")}
	got := Forward(orig, []IdentityRef{aliceIdentity, aliasIdentity}, aliceIdentity)
	if got.From.Address != "feedback@example.test" {
		t.Errorf("from = %q, want the delivered alias", got.From.Address)
	}
	if got.ReplyTarget != "" {
		t.Errorf("reply target = %q, want none on a forward", got.ReplyTarget)
	}
}

// A signature is appended behind the standard `-- ` line; an empty one changes
// nothing, and an empty body is just the signature.
func TestAppendSignature(t *testing.T) {
	t.Parallel()

	if got := AppendSignature("body", ""); got != "body" {
		t.Errorf("empty signature = %q, want the body unchanged", got)
	}
	if got := AppendSignature("body", "   \n "); got != "body" {
		t.Errorf("blank signature = %q, want the body unchanged", got)
	}
	if got := AppendSignature("Hi there", "— Autumn"); got != "Hi there\n\n-- \n— Autumn" {
		t.Errorf("signature = %q, want the separator form", got)
	}
	if got := AppendSignature("", "— Autumn"); got != "-- \n— Autumn" {
		t.Errorf("signature on empty body = %q", got)
	}
	if got := AppendSignature("body\n\n", "sig"); got != "body\n\n-- \nsig" {
		t.Errorf("trailing newlines = %q, want them collapsed before the separator", got)
	}
}

// When Reply-To redirects the reply (a list, a contact form), reply-all must
// still reach the person who wrote the message.
func TestReplyAllKeepsTheOriginalSenderWhenReplyToRedirects(t *testing.T) {
	t.Parallel()
	in := incoming()
	in.ReplyTo = []Address{addr("list@example.com")}
	got := Reply(in, []IdentityRef{aliceIdentity}, aliceIdentity, true)
	if len(got.To) != 1 || got.To[0].Address != "list@example.com" {
		t.Fatalf("to = %v, want the Reply-To", addresses(got.To))
	}
	found := false
	for _, a := range got.Cc {
		found = found || a.Address == "mara@example.com"
	}
	if !found {
		t.Errorf("cc = %v, want the original sender kept", addresses(got.Cc))
	}
	// A direct reply is still only the Reply-To.
	if direct := Reply(in, []IdentityRef{aliceIdentity}, aliceIdentity, false); len(direct.Cc) != 0 {
		t.Errorf("direct cc = %v, want none", addresses(direct.Cc))
	}
}

// Replying to a message the operator sent goes to its recipients, not back to
// the operator.
func TestReplyToOwnMessageGoesToItsRecipients(t *testing.T) {
	t.Parallel()
	in := Incoming{
		From:      []Address{named("Autumn", "hello@example.test")},
		To:        []Address{addr("mara@example.com"), addr("friend@example.com")},
		Subject:   "Moving my blog over",
		MessageID: "<mine-1@example.test>",
	}
	ids := []IdentityRef{aliceIdentity, aliasIdentity}
	got := Reply(in, ids, aliceIdentity, false)
	if len(got.To) != 1 || got.To[0].Address != "mara@example.com" {
		t.Fatalf("to = %v, want the original first recipient", addresses(got.To))
	}
	all := Reply(in, ids, aliceIdentity, true)
	if len(all.To) != 1 || all.To[0].Address != "mara@example.com" ||
		len(all.Cc) != 1 || all.Cc[0].Address != "friend@example.com" {
		t.Fatalf("reply-all to=%v cc=%v, want mara to and friend cc", addresses(all.To), addresses(all.Cc))
	}
	if got.From.Address != "hello@example.test" {
		t.Errorf("from = %q, want the identity that wrote it", got.From.Address)
	}
}
