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
	DefaultListen        = "127.0.0.1:8418"
	DefaultDataDir       = "./data"
	DefaultBackupAt      = "03:00"
	DefaultOpenRouterURL = "https://openrouter.ai/api/v1"
	// DefaultEmbedModel is the operator's choice (round 30): fast, cheap, a 32k
	// context, and it returns native int8 vectors.
	DefaultEmbedModel = "perplexity/pplx-embed-v1-0.6b"
	// DefaultOllamaEmbedModel is the local model name Ollama expects.
	DefaultOllamaEmbedModel = "nomic-embed-text"
	// DefaultMonthlyCapUSD bounds hosted embeddings out of the box (round 54).
	// The measured rate is about $0.15 per 100k messages.
	DefaultMonthlyCapUSD = 5.0
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
	Backup       Backup    `yaml:"backup"`
	LLM          LLM       `yaml:"llm"`
	Update       Update    `yaml:"update"`
}

// LLM holds the non-secret settings for remote model providers. The API key is
// a secret and is read only from the environment (OPENROUTER_API_KEY), never
// from here (ARCHITECTURE.md 8).
type LLM struct {
	// OpenRouterBase is the OpenRouter-compatible base URL, including /api/v1.
	OpenRouterBase string `yaml:"openrouter_base"`
	// EmbedModel is the embeddings model id used with the hosted provider.
	EmbedModel string `yaml:"embed_model"`
	// OllamaURL is the optional local embeddings endpoint.
	OllamaURL string `yaml:"ollama_url"`
	// OllamaEmbedModel is the model Ollama is asked for.
	OllamaEmbedModel string `yaml:"ollama_embed_model"`
	// MonthlyCapUSD bounds hosted embedding spend per account and month.
	MonthlyCapUSD float64 `yaml:"monthly_cap_usd"`
}

// Update is the self-update path (ARCHITECTURE.md 9). SignalDir is the folder
// the container writes a requested image digest into and the host-side watcher
// reads; it defaults to a subfolder of the bind-mounted data directory, so one
// volume covers it. Token is read only from the environment (GITHUB_TOKEN) and
// is optional: it raises the Actions API rate limit while waiting out a build.
type Update struct {
	SignalDir string `yaml:"signal_dir"`
	Token     string `yaml:"-"`
}

// UpdateSignalDir returns the configured signal directory, or the default
// beneath the data directory so the container and the host watcher agree
// without extra configuration.
func (c *Config) UpdateSignalDir() string {
	if strings.TrimSpace(c.Update.SignalDir) != "" {
		return c.Update.SignalDir
	}
	return filepath.Join(c.DataDir, "update-signal")
}

// Backup is where the daily state.db snapshot goes and when it runs. Targets
// are folders; at least one should be off the potato in case the device dies
// (ARCHITECTURE.md 9). An S3-style target is a later track.
type Backup struct {
	Targets []string `yaml:"targets"`
	At      string   `yaml:"at"`
}

// BackupTargets returns the configured targets, or the default folder beside
// the data directory when none were set, so backups work out of the box.
func (c *Config) BackupTargets() []string {
	if len(c.Backup.Targets) > 0 {
		return c.Backup.Targets
	}
	return []string{filepath.Join(c.DataDir, "backups")}
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

// validClock accepts a local "HH:MM" wall time, the only format the backup
// scheduler understands.
func validClock(at string) error {
	parts := strings.Split(strings.TrimSpace(at), ":")
	if len(parts) != 2 {
		return errors.New("must be HH:MM")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return errors.New("must be HH:MM")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return errors.New("must be HH:MM")
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
	// EmbedProvider chooses where this account's vectors come from: "" (off),
	// "openrouter" (hosted; requires llm_enabled) or "ollama" (local, allowed
	// even with smart features off). Empty keeps an account out of every hosted
	// call (ARCHITECTURE.md 6).
	EmbedProvider string `yaml:"embed_provider"`
	// Insecure sends the IMAP login in plaintext. It exists only for the loopback
	// dev fake; the zero value is implicit TLS, so a forgotten field can never put
	// a real password on the wire unencrypted. Named for the unsafe thing
	// (STANDARDS.md 4a.7).
	Insecure bool `yaml:"insecure"`

	Password string `yaml:"-"`
}

// Load reads the config at path. A missing file yields defaults. A .env file
// beside it is loaded without overriding the real environment. Environment
// variables (IVY_LISTEN, IVY_DATA_DIR, IVY_<ID>_PASSWORD) take precedence.
func Load(path string) (*Config, error) {
	cfg := &Config{
		Listen:  DefaultListen,
		DataDir: DefaultDataDir,
		Backup:  Backup{At: DefaultBackupAt},
		LLM: LLM{
			OpenRouterBase:   DefaultOpenRouterURL,
			EmbedModel:       DefaultEmbedModel,
			OllamaEmbedModel: DefaultOllamaEmbedModel,
			MonthlyCapUSD:    DefaultMonthlyCapUSD,
		},
	}

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
	// The Actions token is a secret, so it comes only from the environment, the
	// same rule as the account passwords (ARCHITECTURE.md 8).
	if v := os.Getenv("GITHUB_TOKEN"); v != "" {
		cfg.Update.Token = v
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
	if err := validClock(c.Backup.At); err != nil {
		return fmt.Errorf("backup.at %q: %w", c.Backup.At, err)
	}
	for i, target := range c.Backup.Targets {
		if strings.TrimSpace(target) == "" {
			return fmt.Errorf("backup.targets[%d] is empty", i)
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
		switch a.EmbedProvider {
		case "", "off", "ollama":
		case "openrouter":
			// Hosted embeddings send message text off the device, so they need
			// the account's smart-features opt-in as well as the provider choice.
			if !a.LLMEnabled {
				return fmt.Errorf("%s: embed_provider openrouter needs llm_enabled", where)
			}
		default:
			return fmt.Errorf("%s: unknown embed_provider %q", where, a.EmbedProvider)
		}
	}
	if c.LLM.MonthlyCapUSD < 0 {
		return errors.New("llm.monthly_cap_usd must not be negative")
	}
	if strings.TrimSpace(c.LLM.OpenRouterBase) == "" {
		return errors.New("llm.openrouter_base is empty")
	}
	return nil
}
