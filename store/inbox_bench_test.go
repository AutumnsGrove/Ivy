package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// BenchmarkListInbox measures the hot read path the inbox endpoint serves. The
// 2a note flagged the ORDER BY's temp b-tree as a benchmark item for 2f; this
// is the number to compare against when the index changes.
func BenchmarkListInbox(b *testing.B) {
	ctx := context.Background()
	dbs, err := Open(ctx, b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = dbs.Close() })

	if err := dbs.UpsertAccount(ctx, Account{ID: "acct", Address: "me@example.com", CreatedAt: time.Now()}); err != nil {
		b.Fatal(err)
	}
	if err := dbs.UpsertFolder(ctx, Folder{ID: "inbox", AccountID: "acct", Name: "INBOX", Role: RoleInbox}); err != nil {
		b.Fatal(err)
	}
	seedBenchMessages(b, dbs, 10000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := dbs.ListInbox(ctx, InboxQuery{}); err != nil {
			b.Fatalf("ListInbox: %v", err)
		}
	}
}

// seedBenchMessages inserts n messages in one transaction, which is how a
// batched sync writes; the point is to time the query, not the inserts.
func seedBenchMessages(b *testing.B, dbs *DBs, n int) {
	b.Helper()
	tx, err := dbs.Mirror.Write.BeginTx(context.Background(), nil)
	if err != nil {
		b.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(context.Background(),
		`INSERT INTO messages (id, account_id, folder_id, uid, content_key, subject, date, seen, body_status)
		 VALUES (?, 'acct', 'inbox', ?, ?, ?, ?, 0, 'ok')`)
	if err != nil {
		b.Fatalf("prepare: %v", err)
	}
	defer func() { _ = stmt.Close() }()
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("m%06d", i)
		if _, err := stmt.ExecContext(context.Background(),
			id, i+1, "ck:"+id, "Subject "+id, formatTime(base.Add(time.Duration(i)*time.Minute))); err != nil {
			b.Fatalf("insert %s: %v", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit: %v", err)
	}
}
