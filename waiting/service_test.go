package waiting

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/jev"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

const acct = "a1"

var seedNow = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

type rig struct {
	t    *testing.T
	w    *mailworld.World
	dbs  *store.DBs
	gate *llm.Gate
	svc  *Service
	uid  uint32
}

func newRig(t *testing.T, on bool) *rig {
	t.Helper()
	ctx := context.Background()
	w, err := mailworld.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	dbs, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	if err := dbs.UpsertAccount(ctx, store.Account{ID: acct, Address: "me@example.test"}); err != nil {
		t.Fatal(err)
	}
	for id, role := range map[string]string{"f-inbox": store.RoleInbox, "f-sent": store.RoleSent} {
		if err := dbs.UpsertFolder(ctx, store.Folder{ID: id, AccountID: acct, Name: id, Role: role}); err != nil {
			t.Fatal(err)
		}
	}
	builtin, err := jev.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	reg := jev.NewRegistry()
	if err := reg.Set(builtin, nil); err != nil {
		t.Fatal(err)
	}
	gate := llm.NewGate(dbs,
		llm.WithProviders(llm.ProviderConfig{OpenRouterBase: w.OpenRouterURL(), APIKey: "k"}),
		llm.WithAccountPolicy(llm.NewAccountPolicy(dbs, map[string]llm.AccountSettings{acct: {Smart: true}})),
		llm.WithDefaultCaps(5, 10),
	)
	if on {
		if err := gate.SetFeatureEnabled(ctx, acct, Feature, true); err != nil {
			t.Fatal(err)
		}
	}
	r := &rig{t: t, w: w, dbs: dbs, gate: gate}
	r.svc = NewService(jev.NewEngine(reg, gate, dbs), gate, dbs)
	r.svc.now = func() time.Time { return seedNow }
	return r
}

// put stores one message, days before seedNow, in the folder.
func (r *rig) put(id, thread, folder string, daysAgo float64, subject string) {
	r.t.Helper()
	r.uid++
	at := seedNow.Add(-time.Duration(daysAgo * 24 * float64(time.Hour)))
	m := store.Message{
		ID: id, AccountID: acct, FolderID: "f-" + folder, UID: r.uid, ContentKey: "ck-" + id, ThreadID: thread,
		Subject: subject, BodyText: "Could you send the figures by Friday?", Date: at, InternalDate: at,
		From: store.Address{Address: "me@example.test"},
		To:   []store.Address{{Name: "Sam", Address: "sam@example.com"}},
	}
	if err := r.dbs.UpsertMessage(context.Background(), m); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) list() []Item {
	r.t.Helper()
	items, err := r.svc.List(context.Background(), acct)
	if err != nil {
		r.t.Fatal(err)
	}
	return items
}

func (r *rig) jevCalls() int {
	n := 0
	for _, c := range r.w.Calls() {
		if c.Endpoint == "systemone" {
			n++
		}
	}
	return n
}

func yes() map[string]mailworld.JevAnswer {
	return map[string]mailworld.JevAnswer{"expects_reply": {Choice: "yes", Probabilities: map[string]float64{"yes": 0.95, "no": 0.05}, Confidence: 0.95}}
}

func no() map[string]mailworld.JevAnswer {
	return map[string]mailworld.JevAnswer{"expects_reply": {Choice: "no", Probabilities: map[string]float64{"yes": 0.05, "no": 0.95}, Confidence: 0.95}}
}

func ids(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.MessageID
	}
	return out
}

func TestWithTheSwitchOffNothingIsAskedAndNothingIsListed(t *testing.T) {
	r := newRig(t, false)
	r.put("s1", "t1", "sent", 5, "Figures")
	if got := r.list(); len(got) != 0 {
		t.Fatalf("listed %v with Waiting on off", ids(got))
	}
	if n := r.jevCalls(); n != 0 {
		t.Fatalf("%d provider calls with Waiting on off", n)
	}
}

func TestOnlyASentMessageThatExpectsAnAnswerIsListedAndTheAnswerIsKept(t *testing.T) {
	r := newRig(t, true)
	r.put("s-wait", "t1", "sent", 5, "Figures")
	r.put("s-thanks", "t2", "sent", 4, "Re: Dinner")
	// Longest wait is asked first, so the queue lines up with the days above.
	r.w.QueueJev(yes())
	r.w.QueueJev(no())

	got := r.list()
	if !equal(ids(got), []string{"s-wait"}) {
		t.Fatalf("listed %v, want only s-wait", ids(got))
	}
	it := got[0]
	if it.Subject != "Figures" || it.AccountID != acct || it.Days != 5 || len(it.To) != 1 || it.To[0].Address != "sam@example.com" {
		t.Errorf("item = %+v", it)
	}
	if r.jevCalls() != 2 {
		t.Fatalf("%d provider calls, want one per sent message", r.jevCalls())
	}
	// Listing again costs nothing: both answers are kept.
	if again := r.list(); !equal(ids(again), []string{"s-wait"}) || r.jevCalls() != 2 {
		t.Fatalf("second list = %v after %d calls", ids(again), r.jevCalls())
	}
}

func TestTheDeterministicRulesDecideWhoIsAskedAtAll(t *testing.T) {
	r := newRig(t, true)
	r.put("s-answered", "t1", "sent", 6, "Answered")
	r.put("reply", "t1", "inbox", 5, "Re: Answered")
	r.put("s-recent", "t2", "sent", 1, "Yesterday")
	r.put("s-stopped", "t3", "sent", 7, "Stopped")
	if err := r.dbs.StopWaiting(context.Background(), acct, "ck-s-stopped", seedNow); err != nil {
		t.Fatal(err)
	}
	if got := r.list(); len(got) != 0 {
		t.Fatalf("listed %v", ids(got))
	}
	if n := r.jevCalls(); n != 0 {
		t.Fatalf("%d paid calls for threads that were answered, too recent or stopped", n)
	}
}

func TestTheDaysSettingMovesTheLine(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, true)
	r.put("s1", "t1", "sent", 2, "Two days")
	r.w.QueueJev(yes())
	if got := r.list(); len(got) != 0 {
		t.Fatalf("listed %v inside the default three days", ids(got))
	}
	if err := r.dbs.SetSetting(ctx, acct, SettingDays, "1"); err != nil {
		t.Fatal(err)
	}
	if got := r.list(); !equal(ids(got), []string{"s1"}) {
		t.Fatalf("listed %v with the line at one day", ids(got))
	}
	// A nonsense value falls back to the default rather than listing everything.
	for _, bad := range []string{"0", "-4", "many", "9999"} {
		if err := r.dbs.SetSetting(ctx, acct, SettingDays, bad); err != nil {
			t.Fatal(err)
		}
		if got := r.list(); len(got) != 0 {
			t.Errorf("setting %q listed %v: want the default of three days", bad, ids(got))
		}
	}
}

// One open spends at most MaxAskPerList calls, so a big backlog is worked through over
// several opens instead of in one surprise.
func TestOneOpenAsksAtMostAFewAndTheRestWaitForTheNext(t *testing.T) {
	r := newRig(t, true)
	total := MaxAskPerList + 3
	for i := 0; i < total; i++ {
		r.put(fmt.Sprintf("s%02d", i), fmt.Sprintf("t%02d", i), "sent", 20-float64(i)/10, "Figures")
		r.w.QueueJev(yes())
	}
	first := r.list()
	if r.jevCalls() != MaxAskPerList || len(first) != MaxAskPerList {
		t.Fatalf("first open: %d calls, %d listed; want %d each", r.jevCalls(), len(first), MaxAskPerList)
	}
	second := r.list()
	if r.jevCalls() != total || len(second) != total {
		t.Fatalf("second open: %d calls, %d listed; want %d each", r.jevCalls(), len(second), total)
	}
}

// A provider that is down is the open's problem to hide, not the operator's: the list is
// whatever is already known, no error is shown, and the message is asked again next time.
func TestAProviderFailureLeavesTheKnownListAndIsRetried(t *testing.T) {
	r := newRig(t, true)
	r.put("s1", "t1", "sent", 5, "Figures")
	r.w.Fault(mailworld.LLMDown{})
	if got := r.list(); len(got) != 0 {
		t.Fatalf("listed %v while the provider was down", ids(got))
	}
	r.w.ClearFaults()
	r.w.QueueJev(yes())
	if got := r.list(); !equal(ids(got), []string{"s1"}) {
		t.Fatalf("after recovery listed %v", ids(got))
	}
}

func TestStoppingTheWaitRemovesItWithoutAskingAgain(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, true)
	r.put("s1", "t1", "sent", 5, "Figures")
	r.w.QueueJev(yes())
	if got := r.list(); len(got) != 1 {
		t.Fatalf("listed %v", ids(got))
	}
	if err := r.svc.Stop(ctx, acct, "s1"); err != nil {
		t.Fatal(err)
	}
	if got := r.list(); len(got) != 0 {
		t.Fatalf("still listed %v after Stop", ids(got))
	}
	// A later message in the same thread is a new wait.
	r.put("s2", "t1", "sent", 4, "Figures, again")
	r.w.QueueJev(yes())
	if got := r.list(); !equal(ids(got), []string{"s2"}) {
		t.Fatalf("listed %v, want the new message", ids(got))
	}
	if err := r.svc.Stop(ctx, acct, "no-such-message"); err == nil {
		t.Error("Stop accepted a message that does not exist")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
