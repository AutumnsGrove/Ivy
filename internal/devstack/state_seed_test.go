package devstack_test

import (
	"context"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/store"
)

func populateDemo(t *testing.T, mode devstack.Mode) (*devstack.Stack, devstack.Options) {
	t.Helper()
	opts := prepareOpts(t)
	opts.Profile = "demo"
	opts.Mode = mode
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = stack.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := devstack.Populate(ctx, stack, opts); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	return stack, opts
}

func openDataDir(t *testing.T, stack *devstack.Stack) *store.DBs {
	t.Helper()
	dbs, err := store.Open(context.Background(), stack.Config.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbs.Close() })
	return dbs
}

func TestPopulateSeedsProfilesAndTags(t *testing.T) {
	t.Parallel()
	for _, mode := range []devstack.Mode{devstack.ModeFull, devstack.ModeFast} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			stack, _ := populateDemo(t, mode)
			dbs := openDataDir(t, stack)
			ctx := context.Background()

			accounts, err := dbs.ListAccounts(ctx)
			if err != nil || len(accounts) != 3 {
				t.Fatalf("accounts = %d, err %v", len(accounts), err)
			}
			names := map[string]bool{}
			for _, a := range accounts {
				if a.DisplayName == "" || a.Icon == "" {
					t.Errorf("account %s has no seeded name or icon: %+v", a.ID, a)
				}
				names[a.DisplayName] = true
			}
			if len(names) != 3 {
				t.Errorf("seeded names are not distinct: %v", names)
			}

			var tags, members, orphans int
			q := dbs.State.Read
			if err := q.QueryRow(`SELECT count(*) FROM tags`).Scan(&tags); err != nil {
				t.Fatal(err)
			}
			if err := q.QueryRow(`SELECT count(*) FROM message_tags`).Scan(&members); err != nil {
				t.Fatal(err)
			}
			if tags < 4 || members == 0 {
				t.Errorf("seeded %d tags and %d memberships", tags, members)
			}
			// Membership is by content key, so each key must name a real message.
			rows, err := q.Query(`SELECT account_id, content_key FROM message_tags`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var acct, key string
				if err := rows.Scan(&acct, &key); err != nil {
					t.Fatal(err)
				}
				var n int
				if err := dbs.Mirror.Read.QueryRow(`SELECT count(*) FROM messages WHERE account_id=? AND content_key=?`, acct, key).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n == 0 {
					orphans++
				}
			}
			if orphans != 0 {
				t.Errorf("%d memberships point at no message", orphans)
			}
		})
	}
}

// up runs Populate on every start, so seeding must not undo what the operator
// did in the UI since the last one.
func TestPopulateSeedsStateOnlyOnce(t *testing.T) {
	t.Parallel()
	stack, opts := populateDemo(t, devstack.ModeFast)
	ctx := context.Background()

	dbs := openDataDir(t, stack)
	id := stack.Config.Accounts[0].ID
	if err := dbs.SetAccountProfile(ctx, id, "My own name", "🔥"); err != nil {
		t.Fatal(err)
	}
	if err := dbs.UpsertTag(ctx, store.Tag{ID: "mine", Slug: "mine", Name: "mine"}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Close(); err != nil {
		t.Fatal(err)
	}

	pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := devstack.Populate(pctx, stack, opts); err != nil {
		t.Fatalf("second Populate: %v", err)
	}

	again, err := store.Open(ctx, stack.Config.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	got, err := again.GetAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "My own name" || got.Icon != "🔥" {
		t.Errorf("a restart overwrote the operator's profile: %+v", got)
	}
	var n int
	if err := again.State.Read.QueryRow(`SELECT count(*) FROM tags WHERE id='mine'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("operator's tag lost: n=%d err=%v", n, err)
	}
}
