package store

import (
	"context"
	"testing"
	"time"
)

func waitingIDs(cs []WaitingCandidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.MessageID
	}
	return out
}

// A thread is waiting on someone else when the newest message the owner still has is
// their own, in Sent, and old enough. Anything newer from anyone else ends the wait.
func TestWaitingOnCandidatesAreThreadsTheOwnerSentLast(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedThread(t, dbs, "a",
		// t1: asked a question, no answer yet.
		threadSeed{id: "t1-in", thread: "t1", folder: "inbox", minute: 0},
		threadSeed{id: "t1-sent", thread: "t1", folder: "sent", minute: 10},
		// t2: answered afterwards, so nobody is waiting.
		threadSeed{id: "t2-sent", thread: "t2", folder: "sent", minute: 20},
		threadSeed{id: "t2-reply", thread: "t2", folder: "inbox", minute: 30},
		// t3: the only newer message is in Trash, which the owner no longer has.
		threadSeed{id: "t3-sent", thread: "t3", folder: "sent", minute: 40},
		threadSeed{id: "t3-trashed", thread: "t3", folder: "trash", minute: 50},
		// t4: the newer message is hidden because the server deleted it.
		threadSeed{id: "t4-sent", thread: "t4", folder: "sent", minute: 60},
		threadSeed{id: "t4-gone", thread: "t4", folder: "inbox", minute: 70, disabled: true},
		// t5: a message that belongs to no thread is its own conversation.
		threadSeed{id: "t5-sent", folder: "sent", minute: 80},
		// t6: sent too recently to count as silence.
		threadSeed{id: "t6-sent", thread: "t6", folder: "sent", minute: 200},
	)
	seedThread(t, dbs, "b", threadSeed{id: "b-sent", thread: "bt", folder: "sent", minute: 5})

	cutoff := classifyNow.Add(100 * time.Minute)
	got, err := dbs.WaitingOnCandidates(ctx, "a", cutoff, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Longest wait first, and the other account's thread is not in it.
	want := []string{"t1-sent", "t3-sent", "t4-sent", "t5-sent"}
	if !equalIDs(waitingIDs(got), want) {
		t.Fatalf("candidates = %v, want %v", waitingIDs(got), want)
	}
	if got[0].ContentKey != "ck:t1-sent" || got[0].ThreadID != "t1" || !got[0].Date.Equal(classifyNow.Add(10*time.Minute)) {
		t.Errorf("first candidate = %+v", got[0])
	}

	limited, err := dbs.WaitingOnCandidates(ctx, "a", cutoff, 2)
	if err != nil || !equalIDs(waitingIDs(limited), want[:2]) {
		t.Fatalf("limit 2 = %v, %v; want %v", waitingIDs(limited), err, want[:2])
	}
	if none, err := dbs.WaitingOnCandidates(ctx, "a", cutoff, 0); err != nil || len(none) != 0 {
		t.Fatalf("limit 0 = %v, %v; want nothing", none, err)
	}
}
