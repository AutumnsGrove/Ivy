package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAThreadSummaryRoundTripsAndOneThreadHasOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, ok, err := dbs.GetThreadSummary(ctx, "a", "t1"); err != nil || ok {
		t.Fatalf("before any write = %v, %v; want absent", ok, err)
	}
	row := ThreadSummary{
		AccountID: "a", ThreadID: "t1", SourceHash: "s1", PromptHash: "p1", Model: "chat-1",
		Text: "Sam and Priya settled on Thursday.", MessageCount: 4, WrittenAt: classifyNow,
	}
	if err := dbs.PutThreadSummary(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, ok, err := dbs.GetThreadSummary(ctx, "a", "t1")
	if err != nil || !ok || got != row {
		t.Fatalf("read back = %+v, %v, %v; want %+v", got, ok, err, row)
	}

	// A refresh replaces the old text: one row per thread, so a thread can never show two.
	row.SourceHash, row.Text, row.MessageCount = "s2", "Thursday is confirmed.", 5
	if err := dbs.PutThreadSummary(ctx, row); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := dbs.GetThreadSummary(ctx, "a", "t1"); got.Text != "Thursday is confirmed." || got.SourceHash != "s2" {
		t.Fatalf("after replace = %+v", got)
	}
	// Another account's thread with the same id is a different row.
	if _, ok, _ := dbs.GetThreadSummary(ctx, "b", "t1"); ok {
		t.Fatal("a summary leaked across accounts")
	}
	for _, bad := range []ThreadSummary{
		{ThreadID: "t", SourceHash: "s", Text: "x", Model: "m"},
		{AccountID: "a", SourceHash: "s", Text: "x", Model: "m"},
		{AccountID: "a", ThreadID: "t", Text: "x", Model: "m"},
		{AccountID: "a", ThreadID: "t", SourceHash: "s", Model: "m"},
	} {
		if err := dbs.PutThreadSummary(ctx, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func seedThread(t *testing.T, dbs *DBs, account string, rows ...threadSeed) {
	t.Helper()
	ctx := context.Background()
	seedAccount(t, dbs, account)
	roles := map[string]string{"inbox": RoleInbox, "sent": RoleSent, "trash": RoleTrash, "junk": RoleJunk}
	made := map[string]bool{}
	for i, r := range rows {
		if !made[r.folder] {
			if err := dbs.UpsertFolder(ctx, Folder{ID: account + "-" + r.folder, AccountID: account, Name: r.folder, Role: roles[r.folder]}); err != nil {
				t.Fatal(err)
			}
			made[r.folder] = true
		}
		m := Message{
			ID: r.id, AccountID: account, FolderID: account + "-" + r.folder, UID: uint32(i + 1),
			ContentKey: "ck:" + r.id, Subject: "s " + r.id, BodyText: "body " + r.id, ThreadID: r.thread,
			Date: classifyNow.Add(time.Duration(r.minute) * time.Minute),
		}
		if err := dbs.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
		if r.disabled {
			if _, err := dbs.Mirror.Write.ExecContext(ctx, `UPDATE messages SET disabled_at = ? WHERE id = ?`, formatTime(classifyNow), r.id); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type threadSeed struct {
	id, thread, folder string
	minute             int
	disabled           bool
}

func messageIDs(ms []Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func equalIDs(a, b []string) bool {
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

// The thread a message belongs to, oldest first, as the model will read it: the
// owner's own replies in Sent count, deleted and junked mail and hidden copies do not.
func TestThreadMessagesAreTheLiveConversationOldestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedThread(t, dbs, "a",
		threadSeed{id: "m3", thread: "t1", folder: "inbox", minute: 30},
		threadSeed{id: "m1", thread: "t1", folder: "inbox", minute: 10},
		threadSeed{id: "m2", thread: "t1", folder: "sent", minute: 20},
		threadSeed{id: "gone", thread: "t1", folder: "inbox", minute: 15, disabled: true},
		threadSeed{id: "bin", thread: "t1", folder: "trash", minute: 16},
		threadSeed{id: "spam", thread: "t1", folder: "junk", minute: 17},
		threadSeed{id: "other", thread: "t2", folder: "inbox", minute: 5},
	)
	view, err := dbs.ThreadMessagesFor(ctx, "a", "m2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"m1", "m2", "m3"}; !equalIDs(messageIDs(view.Messages), want) || view.Total != 3 || view.ThreadID != "t1" {
		t.Fatalf("thread = %v total %d id %q, want %v total 3", messageIDs(view.Messages), view.Total, view.ThreadID, want)
	}
}

// A long thread is cut to a bound with the first message and the newest ones kept,
// and the total says how much was left out.
func TestAnOverlongThreadKeepsItsFirstAndNewestMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	var rows []threadSeed
	for i := 1; i <= 8; i++ {
		rows = append(rows, threadSeed{id: "m" + string(rune('0'+i)), thread: "t1", folder: "inbox", minute: i})
	}
	seedThread(t, dbs, "a", rows...)
	view, err := dbs.ThreadMessagesFor(ctx, "a", "m8", 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"m1", "m6", "m7", "m8"}; !equalIDs(messageIDs(view.Messages), want) || view.Total != 8 {
		t.Fatalf("thread = %v total %d, want %v total 8", messageIDs(view.Messages), view.Total, want)
	}
}

func TestAMessageWithNoThreadIsItsOwnThread(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedThread(t, dbs, "a", threadSeed{id: "solo", folder: "inbox", minute: 1}, threadSeed{id: "solo2", folder: "inbox", minute: 2})
	view, err := dbs.ThreadMessagesFor(ctx, "a", "solo", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !equalIDs(messageIDs(view.Messages), []string{"solo"}) || view.Total != 1 || view.ThreadID != "" {
		t.Fatalf("view = %v total %d id %q, want only the message itself", messageIDs(view.Messages), view.Total, view.ThreadID)
	}
}

func TestThreadMessagesRefuseAnotherAccountsMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedThread(t, dbs, "a", threadSeed{id: "m1", thread: "t1", folder: "inbox", minute: 1})
	if _, err := dbs.ThreadMessagesFor(ctx, "b", "m1", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound for a message of another account", err)
	}
	if _, err := dbs.ThreadMessagesFor(ctx, "a", "missing", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
