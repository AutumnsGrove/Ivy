package sync_test

import (
	"context"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

const spamHeader = "No, score=-3.03 required=5 tests=DKIM_SIGNED=0.1,DKIM_VALID=-0.1 autolearn=ham"

// spamFixture mirrors one message the way Purelymail writes it: its own X-Spam-Status
// and an Authentication-Results line naming purelymail.com.
func spamFixture(t *testing.T) (*ivysync.Fetcher, ivysync.Account, *store.DBs, store.Message) {
	t.Helper()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("scored").
		Date(time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)).MessageID("<sp@grove.test>").Text("hello").
		Header("X-Spam-Status", spamHeader).
		Header("Authentication-Results", "purelymail.com; spf=pass; dkim=pass; dmarc=pass").Build())
	account := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	account.TrustedAuthservIDs = []string{"purelymail.com"}
	f := ivysync.NewFetcher(dbs)
	if _, err := f.Fetch(ctx, account); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return f, account, dbs, mustMessage(t, dbs, mustFolder(t, dbs, "acct-1", "INBOX").ID, uid)
}

func TestFetchKeepsTheScoreOfATrustedServer(t *testing.T) {
	t.Parallel()
	_, _, _, m := spamFixture(t)
	a := m.AuthResults
	if !a.SpamPresent || a.SpamFlagged || a.SpamScore != -3.03 || a.SpamRequired != 5 {
		t.Errorf("stored spam = %+v, want a clean -3.03 against 5", a)
	}
}

// Mail mirrored before the score was read heals through the re-derive pass: the
// version moves, so every older row is parsed again from its raw message.
func TestRederiveGivesOlderMailItsScore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, dbs, m := spamFixture(t)
	// As it was stored by the previous pipeline: version 1, no score in the column.
	if _, err := dbs.Mirror.Write.ExecContext(ctx,
		`UPDATE messages SET derived_version = 1, auth_results = ? WHERE id = ?`,
		`{"authserv_id":"purelymail.com","spf":"pass","dkim":"pass","dmarc":"pass"}`, m.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := f.Rederive(ctx, "acct-1", 10); err != nil || n != 1 {
		t.Fatalf("Rederive = %d, %v; want 1 healed (a row at the previous version is behind)", n, err)
	}
	got, err := dbs.GetMessage(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AuthResults.SpamPresent || got.AuthResults.SpamScore != -3.03 {
		t.Errorf("after re-derive: %+v, want the score back", got.AuthResults)
	}
	if got.AuthResults.SPF != "pass" || got.AuthResults.AuthservID != "purelymail.com" {
		t.Errorf("re-deriving lost the verdicts: %+v", got.AuthResults)
	}
}

// Re-deriving must not erase what a trusted fetch stored when it does not know the
// account's trust: parsing with no trusted ids would blank every verdict.
func TestRederiveWithoutKnownTrustLeavesTheVerdictsAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, _, dbs, m := spamFixture(t)
	if _, err := dbs.Mirror.Write.ExecContext(ctx, `UPDATE messages SET derived_version = 0 WHERE id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	other := ivysync.NewFetcher(dbs) // has never fetched this account, so it knows no trusted ids
	if n, err := other.Rederive(ctx, "acct-1", 10); err != nil || n != 1 {
		t.Fatalf("Rederive = %d, %v", n, err)
	}
	got, err := dbs.GetMessage(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthResults.SPF != "pass" || !got.AuthResults.SpamPresent {
		t.Errorf("re-deriving without trust wiped the stored verdicts: %+v", got.AuthResults)
	}
}

// A sender can write X-Spam-Status, so with no trusted verdict the score is not kept.
func TestAForgedScoreWithNoTrustedServerIsNotKept(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	acc := w.Account("me@grove.test", "secret")
	dbs := newStore(t)
	uid := acc.Deliver("INBOX", mailworld.Msg().From("a@example.com").Subject("forged").
		Date(time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)).MessageID("<fg@grove.test>").Text("hello").
		Header("X-Spam-Status", "No, score=-99 required=5").
		Header("Authentication-Results", "evil.example; spf=pass; dkim=pass; dmarc=pass").Build())
	account := accountFor(t, w, "acct-1", "me@grove.test", "secret")
	account.TrustedAuthservIDs = []string{"purelymail.com"}
	if _, err := ivysync.NewFetcher(dbs).Fetch(ctx, account); err != nil {
		t.Fatal(err)
	}
	m := mustMessage(t, dbs, mustFolder(t, dbs, "acct-1", "INBOX").ID, uid)
	if m.AuthResults.SpamPresent {
		t.Errorf("a forged score was kept: %+v", m.AuthResults)
	}
}
