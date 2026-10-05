package store

import (
	"context"
	"testing"
	"time"
)

// A message with no Date header has a NULL date. Every view that lists the
// newest copy of a content key has to carry it rather than fail or drop it.
func TestUndatedMailIsListedEverywhere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "inbox-1")
	alice := Address{Name: "Alice", Address: "alice@example.com"}
	seedPersonMessage(t, dbs, "acct-1", "inbox-1", "dated", time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), alice, nil, nil)
	seedPersonMessage(t, dbs, "acct-1", "inbox-1", "undated", time.Time{}, alice, nil, nil)

	if err := dbs.RebuildPeople(ctx, "acct-1"); err != nil {
		t.Fatalf("RebuildPeople with an undated message: %v", err)
	}
	rows, err := dbs.ListPeople(ctx)
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	if len(rows) != 1 || rows[0].MessageCount != 2 {
		t.Fatalf("people = %+v, want alice with both messages counted", rows)
	}
	if !rows[0].FirstSeen.Equal(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("first seen = %s, want the dated message's date, not year 1", rows[0].FirstSeen)
	}

	convs, err := dbs.ConversationsForAddresses(ctx, []string{"alice@example.com"}, 10)
	if err != nil {
		t.Fatalf("ConversationsForAddresses with an undated message: %v", err)
	}
	if len(convs) != 2 {
		t.Errorf("%d conversations, want 2", len(convs))
	}

	items, err := dbs.MessagesByContentKeys(ctx, "acct-1", []string{"ck:dated", "ck:undated"}, 10)
	if err != nil {
		t.Fatalf("MessagesByContentKeys: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("%d summaries, want 2", len(items))
	}
}
