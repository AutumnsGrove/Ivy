// Package config loads ivy.yaml and .env, applies defaults and validates them.
// Secrets come only from the environment or .env, never from the database
// (ARCHITECTURE.md section 8, STANDARDS.md section 4).
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/joho/godotenv"
)

// Defaults used when ivy.yaml does not say otherwise.
const (
	DefaultListen  = "127.0.0.1:8787"
	DefaultDataDir = "./data"
)

// Config is the non-secret file configuration. Behavior settings live in the
// in-app panel and state.db; only bootstrap and secrets live here.
type Config struct {
	Listen  string `yaml:"listen"`
	DataDir string `yaml:"data_dir"`
	// AllowedHosts are the names (no port, no scheme) a browser may use to reach
	// the API, besides loopback and the listen address. A request whose Host is
	// not one of them is refused, which closes DNS rebinding (N11 in
	// papercuts.md): add the Tailscale name the phone uses.
	AllowedHosts []string  `yaml:"allowed_hosts"`
	Accounts     []Account `yaml:"accounts"`
}

// HostAllowList returns the lower-cased names the API answers to besides
// loopback: the configured allowed_hosts and the host of the listen address.
// A wildcard listen address names no host, so it adds nothing.
func (c *Config) HostAllowList() []string {
	out := make([]string, 0, len(c.AllowedHosts)+1)
	for _, h := range c.AllowedHosts {
		out = append(out, normalizeHost(h))
	}
	if host, _, err := net.SplitHostPort(c.Listen); err == nil && host != "" {
		if ip := net.ParseIP(host); ip == nil || !ip.IsUnspecified() {
			out = append(out, normalizeHost(host))
		}
	}
	return out
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// validAllowedHost accepts a bare host name or IP. A port, a scheme, a path or a
// wildcard would never match what the check compares, so each is an error that
// says what to write instead of a silent no-op.
func validAllowedHost(h string) error {
	h = strings.TrimSpace(h)
	switch {
	case h == "":
		return errors.New("is empty")
	case strings.ContainsAny(h, "/* \t"):
		return errors.New("must be a bare host name or IP (no scheme, path, wildcard or spaces)")
	}
	if _, _, err := net.SplitHostPort(h); err == nil {
		return errors.New("must not include a port")
	}
	return nil
}

// Account is one configured mailbox. Password is never serialised.
type Account struct {
	ID         string `yaml:"id"`
	Address    string `yaml:"address"`
	IMAPHost   string `yaml:"imap_host"`
	IMAPPort   int    `yaml:"imap_port"`
	SMTPHost   string `yaml:"smtp_host"`
	SMTPPort   int    `yaml:"smtp_port"`
	Username   string `yaml:"username"`
	LLMEnabled bool   `yaml:"llm_enabled"`
	// TrustedAuthservIDs lists the Authentication-Results authserv-ids whose
	// verdicts Ivy may believe (RFC 8601; N9 in papercuts.md). Empty is the safe
	// default: no header is trusted. Only set ids a configured provider adds.
	TrustedAuthservIDs []string `yaml:"trusted_authserv_ids"`

	Password string `yaml:"-"`
}

// Load reads the config at path. A missing file yields defaults. A .env file
// beside it is loaded without overriding the real environment. Environment
// variables (IVY_LISTEN, IVY_DATA_DIR, IVY_<ID>_PASSWORD) take precedence.
func Load(path string) (*Config, error) {
	cfg := &Config{Listen: DefaultListen, DataDir: DefaultDataDir}

	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // G304: the operator chose this config path with --config
		switch {
		case err == nil:
			// Strict so a typo'd key fails loudly instead of silently keeping a default.
			if err := yaml.UnmarshalWithOptions(data, cfg, yaml.Strict()); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			// godotenv.Load does not override variables already in the
			// environment, so real env wins over the file. A missing .env is
			// normal; an unreadable or malformed one would silently drop secrets.
			envPath := filepath.Join(filepath.Dir(path), ".env")
			if err := godotenv.Load(envPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("load %s: %w", envPath, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// defaults stand
		default:
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}

	applyEnv(cfg)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("IVY_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("IVY_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	for i := range cfg.Accounts {
		if v := os.Getenv(PasswordEnv(cfg.Accounts[i].ID)); v != "" {
			cfg.Accounts[i].Password = v
		}
	}
}

var envSanitize = regexp.MustCompile(`[^A-Z0-9]+`)

// PasswordEnv maps an account id to its environment variable, e.g. account
// "my-account" -> IVY_MY_ACCOUNT_PASSWORD. It is exported so the dev stack can
// set the same variable it expects config.Load to read.
func PasswordEnv(id string) string {
	slug := envSanitize.ReplaceAllString(strings.ToUpper(id), "_")
	return "IVY_" + slug + "_PASSWORD"
}

func (c *Config) validate() error {
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("listen %q is not host:port", c.Listen)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("listen %q has an invalid port", c.Listen)
	}
	if c.DataDir == "" {
		return errors.New("data_dir is empty")
	}
	for i, h := range c.AllowedHosts {
		if err := validAllowedHost(h); err != nil {
			return fmt.Errorf("allowed_hosts[%d] %q: %w", i, h, err)
		}
	}

	seen := make(map[string]bool)
	seenEnv := make(map[string]string)
	for i, a := range c.Accounts {
		where := fmt.Sprintf("account %d", i)
		if a.ID == "" {
			return fmt.Errorf("%s: id is empty", where)
		}
		if seen[a.ID] {
			return fmt.Errorf("%s: duplicate id %q", where, a.ID)
		}
		seen[a.ID] = true
		// "my-mail" and "my_mail" both map to IVY_MY_MAIL_PASSWORD; sharing one
		// credential between two mailboxes is never what the operator meant.
		if other, ok := seenEnv[PasswordEnv(a.ID)]; ok {
			return fmt.Errorf("%s: id %q and %q share the password variable %s",
				where, a.ID, other, PasswordEnv(a.ID))
		}
		seenEnv[PasswordEnv(a.ID)] = a.ID
		if a.Address == "" {
			return fmt.Errorf("%s: address is empty", where)
		}
		if a.IMAPHost == "" || a.IMAPPort < 1 || a.IMAPPort > 65535 {
			return fmt.Errorf("%s: invalid IMAP host/port", where)
		}
		if a.SMTPHost == "" || a.SMTPPort < 1 || a.SMTPPort > 65535 {
			return fmt.Errorf("%s: invalid SMTP host/port", where)
		}
		if a.Username == "" {
			return fmt.Errorf("%s: username is empty", where)
		}
	}
	return nil
}
