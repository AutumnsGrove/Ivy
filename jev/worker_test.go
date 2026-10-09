package jev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

var epoch = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

type workerRig struct {
	*rig
	clock   time.Time
	worker  *Worker
	nextUID uint32
	folders map[string]bool
}

func newWorkerRig(t *testing.T, opts WorkerOptions) *workerRig {
	t.Helper()
	r := newRig(t)
	w := &workerRig{rig: r, clock: epoch, folders: map[string]bool{}}
	if err := r.dbs.UpsertAccount(context.Background(), store.Account{ID: rigAccount, Address: "me@example.test", CreatedAt: epoch}); err != nil {
		t.Fatal(err)
	}
	opts.Now = func() time.Time { return w.clock }
	w.worker = NewWorker(r.engine, r.gate, r.dbs, opts)
	return w
}

// arrive stores a message that reached the server at t, in the folder with this role.
func (w *workerRig) arrive(id, key, role string, t time.Time, subject, body string) {
	w.t.Helper()
	ctx := context.Background()
	folder := rigAccount + "-" + role
	if !w.folders[role] {
		if err := w.dbs.UpsertFolder(ctx, store.Folder{ID: folder, AccountID: rigAccount, Name: role, Role: role}); err != nil {
			w.t.Fatal(err)
		}
		w.folders[role] = true
	}
	w.nextUID++
	if err := w.dbs.UpsertMessage(ctx, store.Message{
		ID: id, AccountID: rigAccount, FolderID: folder, UID: w.nextUID, ContentKey: key,
		Subject: subject, BodyText: body, Date: t, InternalDate: t,
		From: store.Address{Name: "Ada", Address: "ada@example.com"},
	}); err != nil {
		w.t.Fatal(err)
	}
}

func (w *workerRig) tick() int {
	w.t.Helper()
	n, err := w.worker.RunOnce(context.Background())
	if err != nil {
		w.t.Fatalf("RunOnce: %v", err)
	}
	return n
}

func (w *workerRig) since() time.Time {
	w.t.Helper()
	v, ok, err := w.dbs.GetSetting(context.Background(), rigAccount, SettingSince)
	if err != nil || !ok || v == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		w.t.Fatal(err)
	}
	return ts
}

func TestOnlyMailThatArrivesAfterTurningItOnIsClassified(t *testing.T) {
	w := newWorkerRig(t, WorkerOptions{})
	w.arrive("old1", "old1", store.RoleInbox, epoch.Add(-5*24*time.Hour), "Old news", "from last week")
	w.arrive("old2", "old2", store.RoleInbox, epoch.Add(-time.Hour), "An hour ago", "before the switch")

	// First tick with the feature on: the watermark is set to now and nothing is read.
	if n := w.tick(); n != 0 || w.jev.count() != 0 {
		t.Fatalf("turning it on classified history: processed=%d calls=%d", n, w.jev.count())
	}
	if !w.since().Equal(epoch) {
		t.Fatalf("watermark = %v, want %v", w.since(), epoch)
	}

	w.clock = epoch.Add(time.Hour)
	w.arrive("new1", "new1", store.RoleInbox, epoch.Add(30*time.Minute), "Fresh", "arrived after")
	if n := w.tick(); n != 1 || w.jev.count() != 1 {
		t.Fatalf("processed=%d calls=%d, want 1 and 1", n, w.jev.count())
	}
	if len(w.decisions("new1")) != 2 || len(w.decisions("old1"))+len(w.decisions("old2")) != 0 {
		t.Fatal("the wrong messages were classified")
	}
}

func TestAConsideredMessageIsNotRescanned(t *testing.T) {
	w := newWorkerRig(t, WorkerOptions{})
	w.tick() // sets the watermark
	w.arrive("m1", "k1", store.RoleInbox, epoch.Add(time.Minute), "Hi", "text")
	w.clock = epoch.Add(time.Hour)
	if w.tick() != 1 {
		t.Fatal("first pass should classify it")
	}
	if n := w.tick(); n != 0 || w.jev.count() != 1 {
		t.Fatalf("second pass processed=%d calls=%d; a considered message is never re-read", n, w.jev.count())
	}
}

func TestEmptyMailIsConsideredOnceAndCostsNothing(t *testing.T) {
	w := newWorkerRig(t, WorkerOptions{})
	w.tick()
	w.arrive("m1", "k1", store.RoleInbox, epoch.Add(time.Minute), "", "> only a quote")
	w.clock = epoch.Add(time.Hour)
	if w.tick() != 1 || w.jev.count() != 0 {
		t.Fatalf("calls=%d; mail with nothing to read must not be sent", w.jev.count())
	}
	if w.tick() != 0 {
		t.Fatal("empty mail was reconsidered")
	}
}

func TestJunkIsOnlyAskedTheQuestionsThatNameJunk(t *testing.T) {
	w := newWorkerRig(t, WorkerOptions{})
	junk := Question{
		ID: "junk_rescue", Instructions: "Is this real mail wrongly in Junk?", Feature: "junk_rescue",
		Criteria: map[string]string{"junk": "spam", "looks_real": "a person wrote it"}, QuietOption: "junk",
		Threshold: 0.9, Folders: []string{FolderJunk},
	}
	if err := w.reg.Set(nil, []Question{junk}); err != nil {
		t.Fatal(err)
	}
	if err := w.gate.SetFeatureEnabled(context.Background(), rigAccount, "junk_rescue", true); err != nil {
		t.Fatal(err)
	}
	w.tick()
	w.arrive("j1", "kj", store.RoleJunk, epoch.Add(time.Minute), "Hello from a human", "Please reply")
	w.arrive("i1", "ki", store.RoleInbox, epoch.Add(2*time.Minute), "Inbox mail", "text")
	w.clock = epoch.Add(time.Hour)
	w.tick()
	if w.jev.count() != 1 {
		t.Fatalf("calls = %d; only the Junk message has a question to ask", w.jev.count())
	}
	if len(w.decisions("kj")) != 1 || len(w.decisions("ki")) != 0 {
		t.Fatal("the wrong folder's mail was classified")
	}
}

func TestOneBatchPerPassAndConcurrencyIsBounded(t *testing.T) {
	w := newWorkerRig(t, WorkerOptions{Batch: 6, Concurrency: 2})
	w.jev.delay = 25 * time.Millisecond
	w.tick()
	for i := 0; i < 15; i++ {
		id := fmt.Sprintf("m%02d", i)
		w.arrive(id, id, store.RoleInbox, epoch.Add(time.Duration(i+1)*time.Minute), "Subject "+id, "body "+id)
	}
	w.clock = epoch.Add(time.Hour)
	if n := w.tick(); n != 6 || w.jev.count() != 6 {
		t.Fatalf("one pass processed %d with %d calls; the batch bound is 6", n, w.jev.count())
	}
	if w.jev.maxInflight > 2 {
		t.Fatalf("%d requests were in flight at once; the bound is 2", w.jev.maxInflight)
	}
	if w.jev.maxInflight < 2 {
		t.Fatalf("only %d in flight; concurrency 2 should overlap two calls", w.jev.maxInflight)
	}
	// Newest first: the next pass takes the next six, and the queue drains.
	for w.tick() > 0 {
	}
	if w.jev.count() != 15 {
		t.Fatalf("total calls = %d, want 15", w.jev.count())
	}
}

func TestAnOutageLeavesMailQueuedForTheNextPass(t *testing.T) {
	w := newWorkerRig(t, WorkerOptions{})
	w.tick()
	w.arrive("m1", "k1", store.RoleInbox, epoch.Add(time.Minute), "Hi", "text")
	w.clock = epoch.Add(time.Hour)
	w.jev.setStatus(http.StatusServiceUnavailable)
	if _, err := w.worker.RunOnce(context.Background()); err == nil {
		t.Fatal("an outage was not reported")
	}
	w.jev.setStatus(0)
	if w.tick() != 1 || len(w.decisions("k1")) != 2 {
		t.Fatal("the message was lost during the outage")
	}
}

func TestOffMeansOffForTheWorker(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(*workerRig){
		"account not opted in": func(w *workerRig) {
			if err := w.dbs.SetAccountSmart(ctx, rigAccount, false); err != nil {
				w.t.Fatal(err)
			}
		},
		"classify switch off": func(w *workerRig) {
			if err := w.gate.SetFeatureEnabled(ctx, rigAccount, CallFeature, false); err != nil {
				w.t.Fatal(err)
			}
		},
	}
	for name, off := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorkerRig(t, WorkerOptions{})
			w.tick()
			w.arrive("m1", "k1", store.RoleInbox, epoch.Add(time.Minute), "Hi", "text")
			w.clock = epoch.Add(time.Hour)
			off(w)
			if n, err := w.worker.RunOnce(ctx); err != nil || n != 0 {
				t.Fatalf("processed=%d err=%v; an off account is skipped quietly", n, err)
			}
			if w.jev.count() != 0 {
				t.Fatalf("%d requests for an account that is off", w.jev.count())
			}
			rows, _ := w.dbs.RecentAPICalls(ctx, rigAccount, 10)
			if len(rows) != 0 {
				t.Fatalf("the worker polled the gate into %d refusal rows", len(rows))
			}
			if !w.since().IsZero() {
				t.Fatal("the watermark outlived the switch; turning it back on would classify everything since")
			}
		})
	}
}

func TestTurningItBackOnStartsFromNowNotFromTheOldWatermark(t *testing.T) {
	ctx := context.Background()
	w := newWorkerRig(t, WorkerOptions{})
	w.tick()
	if err := w.gate.SetFeatureEnabled(ctx, rigAccount, CallFeature, false); err != nil {
		t.Fatal(err)
	}
	w.clock = epoch.Add(24 * time.Hour)
	w.arrive("gap", "gap", store.RoleInbox, epoch.Add(12*time.Hour), "While it was off", "text")
	w.tick() // off: clears the watermark
	if err := w.gate.SetFeatureEnabled(ctx, rigAccount, CallFeature, true); err != nil {
		t.Fatal(err)
	}
	w.clock = epoch.Add(48 * time.Hour)
	w.tick() // on again: the watermark is now
	if !w.since().Equal(w.clock) {
		t.Fatalf("watermark = %v, want %v", w.since(), w.clock)
	}
	if w.jev.count() != 0 {
		t.Fatal("mail from while it was off was classified")
	}
}

func TestACapReachedIsReportedAndSpendsNothing(t *testing.T) {
	ctx := context.Background()
	w := newWorkerRig(t, WorkerOptions{})
	w.tick()
	w.arrive("m1", "k1", store.RoleInbox, epoch.Add(time.Minute), "Hi", "text")
	w.clock = epoch.Add(time.Hour)
	if err := w.gate.SetAccountCap(ctx, rigAccount, 0); err != nil {
		t.Fatal(err)
	}
	_, err := w.worker.RunOnce(ctx)
	if !errors.Is(err, llm.ErrCapReached) {
		t.Fatalf("err = %v, want the cap", err)
	}
	if w.jev.count() != 0 {
		t.Fatalf("%d requests past a cap of zero", w.jev.count())
	}
	// Nothing was marked, so mail waits for the next period instead of being dropped.
	n, _ := w.dbs.CountUnclassified(ctx, rigAccount, epoch)
	if n != 1 {
		t.Fatalf("%d messages waiting, want 1", n)
	}
}

func TestNoQuestionsMeansNoWorkAndTheWatermarkFollowsTime(t *testing.T) {
	w := newWorkerRig(t, WorkerOptions{})
	if err := w.reg.Set(nil, nil); err != nil {
		t.Fatal(err)
	}
	w.tick()
	w.arrive("m1", "k1", store.RoleInbox, epoch.Add(time.Minute), "Hi", "text")
	w.clock = epoch.Add(24 * time.Hour)
	if n := w.tick(); n != 0 || w.jev.count() != 0 {
		t.Fatalf("processed=%d calls=%d with no questions", n, w.jev.count())
	}
	// A question that ships later must not reach back over mail that arrived while
	// there was nothing to ask: the watermark stayed with the clock.
	if !w.since().Equal(w.clock) {
		t.Fatalf("watermark = %v, want %v", w.since(), w.clock)
	}
	if err := w.reg.Set(mustParse(t, strings.Replace(validYAML, "enabled: false", "enabled: true", 1)), nil); err != nil {
		t.Fatal(err)
	}
	if w.tick() != 0 || w.jev.count() != 0 {
		t.Fatal("a newly added question classified mail that predates it")
	}
}

func TestBackfillIsOptInPricedAndBounded(t *testing.T) {
	ctx := context.Background()
	w := newWorkerRig(t, WorkerOptions{})
	w.tick() // watermark = epoch
	day := 24 * time.Hour
	w.arrive("d10", "d10", store.RoleInbox, epoch.Add(-10*day), "Ten days ago", "some text")
	w.arrive("d60", "d60", store.RoleInbox, epoch.Add(-60*day), "Sixty days ago", "some text")
	w.arrive("d200", "d200", store.RoleInbox, epoch.Add(-200*day), "Two hundred days ago", "some text")
	w.arrive("ancient", "ancient", store.RoleJunk, epoch.Add(-900*day), "Ancient junk", "some text")

	est30, err := w.worker.EstimateBackfill(ctx, rigAccount, Days(30))
	if err != nil {
		t.Fatal(err)
	}
	est90, _ := w.worker.EstimateBackfill(ctx, rigAccount, Days(90))
	estAll, _ := w.worker.EstimateBackfill(ctx, rigAccount, AllMail)
	// The estimate is a ceiling, so it counts every eligible message and does not
	// guess which have a question to ask.
	if est30.Items != 1 || est90.Items != 2 || estAll.Items != 4 {
		t.Fatalf("items = %d/%d/%d, want 1/2/4", est30.Items, est90.Items, estAll.Items)
	}
	if estAll.Estimate.USD <= 0 || !estAll.Estimate.Fits {
		t.Fatalf("estimate = %+v", estAll.Estimate)
	}
	if w.jev.count() != 0 {
		t.Fatal("estimating spent money")
	}
	// Estimating alone changes nothing: history is still not eligible.
	if w.tick() != 0 {
		t.Fatal("the estimate queued history")
	}

	if err := w.worker.StartBackfill(ctx, rigAccount, Days(30)); err != nil {
		t.Fatal(err)
	}
	if want := epoch.Add(-30 * day); !w.since().Equal(want) {
		t.Fatalf("watermark = %v, want %v", w.since(), want)
	}
	for w.tick() > 0 {
	}
	if len(w.decisions("d10")) == 0 || len(w.decisions("d60"))+len(w.decisions("d200")) != 0 {
		t.Fatal("a 30-day backfill read the wrong mail")
	}

	// A narrower request never raises the watermark: it only ever reaches further back.
	if err := w.worker.StartBackfill(ctx, rigAccount, Days(7)); err != nil {
		t.Fatal(err)
	}
	if want := epoch.Add(-30 * day); !w.since().Equal(want) {
		t.Fatalf("a smaller backfill moved the watermark to %v", w.since())
	}
}

func TestBackfillStopsAtTheCap(t *testing.T) {
	ctx := context.Background()
	w := newWorkerRig(t, WorkerOptions{Batch: 3})
	w.tick()
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("o%d", i)
		w.arrive(id, id, store.RoleInbox, epoch.Add(-time.Duration(i+1)*time.Hour), "Subject "+id, "body")
	}
	if err := w.worker.StartBackfill(ctx, rigAccount, Days(30)); err != nil {
		t.Fatal(err)
	}
	// Each fake call costs $0.0001 and the cap allows two of them.
	if err := w.gate.SetAccountCap(ctx, rigAccount, 0.0002); err != nil {
		t.Fatal(err)
	}
	var done int
	for i := 0; i < 5; i++ {
		n, err := w.worker.RunOnce(ctx)
		done += n
		if errors.Is(err, llm.ErrCapReached) {
			break
		}
	}
	if w.jev.count() > 3 {
		t.Fatalf("%d calls went out under a cap that pays for about two", w.jev.count())
	}
	if left, _ := w.dbs.CountUnclassified(ctx, rigAccount, epoch.Add(-30*24*time.Hour)); left == 0 {
		t.Fatalf("the backfill finished (%d done) despite the cap", done)
	}
}

func TestNextDelayBacksOffOnErrorsAndPausesOnTheCap(t *testing.T) {
	o := WorkerOptions{Interval: time.Minute, BusyInterval: time.Second, MaxBackoff: 10 * time.Minute, CapPause: time.Hour}
	o.applyDefaults()
	cases := []struct {
		name     string
		err      error
		n        int
		failures int
		want     time.Duration
	}{
		{"idle", nil, 0, 0, time.Minute},
		{"work was done, come back soon", nil, 5, 0, time.Second},
		{"first error", errors.New("boom"), 0, 1, 2 * time.Minute},
		{"errors double", errors.New("boom"), 0, 3, 8 * time.Minute},
		{"errors stop at the ceiling", errors.New("boom"), 0, 20, 10 * time.Minute},
		{"a cap waits for the next period", &llm.Refusal{Reason: llm.ReasonCapAccount}, 0, 0, time.Hour},
		{"a global cap too", &llm.Refusal{Reason: llm.ReasonCapGlobal}, 0, 0, time.Hour},
	}
	for _, c := range cases {
		if got := o.nextDelay(c.err, c.n, c.failures); got != c.want {
			t.Errorf("%s: delay = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRunStopsWhenItsContextIsCancelled(t *testing.T) {
	w := newWorkerRig(t, WorkerOptions{Interval: 10 * time.Millisecond, BusyInterval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.worker.Run(ctx) }()
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run ignored cancellation")
	}
}

func TestAMessageBecomesStateWithOnlyTheChosenFields(t *testing.T) {
	m := store.Message{
		AccountID: "a", ContentKey: "k", Subject: "S", BodyText: "B", Date: epoch,
		From:        store.Address{Name: "Ada", Address: "ada@example.com"},
		To:          []store.Address{{Name: "Me", Address: "me@example.com"}},
		CC:          []store.Address{{Address: "x@example.com"}},
		AuthResults: store.AuthResults{SPF: "pass", DKIM: "pass", DMARC: "fail"},
	}
	atts := []store.Attachment{{Filename: "a.pdf", MIMEType: "application/pdf"}}
	got := FromStored(m, store.RoleJunk, atts)
	if got.AccountID != "a" || got.ContentKey != "k" || got.Folder != FolderJunk {
		t.Fatalf("identity = %+v", got)
	}
	s := got.State
	if s.Subject != "S" || s.Body != "B" || s.From.Address != "ada@example.com" || len(s.To) != 1 ||
		s.Auth.DMARC != "fail" || len(s.Attachments) != 1 || s.Attachments[0].Name != "a.pdf" {
		t.Fatalf("state = %+v", s)
	}
	if FromStored(m, store.RoleInbox, nil).Folder != FolderInbox {
		t.Fatal("inbox role did not map to the inbox folder")
	}
}
