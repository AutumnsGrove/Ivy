package llm

import (
	"context"
	"errors"
	"log/slog"

	"github.com/AutumnsGrove/Ivy/store"
)

// AccountSettings is what an account has opted into.
type AccountSettings struct {
	Smart  bool
	Vision bool
}

// AccountPolicy answers "what may this account use right now?". It belongs to the
// gate, not to a request: a caller cannot say an account is enabled, only change
// what is stored. An error means "do not know", and the gate treats that as off.
type AccountPolicy interface {
	Settings(ctx context.Context, accountID string) (AccountSettings, error)
}

// NewAccountPolicy reads an account's switches from where Ivy keeps them. An
// account declared in ivy.yaml keeps what the file said at startup (configured,
// which wins a clash as everywhere else); an account connected from the app is
// read from state.db on every call, so turning smart features off stops remote
// calls at once; a seeded dev or test account falls back to its mirror row. An
// account found nowhere is off.
func NewAccountPolicy(dbs *store.DBs, configured map[string]AccountSettings) AccountPolicy {
	return &storePolicy{dbs: dbs, configured: configured}
}

type storePolicy struct {
	dbs        *store.DBs
	configured map[string]AccountSettings
}

func (p *storePolicy) Settings(ctx context.Context, id string) (AccountSettings, error) {
	if s, ok := p.configured[id]; ok {
		return s, nil
	}
	rows, err := p.dbs.AccountConfigs(ctx)
	if err != nil {
		return AccountSettings{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return AccountSettings{Smart: r.LLMEnabled}, nil
		}
	}
	a, err := p.dbs.GetAccount(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return AccountSettings{}, nil
	}
	if err != nil {
		return AccountSettings{}, err
	}
	return AccountSettings{Smart: a.LLMEnabled, Vision: a.VisionEnabled}, nil
}

// Vetting says whether a message may be shown to a model that reads mail text.
// "Vetted" means the tripwire and the sensitive check have run and did not
// withhold it. An error is "not vetted": the rule fails closed.
type Vetting interface {
	Vetted(ctx context.Context, accountID, contentKey string) (bool, error)
}

// refuseAll is the default Vetting until the real one lands (chunk 5, 5c.0): no
// message is vetted, so every feature that reads mail text is refused. That is the
// intended state, and it holds by construction rather than by each feature
// remembering to check.
type refuseAll struct{}

func (refuseAll) Vetted(context.Context, string, string) (bool, error) { return false, nil }

// vettedOrWithheld reports whether every mail the call names may be shown. A
// feature that reads mail must name it; naming nothing is withheld, not vacuously
// fine.
func (g *Gate) vettedOrWithheld(ctx context.Context, accounts, keys []string) bool {
	if len(keys) == 0 {
		return false
	}
	for _, key := range keys {
		ok := false
		// A multi-account call (Ask) does not say which account a key belongs to; a
		// content key is a hash of the mail, so the account that holds it vouches
		// for it. 5i refines this with account-tagged references.
		for _, id := range accounts {
			vetted, err := g.vetting.Vetted(ctx, id, key)
			if err != nil {
				slog.WarnContext(ctx, "llm: vetting failed; treating the mail as withheld", "account", id, "error", err)
				return false
			}
			if vetted {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
