package devstack_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/internal/devstack"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

func prepareOpts(t *testing.T) devstack.Options {
	t.Helper()
	root, err := os.MkdirTemp("", "ivy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	o := devstack.DefaultOptions()
	o.Root = root
	o.Listen = "127.0.0.1:8787"
	return o
}

func TestPrepareWritesLoopbackConfigAndControl(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = stack.Close() })

	// The config on disk must pass the rails and contain only dev creds.
	raw, err := os.ReadFile(devstack.ConfigPath(opts.Root))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if err := devstack.ValidateConfig(&cfg, opts.Expose); err != nil {
		t.Fatalf("written config fails the rails: %v", err)
	}
	if cfg.DataDir != devstack.DataDir(opts.Root) {
		t.Errorf("data dir = %q, want %q", cfg.DataDir, devstack.DataDir(opts.Root))
	}
	if len(cfg.Accounts) != 3 {
		t.Fatalf("config has %d accounts, want 3", len(cfg.Accounts))
	}

	info, err := os.Stat(devstack.ConfigPath(opts.Root))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o, want 600", info.Mode().Perm())
	}

	// The control socket is live and drives the same world Prepare seeded.
	c, err := devstack.Dial(devstack.ControlPath(opts.Root))
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	defer c.Close()
	before := stack.World.Clock().Now()
	if err := c.AdvanceClock(time.Hour); err != nil {
		t.Fatalf("advance via control: %v", err)
	}
	if got := stack.World.Clock().Now(); !got.Equal(before.Add(time.Hour)) {
		t.Fatalf("control did not reach the prepared world")
	}
}

func TestPrepareIsDeterministicAndClean(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	first, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	hash := first.Seed.Hash
	if first.Seed.Delivered == 0 {
		t.Fatal("expected seeded messages")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	second, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if second.Seed.Hash != hash {
		t.Fatalf("hash changed between prepares: %s vs %s", second.Seed.Hash, hash)
	}
}

func TestResetRemovesDevState(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	stack, err := devstack.Prepare(opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := stack.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := devstack.Reset(opts.Root); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := os.Stat(devstack.DevDir(opts.Root)); !os.IsNotExist(err) {
		t.Fatalf("dev dir survived Reset: %v", err)
	}
}

func TestPrepareExposeBindsTailnetOnly(t *testing.T) {
	t.Parallel()
	opts := prepareOpts(t)
	opts.Expose = true
	opts.Listen = "127.0.0.1:8787"
	if _, err := devstack.Prepare(opts); err == nil {
		t.Fatal("Prepare accepted --expose on a loopback address")
	}
}

func TestPasswordEnvCarriesDevCredentials(t *testing.T) {
	t.Parallel()
	stack, err := devstack.Prepare(prepareOpts(t))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = stack.Close() })

	env := stack.PasswordEnv()
	if len(env) != len(stack.Config.Accounts) {
		t.Fatalf("got %d env vars, want %d", len(env), len(stack.Config.Accounts))
	}
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if value != mailworld.SeedPassword {
			t.Fatalf("unexpected credential value in %q", kv)
		}
		if !strings.HasPrefix(name, "IVY_") || !strings.HasSuffix(name, "_PASSWORD") {
			t.Fatalf("unexpected env name in %q", kv)
		}
	}
}
