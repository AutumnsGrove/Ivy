package llm

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
)

var gateEpoch = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// featuresOn turns per-feature switches on for an account. Features ship dark, so
// a test that wants one to run says so, the way the settings screen will.
func featuresOn(t *testing.T, dbs *store.DBs, accountID string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := dbs.SetSetting(context.Background(), accountID, featureSwitchKey(name), "on"); err != nil {
			t.Fatal(err)
		}
	}
}

// vetAll is a Vetting that vouches for everything, standing in for 5c.0.
type vetAll struct{}

func (vetAll) Vetted(context.Context, string, string) (bool, error) { return true, nil }

type vetError struct{}

func (vetError) Vetted(context.Context, string, string) (bool, error) {
	return false, errors.New("vetting store unreadable")
}

// fixedPolicy answers a fixed set of switches, for the cases the stored policy
// cannot express (a vision opt-in that the app does not offer yet).
type fixedPolicy map[string]AccountSettings

func (p fixedPolicy) Settings(_ context.Context, id string) (AccountSettings, error) {
	return p[id], nil
}

// stubChat is a completer inside the package whose behaviour a test controls.
type stubChat struct {
	calls atomic.Int32
	reply func(ctx context.Context) (string, usage, error)
}

func (s *stubChat) complete(ctx context.Context, _ string, _ []chatMessage, _ int) (string, usage, error) {
	s.calls.Add(1)
	if s.reply != nil {
		return s.reply(ctx)
	}
	return "ok", usage{InputTokens: 10, OutputTokens: 5, CostUSD: 0.001}, nil
}

func completeReq(account string) CompleteRequest {
	return CompleteRequest{
		Feature: "compiler", AccountIDs: []string{account}, ContentKeys: []string{"k1"},
		Model: "test/chat", Prompt: "turn this into a rule", MaxTokens: 100,
	}
}

func decideReq(account string) DecideRequest {
	return DecideRequest{
		Feature: "needs_me", AccountIDs: []string{account}, ContentKeys: []string{"k1"},
		Model: "jev-latest", State: "Subject: lunch?",
		Questions: map[string]Question{"q": {Type: "choice", Instructions: "Does this need a reply?"}},
	}
}

func seeReq(account string) SeeRequest {
	return SeeRequest{
		Feature: "vision", AccountIDs: []string{account}, ContentKeys: []string{"k1"},
		Model: "test/vision", Prompt: "what is this", MediaType: "image/png", Image: []byte{0x89, 'P', 'N', 'G'},
	}
}

func embedReq(account string) EmbedRequest {
	return EmbedRequest{
		Provider: ProviderOpenRouter, AccountID: account, Feature: "search", Model: "m",
		Inputs: []string{"secret text"}, ContentKeys: []string{"k1"},
	}
}

// Test 1: an account with smart features off makes zero outbound calls through any
// of the four entry points, and the ledger says why.
func TestAnAccountThatIsOffReachesNoProviderThroughAnyEntryPoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", false)
	featuresOn(t, dbs, "a", "needs_me", "compiler", "vision")
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return gateEpoch }),
		WithAccountPolicy(fixedPolicy{"a": {}}))

	calls := map[string]func() error{
		"embed":    func() error { _, err := g.Embed(ctx, embedReq("a")); return err },
		"decide":   func() error { _, err := g.Decide(ctx, decideReq("a")); return err },
		"complete": func() error { _, err := g.Complete(ctx, completeReq("a")); return err },
		"see":      func() error { _, err := g.See(ctx, seeReq("a")); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrNotEnabled) {
			t.Errorf("%s: err = %v, want ErrNotEnabled", name, err)
		}
	}
	if n := len(w.Calls()); n != 0 {
		t.Fatalf("the provider received %d requests for an account that is off", n)
	}
	rows, _ := dbs.RecentAPICalls(ctx, "a", 20)
	if len(rows) != len(calls) {
		t.Fatalf("ledger rows = %d, want one per refused call (%d)", len(rows), len(calls))
	}
	for _, r := range rows {
		if r.Outcome != OutcomeRefused || r.Reason != ReasonNotEnabled || r.CostUSD != 0 {
			t.Errorf("row = %+v, want a zero-cost refusal with reason not_enabled", r)
		}
	}
}

// Test 2: nothing in a request can switch an account on; only the stored setting
// can.
func TestOnlyTheStoredSettingChangesTheOptIn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", false)
	featuresOn(t, dbs, "a", "compiler")
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return gateEpoch }))

	if _, err := g.Complete(ctx, completeReq("a")); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("with the account off: err = %v, want ErrNotEnabled", err)
	}
	if err := dbs.SetAccountSmart(ctx, "a", true); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Complete(ctx, completeReq("a")); err != nil {
		t.Fatalf("after the stored setting was turned on: %v", err)
	}
	if n := len(w.Calls()); n != 1 {
		t.Errorf("provider calls = %d, want exactly the one made after turning it on", n)
	}
	if err := dbs.SetAccountSmart(ctx, "a", false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Complete(ctx, completeReq("a")); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("after turning it off again: err = %v, want ErrNotEnabled at once", err)
	}
}

// A feature the table does not know, or that has not been turned on, is refused:
// a typo fails closed, and features ship dark.
func TestAnUnknownOrDarkFeatureIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	g := gateFor(t, dbs, w)

	req := completeReq("a")
	req.Feature = "compilr"
	if _, err := g.Complete(ctx, req); !errors.Is(err, ErrFeatureOff) {
		t.Errorf("unknown feature: err = %v, want ErrFeatureOff", err)
	}
	if _, err := g.Complete(ctx, completeReq("a")); !errors.Is(err, ErrFeatureOff) {
		t.Errorf("dark feature: err = %v, want ErrFeatureOff until its switch is on", err)
	}
	if n := len(w.Calls()); n != 0 {
		t.Errorf("provider calls = %d, want 0", n)
	}
	rows, _ := dbs.RecentAPICalls(ctx, "a", 10)
	for _, r := range rows {
		if r.Reason != ReasonFeatureOff {
			t.Errorf("row reason = %q, want feature_off", r.Reason)
		}
	}
}

// A feature that sees one account's mail refuses a call naming several (brief
// invariant 4), and a call naming none.
func TestAutomatedFeaturesTakeExactlyOneAccount(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dbs := openStore(t)
	g := gateFor(t, dbs, w)
	req := completeReq("a")
	req.AccountIDs = []string{"a", "b"}
	if _, err := g.Complete(context.Background(), req); !errors.Is(err, ErrBadRequest) {
		t.Errorf("two accounts: err = %v, want ErrBadRequest", err)
	}
	req.AccountIDs = nil
	if _, err := g.Complete(context.Background(), req); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no account: err = %v, want ErrBadRequest", err)
	}
}

// Test 3a/3b/3c: the account cap, the global cap and the new month.
func TestCapsRefuseAtZeroCostAndResetNextMonth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	optIn(t, dbs, "b", true)
	featuresOn(t, dbs, "a", "compiler")
	now := gateEpoch
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return now }), WithDefaultCaps(1, 1000))

	spend := func(account string, usd float64) {
		t.Helper()
		if err := dbs.RecordAPICall(ctx, store.APICall{
			At: now, Provider: ProviderOpenRouter, Endpoint: EndpointSystemOne, AccountID: account, CostUSD: usd,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Spend on a different endpoint still counts: the cap is one budget.
	spend("a", 1)
	_, err := g.Complete(ctx, completeReq("a"))
	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("account cap: err = %v, want ErrCapReached", err)
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Reason != ReasonCapAccount {
		t.Errorf("account cap: refusal = %+v, want reason cap_account", refusal)
	}
	if n := len(w.Calls()); n != 0 {
		t.Fatalf("provider called %d times past the account cap", n)
	}
	rows, _ := dbs.RecentAPICalls(ctx, "a", 1)
	if len(rows) != 1 || rows[0].Outcome != OutcomeRefused || rows[0].Reason != ReasonCapAccount || rows[0].CostUSD != 0 {
		t.Errorf("ledger = %+v, want a zero-cost cap_account refusal", rows)
	}

	// The global cap: account a has room, but b has used the whole month.
	if err := dbs.SetSetting(ctx, "a", SettingAccountCapUSD, "50"); err != nil {
		t.Fatal(err)
	}
	if err := dbs.SetSetting(ctx, "", SettingGlobalCapUSD, "5"); err != nil {
		t.Fatal(err)
	}
	spend("b", 4.5)
	_, err = g.Complete(ctx, completeReq("a"))
	if !errors.As(err, &refusal) || refusal.Reason != ReasonCapGlobal || !errors.Is(err, ErrCapReached) {
		t.Fatalf("global cap: err = %v, want a cap_global refusal", err)
	}

	// A new month starts from zero.
	now = now.AddDate(0, 1, 0)
	if _, err := g.Complete(ctx, completeReq("a")); err != nil {
		t.Fatalf("next month: %v", err)
	}
	if n := len(w.Calls()); n != 1 {
		t.Errorf("provider calls = %d, want the one made next month", n)
	}
}

// A cap setting that is not a number falls back to the default instead of to no
// cap.
func TestAnUnusableCapSettingFallsBackToTheDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	featuresOn(t, dbs, "a", "compiler")
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return gateEpoch }), WithDefaultCaps(1, 1000))
	if err := dbs.SetSetting(ctx, "a", SettingAccountCapUSD, "lots"); err != nil {
		t.Fatal(err)
	}
	if err := dbs.RecordAPICall(ctx, store.APICall{At: gateEpoch, Endpoint: EndpointChat, AccountID: "a", CostUSD: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Complete(ctx, completeReq("a")); !errors.Is(err, ErrCapReached) {
		t.Fatalf("err = %v, want the default cap to apply", err)
	}
}

// Test 3d: the cap is checked and reserved under one lock, so concurrent calls
// cannot all pass a cap with room for one. This is the test that fails on a
// check-then-call-then-record gate.
func TestConcurrentCallsCannotOvershootTheCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	featuresOn(t, dbs, "a", "compiler")
	req := completeReq("a")
	est := estimateCost(req.Model, EndpointChat, estimateTokensForBytes(len(req.Prompt)), req.MaxTokens)
	// Room for one call's worst case, not for two.
	if err := dbs.SetSetting(ctx, "a", SettingAccountCapUSD, strconv.FormatFloat(est*1.5, 'f', -1, 64)); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	stub := &stubChat{reply: func(ctx context.Context) (string, usage, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "ok", usage{CostUSD: est / 2}, nil
	}}
	g := NewGate(dbs, WithGateClock(func() time.Time { return gateEpoch }))
	g.completer = stub

	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := g.Complete(ctx, req)
			errs <- err
		}()
	}
	// The refused callers return at once; only the admitted one is still blocked.
	refused := 0
	for refused < n-1 {
		select {
		case err := <-errs:
			if !errors.Is(err, ErrCapReached) {
				t.Fatalf("a caller was refused for the wrong reason: %v", err)
			}
			refused++
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d callers were refused; the rest reached the provider", refused, n-1)
		}
	}
	close(release)
	wg.Wait()
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 under a cap with room for one", got)
	}
}

// Test 4: a call that fails or is cancelled gives its reservation back, so the cap
// does not leak.
func TestAFailedOrCancelledCallReleasesItsReservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	featuresOn(t, dbs, "a", "compiler")
	req := completeReq("a")
	est := estimateCost(req.Model, EndpointChat, estimateTokensForBytes(len(req.Prompt)), req.MaxTokens)
	if err := dbs.SetSetting(ctx, "a", SettingAccountCapUSD, strconv.FormatFloat(est*1.5, 'f', -1, 64)); err != nil {
		t.Fatal(err)
	}
	stub := &stubChat{}
	g := NewGate(dbs, WithGateClock(func() time.Time { return gateEpoch }))
	g.completer = stub

	stub.reply = func(context.Context) (string, usage, error) { return "", usage{}, errors.New("provider down") }
	if _, err := g.Complete(ctx, req); err == nil {
		t.Fatal("expected the provider failure")
	}

	cancelled, cancel := context.WithCancel(ctx)
	stub.reply = func(ctx context.Context) (string, usage, error) {
		cancel()
		<-ctx.Done()
		return "", usage{}, ctx.Err()
	}
	if _, err := g.Complete(cancelled, req); err == nil {
		t.Fatal("expected the cancellation")
	}

	g.mu.Lock()
	leaked, leakedAll := len(g.reserved), g.reservedAll
	g.mu.Unlock()
	if leaked != 0 || leakedAll != 0 {
		t.Fatalf("reservations leaked: %d accounts, $%v", leaked, leakedAll)
	}
	stub.reply = nil
	if _, err := g.Complete(ctx, req); err != nil {
		t.Fatalf("a call after two failures: %v (the cap leaked)", err)
	}
}

// Test 5: a ledger that cannot be written must not leave the cap blind. The next
// call is refused until the ledger works again.
func TestALedgerWriteFailureTripsTheBreakerUntilItHeals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	featuresOn(t, dbs, "a", "compiler")
	stub := &stubChat{}
	g := NewGate(dbs, WithGateClock(func() time.Time { return gateEpoch }))
	g.completer = stub

	if _, err := dbs.State.Write.ExecContext(ctx,
		`CREATE TRIGGER block_ledger BEFORE INSERT ON api_calls BEGIN SELECT RAISE(ABORT, 'ledger broken'); END`); err != nil {
		t.Fatal(err)
	}
	// The call itself ran and its answer is not thrown away; the ledger failure is
	// what trips the breaker.
	if _, err := g.Complete(ctx, completeReq("a")); err != nil {
		t.Fatalf("the paid-for call: %v", err)
	}
	before := stub.calls.Load()
	_, err := g.Complete(ctx, completeReq("a"))
	if !errors.Is(err, ErrLedgerUnwritable) {
		t.Fatalf("with the ledger broken: err = %v, want ErrLedgerUnwritable", err)
	}
	if stub.calls.Load() != before {
		t.Fatal("a call reached the provider while the ledger was unwritable")
	}

	if _, err := dbs.State.Write.ExecContext(ctx, `DROP TRIGGER block_ledger`); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Complete(ctx, completeReq("a")); err != nil {
		t.Fatalf("after the ledger healed: %v", err)
	}
	rows, _ := dbs.RecentAPICalls(ctx, "a", 10)
	if len(rows) != 1 || rows[0].Outcome != OutcomeOK {
		t.Errorf("ledger = %+v, want the one recorded call and no probe rows", rows)
	}
}

// Test 6: with the default vetting, a feature that reads mail text is refused as
// withheld and sends nothing; the checks themselves and the rule compiler, which
// see no vetted mail, are not.
func TestMailReadingFeaturesAreWithheldUntilVetted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	featuresOn(t, dbs, "a", "needs_me_stage2", "summary", "ask", "vision", "injection_tripwire", "compiler")
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return gateEpoch }),
		WithAccountPolicy(fixedPolicy{"a": {Smart: true, Vision: true}}))

	for _, name := range []string{"needs_me_stage2", "summary", "ask"} {
		req := completeReq("a")
		req.Feature = name
		var r *Refusal
		if _, err := g.Complete(ctx, req); !errors.Is(err, ErrWithheld) || !errors.As(err, &r) || r.Reason != ReasonWithheld {
			t.Errorf("%s: err = %v, want a withheld refusal", name, err)
		}
	}
	if _, err := g.See(ctx, seeReq("a")); !errors.Is(err, ErrWithheld) {
		t.Errorf("vision: err = %v, want withheld", err)
	}
	if n := len(w.Calls()); n != 0 {
		t.Fatalf("withheld mail reached the provider %d times", n)
	}

	req := decideReq("a")
	req.Feature = "injection_tripwire"
	if _, err := g.Decide(ctx, req); err != nil {
		t.Errorf("injection_tripwire is the check and must not wait on itself: %v", err)
	}
	if _, err := g.Complete(ctx, completeReq("a")); err != nil {
		t.Errorf("compiler sees no mail and must not be withheld: %v", err)
	}
}

// The withheld rule fails closed: a vetting store that errors, or a call that
// names no mail at all, is withheld; real vetting lets the call through.
func TestVettingFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	featuresOn(t, dbs, "a", "summary")
	req := completeReq("a")
	req.Feature = "summary"

	g := gateFor(t, dbs, w, WithVetting(vetError{}))
	if _, err := g.Complete(ctx, req); !errors.Is(err, ErrWithheld) {
		t.Errorf("vetting error: err = %v, want withheld", err)
	}
	g = gateFor(t, dbs, w, WithVetting(vetAll{}))
	none := req
	none.ContentKeys = nil
	if _, err := g.Complete(ctx, none); !errors.Is(err, ErrWithheld) {
		t.Errorf("names no mail: err = %v, want withheld", err)
	}
	if n := len(w.Calls()); n != 0 {
		t.Fatalf("provider calls = %d before any vetted call", n)
	}
	if _, err := g.Complete(ctx, req); err != nil {
		t.Errorf("vetted mail: %v", err)
	}
}

// Test 7: Ask mixes only accounts the operator selected, and every one must be
// opted in or the whole call is refused naming it. The cost is attributed to the
// selected accounts in equal shares that sum to the provider's number.
func TestAskNeedsEverySelectedAccountOptedIn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	optIn(t, dbs, "b", false)
	featuresOn(t, dbs, "a", "ask")
	featuresOn(t, dbs, "b", "ask")
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return gateEpoch }), WithVetting(vetAll{}))

	req := CompleteRequest{
		Feature: "ask", AccountIDs: []string{"a", "b"}, ContentKeys: []string{"k1"},
		Model: "test/chat", Prompt: "what did they say about the invoice", MaxTokens: 100,
	}
	var r *Refusal
	if _, err := g.Complete(ctx, req); !errors.Is(err, ErrNotEnabled) || !errors.As(err, &r) || r.AccountID != "b" {
		t.Fatalf("one account off: err = %v, want a refusal naming account b", err)
	}
	if n := len(w.Calls()); n != 0 {
		t.Fatalf("provider calls = %d with one selected account off", n)
	}

	optIn(t, dbs, "b", true)
	res, err := g.Complete(ctx, req)
	if err != nil {
		t.Fatalf("both on: %v", err)
	}
	var rows []store.APICall
	for _, id := range []string{"a", "b"} {
		got, _ := dbs.RecentAPICalls(ctx, id, 10)
		for _, row := range got {
			if row.Outcome == OutcomeOK {
				rows = append(rows, row)
			}
		}
	}
	if len(rows) != 2 {
		t.Fatalf("ok rows = %d, want one per selected account", len(rows))
	}
	if rows[0].CallID != rows[1].CallID || rows[0].CallID == "" {
		t.Errorf("call ids %q and %q, want one shared id", rows[0].CallID, rows[1].CallID)
	}
	if rows[0].AccountID == rows[1].AccountID {
		t.Errorf("both rows belong to %s", rows[0].AccountID)
	}
	if sum := rows[0].CostUSD + rows[1].CostUSD; sum <= 0 || diff(sum, res.CostUSD) > 1e-12 {
		t.Errorf("rows sum to %v, the provider reported %v", sum, res.CostUSD)
	}
	if diff(rows[0].CostUSD, rows[1].CostUSD) > 1e-12 {
		t.Errorf("shares %v and %v are not equal", rows[0].CostUSD, rows[1].CostUSD)
	}
}

func diff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

// Jev and vision reach their own endpoints through the gate, ledgered under the
// endpoint the feature table names.
func TestDecideAndSeeReachTheirEndpointsAndAreLedgered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	featuresOn(t, dbs, "a", "needs_me", "vision")
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return gateEpoch }),
		WithAccountPolicy(fixedPolicy{"a": {Smart: true, Vision: true}}), WithVetting(vetAll{}))
	w.QueueJev(map[string]mailworld.JevAnswer{"q": {Choice: "yes", Probabilities: map[string]float64{"yes": 0.9, "no": 0.1}, Confidence: 0.9}})

	d, err := g.Decide(ctx, decideReq("a"))
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got := d.Answers["q"]; got.Choice != "yes" || got.Probabilities["yes"] != 0.9 {
		t.Errorf("answer = %+v, want the queued yes at 0.9", got)
	}
	if _, err := g.See(ctx, seeReq("a")); err != nil {
		t.Fatalf("see: %v", err)
	}

	endpoints := map[string]bool{}
	for _, c := range w.Calls() {
		endpoints[c.Endpoint] = true
	}
	if !endpoints["systemone"] || !endpoints["vision"] {
		t.Errorf("provider saw %v, want systemone and vision", endpoints)
	}
	rows, _ := dbs.RecentAPICalls(ctx, "a", 10)
	byEndpoint := map[string]store.APICall{}
	for _, r := range rows {
		byEndpoint[r.Endpoint] = r
	}
	for _, e := range []string{EndpointSystemOne, EndpointVision} {
		r, ok := byEndpoint[e]
		if !ok || r.Outcome != OutcomeOK || r.CostUSD <= 0 || r.ContentKey != "k1" {
			t.Errorf("%s row = %+v, want an ok row with its exact cost and content key", e, r)
		}
	}
	if spend, _ := dbs.AccountSpend(ctx, "a", "2026-10"); spend.Calls != 2 {
		t.Errorf("counter calls = %d, want 2", spend.Calls)
	}
}

// A payload over its bound is refused as too_large before any provider is
// reached, per entry point.
func TestOversizePayloadsAreRefusedBeforeTheProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	featuresOn(t, dbs, "a", "needs_me", "compiler", "vision")
	g := gateFor(t, dbs, w, WithAccountPolicy(fixedPolicy{"a": {Smart: true, Vision: true}}), WithVetting(vetAll{}))

	big := make([]byte, MaxDecideBytes+1)
	d := decideReq("a")
	d.State = string(big)
	if _, err := g.Decide(ctx, d); !errors.Is(err, ErrTooLarge) {
		t.Errorf("decide: err = %v, want ErrTooLarge", err)
	}
	c := completeReq("a")
	c.MaxTokens = MaxCompleteOutputTokens + 1
	if _, err := g.Complete(ctx, c); !errors.Is(err, ErrTooLarge) {
		t.Errorf("complete: err = %v, want ErrTooLarge", err)
	}
	s := seeReq("a")
	s.Image = make([]byte, MaxImageBytes+1)
	if _, err := g.See(ctx, s); !errors.Is(err, ErrTooLarge) {
		t.Errorf("see: err = %v, want ErrTooLarge", err)
	}
	if n := len(w.Calls()); n != 0 {
		t.Errorf("provider calls = %d for oversize payloads", n)
	}
}

// A provider that never answers cannot hold the gate: the caller's context ends
// the wait, whether it is waiting for a slot or for the answer.
func TestACallOverASlotWaitsOnItsContext(t *testing.T) {
	t.Parallel()
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	featuresOn(t, dbs, "a", "compiler")
	g := NewGate(dbs, WithGateClock(func() time.Time { return gateEpoch }))
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	g.completer = &stubChat{reply: func(ctx context.Context) (string, usage, error) {
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return "", usage{}, ctx.Err()
	}}

	// Fill every chat slot with callers that never return on their own.
	started := make(chan struct{}, slotsChat)
	for range slotsChat {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go func() {
			started <- struct{}{}
			_, _ = g.Complete(ctx, completeReq("a"))
		}()
	}
	for range slotsChat {
		<-started
	}
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := g.Complete(ctx, completeReq("a")); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a call over the slots succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a call waiting for a slot ignored its context")
	}
}
