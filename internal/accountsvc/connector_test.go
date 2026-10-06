package accountsvc_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/gateway"
	"github.com/AutumnsGrove/Ivy/internal/accountsvc"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/internal/secrets"
	"github.com/AutumnsGrove/Ivy/store"
)

type rig struct {
	w          *mailworld.World
	dbs        *store.DBs
	secretsDir string
	existing   []config.Account
	c          *accountsvc.Connector
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{w: newWorld(t), dbs: newStore(t), secretsDir: filepath.Join(t.TempDir(), "secrets")}
	r.w.Account("me@grove.test", "secret").Deliver("INBOX", []byte(rawMessage))
	r.c = accountsvc.NewConnector(accountsvc.ConnectorOptions{
		DBs:        r.dbs,
		Supervisor: startSupervisor(t, r.dbs),
		SecretsDir: r.secretsDir,
		Provider:   provider(t, r.w),
		Existing:   func() []config.Account { return r.existing },
		Now:        func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) },
	})
	return r
}

func connectCode(t *testing.T, err error) string {
	t.Helper()
	var ce *gateway.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a *gateway.ConnectError", err)
	}
	return ce.Code
}

func TestConnectStoresEverythingAndStartsSyncing(t *testing.T) {
	t.Parallel()
	r := newRig(t)

	id, err := r.c.Connect(context.Background(), "me@grove.test", "secret", false)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if id != "purelymail" {
		t.Errorf("id = %q, want purelymail", id)
	}
	if pw, err := secrets.Read(r.secretsDir, id); err != nil || pw != "secret" {
		t.Errorf("stored password = %q, %v", pw, err)
	}
	cfgs, err := r.dbs.AccountConfigs(context.Background())
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("AccountConfigs = %v, %v", cfgs, err)
	}
	p := provider(t, r.w)
	got := cfgs[0]
	if got.Address != "me@grove.test" || got.Username != "me@grove.test" ||
		got.IMAPHost != p.IMAPHost || got.IMAPPort != p.IMAPPort || got.LLMEnabled || got.EmbedProvider != "" {
		t.Errorf("config = %+v", got)
	}
	eventually(t, "the first sync", func() bool {
		s, err := r.dbs.GetSyncState(context.Background(), id)
		return err == nil && s.Status == store.SyncOK && s.BackfillDone == 1
	})
}

func TestConnectWithSmartFeaturesTurnsOnHostedEmbeddings(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if _, err := r.c.Connect(context.Background(), "me@grove.test", "secret", true); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	cfgs, _ := r.dbs.AccountConfigs(context.Background())
	if len(cfgs) != 1 || !cfgs[0].LLMEnabled || cfgs[0].EmbedProvider != "openrouter" {
		t.Errorf("config = %+v, want llm_enabled with the openrouter embedder", cfgs)
	}
}

// Probe first, persist second: a mistyped password must leave no secret, no
// config row and no worker behind.
func TestConnectWithAWrongPasswordPersistsNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)

	_, err := r.c.Connect(context.Background(), "me@grove.test", "wrong-password", false)
	if code := connectCode(t, err); code != "auth_failed" {
		t.Errorf("code = %q, want auth_failed (err: %v)", code, err)
	}
	if _, err := secrets.Read(r.secretsDir, "purelymail"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("a password was stored: %v", err)
	}
	if cfgs, _ := r.dbs.AccountConfigs(context.Background()); len(cfgs) != 0 {
		t.Errorf("a config row was stored: %+v", cfgs)
	}
	time.Sleep(150 * time.Millisecond) // a stray worker would have recorded a state by now
	if _, err := r.dbs.GetSyncState(context.Background(), "purelymail"); err == nil {
		t.Error("a worker was started for a failed connect")
	}
}

func TestConnectToAnUnreachableProviderSaysSo(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	dbs := newStore(t)
	c := accountsvc.NewConnector(accountsvc.ConnectorOptions{
		DBs: dbs, Supervisor: startSupervisor(t, dbs), SecretsDir: t.TempDir(),
		Provider: accountsvc.Provider{IMAPHost: "127.0.0.1", IMAPPort: port, SMTPHost: "127.0.0.1", SMTPPort: 1, Insecure: true},
	})

	_, err = c.Connect(context.Background(), "me@grove.test", "secret", false)
	if code := connectCode(t, err); code != "unreachable" {
		t.Errorf("code = %q, want unreachable (err: %v)", code, err)
	}
}

func TestConnectRefusesAnAddressThatIsAlreadyConnected(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if _, err := r.c.Connect(context.Background(), "me@grove.test", "secret", false); err != nil {
		t.Fatal(err)
	}
	// Mixed case: the same mailbox must not be mirrored twice.
	_, err := r.c.Connect(context.Background(), "Me@Grove.test", "secret", false)
	if !errors.Is(err, gateway.ErrAlreadyConnected) {
		t.Errorf("err = %v, want ErrAlreadyConnected", err)
	}

	r2 := newRig(t)
	r2.existing = []config.Account{{ID: "autumn", Address: "me@grove.test"}}
	_, err = r2.c.Connect(context.Background(), "me@grove.test", "secret", false)
	if !errors.Is(err, gateway.ErrAlreadyConnected) {
		t.Errorf("an ivy.yaml account did not count: %v", err)
	}
}

func TestASecondMailboxGetsItsOwnID(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.w.Account("two@grove.test", "secret2")
	if _, err := r.c.Connect(context.Background(), "me@grove.test", "secret", false); err != nil {
		t.Fatal(err)
	}
	id, err := r.c.Connect(context.Background(), "two@grove.test", "secret2", false)
	if err != nil {
		t.Fatalf("second Connect: %v", err)
	}
	if id != "purelymail-2" {
		t.Errorf("id = %q, want purelymail-2", id)
	}
}

func TestUpdatePasswordKeepsTheOldOneWhenTheNewOneFails(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	id, err := r.c.Connect(context.Background(), "me@grove.test", "secret", false)
	if err != nil {
		t.Fatal(err)
	}

	err = r.c.UpdatePassword(context.Background(), id, "not-it")
	if code := connectCode(t, err); code != "auth_failed" {
		t.Errorf("code = %q, want auth_failed (err: %v)", code, err)
	}
	if pw, _ := secrets.Read(r.secretsDir, id); pw != "secret" {
		t.Errorf("stored password = %q, want the old one kept", pw)
	}
}

func TestUpdatePasswordReplacesTheStoredPassword(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	id, err := r.c.Connect(context.Background(), "me@grove.test", "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	// Plant a stale password the way a change at the provider would leave it.
	if err := secrets.Write(r.secretsDir, id, "stale"); err != nil {
		t.Fatal(err)
	}

	if err := r.c.UpdatePassword(context.Background(), id, "secret"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	if pw, _ := secrets.Read(r.secretsDir, id); pw != "secret" {
		t.Errorf("stored password = %q, want the new one", pw)
	}
	info, err := os.Stat(filepath.Join(r.secretsDir, id))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("secret file = %v, %v; want mode 600", info, err)
	}
}

// An account written in ivy.yaml can be fixed from the app too, which is what
// the "Update password" banner offers for any account.
func TestUpdatePasswordWorksForAnIvyYAMLAccount(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	p := provider(t, r.w)
	r.existing = []config.Account{{
		ID: "autumn", Address: "me@grove.test", Username: "me@grove.test",
		IMAPHost: p.IMAPHost, IMAPPort: p.IMAPPort, SMTPHost: p.SMTPHost, SMTPPort: p.SMTPPort, Insecure: true,
	}}

	if err := r.c.UpdatePassword(context.Background(), "autumn", "secret"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	if pw, err := secrets.Read(r.secretsDir, "autumn"); err != nil || pw != "secret" {
		t.Errorf("stored password = %q, %v", pw, err)
	}
	eventually(t, "the account to sync", func() bool { return syncStatus(r.dbs, "autumn") == store.SyncOK })
}

func TestUpdatePasswordForAnUnknownAccountIsNotFound(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if err := r.c.UpdatePassword(context.Background(), "nobody", "secret"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, want store.ErrNotFound", err)
	}
}
