package store

import (
	"context"
	"testing"
	"time"
)

// Another copy carrying the keyword is judged on live rows only, ignoring case,
// and a row with no flags at all (NULL in the column) is not an error.
func TestAnotherRowCarriesKeyword(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct-1")
	seedFolder(t, dbs, "acct-1", "folder-1")
	seedFolder(t, dbs, "acct-1", "folder-2")

	put := func(id, folder string, uid uint32, flags []string, disabled bool) {
		t.Helper()
		m := Message{ID: id, AccountID: "acct-1", FolderID: folder, UID: uid, ContentKey: "ck", Flags: flags}
		if disabled {
			m.DisabledAt, m.DisabledReason = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), DisabledMoved
		}
		if err := dbs.UpsertMessage(ctx, m); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	put("self", "folder-1", 1, []string{"$ivy-work"}, false)
	put("bare", "folder-2", 1, nil, false)

	got, err := dbs.AnotherRowCarriesKeyword(ctx, "acct-1", "ck", "self", "$ivy-work")
	if err != nil || got {
		t.Fatalf("with only a flagless copy elsewhere: %v, %v; want false", got, err)
	}

	put("loud", "folder-2", 2, []string{`\Seen`, "$IVY-Work"}, false)
	if got, err := dbs.AnotherRowCarriesKeyword(ctx, "acct-1", "ck", "self", "$ivy-work"); err != nil || !got {
		t.Errorf("a copy carrying $IVY-Work: %v, %v; want true", got, err)
	}
	if got, err := dbs.AnotherRowCarriesKeyword(ctx, "acct-1", "ck", "loud", "$ivy-work"); err != nil || !got {
		t.Errorf("excluding the other copy, self still carries it: %v, %v; want true", got, err)
	}
	if got, err := dbs.AnotherRowCarriesKeyword(ctx, "acct-2", "ck", "self", "$ivy-work"); err != nil || got {
		t.Errorf("another account: %v, %v; want false", got, err)
	}

	put("loud", "folder-2", 2, []string{"$ivy-work"}, true)
	if got, err := dbs.AnotherRowCarriesKeyword(ctx, "acct-1", "ck", "self", "$ivy-work"); err != nil || got {
		t.Errorf("a hidden copy counted: %v, %v; want false", got, err)
	}
}
