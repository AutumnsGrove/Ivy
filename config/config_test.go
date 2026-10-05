package config

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != DefaultListen {
		t.Errorf("Listen = %q, want %q", cfg.Listen, DefaultListen)
	}
	if cfg.DataDir != DefaultDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, DefaultDataDir)
	}
}

func TestLoadYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ivy.yaml")
	write(t, path, `
listen: 127.0.0.1:9999
data_dir: /var/lib/ivy
accounts:
  - id: autumn
    address: me@example.com
    imap_host: imap.example.com
    imap_port: 993
    smtp_host: smtp.example.com
    smtp_port: 465
    username: me@example.com
    llm_enabled: true
    trusted_authserv_ids:
      - mx.example.net
      - mail.purelymail.com
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9999" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.DataDir != "/var/lib/ivy" {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
	if len(cfg.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(cfg.Accounts))
	}
	a := cfg.Accounts[0]
	if a.ID != "autumn" || a.Address != "me@example.com" || !a.LLMEnabled {
		t.Errorf("account = %+v", a)
	}
	if len(a.TrustedAuthservIDs) != 2 || a.TrustedAuthservIDs[0] != "mx.example.net" || a.TrustedAuthservIDs[1] != "mail.purelymail.com" {
		t.Errorf("TrustedAuthservIDs = %v, want the two configured ids", a.TrustedAuthservIDs)
	}
}

func TestAccountPasswordFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ivy.yaml")
	write(t, path, `
accounts:
  - id: autumn
    address: me@example.com
    imap_host: imap.example.com
    imap_port: 993
    smtp_host: smtp.example.com
    smtp_port: 465
    username: me@example.com
`)
	t.Setenv("IVY_AUTUMN_PASSWORD", "hunter2")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Accounts[0].Password; got != "hunter2" {
		t.Errorf("Password = %q, want from env", got)
	}
}

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".env"), "IVY_AUTUMN_PASSWORD=fromfile\n")
	path := filepath.Join(dir, "ivy.yaml")
	write(t, path, `
accounts:
  - id: autumn
    address: me@example.com
    imap_host: imap.example.com
    imap_port: 993
    smtp_host: smtp.example.com
    smtp_port: 465
    username: me@example.com
`)
	// Load exports the .env values into the process; t.Setenv makes the test
	// restore the variable afterwards so it cannot leak into other tests.
	t.Setenv("IVY_AUTUMN_PASSWORD", "")
	os.Unsetenv("IVY_AUTUMN_PASSWORD")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Accounts[0].Password; got != "fromfile" {
		t.Errorf("Password = %q, want fromfile", got)
	}
}

func TestInvalidAccountRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ivy.yaml")
	write(t, path, `
accounts:
  - id: ""
    address: me@example.com
`)

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for account with empty id")
	}
}

func TestInvalidListenRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ivy.yaml")
	write(t, path, "listen: not-an-address\n")

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for malformed listen address")
	}
}

func TestMissingConfigFileIsNotAnError(t *testing.T) {
	t.Parallel()
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A typo'd key must not silently fall back to a default: "data-dir" would put
// the mailbox mirror in ./data and "llm_enable" would quietly keep the LLM off.
func TestUnknownKeyRejected(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ivy.yaml")
	write(t, path, "data-dir: /var/lib/ivy\n")

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown config key")
	}
}

func TestListenPortRange(t *testing.T) {
	t.Parallel()
	for _, listen := range []string{"127.0.0.1:99999", "127.0.0.1:-1"} {
		path := filepath.Join(t.TempDir(), "ivy.yaml")
		write(t, path, "listen: "+listen+"\n")
		if _, err := Load(path); err == nil {
			t.Errorf("listen %q accepted, want error", listen)
		}
	}
}

func TestMalformedEnvFileRejected(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".env"), "this is = not [valid\nIVY_X='unterminated\n")
	path := filepath.Join(dir, "ivy.yaml")
	write(t, path, "listen: 127.0.0.1:9999\n")

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unparseable .env")
	}
}

// Two ids that sanitise to the same variable would share one password, which
// is a credential-mixup waiting to happen.
func TestAccountIDsCollidingOnPasswordEnvRejected(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ivy.yaml")
	acct := func(id string) string {
		return `
  - id: ` + id + `
    address: me@example.com
    imap_host: imap.example.com
    imap_port: 993
    smtp_host: smtp.example.com
    smtp_port: 465
    username: me@example.com`
	}
	write(t, path, "accounts:"+acct("my-mail")+acct("my_mail")+"\n")

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for ids sharing a password variable")
	}
}

func TestAllowedHostsLoadedAndListenHostAdded(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ivy.yaml")
	write(t, path, "listen: 100.64.0.7:8787\nallowed_hosts:\n  - Ivy.tail1234.ts.net\n  - \"::1\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.HostAllowList()
	want := []string{"ivy.tail1234.ts.net", "::1", "100.64.0.7"}
	if len(got) != len(want) {
		t.Fatalf("HostAllowList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("HostAllowList[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

// A wildcard listen address names no host, so it must not add one: the operator
// lists the names the phone really uses.
func TestWildcardListenAddsNoAllowedHost(t *testing.T) {
	t.Parallel()
	for _, listen := range []string{"0.0.0.0:8787", "[::]:8787"} {
		cfg := &Config{Listen: listen, DataDir: "d"}
		if got := cfg.HostAllowList(); len(got) != 0 {
			t.Errorf("listen %q: HostAllowList = %v, want none", listen, got)
		}
	}
}

func TestInvalidAllowedHostsRejected(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"http://ivy.ts.net", "ivy.ts.net:8787", "*.ts.net", "a/b", "", "has space"} {
		cfg := &Config{Listen: DefaultListen, DataDir: "d", AllowedHosts: []string{bad}}
		if err := cfg.validate(); err == nil {
			t.Errorf("allowed_hosts entry %q accepted, want an error", bad)
		}
	}
}

// 8787 is wrangler's (and so workerd's) default; on the operator's machine it is
// always taken, so Ivy defaulting to it made every first run fail to bind.
func TestDefaultListenAvoidsWranglersPort(t *testing.T) {
	t.Parallel()
	_, port, err := net.SplitHostPort(DefaultListen)
	if err != nil {
		t.Fatalf("DefaultListen %q: %v", DefaultListen, err)
	}
	if port == "8787" {
		t.Errorf("DefaultListen %q uses wrangler's port", DefaultListen)
	}
}

func TestLLMDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.OpenRouterBase != DefaultOpenRouterURL {
		t.Errorf("OpenRouterBase = %q, want %q", cfg.LLM.OpenRouterBase, DefaultOpenRouterURL)
	}
	if cfg.LLM.EmbedModel != DefaultEmbedModel {
		t.Errorf("EmbedModel = %q, want %q", cfg.LLM.EmbedModel, DefaultEmbedModel)
	}
	if cfg.LLM.MonthlyCapUSD != DefaultMonthlyCapUSD {
		t.Errorf("MonthlyCapUSD = %v, want %v", cfg.LLM.MonthlyCapUSD, DefaultMonthlyCapUSD)
	}
}

func TestEmbedProviderNeedsOptInForHosted(t *testing.T) {
	t.Parallel()
	base := Account{
		ID: "a", Address: "a@example.com", IMAPHost: "i", IMAPPort: 993,
		SMTPHost: "s", SMTPPort: 465, Username: "a",
	}

	hosted := base
	hosted.EmbedProvider = "openrouter"
	cfg := &Config{
		Listen: DefaultListen, DataDir: "d", Accounts: []Account{hosted},
		Backup: Backup{At: DefaultBackupAt}, LLM: LLM{OpenRouterBase: DefaultOpenRouterURL},
	}
	if err := cfg.validate(); err == nil {
		t.Error("openrouter embeddings without llm_enabled accepted, want an error")
	}

	hosted.LLMEnabled = true
	cfg.Accounts = []Account{hosted}
	if err := cfg.validate(); err != nil {
		t.Errorf("openrouter embeddings with llm_enabled rejected: %v", err)
	}

	local := base
	local.EmbedProvider = "ollama"
	local.LLMEnabled = false
	cfg.Accounts = []Account{local}
	if err := cfg.validate(); err != nil {
		t.Errorf("local ollama embeddings with smart features off rejected: %v", err)
	}

	bad := base
	bad.EmbedProvider = "magic"
	cfg.Accounts = []Account{bad}
	if err := cfg.validate(); err == nil {
		t.Error("unknown embed_provider accepted, want an error")
	}
}
