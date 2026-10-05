package sync_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/rules"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// settleFixture mirrors n visible messages into one account, straight into the
// store (no IMAP), so the benchmark times what Settle does to a mailbox of that
// size and nothing else. Senders repeat (about one per six messages), as they do
// in a real mailbox, so People has real aggregation to do.
func settleFixture(b *testing.B, n int) (*store.DBs, string) {
	b.Helper()
	ctx := context.Background()
	dbs, err := store.Open(ctx, b.TempDir())
	if err != nil {
		b.Fatalf("store: %v", err)
	}
	b.Cleanup(func() { _ = dbs.Close() })
	if err := dbs.UpsertAccount(ctx, store.Account{ID: "acct", Address: "me@example.test"}); err != nil {
		b.Fatal(err)
	}
	if err := dbs.UpsertFolder(ctx, store.Folder{ID: "inbox", AccountID: "acct", Name: "INBOX", Role: store.RoleInbox}); err != nil {
		b.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		sender := fmt.Sprintf("sender%d@example.test", i%(n/6+1))
		if err := dbs.UpsertMessage(ctx, store.Message{
			ID: fmt.Sprintf("m%06d", i), AccountID: "acct", FolderID: "inbox", UID: uint32(i + 1),
			ContentKey: fmt.Sprintf("ck%06d", i), MessageID: fmt.Sprintf("<m%d@example.test>", i),
			Subject: fmt.Sprintf("Subject number %d about the quarterly garden", i),
			BodyText: "A short body of about the length of a typical message. " +
				"It mentions invoices, newsletters and lunch plans for the week.",
			Snippet: "A short body", From: store.Address{Name: "Sender", Address: sender},
			To:   []store.Address{{Address: "me@example.test"}},
			Date: start.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			b.Fatal(err)
		}
	}
	return dbs, "acct"
}

// BenchmarkSettleSteadyState is the cost of a settle pass when a sync stored
// nothing new, which is most of them (N36, N38, N40 in papercuts.md). Each
// sub-benchmark is one pass on its own, so the dear one is visible; "settle" is
// the whole call. Run it at 1k and 5k on the board and extrapolate; never the
// 100k profile (PERFORMANCE.md).
//
//	GOOS=linux GOARCH=arm64 go test -c -o settle.test ./sync
//	./settle.test -test.run XXX -test.bench SettleSteadyState -test.benchtime 5x
func BenchmarkSettleSteadyState(b *testing.B) {
	for _, n := range []int{1000, 5000} {
		b.Run(fmt.Sprintf("%dmsgs", n), func(b *testing.B) {
			ctx := context.Background()
			dbs, acct := settleFixture(b, n)
			f := ivysync.NewFetcher(dbs)
			// Settle until the backlogs are drained (the search backfill takes 500
			// messages a pass and the rule pass 200), so that what is timed is the
			// steady state and not a mailbox still being worked through.
			for range n/100 + 3 {
				if err := f.Settle(ctx, acct); err != nil {
					b.Fatal(err)
				}
			}
			if left, err := dbs.IndexMissingSearchDocs(ctx, acct, 1); err != nil || left != 0 {
				b.Fatalf("search backlog not drained: %d, %v", left, err)
			}
			if left, err := dbs.RuleMessages(ctx, acct, 1, true); err != nil || len(left) != 0 {
				b.Fatalf("rule backlog not drained: %d, %v", len(left), err)
			}

			run := func(name string, fn func() error) {
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						if err := fn(); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
			run("settle", func() error { return f.Settle(ctx, acct) })
			run("people", func() error { return dbs.RebuildPeople(ctx, acct) })
			run("rules", func() error {
				_, err := rules.NewApplier(dbs, nil, nil).Evaluate(ctx, acct, store.MaxRuleEvalBatch)
				return err
			})
			run("search-backfill", func() error {
				_, err := dbs.IndexMissingSearchDocs(ctx, acct, 500)
				return err
			})
		})
	}
}
