package devstack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

func TestIsLoopbackHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.9.9.9", true},
		{"::1", true},
		{"localhost", true},
		{"LOCALHOST", true},
		{"100.64.0.1", false},
		{"0.0.0.0", false},
		{"example.com", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := devstack.IsLoopbackHost(tc.host); got != tc.want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestIsTailnetHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host string
		want bool
	}{
		{"100.64.0.1", true},
		{"100.127.255.254", true},
		{"100.128.0.1", false},
		{"100.63.0.1", false},
		{"127.0.0.1", false},
		{"example.com", false},
	}
	for _, tc := range cases {
		if got := devstack.IsTailnetHost(tc.host); got != tc.want {
			t.Errorf("IsTailnetHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestValidateListen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		addr    string
		expose  bool
		wantErr bool
	}{
		{"loopback closed", "127.0.0.1:8787", false, false},
		{"tailnet exposed", "100.64.0.1:8787", true, false},
		{"wildcard refused", "0.0.0.0:8787", false, true},
		{"public refused", "203.0.113.5:8787", false, true},
		{"tailnet without expose refused", "100.64.0.1:8787", false, true},
		{"loopback with expose refused", "127.0.0.1:8787", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := devstack.ValidateListen(tc.addr, tc.expose)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateListen(%q, %v) err = %v, wantErr %v", tc.addr, tc.expose, err, tc.wantErr)
			}
		})
	}
}

func TestOptionsValidate(t *testing.T) {
	t.Parallel()
	good := devstack.DefaultOptions()
	good.Root = t.TempDir()
	good.Listen = "127.0.0.1:8787"
	good.Accounts = 3

	if err := good.Validate(); err != nil {
		t.Fatalf("default options invalid: %v", err)
	}

	bad := []struct {
		name string
		opt  func(o *devstack.Options)
	}{
		{"unknown profile", func(o *devstack.Options) { o.Profile = "huge" }},
		{"unknown mode", func(o *devstack.Options) { o.Mode = "turbo" }},
		{"unknown llm", func(o *devstack.Options) { o.LLM = "cloud" }},
		{"negative accounts", func(o *devstack.Options) { o.Accounts = -1 }},
		{"bad listen", func(o *devstack.Options) { o.Listen = "nope" }},
		{"bad root", func(o *devstack.Options) { o.Root = "" }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := good
			tc.opt(&o)
			if err := o.Validate(); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestDevPathsStayUnderRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, path := range []string{
		devstack.DevDir(root),
		devstack.DataDir(root),
		devstack.ConfigPath(root),
		devstack.ControlPath(root),
	} {
		if !strings.HasPrefix(path, filepath.Join(root, devstack.DevDirName)) {
			t.Errorf("%s is not under %s", path, filepath.Join(root, devstack.DevDirName))
		}
	}
}

func seededAccounts(t *testing.T) (*mailworld.World, mailworld.SeedResult) {
	t.Helper()
	w, err := mailworld.New()
	if err != nil {
		t.Fatalf("new world: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	res, err := mailworld.Seed(w, mailworld.Demo(), mailworld.WithSeed(7))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return w, res
}

func buildOpts(t *testing.T, accounts int, pair bool) devstack.Options {
	t.Helper()
	o := devstack.DefaultOptions()
	o.Root = t.TempDir()
	o.Listen = "127.0.0.1:8787"
	o.Accounts = accounts
	o.Pair = pair
	return o
}

func TestBuildConfigIsLoopbackOnly(t *testing.T) {
	t.Parallel()
	w, res := seededAccounts(t)
	opts := buildOpts(t, 3, false)

	cfg, err := devstack.BuildConfig(w.IMAPAddr(), w.SMTPAddr(), res.Accounts, opts)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if err := devstack.ValidateConfig(cfg, opts.Expose); err != nil {
		t.Fatalf("generated config must pass the rails: %v", err)
	}
	if len(cfg.Accounts) != 3 {
		t.Fatalf("got %d accounts, want 3", len(cfg.Accounts))
	}
	for _, a := range cfg.Accounts {
		if a.Password != mailworld.SeedPassword {
			t.Errorf("account %s password = %q, want the dev seed password", a.ID, a.Password)
		}
		if !devstack.IsLoopbackHost(a.IMAPHost) || !devstack.IsLoopbackHost(a.SMTPHost) {
			t.Errorf("account %s points outside loopback: %s / %s", a.ID, a.IMAPHost, a.SMTPHost)
		}
	}
	if cfg.DataDir != devstack.DataDir(opts.Root) {
		t.Errorf("data dir = %q, want %q", cfg.DataDir, devstack.DataDir(opts.Root))
	}
}

func TestBuildConfigRejectsNonLoopbackMailHost(t *testing.T) {
	t.Parallel()
	w, res := seededAccounts(t)
	opts := buildOpts(t, 3, false)

	_, err := devstack.BuildConfig("mail.example.com:993", w.SMTPAddr(), res.Accounts, opts)
	if err == nil {
		t.Fatal("BuildConfig accepted a non-loopback IMAP host")
	}
}

func TestValidateConfigRejectsRealMailbox(t *testing.T) {
	t.Parallel()
	w, res := seededAccounts(t)
	opts := buildOpts(t, 3, false)
	cfg, err := devstack.BuildConfig(w.IMAPAddr(), w.SMTPAddr(), res.Accounts, opts)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	cfg.Accounts[0].IMAPHost = "imap.example.com"
	if err := devstack.ValidateConfig(cfg, opts.Expose); err == nil {
		t.Fatal("ValidateConfig accepted a real IMAP host")
	}
}

func TestBuildConfigIgnoresEnvPasswords(t *testing.T) {
	w, res := seededAccounts(t)
	opts := buildOpts(t, 3, false)

	// A real credential sitting in the environment must never reach dev mail.
	t.Setenv("IVY_IVY_A_PASSWORD", "real-secret")
	cfg, err := devstack.BuildConfig(w.IMAPAddr(), w.SMTPAddr(), res.Accounts, opts)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	for _, a := range cfg.Accounts {
		if a.Password != mailworld.SeedPassword {
			t.Fatalf("account %s picked up %q from the environment", a.ID, a.Password)
		}
	}
}

func TestReadEnvOnlyAllowsListedKeys(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	body := "MAIL_PASSWORD=real-secret\nIMAP_HOST=mail.example.com\nOPENROUTER_API_KEY=sk-dev\n"
	if err := os.WriteFile(env, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := devstack.ReadEnv(env, "OPENROUTER_API_KEY")
	if err != nil {
		t.Fatalf("ReadEnv: %v", err)
	}
	if got["OPENROUTER_API_KEY"] != "sk-dev" {
		t.Fatalf("missing OpenRouter key: %v", got)
	}
	if _, ok := got["MAIL_PASSWORD"]; ok {
		t.Fatal("ReadEnv leaked a mail credential")
	}
	if _, ok := got["IMAP_HOST"]; ok {
		t.Fatal("ReadEnv leaked a mail host")
	}
}

func TestReadEnvMissingFileIsEmpty(t *testing.T) {
	t.Parallel()
	got, err := devstack.ReadEnv(filepath.Join(t.TempDir(), "absent"), "OPENROUTER_API_KEY")
	if err != nil {
		t.Fatalf("ReadEnv on a missing file: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestAccountsFrom(t *testing.T) {
	t.Parallel()
	_, res := seededAccounts(t)

	two, err := devstack.AccountsFrom(res.Accounts, buildOpts(t, 0, true))
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if len(two) != 2 {
		t.Fatalf("pair selected %d accounts, want 2", len(two))
	}

	one, err := devstack.AccountsFrom(res.Accounts, buildOpts(t, 1, false))
	if err != nil {
		t.Fatalf("accounts=1: %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("accounts=1 selected %d, want 1", len(one))
	}

	if _, err := devstack.AccountsFrom(res.Accounts, buildOpts(t, 99, false)); err == nil {
		t.Fatal("expected an error for more accounts than the profile seeds")
	}
}
