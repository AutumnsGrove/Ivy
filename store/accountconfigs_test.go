package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAccountConfigRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if got, err := dbs.AccountConfigs(ctx); err != nil || len(got) != 0 {
		t.Fatalf("fresh AccountConfigs = %v, %v; want none", got, err)
	}

	created := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	in := AccountConfig{
		ID: "purelymail", Address: "me@example.test", Username: "me@example.test",
		IMAPHost: "imap.example.test", IMAPPort: 993, SMTPHost: "smtp.example.test", SMTPPort: 465,
		LLMEnabled: true, EmbedProvider: "openrouter", CreatedAt: created,
	}
	if err := dbs.SaveAccountConfig(ctx, in); err != nil {
		t.Fatalf("SaveAccountConfig: %v", err)
	}
	got, err := dbs.AccountConfigs(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("AccountConfigs = %v, %v; want one", got, err)
	}
	if got[0] != in {
		t.Errorf("round trip changed the row:\n got %+v\nwant %+v", got[0], in)
	}
}

// Saving again (a changed address, say) must not reset when the account was
// first connected, the same rule UpsertAccount keeps for the mirror row.
func TestSaveAccountConfigKeepsTheOriginalCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	first := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	later := first.Add(48 * time.Hour)
	base := AccountConfig{
		ID: "purelymail", Address: "a@example.test", Username: "a@example.test",
		IMAPHost: "imap.example.test", IMAPPort: 993, SMTPHost: "smtp.example.test", SMTPPort: 465,
	}

	base.CreatedAt = first
	if err := dbs.SaveAccountConfig(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.Address, base.CreatedAt = "b@example.test", later
	if err := dbs.SaveAccountConfig(ctx, base); err != nil {
		t.Fatal(err)
	}
	got, err := dbs.AccountConfigs(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("AccountConfigs = %v, %v", got, err)
	}
	if got[0].Address != "b@example.test" {
		t.Errorf("Address = %q, want the update", got[0].Address)
	}
	if !got[0].CreatedAt.Equal(first) {
		t.Errorf("CreatedAt = %v, want the original %v", got[0].CreatedAt, first)
	}
}

// The table is in state.db, which is backed up and copied off the board, so it
// must have nowhere to put a password (STANDARDS.md 2).
func TestAccountConfigsTableHasNoSecretColumn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	rows, err := dbs.State.Read.QueryContext(ctx, `SELECT name FROM pragma_table_info('account_configs')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		n++
		for _, bad := range []string{"password", "secret", "token"} {
			if strings.Contains(strings.ToLower(name), bad) {
				t.Errorf("account_configs has a %q column", name)
			}
		}
	}
	if n == 0 {
		t.Fatal("account_configs does not exist")
	}
}

func TestSaveAccountConfigRejectsAnIncompleteAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	for name, mutate := range map[string]func(*AccountConfig){
		"no id":       func(a *AccountConfig) { a.ID = "" },
		"no address":  func(a *AccountConfig) { a.Address = "" },
		"no username": func(a *AccountConfig) { a.Username = "" },
		"no imap":     func(a *AccountConfig) { a.IMAPHost = "" },
		"bad port":    func(a *AccountConfig) { a.IMAPPort = 0 },
		"no smtp":     func(a *AccountConfig) { a.SMTPHost = "" },
	} {
		a := AccountConfig{
			ID: "purelymail", Address: "me@example.test", Username: "me@example.test",
			IMAPHost: "imap.example.test", IMAPPort: 993, SMTPHost: "smtp.example.test", SMTPPort: 465,
			CreatedAt: time.Now(),
		}
		mutate(&a)
		if err := dbs.SaveAccountConfig(ctx, a); err == nil {
			t.Errorf("%s: SaveAccountConfig accepted it", name)
		}
	}
}
