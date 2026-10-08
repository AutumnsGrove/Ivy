package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AutumnsGrove/Ivy/store"
)

// Gate outcomes, recorded in the ledger so a blocked call is distinguishable
// from a failed one (STANDARDS.md 4a.3).
const (
	OutcomeOK      = "ok"
	OutcomeError   = "error"
	OutcomeRefused = "refused"
	// OutcomeRejected is a call the provider refused because of the document
	// itself (400, 413, 422), as opposed to an outage, a rate limit or a bad key.
	OutcomeRejected = store.OutcomeRejected
)

// Why the gate refused a call: the closed set recorded in the ledger's reason
// column. Anything else is a bug.
const (
	ReasonFeatureOff       = "feature_off"
	ReasonNotEnabled       = "not_enabled"
	ReasonVisionOff        = "vision_off"
	ReasonCapAccount       = "cap_account"
	ReasonCapGlobal        = "cap_global"
	ReasonWithheld         = "withheld"
	ReasonTooLarge         = "too_large"
	ReasonNoProvider       = "no_provider"
	ReasonLedgerUnwritable = "ledger_unwritable"
)

// Bounds (STANDARDS.md 4a). Each has a defined outcome above it: the whole call is
// refused as too_large at zero cost, before any provider is reached.
const (
	MaxBatchInputs = 64
	MaxInputBytes  = 64 << 10
	// MaxLedgerKeys bounds the content keys of one call, so a call cannot write
	// an unbounded number of ledger rows.
	MaxLedgerKeys = 64
	// MaxDecideBytes keeps a Jev call's state plus questions well under its 32k
	// token context (about 24k tokens at 4 bytes), with margin; the caller clips.
	MaxDecideBytes = 96 << 10
	// MaxCompleteBytes and MaxCompleteOutputTokens are placeholders until the
	// first user of chat sets the real figures (5c); MaxImageBytes likewise for
	// vision (5h, gate G4). The images arrive already downscaled.
	MaxCompleteBytes        = 256 << 10
	MaxCompleteOutputTokens = 8192
	MaxImageBytes           = 5 << 20
)

// Defaults for the caps, used until the settings say otherwise. The per-account
// default is the operator's llm.monthly_cap_usd; the global one has a built-in
// default and lives in settings (G1 decision 4).
const (
	DefaultAccountCapUSD = 5.0
	DefaultGlobalCapUSD  = 10.0
)

// Per-call deadlines and per-endpoint concurrency (G1). A call over a slot waits
// on its context, never forever.
const (
	deadlineEmbed  = 60 * time.Second
	deadlineJev    = 15 * time.Second
	deadlineChat   = 90 * time.Second
	deadlineVision = 120 * time.Second

	slotsEmbed  = 1
	slotsJev    = 8
	slotsChat   = 4
	slotsVision = 2
)

// What a call is assumed to cost before it runs, when the request cannot say:
// Jev's answers are short, and an image is a bounded number of tokens however
// large its bytes.
const (
	decideOutputTokensPerQuestion = 64
	imageTokens                   = 4000
	defaultCompleteOutputTokens   = 1024
)

// Gate errors. A caller can tell a policy refusal (do not retry) from a provider
// failure (retry) with errors.Is, whatever the precise reason.
var (
	ErrNotEnabled       = errors.New("llm: account has smart features off")
	ErrCapReached       = errors.New("llm: monthly cap reached")
	ErrTooLarge         = errors.New("llm: request over its limit")
	ErrNoProvider       = errors.New("llm: no provider configured")
	ErrFeatureOff       = errors.New("llm: feature is off")
	ErrWithheld         = errors.New("llm: mail is withheld from this feature")
	ErrLedgerUnwritable = errors.New("llm: the cost ledger cannot be written")
	// ErrBadRequest is a caller bug (no account, several accounts for a feature
	// that sees one), not a policy outcome, so it is returned but not ledgered.
	ErrBadRequest = errors.New("llm: malformed request")
)

// Refusal is the gate declining a call before any provider was reached. It is
// recorded at zero cost with its Reason. errors.Is matches the sentinel for the
// reason, so callers keep branching on ErrNotEnabled and ErrCapReached.
type Refusal struct {
	Reason string
	// AccountID names the account that failed an all-must-pass check (Ask).
	AccountID string
	Detail    string
}

func (r *Refusal) Error() string {
	msg := "llm: call refused (" + r.Reason + ")"
	if r.AccountID != "" {
		msg += " for account " + r.AccountID
	}
	if r.Detail != "" {
		msg += ": " + r.Detail
	}
	return msg
}

// Is reports whether target is the sentinel for this refusal's reason.
func (r *Refusal) Is(target error) bool {
	switch r.Reason {
	case ReasonFeatureOff:
		return target == ErrFeatureOff
	case ReasonNotEnabled, ReasonVisionOff:
		return target == ErrNotEnabled
	case ReasonCapAccount, ReasonCapGlobal:
		return target == ErrCapReached
	case ReasonWithheld:
		return target == ErrWithheld
	case ReasonTooLarge:
		return target == ErrTooLarge
	case ReasonNoProvider:
		return target == ErrNoProvider
	case ReasonLedgerUnwritable:
		return target == ErrLedgerUnwritable
	}
	return false
}

// Gate is the only way to reach a provider. It owns the provider clients, so no
// request can bring its own; it decides opt-in, caps and vetting itself from what
// is stored, so no request can claim them; and it is the only writer of the cost
// ledger. Every entry point runs the same admit, call, settle sequence.
type Gate struct {
	store   *store.DBs
	now     func() time.Time
	seq     atomic.Uint64
	policy  AccountPolicy
	vetting Vetting

	embedders map[string]embedder // by provider kind
	decider   decider
	completer completer

	defaultAccountCap float64
	defaultGlobalCap  float64

	// Reserved dollars of calls admitted but not yet settled, so concurrent calls
	// cannot all pass a cap that has room for one. Ivy is one process (G1
	// decision 2); a crash only drops short-lived reservations.
	mu          sync.Mutex
	reserved    map[string]float64
	reservedAll float64

	// broken is set when a ledger write fails: the ledger is the cap's only record
	// of spend, so no call runs until a probe write succeeds.
	broken atomic.Bool

	slots map[string]chan struct{}
}

// GateOption customises a Gate.
type GateOption func(*Gate)

// WithGateClock injects the clock used for ledger timestamps and cap periods.
func WithGateClock(now func() time.Time) GateOption {
	return func(g *Gate) { g.now = now }
}

// WithAccountPolicy replaces where opt-ins are read from. The default reads
// state.db and the mirror (NewAccountPolicy); `ivy run` adds the accounts ivy.yaml
// declares.
func WithAccountPolicy(p AccountPolicy) GateOption {
	return func(g *Gate) { g.policy = p }
}

// WithVetting installs the real vetting (5c.0). The default refuses all mail, so
// every feature that reads mail text stays off until it exists.
func WithVetting(v Vetting) GateOption {
	return func(g *Gate) { g.vetting = v }
}

// WithDefaultCaps sets the monthly caps used where the settings name none: per
// account, and one global. A cap of zero or less allows no hosted spend.
func WithDefaultCaps(account, global float64) GateOption {
	return func(g *Gate) { g.defaultAccountCap, g.defaultGlobalCap = account, global }
}

// ProviderConfig says where the providers are. An empty field leaves that
// provider out, and a call that needs it is refused as no_provider.
type ProviderConfig struct {
	// OpenRouterBase includes /api/v1. APIKey is the one key for Jev, chat,
	// vision and embeddings.
	OpenRouterBase string
	APIKey         string
	// OllamaURL is the server root, without a path.
	OllamaURL string
}

// WithProviders builds the gate's own provider clients. They are unexported, so
// this is the only way to get one.
func WithProviders(c ProviderConfig) GateOption {
	return func(g *Gate) {
		if c.OpenRouterBase != "" && c.APIKey != "" {
			or := newOpenRouter(c.OpenRouterBase, c.APIKey)
			g.embedders[ProviderOpenRouter] = or
			g.decider, g.completer = or, or
		}
		if c.OllamaURL != "" {
			g.embedders[ProviderOllama] = newOllama(c.OllamaURL)
		}
	}
}

// NewGate builds the gate over the state database.
func NewGate(dbs *store.DBs, opts ...GateOption) *Gate {
	g := &Gate{
		store:             dbs,
		now:               time.Now,
		policy:            NewAccountPolicy(dbs, nil),
		vetting:           refuseAll{},
		embedders:         map[string]embedder{},
		defaultAccountCap: DefaultAccountCapUSD,
		defaultGlobalCap:  DefaultGlobalCapUSD,
		reserved:          map[string]float64{},
		slots: map[string]chan struct{}{
			EndpointEmbeddings: make(chan struct{}, slotsEmbed),
			EndpointSystemOne:  make(chan struct{}, slotsJev),
			EndpointChat:       make(chan struct{}, slotsChat),
			EndpointVision:     make(chan struct{}, slotsVision),
		},
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

// admission is everything the policy check needs to know about a call. It is built
// by an entry point from the request, never supplied by a caller.
type admission struct {
	feature  string
	accounts []string
	keys     []string
	weights  []int // ledger share per key; nil means equal
	provider string
	model    string
	// oversize is set by the entry point when the payload is over its bound.
	oversize string
	// estimate is the worst-case dollars of the call.
	estimate float64
}

func (a admission) endpoint(spec feature) string {
	if a.provider == ProviderOllama {
		return EndpointOllamaEmbed
	}
	return spec.endpoint
}

// reservation is the dollars set aside for an admitted call.
type reservation struct {
	accounts []string
	each     float64
	total    float64
}

// admit is the single policy check. A feature, a provider, an account or a cap
// that is not known is a refusal, so a typo fails closed. The order is: shape,
// feature, provider, size, feature switch, opt-in, vetting, ledger, caps.
func (g *Gate) admit(ctx context.Context, a admission) (*reservation, *Refusal, error) {
	if len(a.accounts) == 0 {
		return nil, nil, fmt.Errorf("%w: no account", ErrBadRequest)
	}
	spec, known := features[a.feature]
	if !known {
		return nil, &Refusal{Reason: ReasonFeatureOff, Detail: "unknown feature " + strconv.Quote(a.feature)}, nil
	}
	if len(a.accounts) > 1 && !spec.multi {
		return nil, nil, fmt.Errorf("%w: feature %s sees one account's mail at a time", ErrBadRequest, a.feature)
	}
	if !g.hasProvider(a.provider, spec) {
		return nil, &Refusal{Reason: ReasonNoProvider}, nil
	}
	if a.oversize != "" {
		return nil, &Refusal{Reason: ReasonTooLarge, Detail: a.oversize}, nil
	}
	// A local model never leaves the device, so it needs no opt-in and has no cap.
	if a.provider == ProviderOllama {
		return &reservation{}, nil, nil
	}
	for _, id := range a.accounts {
		if !g.featureOn(ctx, id, a.feature, spec) {
			return nil, &Refusal{Reason: ReasonFeatureOff, AccountID: id}, nil
		}
	}
	for _, id := range a.accounts {
		s, err := g.policy.Settings(ctx, id)
		if err != nil {
			slog.WarnContext(ctx, "llm: cannot read the smart-features switch; treating it as off", "account", id, "error", err)
			return nil, &Refusal{Reason: ReasonNotEnabled, AccountID: id}, nil
		}
		if !s.Smart {
			return nil, &Refusal{Reason: ReasonNotEnabled, AccountID: id}, nil
		}
		if spec.vision && !s.Vision {
			return nil, &Refusal{Reason: ReasonVisionOff, AccountID: id}, nil
		}
	}
	if spec.vetted && !g.vettedOrWithheld(ctx, a.accounts, a.keys) {
		return nil, &Refusal{Reason: ReasonWithheld}, nil
	}
	if !g.ledgerHealthy(ctx) {
		return nil, &Refusal{Reason: ReasonLedgerUnwritable}, nil
	}
	return g.reserve(ctx, a)
}

func (g *Gate) hasProvider(kind string, spec feature) bool {
	if spec.endpoint == EndpointEmbeddings {
		_, ok := g.embedders[kind]
		return ok
	}
	if kind != ProviderOpenRouter {
		return false
	}
	if spec.endpoint == EndpointSystemOne {
		return g.decider != nil
	}
	return g.completer != nil
}

// featureOn reads the per-account switch for one feature. A feature that ships
// dark stays off until its switch says on.
func (g *Gate) featureOn(ctx context.Context, accountID, name string, spec feature) bool {
	v, ok, err := g.store.GetSetting(ctx, accountID, featureSwitchKey(name))
	switch {
	case err != nil:
		slog.WarnContext(ctx, "llm: cannot read a feature switch; treating it as off", "feature", name, "error", err)
		return false
	case !ok:
		return spec.defaultOn
	}
	return v == "on"
}

// ledgerHealthy is false while the breaker is tripped and a probe write still
// fails. A broken ledger would otherwise let spend go unrecorded and the cap
// silently stop working.
func (g *Gate) ledgerHealthy(ctx context.Context) bool {
	if !g.broken.Load() {
		return true
	}
	if err := g.store.ProbeLedger(ctx); err != nil {
		slog.ErrorContext(ctx, "llm: the cost ledger is still unwritable; refusing calls", "error", err)
		return false
	}
	g.broken.Store(false)
	slog.InfoContext(ctx, "llm: the cost ledger is writable again")
	return true
}

// reserve checks every cap against recorded spend plus what is already in flight
// plus this call's worst case, and sets the worst case aside if it fits. The
// recorded spend is read under the same lock so two calls cannot both see room.
func (g *Gate) reserve(ctx context.Context, a admission) (*reservation, *Refusal, error) {
	period := store.Period(g.now())
	each := a.estimate / float64(len(a.accounts))

	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range a.accounts {
		spent, err := g.store.AccountSpend(ctx, id, period)
		if err != nil {
			return nil, nil, err
		}
		if spent.USD+g.reserved[id]+each > g.capFor(ctx, id) {
			return nil, &Refusal{Reason: ReasonCapAccount, AccountID: id}, nil
		}
	}
	spent, err := g.store.GlobalSpend(ctx, period)
	if err != nil {
		return nil, nil, err
	}
	if spent.USD+g.reservedAll+a.estimate > g.globalCap(ctx) {
		return nil, &Refusal{Reason: ReasonCapGlobal}, nil
	}
	for _, id := range a.accounts {
		g.reserved[id] += each
	}
	g.reservedAll += a.estimate
	return &reservation{accounts: a.accounts, each: each, total: a.estimate}, nil, nil
}

func (g *Gate) release(r *reservation) {
	if r == nil || r.total == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range r.accounts {
		g.reserved[id] -= r.each
		if g.reserved[id] <= 1e-12 {
			delete(g.reserved, id)
		}
	}
	g.reservedAll -= r.total
	if g.reservedAll <= 1e-12 {
		g.reservedAll = 0
	}
}

// AccountCapUSD and GlobalCapUSD are the caps the gate is enforcing right now, for
// the stats panel to show. They are the same lookup the admission check uses, so
// the screen can never state a cap the gate is not applying.
func (g *Gate) AccountCapUSD(ctx context.Context, accountID string) float64 {
	return g.capFor(ctx, accountID)
}

// GlobalCapUSD is the one monthly cap across every account.
func (g *Gate) GlobalCapUSD(ctx context.Context) float64 { return g.globalCap(ctx) }

// capFor is an account's monthly cap: its setting, or the default. A setting that
// does not parse falls back to the default rather than to no cap.
func (g *Gate) capFor(ctx context.Context, accountID string) float64 {
	return g.capSetting(ctx, accountID, SettingAccountCapUSD, g.defaultAccountCap)
}

func (g *Gate) globalCap(ctx context.Context) float64 {
	return g.capSetting(ctx, "", SettingGlobalCapUSD, g.defaultGlobalCap)
}

func (g *Gate) capSetting(ctx context.Context, accountID, key string, fallback float64) float64 {
	v, ok, err := g.store.GetSetting(ctx, accountID, key)
	if err != nil || !ok {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		slog.WarnContext(ctx, "llm: a cap setting is not a usable number; using the default", "key", key)
		return fallback
	}
	return f
}

// run is the one path every entry point takes: admit, make exactly one provider
// call under a slot and a deadline, then settle the ledger. A refusal never
// reaches invoke.
func (g *Gate) run(ctx context.Context, a admission, invoke func(context.Context) (usage, error)) error {
	spec := features[a.feature]
	res, refusal, err := g.admit(ctx, a)
	if err != nil {
		return err
	}
	if refusal != nil {
		g.settle(ctx, a, spec, OutcomeRefused, refusal.Reason, usage{}, 0)
		return refusal
	}
	// The reservation outlives settle, so a call is never invisible to the cap.
	defer g.release(res)

	slot := g.slots[spec.endpoint]
	if a.provider == ProviderOllama {
		slot = nil
	}
	if slot != nil {
		select {
		case slot <- struct{}{}:
			defer func() { <-slot }()
		case <-ctx.Done():
			g.settle(ctx, a, spec, OutcomeError, "", usage{}, 0)
			return ctx.Err()
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, deadlineFor(spec.endpoint))
	defer cancel()

	started := g.now()
	u, err := invoke(callCtx)
	latency := int(g.now().Sub(started).Milliseconds())
	if err != nil {
		g.settle(ctx, a, spec, failureOutcome(err), "", usage{}, latency)
		return err
	}
	g.settle(ctx, a, spec, OutcomeOK, "", u, latency)
	return nil
}

func deadlineFor(endpoint string) time.Duration {
	switch endpoint {
	case EndpointSystemOne:
		return deadlineJev
	case EndpointChat:
		return deadlineChat
	case EndpointVision:
		return deadlineVision
	}
	return deadlineEmbed
}

// failureOutcome is "rejected" when the provider answered that it will not take
// this input, and "error" for everything else, so a document is never blamed for
// an outage, a rate limit or a bad key.
func failureOutcome(err error) string {
	var status *StatusError
	if errors.As(err, &status) {
		switch status.Status {
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
			return OutcomeRejected
		}
	}
	return OutcomeError
}

// settle writes the call's ledger rows and counters in one transaction: a row per
// account and per content key, each carrying its share of the call's cost, so the
// rows sum to the provider's own number. A refusal or failure carries zero cost so
// the volume stays visible. If the write fails the breaker trips: the ledger is
// the cap's only record of spend.
func (g *Gate) settle(ctx context.Context, a admission, spec feature, outcome, reason string, u usage, latencyMS int) {
	keys := a.keys
	if len(keys) == 0 {
		keys = []string{""}
	}
	weights := a.weights
	if len(weights) != len(keys) {
		weights = make([]int, len(keys))
		for i := range weights {
			weights[i] = 1
		}
	}
	perAccount := shares(u.CostUSD, equalWeights(len(a.accounts)))
	tokensIn := intShares(u.InputTokens, equalWeights(len(a.accounts)))
	tokensOut := intShares(u.OutputTokens, equalWeights(len(a.accounts)))

	at := g.now()
	// One id for the whole call, shared by its rows, so a call is countable as one
	// call whatever its row count or the clock's resolution.
	callID := fmt.Sprintf("%d-%d", at.UnixNano(), g.seq.Add(1))
	var calls []store.APICall
	for ai, id := range a.accounts {
		costs := shares(perAccount[ai], weights)
		in := intShares(tokensIn[ai], weights)
		out := intShares(tokensOut[ai], weights)
		for ki, key := range keys {
			calls = append(calls, store.APICall{
				At:            at,
				Provider:      a.provider,
				Endpoint:      a.endpoint(spec),
				Model:         a.model,
				Feature:       a.feature,
				AccountID:     id,
				ContentKey:    key,
				InputTokens:   in[ki],
				OutputTokens:  out[ki],
				CostUSD:       costs[ki],
				CostEstimated: u.CostEstimated,
				LatencyMS:     latencyMS,
				Outcome:       outcome,
				Reason:        reason,
				CallID:        callID,
			})
		}
	}
	if err := g.store.RecordAPICalls(ctx, calls); err != nil {
		g.broken.Store(true)
		slog.ErrorContext(ctx, "llm: the cost ledger could not be written; this call is unrecorded and further calls are refused until it can be",
			"provider", a.provider, "endpoint", a.endpoint(spec), "outcome", outcome,
			"rows", len(calls), "cost_usd", u.CostUSD, "error", err)
	}
}

func equalWeights(n int) []int {
	w := make([]int, n)
	for i := range w {
		w[i] = 1
	}
	return w
}

// shares splits total across weights so the parts sum exactly to total. A
// zero-weight input is only a share when every weight is zero, in which case the
// split is equal.
func shares(total float64, weights []int) []float64 {
	out := make([]float64, len(weights))
	if len(weights) == 0 || total == 0 {
		return out
	}
	sum := 0
	for _, w := range weights {
		sum += w
	}
	if sum == 0 {
		each := total / float64(len(weights))
		for i := range out {
			out[i] = each
		}
		return out
	}
	var assigned float64
	for i, w := range weights {
		if i == len(weights)-1 {
			out[i] = total - assigned
			break
		}
		out[i] = total * float64(w) / float64(sum)
		assigned += out[i]
	}
	return out
}

// intShares is shares rounded to whole tokens and corrected so the parts sum to
// total (the ledger's token counts are informational, but they should add up).
func intShares(total int, weights []int) []int {
	floats := shares(float64(total), weights)
	out := make([]int, len(floats))
	sum := 0
	for i, f := range floats {
		out[i] = int(f + 0.5)
		sum += out[i]
	}
	if len(out) > 0 && sum != total {
		out[len(out)-1] += total - sum
	}
	return out
}
