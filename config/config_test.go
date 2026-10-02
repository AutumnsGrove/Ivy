package config

import (
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
