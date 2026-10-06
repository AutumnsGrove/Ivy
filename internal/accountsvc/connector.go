package accountsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AutumnsGrove/Ivy/config"
	"github.com/AutumnsGrove/Ivy/gateway"
	"github.com/AutumnsGrove/Ivy/internal/secrets"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

// Provider fixes the servers an account typed into the app talks to. The
// browser only supplies an address and a password, so the server never dials a
// host a request named (an SSRF door). Other providers arrive later as plain
// host fields, not presets (round 59).
type Provider struct {
	IMAPHost string
	IMAPPort int
	SMTPHost string
	SMTPPort int
	// Insecure is for the loopback fake mail world in tests only; it is never
	// stored, so a restart always connects with implicit TLS.
	Insecure bool
}

// Purelymail is the one provider Ivy connects from the app for now.
var Purelymail = Provider{
	IMAPHost: "imap.purelymail.com", IMAPPort: 993,
	SMTPHost: "smtp.purelymail.com", SMTPPort: 465,
}

// idBase names the first account; later ones are idBase-2, idBase-3.
const idBase = "purelymail"

// ConnectorOptions are the connector's collaborators.
type ConnectorOptions struct {
	DBs        *store.DBs
	Supervisor *Supervisor
	SecretsDir string
	// Provider defaults to Purelymail.
	Provider Provider
	// Existing lists the accounts from ivy.yaml, which the connector must not
	// duplicate and may update the password of.
	Existing func() []config.Account
	Now      func() time.Time
}

// Connector implements gateway.AccountConnector.
type Connector struct {
	opts    ConnectorOptions
	fetcher *ivysync.Fetcher
	// mu makes connect and update one at a time, so two requests cannot pick the
	// same id or race a password file.
	mu sync.Mutex
}

var _ gateway.AccountConnector = (*Connector)(nil)

// NewConnector builds a Connector, filling the defaults.
func NewConnector(opts ConnectorOptions) *Connector {
	if opts.Provider.IMAPHost == "" {
		opts.Provider = Purelymail
	}
	if opts.Existing == nil {
		opts.Existing = func() []config.Account { return nil }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Connector{opts: opts, fetcher: ivysync.NewFetcher(opts.DBs)}
}

// Connect tests the login, and only when it works stores the password, the
// connection details and starts syncing. The order is the point: a mistyped
// password leaves no secret, no row and no worker behind.
func (c *Connector) Connect(ctx context.Context, address, password string, smart bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	configs, err := c.opts.DBs.AccountConfigs(ctx)
	if err != nil {
		return "", err
	}
	taken := make(map[string]bool)
	for _, a := range configs {
		taken[a.ID] = true
		if strings.EqualFold(a.Address, address) {
			return "", gateway.ErrAlreadyConnected
		}
	}
	for _, a := range c.opts.Existing() {
		taken[a.ID] = true
		if strings.EqualFold(a.Address, address) {
			return "", gateway.ErrAlreadyConnected
		}
	}

	p := c.opts.Provider
	row := store.AccountConfig{
		ID: freeID(taken), Address: address, Username: address,
		IMAPHost: p.IMAPHost, IMAPPort: p.IMAPPort, SMTPHost: p.SMTPHost, SMTPPort: p.SMTPPort,
		CreatedAt: c.opts.Now(),
	}
	if smart {
		// Takes effect at the next start: the embedding pipeline is built from
		// the accounts present at startup.
		row.LLMEnabled, row.EmbedProvider = true, "openrouter"
	}
	acct := c.syncAccount(row, password)
	if err := c.probe(ctx, acct); err != nil {
		return "", err
	}
	if err := secrets.Write(c.opts.SecretsDir, row.ID, password); err != nil {
		return "", fmt.Errorf("store password: %w", err)
	}
	if err := c.opts.DBs.SaveAccountConfig(ctx, row); err != nil {
		return "", err
	}
	//nolint:contextcheck // the supervisor owns its workers' lifetime, not this request's context
	c.opts.Supervisor.Start(acct)
	return row.ID, nil
}

// UpdatePassword replaces the password of an account connected from the app or
// written in ivy.yaml. The new one is tested first, so a typo keeps the old one,
// and a success restarts the account's workers with it.
func (c *Connector) UpdatePassword(ctx context.Context, id, password string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	acct, err := c.find(ctx, id, password)
	if err != nil {
		return err
	}
	if err := c.probe(ctx, acct); err != nil {
		return err
	}
	if err := secrets.Write(c.opts.SecretsDir, id, password); err != nil {
		return fmt.Errorf("store password: %w", err)
	}
	//nolint:contextcheck // the supervisor owns its workers' lifetime, not this request's context
	c.opts.Supervisor.Start(acct)
	return nil
}

func (c *Connector) find(ctx context.Context, id, password string) (ivysync.Account, error) {
	configs, err := c.opts.DBs.AccountConfigs(ctx)
	if err != nil {
		return ivysync.Account{}, err
	}
	for _, row := range configs {
		if row.ID == id {
			return c.syncAccount(row, password), nil
		}
	}
	for _, a := range c.opts.Existing() {
		if a.ID == id {
			a.Password = password
			return SyncAccount(a), nil
		}
	}
	return ivysync.Account{}, store.ErrNotFound
}

func (c *Connector) syncAccount(row store.AccountConfig, password string) ivysync.Account {
	return ivysync.Account{
		ID: row.ID, Address: row.Address, Username: row.Username, Password: password,
		IMAPHost: row.IMAPHost, IMAPPort: row.IMAPPort, SMTPHost: row.SMTPHost, SMTPPort: row.SMTPPort,
		Insecure: c.opts.Provider.Insecure,
	}
}

// probe turns the sync package's failure into the gateway's typed one.
func (c *Connector) probe(ctx context.Context, acct ivysync.Account) error {
	err := c.fetcher.Probe(ctx, acct)
	var pe *ivysync.ProbeError
	if errors.As(err, &pe) {
		return &gateway.ConnectError{Code: pe.Code, Err: pe.Err}
	}
	return err
}

func freeID(taken map[string]bool) string {
	if !taken[idBase] {
		return idBase
	}
	for n := 2; ; n++ {
		if id := idBase + "-" + strconv.Itoa(n); !taken[id] {
			return id
		}
	}
}

// SyncAccount is the connection descriptor a sync worker uses for a configured
// account. Real accounts use implicit TLS; only the loopback dev fake sets
// Insecure (config.Account).
func SyncAccount(a config.Account) ivysync.Account {
	return ivysync.Account{
		ID: a.ID, Address: a.Address,
		IMAPHost: a.IMAPHost, IMAPPort: a.IMAPPort,
		SMTPHost: a.SMTPHost, SMTPPort: a.SMTPPort,
		Username: a.Username, Password: a.Password,
		Insecure:           a.Insecure,
		TrustedAuthservIDs: a.TrustedAuthservIDs,
	}
}

// StoredAccounts returns the accounts connected from the app, with passwords
// from data/secrets, for the accounts not already in cfg. An account whose
// password file is missing or unusable is still returned with no password: its
// sync then says auth_failed, which is the banner that offers "Update password",
// instead of the account silently vanishing.
func StoredAccounts(ctx context.Context, dbs *store.DBs, cfg *config.Config) ([]config.Account, error) {
	rows, err := dbs.AccountConfigs(ctx)
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		have[a.ID] = true
	}
	var out []config.Account
	for _, row := range rows {
		if have[row.ID] {
			slog.WarnContext(ctx, "account is in ivy.yaml and the app; using ivy.yaml", "account", row.ID)
			continue
		}
		pw, err := secrets.Read(cfg.SecretsDir(), row.ID)
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			slog.WarnContext(ctx, "cannot read the stored password", "account", row.ID, "error", err)
		}
		out = append(out, config.Account{
			ID: row.ID, Address: row.Address, Username: row.Username,
			IMAPHost: row.IMAPHost, IMAPPort: row.IMAPPort, SMTPHost: row.SMTPHost, SMTPPort: row.SMTPPort,
			LLMEnabled: row.LLMEnabled, EmbedProvider: row.EmbedProvider, Password: pw,
		})
	}
	return out, nil
}
