package thread

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var base = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// msg builds an input message; the date is the base plus i minutes so ordering
// is obvious in a test.
func msg(i int, id, messageID, subject, refs, inReplyTo string) Message {
	return Message{
		ID:         id,
		Key:        "key-" + id,
		MessageID:  messageID,
		References: refs,
		InReplyTo:  inReplyTo,
		Subject:    subject,
		Date:       base.Add(time.Duration(i) * time.Minute),
	}
}

// threadOf returns the thread id holding messageID.
func threadOf(threads []Thread, messageID string) (string, bool) {
	for _, t := range threads {
		if slices.Contains(t.MessageIDs, messageID) {
			return t.ID, true
		}
	}
	return "", false
}

// assertPartition fails unless every input id appears in exactly one thread and
// no thread is empty.
func assertPartition(t *testing.T, threads []Thread, want []string) {
	t.Helper()
	seen := map[string]int{}
	for _, th := range threads {
		if len(th.MessageIDs) == 0 {
			t.Errorf("thread %q has no messages", th.ID)
		}
		if th.LastDate.IsZero() {
			t.Errorf("thread %q has a zero LastDate", th.ID)
		}
		for _, id := range th.MessageIDs {
			seen[id]++
		}
	}
	for _, id := range want {
		if seen[id] != 1 {
			t.Errorf("message %s appears %d times, want exactly once", id, seen[id])
		}
	}
	if len(seen) != len(want) {
		t.Errorf("partition has %d messages, want %d", len(seen), len(want))
	}
}

func TestBuildReferencesChain(t *testing.T) {
	t.Parallel()
	// a <- b <- c, linked only by References (the classic reply chain).
	msgs := []Message{
		msg(0, "a", "<a@x>", "Topic", "", ""),
		msg(1, "b", "<b@x>", "Re: Topic", "<a@x>", "<a@x>"),
		msg(2, "c", "<c@x>", "Re: Topic", "<a@x> <b@x>", "<b@x>"),
	}
	threads := Build(msgs)
	assertPartition(t, threads, []string{"a", "b", "c"})
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}
	if got := threads[0].MessageIDs; !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("members = %v, want [a b c] oldest first", got)
	}
	if threads[0].RootMessageID != "<a@x>" {
		t.Errorf("RootMessageID = %q, want <a@x>", threads[0].RootMessageID)
	}
	if threads[0].SubjectNorm != "Topic" {
		t.Errorf("SubjectNorm = %q, want Topic", threads[0].SubjectNorm)
	}
	if threads[0].ID != "key-a" {
		t.Errorf("thread id = %q, want the root's key", threads[0].ID)
	}
}

func TestBuildInReplyToOnly(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		msg(0, "a", "<a@x>", "Topic", "", ""),
		msg(1, "b", "<b@x>", "Re: Topic", "", "<a@x>"),
	}
	threads := Build(msgs)
	assertPartition(t, threads, []string{"a", "b"})
	if len(threads) != 1 || len(threads[0].MessageIDs) != 2 {
		t.Fatalf("threads = %+v, want one thread of two", threads)
	}
}

// TestBuildSubjectFallback groups messages that carry no threading headers at
// all, the case JWZ's subject grouping exists for.
func TestBuildSubjectFallback(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		msg(0, "a", "<a@x>", "Lunch?", "", ""),
		msg(1, "b", "<b@x>", "Re: Lunch?", "", ""),
		msg(2, "c", "<c@x>", "RE[4]: Re: Lunch?", "", ""),
		msg(3, "d", "<d@x>", "Unrelated", "", ""),
	}
	threads := Build(msgs)
	assertPartition(t, threads, []string{"a", "b", "c", "d"})
	if len(threads) != 2 {
		t.Fatalf("threads = %d, want 2 (Lunch and Unrelated)", len(threads))
	}
	lunch, ok := threadOf(threads, "a")
	if !ok {
		t.Fatal("a not threaded")
	}
	for _, id := range []string{"b", "c"} {
		got, _ := threadOf(threads, id)
		if got != lunch {
			t.Errorf("%s is in thread %q, want %q (same normalized subject)", id, got, lunch)
		}
	}
	other, _ := threadOf(threads, "d")
	if other == lunch {
		t.Errorf("Unrelated shares a thread with Lunch?")
	}
}

// TestBuildEmptySubjectNotGrouped keeps the JWZ rule: a subject that normalizes
// to "" gives up on grouping, so two blank-subject messages are not merged.
func TestBuildEmptySubjectNotGrouped(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		msg(0, "a", "<a@x>", "", "", ""),
		msg(1, "b", "<b@x>", "Re:", "", ""),
	}
	threads := Build(msgs)
	assertPartition(t, threads, []string{"a", "b"})
	if len(threads) != 2 {
		t.Errorf("threads = %d, want 2 (empty subjects are never grouped)", len(threads))
	}
}

// TestBuildReplyIsChildOfBase checks the JWZ preference: the non-Re: message is
// the root even when it arrives later.
func TestBuildReplyIsChildOfBase(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		msg(0, "reply", "<r@x>", "Re: Report", "", ""),
		msg(1, "base", "<b@x>", "Report", "", ""),
	}
	threads := Build(msgs)
	assertPartition(t, threads, []string{"reply", "base"})
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}
	if threads[0].RootMessageID != "<b@x>" {
		t.Errorf("RootMessageID = %q, want the non-Re: message <b@x>", threads[0].RootMessageID)
	}
	if got := threads[0].MessageIDs; !reflect.DeepEqual(got, []string{"reply", "base"}) {
		t.Errorf("members = %v, want chronological [reply base]", got)
	}
}

// TestBuildMissingParentKeepsThread covers replies whose parent is not in the
// mirror: with two children the placeholder stands as the root, so a later
// arrival of the parent reuses the same thread identity.
func TestBuildMissingParentKeepsThread(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		msg(0, "b", "<b@x>", "Re: Topic", "<gone@x>", "<gone@x>"),
		msg(1, "c", "<c@x>", "Re: Topic", "<gone@x>", "<gone@x>"),
	}
	threads := Build(msgs)
	assertPartition(t, threads, []string{"b", "c"})
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}
	if threads[0].RootMessageID != "<gone@x>" {
		t.Errorf("RootMessageID = %q, want the placeholder <gone@x>", threads[0].RootMessageID)
	}
	if got := threads[0].MessageIDs; !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("members = %v, want [b c]", got)
	}
}

// TestBuildSingleChildPlaceholderPromotes is the JWZ step 4.2 rule: a lone
// reply to a message we do not hold becomes the root itself.
func TestBuildSingleChildPlaceholderPromotes(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		msg(0, "b", "<b@x>", "Re: Topic", "<gone@x>", "<gone@x>"),
	}
	threads := Build(msgs)
	assertPartition(t, threads, []string{"b"})
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}
	if threads[0].RootMessageID != "<b@x>" {
		t.Errorf("RootMessageID = %q, want the promoted <b@x>", threads[0].RootMessageID)
	}
	if threads[0].ID != "key-b" {
		t.Errorf("thread id = %q, want the only real message's key", threads[0].ID)
	}
}

// TestBuildDuplicateMessageID keeps the every-message-once invariant when a
// hostile sender reuses a Message-ID (N8, papercuts.md).
func TestBuildDuplicateMessageID(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		msg(0, "a", "<dup@x>", "One", "", ""),
		msg(1, "b", "<dup@x>", "One", "", ""),
		msg(2, "c", "<c@x>", "Re: One", "<dup@x>", "<dup@x>"),
	}
	threads := Build(msgs)
	assertPartition(t, threads, []string{"a", "b", "c"})
	if len(threads) != 1 {
		t.Errorf("threads = %d, want 1 (the reply attaches to the shared id)", len(threads))
	}
}

// TestBuildDeterministicUnderShuffle is the property the mirror relies on: the
// same set of messages always produces the same partition, whatever order the
// rows came back in.
func TestBuildDeterministicUnderShuffle(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		msg(0, "a", "<a@x>", "Root", "", ""),
		msg(1, "b", "<b@x>", "Re: Root", "<a@x>", "<a@x>"),
		msg(2, "c", "<c@x>", "Re: Root", "<a@x>", "<a@x>"),
		msg(3, "d", "<d@x>", "Other", "", ""),
		msg(4, "e", "<e@x>", "Re: Other", "", ""),
		msg(5, "f", "<f@x>", "Re: Re: Other", "", ""),
	}

	normalize := func(threads []Thread) map[string]string {
		out := map[string]string{}
		for _, th := range threads {
			for _, id := range th.MessageIDs {
				out[id] = th.ID + "|" + th.RootMessageID + "|" + th.SubjectNorm
			}
		}
		return out
	}

	want := normalize(Build(msgs))
	if len(want) != len(msgs) {
		t.Fatalf("built %d messages, want %d", len(want), len(msgs))
	}

	rng := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic test shuffle
	for i := 0; i < 50; i++ {
		shuffled := slices.Clone(msgs)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := normalize(Build(shuffled)); !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: grouping changed under shuffle:\n got %v\nwant %v", i, got, want)
		}
	}
}

// TestBuildDoesNotGroupForwards documents the deliberate JWZ rule: Fwd: is a
// different subject, not a reply, so it is never merged by subject.
func TestBuildDoesNotGroupForwards(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		msg(0, "a", "<a@x>", "Report", "", ""),
		msg(1, "b", "<b@x>", "Fwd: Report", "", ""),
	}
	threads := Build(msgs)
	assertPartition(t, threads, []string{"a", "b"})
	if len(threads) != 2 {
		t.Errorf("threads = %d, want 2 (a forward is not a reply)", len(threads))
	}
}

// TestBuildDeepChainIsBounded runs the recursion-prone paths (prune, collect,
// sort) over a hostile-length References header. It asserts the call returns
// and the partition holds; STANDARDS.md 4a wants hostile input to cost bounded
// time and memory.
func TestBuildDeepChainIsBounded(t *testing.T) {
	t.Parallel()
	const depth = 20000
	refs := ""
	for i := 0; i < depth; i++ {
		refs += "<gone" + strconv.Itoa(i) + "@x> "
	}
	msgs := []Message{msg(0, "leaf", "<leaf@x>", "Re: Deep", refs, "")}
	threads := Build(msgs)
	assertPartition(t, threads, []string{"leaf"})
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}
}

type fixtureMessage struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	MessageID  string `json:"message_id"`
	References string `json:"references"`
	InReplyTo  string `json:"in_reply_to"`
	Subject    string `json:"subject"`
	Date       string `json:"date"`
}

type fixtureThread struct {
	ID            string   `json:"id"`
	RootMessageID string   `json:"root_message_id"`
	SubjectNorm   string   `json:"subject_norm"`
	Members       []string `json:"members"`
}

type fixtureFile struct {
	Name     string           `json:"name"`
	Messages []fixtureMessage `json:"messages"`
	Want     []fixtureThread  `json:"want"`
}

// TestBuildFixtures checks hand-built real-world shapes (reply chains,
// subject fallback, a missing parent) against testdata/*.json.
func TestBuildFixtures(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("testdata/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("fixture glob: %v (%d files)", err, len(files))
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			var fx fixtureFile
			if err := json.Unmarshal(data, &fx); err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			msgs := make([]Message, len(fx.Messages))
			for i, m := range fx.Messages {
				d, err := time.Parse(time.RFC3339, m.Date)
				if err != nil {
					t.Fatalf("%s: %v", m.ID, err)
				}
				msgs[i] = Message{ID: m.ID, Key: m.Key, MessageID: m.MessageID, References: m.References, InReplyTo: m.InReplyTo, Subject: m.Subject, Date: d}
			}
			got := map[string]Thread{}
			for _, th := range Build(msgs) {
				got[th.ID] = th
			}
			if len(got) != len(fx.Want) {
				t.Fatalf("%s: %d threads, want %d", fx.Name, len(got), len(fx.Want))
			}
			for _, w := range fx.Want {
				th, ok := got[w.ID]
				if !ok {
					t.Errorf("%s: thread %q missing", fx.Name, w.ID)
					continue
				}
				if th.RootMessageID != w.RootMessageID {
					t.Errorf("%s: %s root = %q, want %q", fx.Name, w.ID, th.RootMessageID, w.RootMessageID)
				}
				if th.SubjectNorm != w.SubjectNorm {
					t.Errorf("%s: %s subject = %q, want %q", fx.Name, w.ID, th.SubjectNorm, w.SubjectNorm)
				}
				if !reflect.DeepEqual(th.MessageIDs, w.Members) {
					t.Errorf("%s: %s members = %v, want %v", fx.Name, w.ID, th.MessageIDs, w.Members)
				}
			}
		})
	}
}

// TestBuildRandomGraphs is a property test over generated reference graphs: no
// message is ever lost, duplicated or put in two threads, and Build always
// terminates.
func TestBuildRandomGraphs(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // deterministic test generator
	for i := 0; i < 200; i++ {
		n := 1 + rng.Intn(12)
		msgs := make([]Message, n)
		var ids []string
		for j := 0; j < n; j++ {
			id := "m" + string(rune('a'+j))
			ids = append(ids, id)
			msgs[j] = msg(j, id, "<"+id+"@x>", "S"+string(rune('a'+rng.Intn(3))), "", "")
		}
		// Wire each message to an earlier one by References/In-Reply-To; the
		// earlier message may or may not exist, exercising placeholders.
		for j := 1; j < n; j++ {
			if rng.Intn(3) == 0 {
				continue
			}
			target := "m" + string(rune('a'+rng.Intn(j)))
			if rng.Intn(4) == 0 {
				target = "ghost" + target
			}
			msgs[j].References = "<" + target + "@x>"
			if rng.Intn(2) == 0 {
				msgs[j].InReplyTo = "<" + target + "@x>"
			}
		}
		threads := Build(msgs)
		assertPartition(t, threads, ids)
	}
}

// referencesWithJunk builds <r> <q> <junk...> <p>: the root first, a real but
// unrelated message in the middle and the real parent last.
func referencesWithJunk(junk int) string {
	var b strings.Builder
	b.WriteString("<r@x> <q@x>")
	for i := 0; i < junk; i++ {
		b.WriteString(" <junk" + strconv.Itoa(i) + "@x>")
	}
	b.WriteString(" <p@x>")
	return b.String()
}

func referenceCapMessages(junk int) []Message {
	return []Message{
		msg(0, "r", "<r@x>", "Plan", "", ""),
		msg(1, "q", "<q@x>", "Something unrelated", "", ""),
		msg(2, "p", "<p@x>", "Re: Plan", "<r@x>", "<r@x>"),
		msg(3, "m", "<m@x>", "Re: Plan", referencesWithJunk(junk), "<p@x>"),
	}
}

// A References header can be 1 MiB, so one message can name ~250k ids and cost
// linear time per message on every rethread (N13). Only the first id (the
// conversation's root) and the last MaxReferences (its nearest ancestors) are
// honoured, so an id in the middle of a long chain no longer links.
func TestBuildHonoursOnlyTheFirstAndLastReferences(t *testing.T) {
	t.Parallel()
	threads := Build(referenceCapMessages(MaxReferences + 200))
	assertPartition(t, threads, []string{"r", "q", "p", "m"})

	root, _ := threadOf(threads, "r")
	for _, id := range []string{"p", "m"} {
		if got, _ := threadOf(threads, id); got != root {
			t.Errorf("%s is in thread %q, want the root's %q (the first and the last ids are kept)", id, got, root)
		}
	}
	if got, _ := threadOf(threads, "q"); got == root {
		t.Error("q, named only in the dropped middle of the header, was still linked into the conversation")
	}
}

// Ordinary mail, far below the cap, threads exactly as before.
func TestBuildKeepsEveryReferenceWithinTheCap(t *testing.T) {
	t.Parallel()
	threads := Build(referenceCapMessages(10))
	assertPartition(t, threads, []string{"r", "q", "p", "m"})
	root, _ := threadOf(threads, "r")
	if got, _ := threadOf(threads, "q"); got != root {
		t.Errorf("q is in thread %q, want the root's %q: every id within the cap is honoured", got, root)
	}
}

// The boundary itself: the first id plus exactly MaxReferences more is kept whole.
func TestBuildKeepsTheCapBoundaryExactly(t *testing.T) {
	t.Parallel()
	// <r> <q> <junk x (MaxReferences-2)> <p> is MaxReferences+1 ids: all honoured.
	threads := Build(referenceCapMessages(MaxReferences - 2))
	root, _ := threadOf(threads, "r")
	if got, _ := threadOf(threads, "q"); got != root {
		t.Errorf("q dropped at %d ids; the first id plus %d more must all be honoured", MaxReferences+1, MaxReferences)
	}
	// One more id pushes q (second) out of the last MaxReferences.
	threads = Build(referenceCapMessages(MaxReferences - 1))
	root, _ = threadOf(threads, "r")
	if got, _ := threadOf(threads, "q"); got == root {
		t.Error("q survived with one id over the cap")
	}
}

// scanMessageIDs replaces a regexp scan that cost ~75 ms per 1.4 MB header
// (N13): it must find exactly the ids the regexp did, on any input.
func TestScanMessageIDsMatchesTheRegexp(t *testing.T) {
	t.Parallel()
	fixed := []string{
		"", "<a@x>", "<a@x> <b@x>", "<a@x><b@x>", "<>", "<> <a@x>", "<<a@x>", "<a@x>>", "<a <b@x>",
		"<a@x", "a@x>", "junk <a@x> junk <b@x> junk", "<a@x>\r\n <b@x>", "<><><a@x>", "< >", "<<>>",
	}
	for _, s := range fixed {
		if got, want := scanMessageIDs(s, 1<<30), messageIDRe.FindAllString(s, -1); !slices.Equal(got, want) {
			t.Errorf("scan(%q) = %q, want %q", s, got, want)
		}
	}
	rng := rand.New(rand.NewSource(13))
	alphabet := []string{"<", ">", "a", "b@x", " ", "\r\n", "<a@x>", "<"}
	for i := 0; i < 2000; i++ {
		var b strings.Builder
		for n := rng.Intn(30); n > 0; n-- {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		s := b.String()
		if got, want := scanMessageIDs(s, 1<<30), messageIDRe.FindAllString(s, -1); !slices.Equal(got, want) {
			t.Fatalf("scan(%q) = %q, want %q", s, got, want)
		}
	}
}

// The first id and the last keepLast, in order, whatever the length.
func TestScanMessageIDsKeepsTheFirstAndTheLast(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString("<" + strconv.Itoa(i) + "@x> ")
	}
	got := scanMessageIDs(b.String(), 3)
	if want := []string{"<0@x>", "<997@x>", "<998@x>", "<999@x>"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := scanMessageIDs("<a@x> <b@x> <c@x>", 3); len(got) != 3 {
		t.Errorf("a header at the cap lost ids: %q", got)
	}
}

// What makes it safe against a 1 MiB header is that the work and the memory do
// not grow with the number of ids: asserted on allocations, which do not flake
// the way a timing would.
func TestScanMessageIDsDoesNotMaterialiseEveryID(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200_000; i++ {
		b.WriteString("<" + strconv.Itoa(i) + "@x>")
	}
	header := b.String()
	if allocs := testing.AllocsPerRun(3, func() { _ = scanMessageIDs(header, MaxReferences) }); allocs > 5 {
		t.Errorf("scanning 200k ids made %.0f allocations, want a handful independent of the count", allocs)
	}
}
