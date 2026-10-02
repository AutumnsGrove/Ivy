package devstack

import (
	"errors"
	"fmt"
	"os"

	"github.com/goccy/go-yaml"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

// Stack is a prepared dev stack: a freshly seeded mailworld, the loopback-only
// config it was built from, and the control socket `ivy-dev deliver` drives. It
// does not start Ivy or the web server, so Prepare stays fast and testable.
type Stack struct {
	World   *mailworld.World
	Config  *config.Config
	Seed    mailworld.SeedResult
	Root    string
	control *ControlServer
}

// Prepare builds the stack under opts.Root: starts mailworld, seeds the
// profile, writes the guarded ivy.yaml and serves the control socket. The
// mailworld starts empty every time, so the same seed gives the same mailbox
// (DEV.md section 8); the data directory is left for Ivy to manage.
func Prepare(opts Options) (*Stack, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	profile, err := mailworld.ParseProfile(opts.Profile)
	if err != nil {
		return nil, err
	}
	dataDir := DataDir(opts.Root)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("devstack: create %s: %w", dataDir, err)
	}

	w, err := mailworld.New()
	if err != nil {
		return nil, fmt.Errorf("devstack: start mailworld: %w", err)
	}
	res, err := mailworld.Seed(w, profile, mailworld.WithSeed(opts.Seed))
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	cfg, err := BuildConfig(w.IMAPAddr(), w.SMTPAddr(), res.Accounts, opts)
	if err != nil {
		_ = w.Close()
		return nil, err
	}

	sock := ControlPath(opts.Root)
	// A crashed run can leave the socket file behind; binding would then fail.
	_ = os.Remove(sock)
	srv, err := NewControlServer(w, sock)
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := WriteConfig(ConfigPath(opts.Root), cfg); err != nil {
		_ = srv.Close()
		_ = w.Close()
		return nil, err
	}
	recipe := Recipe{
		Profile:  opts.Profile,
		Seed:     opts.Seed,
		Mode:     opts.Mode,
		LLM:      opts.LLM,
		Accounts: opts.Accounts,
		Pair:     opts.Pair,
		LLMCap:   opts.LLMCap,
		Hash:     res.Hash,
	}
	if err := writeRecipe(RecipePath(opts.Root), recipe); err != nil {
		_ = srv.Close()
		_ = w.Close()
		return nil, err
	}
	return &Stack{World: w, Config: cfg, Seed: res, Root: opts.Root, control: srv}, nil
}

// PasswordEnv returns the IVY_<ID>_PASSWORD variables the Ivy process needs,
// since secrets are never written to ivy.yaml (STANDARDS.md section 4).
func (s *Stack) PasswordEnv() []string {
	env := make([]string, 0, len(s.Config.Accounts))
	for _, a := range s.Config.Accounts {
		env = append(env, config.PasswordEnv(a.ID)+"="+a.Password)
	}
	return env
}

// Close stops the control socket and the mailworld.
func (s *Stack) Close() error {
	return errors.Join(s.control.Close(), s.World.Close())
}

// Reset removes the built dev state (databases, config and control socket) but
// keeps the recipe and snapshots, so a restore or the next up rebuilds from
// the same seed in seconds (DEV.md section 8).
func Reset(root string) error {
	for _, path := range []string{DataDir(root), ConfigPath(root), ControlPath(root)} {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("devstack: reset %s: %w", path, err)
		}
	}
	return nil
}

// WriteConfig writes cfg as YAML with 0600 so the dev config cannot be read by
// other users; passwords are never included (the field is yaml:"-").
func WriteConfig(path string, cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("devstack: marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("devstack: write %s: %w", path, err)
	}
	return nil
}
