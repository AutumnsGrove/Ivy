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

	for id, want := range map[string]string{"a": "ck:a", "b": "ck:a", "c": "ck:c"} {
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
	if stored[0].ID != "ck:a" || stored[0].MessageCount != 2 || stored[0].RootMessageID != "<a@x>" || stored[0].SubjectNorm != "Topic" {
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
	if len(stored) != 1 || stored[0].ID != "new" {
		t.Errorf("threads = %+v, want only the new thread", stored)
	}
	m, err := dbs.GetMessage(ctx, "a")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if m.ThreadID != "new" {
		t.Errorf("thread_id = %q, want new", m.ThreadID)
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
	if m.ThreadID != "t2" {
		t.Errorf("other account thread_id = %q, want t2", m.ThreadID)
	}
}
