package store

import (
	"context"
	"testing"
	"time"
)

var threadBase = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func seedThreadMessage(t *testing.T, dbs *DBs, accountID, folderID, id, messageID, subject, refs, inReplyTo string, date time.Time) {
	t.Helper()
	err := dbs.UpsertMessage(context.Background(), Message{
		ID: id, AccountID: accountID, FolderID: folderID, UID: uidFor(id),
		ContentKey: "ck:" + id, MessageID: messageID, Subject: subject,
		References: refs, InReplyTo: inReplyTo, Date: date,
	})
	if err != nil {
		t.Fatalf("seedThreadMessage(%s): %v", id, err)
	}
}

func uidFor(id string) uint32 {
	var h uint32 = 2166136261
	for _, b := range []byte(id) {
		h = (h ^ uint32(b)) * 16777619
	}
	return h
}

func TestMessagesForThreadingProjectsHeaders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedAccount(t, dbs, "acct-2")
	seedFolder(t, dbs, "acct-2", "folder-2")

	seedThreadMessage(t, dbs, "acct-1", "folder-1", "a", "<a@x>", "Topic", "", "", threadBase)
	seedThreadMessage(t, dbs, "acct-1", "folder-1", "b", "<b@x>", "Re: Topic", "<a@x>", "<a@x>", threadBase.Add(time.Minute))
	seedThreadMessage(t, dbs, "acct-2", "folder-2", "other", "<o@x>", "Other", "", "", threadBase)

	hidden := Message{
		ID: "gone", AccountID: "acct-1", FolderID: "folder-1", UID: 999,
		ContentKey: "ck:gone", MessageID: "<gone@x>", Subject: "Hidden",
		DisabledAt: threadBase, DisabledReason: "server removed",
	}
	if err := dbs.UpsertMessage(ctx, hidden); err != nil {
		t.Fatalf("seed disabled: %v", err)
	}

	got, err := dbs.MessagesForThreading(ctx, "acct-1")
	if err != nil {
		t.Fatalf("MessagesForThreading: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("messages = %d, want 2 (disabled and other account excluded)", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Errorf("order = %s,%s want a,b (oldest first)", got[0].ID, got[1].ID)
	}
	if got[1].ContentKey != "ck:b" || got[1].MessageID != "<b@x>" || got[1].References != "<a@x>" || got[1].InReplyTo != "<a@x>" {
		t.Errorf("projection = %+v, want the header fields", got[1])
	}
	if !got[1].Date.Equal(threadBase.Add(time.Minute)) {
		t.Errorf("Date = %s", got[1].Date)
	}
}

func TestReplaceThreadsAssignsAndReadsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedThreadMessage(t, dbs, "acct-1", "folder-1", "a", "<a@x>", "Topic", "", "", threadBase)
	seedThreadMessage(t, dbs, "acct-1", "folder-1", "b", "<b@x>", "Re: Topic", "<a@x>", "<a@x>", threadBase.Add(time.Minute))
	seedThreadMessage(t, dbs, "acct-1", "folder-1", "c", "<c@x>", "Other", "", "", threadBase)

	threads := []Thread{
		{
			ID: "ck:a", AccountID: "acct-1", RootMessageID: "<a@x>", SubjectNorm: "Topic",
			LastDate: threadBase.Add(time.Minute), MessageIDs: []string{"a", "b"},
		},
		{
			ID: "ck:c", AccountID: "acct-1", RootMessageID: "<c@x>", SubjectNorm: "Other",
			LastDate: threadBase, MessageIDs: []string{"c"},
		},
	}
	if err := dbs.ReplaceThreads(ctx, "acct-1", threads); err != nil {
		t.Fatalf("ReplaceThreads: %v", err)
	}

	// A minted id is the proposed key scoped to the account.
	for id, want := range map[string]string{"a": "acct-1:ck:a", "b": "acct-1:ck:a", "c": "acct-1:ck:c"} {
		m, err := dbs.GetMessage(ctx, id)
		if err != nil {
			t.Fatalf("GetMessage(%s): %v", id, err)
		}
		if m.ThreadID != want {
			t.Errorf("%s thread_id = %q, want %q", id, m.ThreadID, want)
		}
	}

	stored, err := dbs.ListThreads(ctx, "acct-1")
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("threads = %d, want 2", len(stored))
	}
	// Newest last_date first: the Topic thread.
	if stored[0].ID != "acct-1:ck:a" || stored[0].MessageCount != 2 || stored[0].RootMessageID != "<a@x>" || stored[0].SubjectNorm != "Topic" {
		t.Errorf("thread row = %+v, want the Topic thread with 2 messages", stored[0])
	}
}

func TestReplaceThreadsReplacesPrevious(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedThreadMessage(t, dbs, "acct-1", "folder-1", "a", "<a@x>", "Topic", "", "", threadBase)

	first := []Thread{{ID: "old", AccountID: "acct-1", RootMessageID: "<a@x>", SubjectNorm: "Topic", LastDate: threadBase, MessageIDs: []string{"a"}}}
	if err := dbs.ReplaceThreads(ctx, "acct-1", first); err != nil {
		t.Fatalf("first ReplaceThreads: %v", err)
	}
	second := []Thread{{ID: "new", AccountID: "acct-1", RootMessageID: "<a@x>", SubjectNorm: "Topic", LastDate: threadBase, MessageIDs: []string{"a"}}}
	if err := dbs.ReplaceThreads(ctx, "acct-1", second); err != nil {
		t.Fatalf("second ReplaceThreads: %v", err)
	}

	stored, err := dbs.ListThreads(ctx, "acct-1")
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	// The same conversation proposed under a new key keeps its stored id (N12).
	if len(stored) != 1 || stored[0].ID != "acct-1:old" {
		t.Errorf("threads = %+v, want one row, under the id the conversation already had", stored)
	}
	m, err := dbs.GetMessage(ctx, "a")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if m.ThreadID != "acct-1:old" {
		t.Errorf("thread_id = %q, want acct-1:old", m.ThreadID)
	}
}

func TestReplaceThreadsEmptyClearsAssignment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedThreadMessage(t, dbs, "acct-1", "folder-1", "a", "<a@x>", "Topic", "", "", threadBase)

	if err := dbs.ReplaceThreads(ctx, "acct-1", []Thread{{ID: "t", AccountID: "acct-1", MessageIDs: []string{"a"}}}); err != nil {
		t.Fatalf("ReplaceThreads: %v", err)
	}
	if err := dbs.ReplaceThreads(ctx, "acct-1", nil); err != nil {
		t.Fatalf("clear ReplaceThreads: %v", err)
	}
	m, err := dbs.GetMessage(ctx, "a")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if m.ThreadID != "" {
		t.Errorf("thread_id = %q, want cleared", m.ThreadID)
	}
	stored, err := dbs.ListThreads(ctx, "acct-1")
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(stored) != 0 {
		t.Errorf("threads = %+v, want none", stored)
	}
}

// TestReplaceThreadsLeavesOtherAccounts ensures one account's retread does not
// clear another account's assignments.
func TestReplaceThreadsLeavesOtherAccounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedAccount(t, dbs, "acct-2")
	seedFolder(t, dbs, "acct-2", "folder-2")
	seedThreadMessage(t, dbs, "acct-1", "folder-1", "a", "<a@x>", "One", "", "", threadBase)
	seedThreadMessage(t, dbs, "acct-2", "folder-2", "b", "<b@x>", "Two", "", "", threadBase)

	if err := dbs.ReplaceThreads(ctx, "acct-2", []Thread{{ID: "t2", AccountID: "acct-2", MessageIDs: []string{"b"}}}); err != nil {
		t.Fatalf("ReplaceThreads other acct: %v", err)
	}
	if err := dbs.ReplaceThreads(ctx, "acct-1", []Thread{{ID: "t1", AccountID: "acct-1", MessageIDs: []string{"a"}}}); err != nil {
		t.Fatalf("ReplaceThreads acct-1: %v", err)
	}
	m, err := dbs.GetMessage(ctx, "b")
	if err != nil {
		t.Fatalf("GetMessage(b): %v", err)
	}
	if m.ThreadID != "acct-2:t2" {
		t.Errorf("other account thread_id = %q, want acct-2:t2", m.ThreadID)
	}
}

// seedPair puts messages a (older) and b, c, d (each a minute later) in one
// account so a test can regroup them.
func seedThreadSet(t *testing.T, dbs *DBs, accountID, folderID string) {
	t.Helper()
	seedAccount(t, dbs, accountID)
	seedFolder(t, dbs, accountID, folderID)
	for i, id := range []string{"a", "b", "c", "d"} {
		seedThreadMessage(t, dbs, accountID, folderID, id, "<"+id+"@x>", "S "+id, "", "", threadBase.Add(time.Duration(i)*time.Minute))
	}
}

func threadIDOf(t *testing.T, dbs *DBs, id string) string {
	t.Helper()
	m, err := dbs.GetMessage(context.Background(), id)
	if err != nil {
		t.Fatalf("GetMessage(%s): %v", id, err)
	}
	return m.ThreadID
}

// Sync is newest-first, so a conversation is first built from its replies and
// its root arrives later. The conversation keeps the id it had, or anything
// attached to it (a snooze, a tag) would silently detach (N12).
func TestReplaceThreadsKeepsTheOldIdWhenTheRootArrivesLater(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedThreadSet(t, dbs, "acct-1", "folder-1")

	// First pass: only the reply (b) is mirrored as a conversation of its own.
	if err := dbs.ReplaceThreads(ctx, "acct-1", []Thread{
		{ID: "ck:b", RootMessageID: "<b@x>", LastDate: threadBase.Add(time.Minute), MessageIDs: []string{"b"}},
	}); err != nil {
		t.Fatalf("first ReplaceThreads: %v", err)
	}
	first := threadIDOf(t, dbs, "b")
	if first == "" {
		t.Fatal("b has no thread id after the first pass")
	}

	// Second pass: the older root a arrives, so the thread is now rooted at a and
	// the algorithm proposes a new id; the conversation must keep its own.
	if err := dbs.ReplaceThreads(ctx, "acct-1", []Thread{
		{ID: "ck:a", RootMessageID: "<a@x>", LastDate: threadBase.Add(time.Minute), MessageIDs: []string{"a", "b"}},
	}); err != nil {
		t.Fatalf("second ReplaceThreads: %v", err)
	}
	if got := threadIDOf(t, dbs, "a"); got != first {
		t.Errorf("a thread_id = %q, want the conversation's existing id %q", got, first)
	}
	if got := threadIDOf(t, dbs, "b"); got != first {
		t.Errorf("b thread_id = %q, want %q kept", got, first)
	}
	threads, err := dbs.ListThreads(ctx, "acct-1")
	if err != nil || len(threads) != 1 || threads[0].ID != first || threads[0].RootMessageID != "<a@x>" {
		t.Errorf("threads = %+v, %v; want one row under the kept id, re-rooted at a", threads, err)
	}
}

// Two conversations that turn out to be one collapse onto the id of the one
// holding the older message.
func TestReplaceThreadsMergeKeepsTheOlderConversationsId(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedThreadSet(t, dbs, "acct-1", "folder-1")
	if err := dbs.ReplaceThreads(ctx, "acct-1", []Thread{
		{ID: "ck:a", RootMessageID: "<a@x>", LastDate: threadBase.Add(time.Minute), MessageIDs: []string{"a", "b"}},
		{ID: "ck:c", RootMessageID: "<c@x>", LastDate: threadBase.Add(3 * time.Minute), MessageIDs: []string{"c", "d"}},
	}); err != nil {
		t.Fatalf("first ReplaceThreads: %v", err)
	}
	older, newer := threadIDOf(t, dbs, "a"), threadIDOf(t, dbs, "c")
	if older == newer {
		t.Fatalf("setup: both conversations got %q", older)
	}

	// The merged conversation is proposed under a brand-new id.
	if err := dbs.ReplaceThreads(ctx, "acct-1", []Thread{
		{ID: "ck:merged", RootMessageID: "<a@x>", LastDate: threadBase.Add(3 * time.Minute), MessageIDs: []string{"a", "b", "c", "d"}},
	}); err != nil {
		t.Fatalf("merge ReplaceThreads: %v", err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if got := threadIDOf(t, dbs, id); got != older {
			t.Errorf("%s thread_id = %q, want the older conversation's id %q", id, got, older)
		}
	}
	if threads, _ := dbs.ListThreads(ctx, "acct-1"); len(threads) != 1 {
		t.Errorf("threads = %+v, want the merge to leave one", threads)
	}
}

// A conversation that splits keeps its id on exactly one half; the other gets a
// fresh one. Two rows can never share an id.
func TestReplaceThreadsSplitGivesEachHalfItsOwnId(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedThreadSet(t, dbs, "acct-1", "folder-1")
	if err := dbs.ReplaceThreads(ctx, "acct-1", []Thread{
		{ID: "ck:a", RootMessageID: "<a@x>", LastDate: threadBase.Add(time.Minute), MessageIDs: []string{"a", "b"}},
	}); err != nil {
		t.Fatalf("first ReplaceThreads: %v", err)
	}
	original := threadIDOf(t, dbs, "a")

	// Neither half is proposed under the original's own key.
	if err := dbs.ReplaceThreads(ctx, "acct-1", []Thread{
		{ID: "ck:first-half", RootMessageID: "<a@x>", LastDate: threadBase, MessageIDs: []string{"a"}},
		{ID: "ck:second-half", RootMessageID: "<b@x>", LastDate: threadBase.Add(time.Minute), MessageIDs: []string{"b"}},
	}); err != nil {
		t.Fatalf("split ReplaceThreads: %v", err)
	}
	if got := threadIDOf(t, dbs, "a"); got != original {
		t.Errorf("a thread_id = %q, want %q (the half with the oldest message keeps it)", got, original)
	}
	if got := threadIDOf(t, dbs, "b"); got == original || got == "" {
		t.Errorf("b thread_id = %q, want a fresh id distinct from %q", got, original)
	}
	if threads, _ := dbs.ListThreads(ctx, "acct-1"); len(threads) != 2 {
		t.Errorf("threads = %+v, want 2", threads)
	}
}

// Rebuilding an unchanged account changes nothing, which is what lets sync
// rethread after every fetch.
func TestReplaceThreadsIsStableWhenNothingChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedThreadSet(t, dbs, "acct-1", "folder-1")
	set := []Thread{
		{ID: "ck:a", RootMessageID: "<a@x>", LastDate: threadBase.Add(time.Minute), MessageIDs: []string{"a", "b"}},
		{ID: "ck:c", RootMessageID: "<c@x>", LastDate: threadBase.Add(3 * time.Minute), MessageIDs: []string{"c", "d"}},
	}
	for i := 0; i < 3; i++ {
		if err := dbs.ReplaceThreads(ctx, "acct-1", set); err != nil {
			t.Fatalf("ReplaceThreads #%d: %v", i, err)
		}
	}
	first := map[string]string{"a": threadIDOf(t, dbs, "a"), "c": threadIDOf(t, dbs, "c")}
	if err := dbs.ReplaceThreads(ctx, "acct-1", set); err != nil {
		t.Fatalf("ReplaceThreads again: %v", err)
	}
	for id, want := range first {
		if got := threadIDOf(t, dbs, id); got != want {
			t.Errorf("%s thread_id changed from %q to %q on an identical rebuild", id, want, got)
		}
	}
}

// The same email in two accounts has one content key; each account still gets
// its own conversation row (the threads id is a global primary key).
func TestReplaceThreadsSameProposedIdInTwoAccounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedThreadSet(t, dbs, "acct-1", "folder-1")
	seedAccount(t, dbs, "acct-2")
	seedFolder(t, dbs, "acct-2", "folder-2")
	seedThreadMessage(t, dbs, "acct-2", "folder-2", "a-in-2", "<a@x>", "S a", "", "", threadBase)
	for acct, member := range map[string]string{"acct-1": "a", "acct-2": "a-in-2"} {
		if err := dbs.ReplaceThreads(ctx, acct, []Thread{
			{ID: "ck:shared", RootMessageID: "<a@x>", LastDate: threadBase, MessageIDs: []string{member}},
		}); err != nil {
			t.Fatalf("ReplaceThreads(%s): %v", acct, err)
		}
	}
	for _, acct := range []string{"acct-1", "acct-2"} {
		if threads, err := dbs.ListThreads(ctx, acct); err != nil || len(threads) != 1 {
			t.Errorf("%s threads = %+v, %v; want one", acct, threads, err)
		}
	}
}
