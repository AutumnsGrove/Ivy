// Package devstack builds and guards the local development stack. It turns a
// profile and launch options into a mailworld and an Ivy config that can only
// ever point at loopback, and it never reads real mail credentials from .env
// (DEV.md section 6). The ivy-dev CLI is a thin shell over this package.
package devstack

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

// DevDirName is the git-ignored directory every dev artefact lives under.
const DevDirName = ".dev"

// Mode selects how Ivy is populated (DEV.md section 2).
type Mode string

const (
	// ModeFull builds the dev stack by running the real sync against mailworld.
	ModeFull Mode = "full"
	// ModeFast seeds the databases directly, skipping the sync (chunk 2h).
	ModeFast Mode = "fast"
)

// LLM selects the language-model provider (DEV.md section 5).
type LLM string

const (
	// LLMLive uses real OpenRouter behind the spend cap.
	LLMLive LLM = "live"
	// LLMFake uses the deterministic fake provider from mailworld.
	LLMFake LLM = "fake"
)

// Options are the resolved flags for one dev-stack launch.
type Options struct {
	Root     string
	Profile  string
	Seed     int64
	Mode     Mode
	LLM      LLM
	Accounts int
	Pair     bool
	Expose   bool
	LLMCap   float64
	Listen   string
}

// DefaultOptions is the everyday `make dev` shape: the demo profile (three
// accounts), real OpenRouter behind a low dev cap. Accounts 0 means "every
// account the profile seeds", so `--profile minimal` needs no extra flag.
func DefaultOptions() Options {
	return Options{
		Profile:  "demo",
		Seed:     1,
		Mode:     ModeFull,
		LLM:      LLMLive,
		Accounts: 0,
		LLMCap:   1,
	}
}

// Validate checks the options against the profiles and modes DEV.md defines.
func (o Options) Validate() error {
	if o.Root == "" {
		return errors.New("devstack: root is empty")
	}
	if _, err := mailworld.ParseProfile(o.Profile); err != nil {
		return err
	}
	if o.Mode != ModeFull && o.Mode != ModeFast {
		return fmt.Errorf("devstack: unknown mode %q", o.Mode)
	}
	if o.LLM != LLMLive && o.LLM != LLMFake {
		return fmt.Errorf("devstack: unknown llm %q", o.LLM)
	}
	if o.Accounts < 0 {
		return fmt.Errorf("devstack: accounts must not be negative, got %d", o.Accounts)
	}
	if o.Pair && o.Accounts < 2 {
		return errors.New("devstack: --pair needs at least two accounts")
	}
	if err := ValidateListen(o.Listen, o.Expose); err != nil {
		return err
	}
	return nil
}

// IsLoopbackHost reports whether host is a loopback address: "localhost", an
// IPv4 127.0.0.0/8, or ::1.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// tailnet is Tailscale's CGNAT range; the only non-loopback interface
// --expose may bind (DEV.md section 6).
var tailnet = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// IsTailnetHost reports whether host is in Tailscale's CGNAT range
// (100.64.0.0/10), the only non-loopback interface --expose may bind.
func IsTailnetHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && tailnet.Contains(ip)
}

// ValidateListen enforces the DEV.md section 6 exposure rail: loopback by
// default, the tailnet only with --expose.
func ValidateListen(addr string, expose bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("devstack: listen %q is not host:port: %w", addr, err)
	}
	if expose {
		if !IsTailnetHost(host) {
			return fmt.Errorf("devstack: --expose must bind the tailnet, not %q", host)
		}
		return nil
	}
	if !IsLoopbackHost(host) {
		return fmt.Errorf("devstack: refusing to bind non-loopback host %q without --expose", host)
	}
	return nil
}

// DevDir returns the stack's git-ignored data root under the repo root.
func DevDir(root string) string { return filepath.Join(root, DevDirName) }

// DataDir is where the dev databases live.
func DataDir(root string) string { return filepath.Join(DevDir(root), "data") }

// ConfigPath is the generated ivy.yaml the dev binary reads.
func ConfigPath(root string) string { return filepath.Join(DevDir(root), "ivy.yaml") }

// ControlPath is the socket a running stack exposes to `ivy-dev deliver` etc.
func ControlPath(root string) string { return filepath.Join(DevDir(root), "control.sock") }

// ReadEnv reads a .env file and returns only the named keys, so mail
// credentials living beside a real key can never reach the dev config. A
// missing file yields an empty map (DEV.md section 6).
func ReadEnv(path string, allowed ...string) (map[string]string, error) {
	all, err := godotenv.Read(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	keep := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		keep[k] = true
	}
	out := make(map[string]string)
	for k, v := range all {
		if keep[k] {
			out[k] = v
		}
	}
	return out, nil
}

// AccountsFrom applies --accounts/--pair to the seeded accounts. Zero means
// every account the profile seeds; pair is a preset for the first two, so the
// two-account sending test always uses a and b rather than an arbitrary slice.
func AccountsFrom(accounts []mailworld.SeedAccount, opts Options) ([]mailworld.SeedAccount, error) {
	n := opts.Accounts
	if opts.Pair {
		n = 2
	}
	if n == 0 {
		n = len(accounts)
	}
	if n > len(accounts) {
		return nil, fmt.Errorf("devstack: profile %q seeds %d accounts, %d requested",
			opts.Profile, len(accounts), n)
	}
	return accounts[:n], nil
}

// BuildConfig turns a seeded mailworld into the dev ivy.yaml. It fails closed:
// a non-loopback mail host is rejected rather than written to disk, and every
// password is the mailworld seed password, never one from the environment.
func BuildConfig(imapAddr, smtpAddr string, accounts []mailworld.SeedAccount, opts Options) (*config.Config, error) {
	imapHost, imapPort, err := splitEndpoint(imapAddr)
	if err != nil {
		return nil, fmt.Errorf("devstack: mailworld IMAP: %w", err)
	}
	smtpHost, smtpPort, err := splitEndpoint(smtpAddr)
	if err != nil {
		return nil, fmt.Errorf("devstack: mailworld SMTP: %w", err)
	}
	if !IsLoopbackHost(imapHost) || !IsLoopbackHost(smtpHost) {
		return nil, fmt.Errorf("devstack: mailworld must be loopback, got %s / %s", imapHost, smtpHost)
	}

	selected, err := AccountsFrom(accounts, opts)
	if err != nil {
		return nil, err
	}

	cfg := &config.Config{
		Listen:  opts.Listen,
		DataDir: DataDir(opts.Root),
	}
	for _, sa := range selected {
		cfg.Accounts = append(cfg.Accounts, config.Account{
			ID:       accountID(sa.Address),
			Address:  sa.Address,
			IMAPHost: imapHost,
			IMAPPort: imapPort,
			SMTPHost: smtpHost,
			SMTPPort: smtpPort,
			Username: sa.Address,
			// On for both providers: the fake exists to serve these features offline.
			LLMEnabled: true,
			Password:   sa.Password,
		})
	}
	if err := ValidateConfig(cfg, opts.Expose); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ValidateConfig re-checks a generated config against the rails before it is
// written or handed to the binary.
func ValidateConfig(cfg *config.Config, expose bool) error {
	if err := ValidateListen(cfg.Listen, expose); err != nil {
		return err
	}
	if cfg.DataDir == "" {
		return errors.New("devstack: data dir is empty")
	}
	for _, a := range cfg.Accounts {
		if !IsLoopbackHost(a.IMAPHost) {
			return fmt.Errorf("devstack: account %s IMAP host %q is not loopback", a.ID, a.IMAPHost)
		}
		if !IsLoopbackHost(a.SMTPHost) {
			return fmt.Errorf("devstack: account %s SMTP host %q is not loopback", a.ID, a.SMTPHost)
		}
	}
	return nil
}

func splitEndpoint(addr string) (string, int, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, fmt.Errorf("port %q is not numeric", port)
	}
	return host, n, nil
}

// accountID is the local part of the address, e.g. ivy-a@grove.test -> ivy-a.
func accountID(address string) string {
	local := address
	if i := strings.IndexByte(address, '@'); i >= 0 {
		local = address[:i]
	}
	return strings.ToLower(local)
}
