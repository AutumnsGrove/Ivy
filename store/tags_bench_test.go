package store

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkTagsForMessagesPage measures the lookup every inbox page makes to show
// each message's tag: one query for a full page of keys, over a state.db that
// already holds a realistic spread of memberships (20 tags over 5,000 messages).
// It is small on purpose; the number to watch is its ratio to ListInbox.
func BenchmarkTagsForMessagesPage(b *testing.B) {
	ctx := context.Background()
	dbs, err := Open(ctx, b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = dbs.Close() })

	for t := range 20 {
		if _, err := dbs.CreateTag(ctx, fmt.Sprintf("t%d", t), fmt.Sprintf("tag %d", t), "sky"); err != nil {
			b.Fatal(err)
		}
	}
	keys := make([]string, 0, maxInboxLimit)
	for i := range 5000 {
		key := fmt.Sprintf("key-%d", i)
		if err := dbs.TagMessage(ctx, "acct", key, fmt.Sprintf("t%d", i%20), TagSourceOperator); err != nil {
			b.Fatal(err)
		}
		if i < maxInboxLimit {
			keys = append(keys, key)
		}
	}

	b.ResetTimer()
	for range b.N {
		got, err := dbs.TagsForMessages(ctx, "acct", keys)
		if err != nil || len(got) != len(keys) {
			b.Fatalf("TagsForMessages = %d keys, %v", len(got), err)
		}
	}
}
