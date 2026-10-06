package accountsvc_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/internal/accountsvc"
	"github.com/AutumnsGrove/Ivy/internal/secrets"
	"github.com/AutumnsGrove/Ivy/store"
)

func storedRow(id, address string) store.AccountConfig {
	return store.AccountConfig{
		ID: id, Address: address, Username: address,
		IMAPHost: "imap.example.test", IMAPPort: 993, SMTPHost: "smtp.example.test", SMTPPort: 465,
		LLMEnabled: true, EmbedProvider: "openrouter", CreatedAt: time.Now(),
	}
}

// After a restart the account typed into the app must come back with its
// password, with no ivy.yaml entry and no environment variable.
func TestStoredAccountsComeBackAfterARestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := newStore(t)
	cfg := &config.Config{DataDir: t.TempDir()}
	if err := dbs.SaveAccountConfig(ctx, storedRow("purelymail", "me@example.test")); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Write(cfg.SecretsDir(), "purelymail", "hunter2"); err != nil {
		t.Fatal(err)
	}

	got, err := accountsvc.StoredAccounts(ctx, dbs, cfg)
	if err != nil || len(got) != 1 {
		t.Fatalf("StoredAccounts = %v, %v", got, err)
	}
	a := got[0]
	if a.ID != "purelymail" || a.Password != "hunter2" || a.IMAPHost != "imap.example.test" ||
		!a.LLMEnabled || a.EmbedProvider != "openrouter" || a.Insecure {
		t.Errorf("account = %+v", a)
	}
}

func TestStoredAccountsLeaveAnIvyYAMLAccountAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := newStore(t)
	cfg := &config.Config{DataDir: t.TempDir(), Accounts: []config.Account{{ID: "purelymail", Address: "yaml@example.test"}}}
	if err := dbs.SaveAccountConfig(ctx, storedRow("purelymail", "app@example.test")); err != nil {
		t.Fatal(err)
	}
	got, err := accountsvc.StoredAccounts(ctx, dbs, cfg)
	if err != nil || len(got) != 0 {
		t.Errorf("StoredAccounts = %v, %v; want none (ivy.yaml wins)", got, err)
	}
}

// A missing or unusable password file must not make the account disappear: it
// comes back with no password, so sync says auth_failed and the banner offers
// "Update password".
func TestStoredAccountWithoutAUsablePasswordStillComesBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := newStore(t)
	cfg := &config.Config{DataDir: t.TempDir()}
	for _, id := range []string{"purelymail", "purelymail-2"} {
		if err := dbs.SaveAccountConfig(ctx, storedRow(id, id+"@example.test")); err != nil {
			t.Fatal(err)
		}
	}
	// purelymail has no file at all; purelymail-2 has one others can read.
	if err := secrets.Write(cfg.SecretsDir(), "purelymail-2", "hunter2"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(cfg.SecretsDir(), "purelymail-2"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := accountsvc.StoredAccounts(ctx, dbs, cfg)
	if err != nil || len(got) != 2 {
		t.Fatalf("StoredAccounts = %v, %v; want both accounts", got, err)
	}
	for _, a := range got {
		if a.Password != "" {
			t.Errorf("%s has a password %q, want none", a.ID, a.Password)
		}
	}
}
