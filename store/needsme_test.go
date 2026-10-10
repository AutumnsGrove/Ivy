package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNeedsMeRowRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, ok, err := dbs.GetNeedsMe(ctx, "a", "k"); err != nil || ok {
		t.Fatalf("before any write = %v, %v; want absent", ok, err)
	}
	row := NeedsMeRow{AccountID: "a", ContentKey: "k", Verdict: "needs", Reason: "Ada wants the venue confirmed.", Model: "m1", State: "open"}
	if err := dbs.PutNeedsMe(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, ok, err := dbs.GetNeedsMe(ctx, "a", "k")
	if err != nil || !ok || got != row {
		t.Fatalf("read back = %+v, %v, %v; want %+v", got, ok, err, row)
	}
	// A second verdict for the same message replaces the first: one row per message.
	row.Verdict, row.Reason = "not", ""
	if err := dbs.PutNeedsMe(ctx, row); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := dbs.GetNeedsMe(ctx, "a", "k"); got.Verdict != "not" || got.Reason != "" {
		t.Fatalf("after replace = %+v", got)
	}
	for _, bad := range []NeedsMeRow{
		{ContentKey: "k", Verdict: "needs"},
		{AccountID: "a", Verdict: "needs"},
		{AccountID: "a", ContentKey: "k", Verdict: "maybe"},
	} {
		if err := dbs.PutNeedsMe(ctx, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

// The inbox query already flags rows whose verdict is "needs"; writing one must light
// that flag without touching the query.
func TestAStoredNeedsVerdictFlagsTheInboxRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a", classifySeed{id: "m1", folder: "inbox", key: "k1", arrived: classifyNow})
	if err := dbs.PutNeedsMe(ctx, NeedsMeRow{AccountID: "a", ContentKey: "k1", Verdict: "needs", Reason: "r", Model: "m", State: "open"}); err != nil {
		t.Fatal(err)
	}
	page, err := dbs.ListInbox(ctx, InboxQuery{AccountID: "a", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.Items[0].Needs || page.NeedCount != 1 {
		t.Fatalf("inbox = %+v, want the one message flagged and counted", page)
	}
}

var stage2Q = Stage2Query{
	QuestionID: "needs_me", Hash: "h1", Model: "jev-latest", Quiet: "none", Threshold: 0.65,
	StageQuestion: "needs_me_stage2", StageHash: "p1", StageModel: "chat-1",
}

func putAnswer(t *testing.T, dbs *DBs, key, choice string, p, conf float64, mut func(*Decision)) {
	t.Helper()
	d := Decision{
		AccountID: "a", ContentKey: key, QuestionID: "needs_me", InstructionHash: "h1", Model: "jev-latest",
		Choice: choice, Probabilities: map[string]float64{choice: p}, Confidence: conf, DecidedAt: classifyNow,
	}
	if mut != nil {
		mut(&d)
	}
	if err := dbs.PutDecisions(context.Background(), []Decision{d}); err != nil {
		t.Fatal(err)
	}
}

func TestStage2CandidatesAreTheMessagesStageOneFlagged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a",
		classifySeed{id: "m-likely", folder: "inbox", key: "likely", arrived: classifyNow.Add(3 * time.Hour)},
		classifySeed{id: "m-maybe", folder: "inbox", key: "maybe", arrived: classifyNow.Add(2 * time.Hour)},
		classifySeed{id: "m-quiet", folder: "inbox", key: "quiet", arrived: classifyNow.Add(1 * time.Hour)},
		classifySeed{id: "m-weak", folder: "inbox", key: "weak", arrived: classifyNow},
	)
	putAnswer(t, dbs, "likely", "likely", 0.9, 0.9, nil)
	putAnswer(t, dbs, "maybe", "maybe", 0.7, 0.8, nil)
	putAnswer(t, dbs, "quiet", "none", 0.99, 0.99, nil)
	putAnswer(t, dbs, "weak", "likely", 0.5, 0.9, nil)

	got, err := dbs.Stage2Candidates(ctx, "a", stage2Q, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"likely", "maybe"}; !eq(keysOf(got), want) {
		t.Fatalf("candidates = %v, want %v (newest first; the quiet and the under-the-bar answers wait out)", keysOf(got), want)
	}
	if got[0].Message.Subject == "" || got[0].Role != RoleInbox {
		t.Fatalf("a candidate should carry its whole message: %+v", got[0])
	}
	if limited, _ := dbs.Stage2Candidates(ctx, "a", stage2Q, 1); len(limited) != 1 {
		t.Fatalf("limit 1 returned %d", len(limited))
	}
	if none, _ := dbs.Stage2Candidates(ctx, "a", stage2Q, 0); len(none) != 0 {
		t.Fatalf("limit 0 returned %d", len(none))
	}
}

// Only the answer to the current wording from the current model counts, and a
// confidence under the bar is not a flag even when the probability clears it.
func TestStage2IgnoresStaleAndUnsureAnswers(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a",
		classifySeed{id: "m1", folder: "inbox", key: "old-wording", arrived: classifyNow},
		classifySeed{id: "m2", folder: "inbox", key: "other-model", arrived: classifyNow},
		classifySeed{id: "m3", folder: "inbox", key: "unsure", arrived: classifyNow},
	)
	putAnswer(t, dbs, "old-wording", "likely", 0.9, 0.9, func(d *Decision) { d.InstructionHash = "h0" })
	putAnswer(t, dbs, "other-model", "likely", 0.9, 0.9, func(d *Decision) { d.Model = "jev-old" })
	putAnswer(t, dbs, "unsure", "likely", 0.9, 0.4, nil)
	if got, _ := dbs.Stage2Candidates(context.Background(), "a", stage2Q, 10); len(got) != 0 {
		t.Fatalf("candidates = %v, want none", keysOf(got))
	}
}

// A message leaves the queue once it has a verdict (of either kind) or a recorded
// failed attempt at the current prompt and model; changing either brings it back.
func TestStage2QueueDrainsAndRefillsOnANewPromptOrModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a",
		classifySeed{id: "m1", folder: "inbox", key: "decided", arrived: classifyNow},
		classifySeed{id: "m2", folder: "inbox", key: "failed", arrived: classifyNow},
		classifySeed{id: "m3", folder: "inbox", key: "waiting", arrived: classifyNow},
	)
	for _, k := range []string{"decided", "failed", "waiting"} {
		putAnswer(t, dbs, k, "likely", 0.9, 0.9, nil)
	}
	if err := dbs.PutNeedsMe(ctx, NeedsMeRow{AccountID: "a", ContentKey: "decided", Verdict: "not", Model: "chat-1", State: "open"}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.PutDecisionMisses(ctx, []DecisionMiss{{
		AccountID: "a", ContentKey: "failed", QuestionID: "needs_me_stage2", InstructionHash: "p1", Model: "chat-1",
		Reason: MissInvalidAnswer, At: classifyNow,
	}}); err != nil {
		t.Fatal(err)
	}
	got, _ := dbs.Stage2Candidates(ctx, "a", stage2Q, 10)
	if want := []string{"waiting"}; !eq(keysOf(got), want) {
		t.Fatalf("candidates = %v, want %v", keysOf(got), want)
	}

	newPrompt := stage2Q
	newPrompt.StageHash = "p2"
	got, _ = dbs.Stage2Candidates(ctx, "a", newPrompt, 10)
	if want := []string{"failed", "waiting"}; !eq(keysOf(got), want) {
		t.Fatalf("with a new prompt candidates = %v, want %v (the failed attempt was for the old one)", keysOf(got), want)
	}
}

func TestStage2OnlyReadsVisibleInboxMail(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a",
		classifySeed{id: "m1", folder: "archive", key: "archived", arrived: classifyNow},
		classifySeed{id: "m2", folder: "junk", key: "junked", arrived: classifyNow},
		classifySeed{id: "m3", folder: "inbox", key: "gone", arrived: classifyNow, disabled: true},
		classifySeed{id: "m4", folder: "inbox", key: "kept", arrived: classifyNow},
	)
	for _, k := range []string{"archived", "junked", "gone", "kept"} {
		putAnswer(t, dbs, k, "likely", 0.9, 0.9, nil)
	}
	got, _ := dbs.Stage2Candidates(context.Background(), "a", stage2Q, 10)
	if want := []string{"kept"}; !eq(keysOf(got), want) {
		t.Fatalf("candidates = %v, want %v", keysOf(got), want)
	}
	if other, _ := dbs.Stage2Candidates(context.Background(), "someone-else", stage2Q, 10); len(other) != 0 {
		t.Fatal("another account's mail was returned")
	}
}

// A withheld message is skipped so it cannot block the queue, but only for a while:
// the checks may be re-run, and the operator may allow it. The skip expires, and the
// override ends it at once.
func TestAWithheldSkipExpiresAndTheOverrideEndsIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a", classifySeed{id: "m1", folder: "inbox", key: "held", arrived: classifyNow})
	putAnswer(t, dbs, "held", "likely", 0.9, 0.9, nil)
	if err := dbs.PutDecisionMisses(ctx, []DecisionMiss{{
		AccountID: "a", ContentKey: "held", QuestionID: "needs_me_stage2", InstructionHash: "p1", Model: "chat-1",
		Reason: MissWithheld, At: classifyNow,
	}}); err != nil {
		t.Fatal(err)
	}

	q := stage2Q
	q.RetryWithheldBefore = classifyNow.Add(-time.Minute) // the skip is newer than the cutoff
	if got, _ := dbs.Stage2Candidates(ctx, "a", q, 10); len(got) != 0 {
		t.Fatalf("a fresh withheld skip still queued the message: %v", keysOf(got))
	}
	q.RetryWithheldBefore = classifyNow.Add(time.Minute) // the skip is older than the cutoff
	if got, _ := dbs.Stage2Candidates(ctx, "a", q, 10); !eq(keysOf(got), []string{"held"}) {
		t.Fatalf("an expired withheld skip kept the message out: %v", keysOf(got))
	}

	// A different kind of miss never expires this way: an invalid reply stays settled.
	if err := dbs.PutDecisionMisses(ctx, []DecisionMiss{{
		AccountID: "a", ContentKey: "held", QuestionID: "needs_me_stage2", InstructionHash: "p1", Model: "chat-1",
		Reason: MissInvalidAnswer, At: classifyNow,
	}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := dbs.Stage2Candidates(ctx, "a", q, 10); len(got) != 0 {
		t.Fatalf("an invalid-reply miss expired: %v", keysOf(got))
	}

	// The override clears the withheld skip for that message now.
	if err := dbs.PutDecisionMisses(ctx, []DecisionMiss{{
		AccountID: "a", ContentKey: "held", QuestionID: "needs_me_stage2", InstructionHash: "p1", Model: "chat-1",
		Reason: MissWithheld, At: classifyNow,
	}}); err != nil {
		t.Fatal(err)
	}
	q.RetryWithheldBefore = classifyNow.Add(-time.Minute)
	if err := dbs.SetVettingOverride(ctx, "a", "held", classifyNow); err != nil {
		t.Fatal(err)
	}
	if got, _ := dbs.Stage2Candidates(ctx, "a", q, 10); !eq(keysOf(got), []string{"held"}) {
		t.Fatalf("the override did not end the withheld skip: %v", keysOf(got))
	}
}

func flaggedInbox(t *testing.T, dbs *DBs) {
	t.Helper()
	seedClassify(t, dbs, "a",
		classifySeed{id: "m1", folder: "inbox", key: "k1", arrived: classifyNow},
		classifySeed{id: "m2", folder: "inbox", key: "k2", arrived: classifyNow.Add(time.Hour)},
	)
	for _, k := range []string{"k1", "k2"} {
		if err := dbs.PutNeedsMe(context.Background(), NeedsMeRow{
			AccountID: "a", ContentKey: k, Verdict: "needs", Reason: "reason for " + k, Model: "m", State: NeedsOpen,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// The inbox row carries the reason, so the marker can say why without a second call.
func TestTheInboxRowCarriesTheReason(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	flaggedInbox(t, dbs)
	page, err := dbs.ListInbox(context.Background(), InboxQuery{AccountID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].NeedsReason != "reason for k2" || page.Items[1].NeedsReason != "reason for k1" {
		t.Fatalf("items = %+v", page.Items)
	}
}

// Decision 2: tapping dismiss clears the flag for that one message, kept locally so it
// does not come back on a re-read or when stage two runs again.
func TestDismissingClearsTheFlagEverywhereAndItStaysCleared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	flaggedInbox(t, dbs)

	if err := dbs.DismissNeedsMe(ctx, "a", "k1"); err != nil {
		t.Fatal(err)
	}
	page, _ := dbs.ListInbox(ctx, InboxQuery{AccountID: "a"})
	if page.NeedCount != 1 || page.Items[1].Needs || page.Items[1].NeedsReason != "" || !page.Items[0].Needs {
		t.Fatalf("after dismissing k1: %+v", page)
	}
	if on, _ := dbs.MessageNeeds(ctx, "a", "k1"); on {
		t.Error("MessageNeeds still reports a dismissed message")
	}
	if on, _ := dbs.MessageNeeds(ctx, "a", "k2"); !on {
		t.Error("dismissing k1 cleared k2")
	}
	byRef, err := dbs.MessagesByContentRefs(ctx, "a", []ContentRef{{AccountID: "a", ContentKey: "k1"}}, "", 10)
	if err != nil || len(byRef.Items) != 1 || byRef.Items[0].Needs {
		t.Fatalf("by-ref view = %+v, %v; want the message unflagged", byRef, err)
	}

	// The verdict is kept, so stage two does not decide it again, and a later write of
	// the same verdict does not bring the flag back.
	row, ok, _ := dbs.GetNeedsMe(ctx, "a", "k1")
	if !ok || row.State != NeedsDismissed {
		t.Fatalf("row = %+v, %v; want it kept as dismissed", row, ok)
	}
	if err := dbs.PutNeedsMe(ctx, NeedsMeRow{AccountID: "a", ContentKey: "k1", Verdict: "needs", Reason: "again", Model: "m", State: NeedsOpen}); err != nil {
		t.Fatal(err)
	}
	if on, _ := dbs.MessageNeeds(ctx, "a", "k1"); on {
		t.Error("rewriting the verdict resurrected a dismissed flag")
	}
	if got, _ := dbs.Stage2Candidates(ctx, "a", stage2Q, 10); len(got) != 0 {
		t.Errorf("a dismissed message was queued for stage two again: %v", keysOf(got))
	}
}

func TestDismissingTwiceOrWithoutAVerdictIsHarmless(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	flaggedInbox(t, dbs)
	for i := 0; i < 2; i++ {
		if err := dbs.DismissNeedsMe(ctx, "a", "k1"); err != nil {
			t.Fatalf("dismiss #%d: %v", i+1, err)
		}
	}
	if err := dbs.DismissNeedsMe(ctx, "a", "never-judged"); err != nil {
		t.Fatalf("dismissing a message with no verdict: %v", err)
	}
	if _, ok, _ := dbs.GetNeedsMe(ctx, "a", "never-judged"); ok {
		t.Error("dismissing a message with no verdict invented a row")
	}
}

var urgentBar = UrgencyBar{QuestionID: "urgency", Hash: "u1", Model: "jev-latest", High: "high", Threshold: 0.8}

func putUrgency(t *testing.T, dbs *DBs, key, choice string, p float64, mut func(*Decision)) {
	t.Helper()
	d := Decision{
		AccountID: "a", ContentKey: key, QuestionID: "urgency", InstructionHash: "u1", Model: "jev-latest",
		Choice: choice, Probabilities: map[string]float64{choice: p}, Confidence: p, DecidedAt: classifyNow,
	}
	if mut != nil {
		mut(&d)
	}
	if err := dbs.PutDecisions(context.Background(), []Decision{d}); err != nil {
		t.Fatal(err)
	}
}

func needsView(t *testing.T) *DBs {
	t.Helper()
	dbs := openTemp(t)
	seedClassify(t, dbs, "a",
		classifySeed{id: "m-old-urgent", folder: "inbox", key: "old-urgent", arrived: classifyNow},
		classifySeed{id: "m-new-plain", folder: "inbox", key: "new-plain", arrived: classifyNow.Add(4 * time.Hour)},
		classifySeed{id: "m-mid-urgent", folder: "inbox", key: "mid-urgent", arrived: classifyNow.Add(2 * time.Hour)},
		classifySeed{id: "m-weak-urgent", folder: "inbox", key: "weak-urgent", arrived: classifyNow.Add(3 * time.Hour)},
		classifySeed{id: "m-archived", folder: "archive", key: "archived", arrived: classifyNow.Add(5 * time.Hour)},
		classifySeed{id: "m-unflagged", folder: "inbox", key: "unflagged", arrived: classifyNow.Add(6 * time.Hour)},
	)
	for _, k := range []string{"old-urgent", "new-plain", "mid-urgent", "weak-urgent", "archived"} {
		if err := dbs.PutNeedsMe(context.Background(), NeedsMeRow{AccountID: "a", ContentKey: k, Verdict: "needs", Reason: "why " + k, Model: "m", State: NeedsOpen}); err != nil {
			t.Fatal(err)
		}
	}
	putUrgency(t, dbs, "old-urgent", "high", 0.95, nil)
	putUrgency(t, dbs, "mid-urgent", "high", 0.9, nil)
	putUrgency(t, dbs, "weak-urgent", "high", 0.5, nil) // under the bar: not urgent
	putUrgency(t, dbs, "new-plain", "normal", 0.99, nil)
	return dbs
}

func TestNeedsAttentionListsUrgentFirstThenNewest(t *testing.T) {
	t.Parallel()
	dbs := needsView(t)
	page, err := dbs.NeedsAttention(context.Background(), NeedsAttentionQuery{Urgency: urgentBar})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mid-urgent", "old-urgent", "new-plain", "weak-urgent"}
	var got []string
	for _, it := range page.Items {
		got = append(got, it.ContentKey)
		if !it.Needs || it.NeedsReason == "" {
			t.Errorf("%s: a row in this view must be flagged with its reason: %+v", it.ContentKey, it)
		}
	}
	if !eq(got, want) {
		t.Fatalf("order = %v, want %v (urgent first, newest first within each group; the archived and unflagged mail is not here)", got, want)
	}
	if page.NeedCount != 4 {
		t.Fatalf("NeedCount = %d, want 4", page.NeedCount)
	}
}

func TestNeedsAttentionPagesAcrossTheUrgentBoundary(t *testing.T) {
	t.Parallel()
	dbs := needsView(t)
	var got []string
	cursor := ""
	for i := 0; i < 10; i++ {
		page, err := dbs.NeedsAttention(context.Background(), NeedsAttentionQuery{Urgency: urgentBar, Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			got = append(got, it.ContentKey)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if want := []string{"mid-urgent", "old-urgent", "new-plain", "weak-urgent"}; !eq(got, want) {
		t.Fatalf("paged order = %v, want %v with nothing repeated or skipped", got, want)
	}
}

func TestNeedsAttentionLeavesOutDismissedHiddenAndStaleUrgency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := needsView(t)
	if err := dbs.DismissNeedsMe(ctx, "a", "old-urgent"); err != nil {
		t.Fatal(err)
	}
	q := NeedsAttentionQuery{Urgency: urgentBar, Hide: []ContentRef{{AccountID: "a", ContentKey: "new-plain"}}}
	page, _ := dbs.NeedsAttention(ctx, q)
	var got []string
	for _, it := range page.Items {
		got = append(got, it.ContentKey)
	}
	if want := []string{"mid-urgent", "weak-urgent"}; !eq(got, want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	if page.NeedCount != 2 {
		t.Fatalf("NeedCount = %d, want 2", page.NeedCount)
	}

	// An urgency answer to older wording does not rank anything.
	stale := urgentBar
	stale.Hash = "u0"
	page, _ = dbs.NeedsAttention(ctx, NeedsAttentionQuery{Urgency: stale})
	if len(page.Items) == 0 || page.Items[0].ContentKey != "new-plain" {
		t.Fatalf("with stale urgency the view should be plain newest-first: %+v", keysOfSummaries(page.Items))
	}
	if other, _ := dbs.NeedsAttention(ctx, NeedsAttentionQuery{AccountID: "someone-else", Urgency: urgentBar}); len(other.Items) != 0 {
		t.Fatal("another account's mail was returned")
	}
	if _, err := dbs.NeedsAttention(ctx, NeedsAttentionQuery{Cursor: "!!!"}); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("a bad cursor = %v, want ErrBadCursor", err)
	}
}

func keysOfSummaries(items []MessageSummary) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ContentKey
	}
	return out
}

func tagIDs(t *testing.T, dbs *DBs, account, key string) []string {
	t.Helper()
	m, err := dbs.TagsForMessages(context.Background(), account, []string{key})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tag := range m[key] {
		out = append(out, tag.ID)
	}
	return out
}

// The "placed for you" tag is reserved so it cannot be deleted from under the view,
// and local only so it never becomes an IMAP keyword on someone's mail.
func TestThePlacedTagIsReservedLocalOnlyAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	tag, err := dbs.EnsureNeedsTag(ctx)
	if err != nil || tag.ID != NeedsTagID || tag.Slug != NeedsSlug || tag.Name != NeedsTagName {
		t.Fatalf("tag = %+v, %v", tag, err)
	}
	again, err := dbs.EnsureNeedsTag(ctx)
	if err != nil || again.ID != tag.ID {
		t.Fatalf("a second ensure = %+v, %v; want the same tag", again, err)
	}
	if !IsReservedTag(NeedsTagID) || !tag.LocalOnly() {
		t.Fatal("the placed tag must be reserved and local only")
	}
	if (Tag{ID: "t-other", Slug: "work"}).LocalOnly() {
		t.Fatal("an ordinary tag reads as local only")
	}

	for i := 0; i < 2; i++ {
		if err := dbs.PlaceNeedsTag(ctx, "a", "k1"); err != nil {
			t.Fatal(err)
		}
	}
	if got := tagIDs(t, dbs, "a", "k1"); len(got) != 1 || got[0] != NeedsTagID {
		t.Fatalf("tags = %v, want the placed tag once", got)
	}
	var source string
	if err := dbs.State.Read.QueryRowContext(ctx,
		`SELECT source FROM message_tags WHERE tag_id = ? AND content_key = 'k1'`, NeedsTagID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != TagSourcePlaced {
		t.Fatalf("source = %q, want %q: placed by the layer, not the operator", source, TagSourcePlaced)
	}
}

// Decision 5: removing the tag and dismissing the flag are the same action, so
// dismissing, by whatever path, takes the tag away too.
func TestDismissingTakesThePlacedTagAway(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	flaggedInbox(t, dbs)
	for _, k := range []string{"k1", "k2"} {
		if err := dbs.PlaceNeedsTag(ctx, "a", k); err != nil {
			t.Fatal(err)
		}
	}
	if err := dbs.DismissNeedsMe(ctx, "a", "k1"); err != nil {
		t.Fatal(err)
	}
	if got := tagIDs(t, dbs, "a", "k1"); len(got) != 0 {
		t.Fatalf("k1 still carries %v after being dismissed", got)
	}
	if got := tagIDs(t, dbs, "a", "k2"); len(got) != 1 {
		t.Fatalf("dismissing k1 took k2's tag: %v", got)
	}
	// A message the operator tagged with something else keeps that.
	if _, err := dbs.CreateTag(ctx, "t-work", "Work", "sky"); err != nil {
		t.Fatal(err)
	}
	if err := dbs.TagMessage(ctx, "a", "k2", "t-work", TagSourceOperator); err != nil {
		t.Fatal(err)
	}
	if err := dbs.DismissNeedsMe(ctx, "a", "k2"); err != nil {
		t.Fatal(err)
	}
	if got := tagIDs(t, dbs, "a", "k2"); len(got) != 1 || got[0] != "t-work" {
		t.Fatalf("k2 tags = %v, want only the operator's own tag left", got)
	}
}
