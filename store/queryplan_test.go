package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// TestReadQueriesUseIndexes guards the large-table rule in STANDARDS.md
// section 4: every query that can run on a large table must use an index
// rather than scanning the messages table.
func TestReadQueriesUseIndexes(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)

	cases := []struct {
		name      string
		query     string
		args      []any
		wantIndex string
	}{
		{"inbox", inboxSelect, []any{"inbox", "", "", "[]", "", "", "", 50}, ""},
		{"inbox counts", inboxCountsSelect, []any{"inbox", "", "", "[]"}, ""},
		{
			"classify queue",
			"SELECT COUNT(DISTINCT m.content_key) " + classifiable,
			[]any{"acct", formatTime(time.Now())},
			// A looser account index would still pass the search check above, so name
			// the arrival expression index the pass depends on.
			"idx_messages_classify_arrival",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan := explain(t, dbs.Mirror.Read, tc.query, tc.args...)
			if strings.Contains(plan, "SCAN m ") || strings.Contains(plan, "SCAN messages") {
				t.Errorf("full table scan of messages:\n%s", plan)
			}
			if !strings.Contains(plan, "SEARCH") {
				t.Errorf("no index search in plan:\n%s", plan)
			}
			if tc.wantIndex != "" && !strings.Contains(plan, tc.wantIndex) {
				t.Errorf("plan does not use %s:\n%s", tc.wantIndex, plan)
			}
		})
	}
}

func explain(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan: %v", err)
	}
	return strings.Join(lines, "\n")
}
