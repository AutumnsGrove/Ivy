package llm

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"
)

func TestCapsAreSavedAndTheGateAppliesThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	g := NewGate(dbs)

	if err := g.SetAccountCap(ctx, "a", 2.5); err != nil {
		t.Fatal(err)
	}
	if err := g.SetGlobalCap(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if got := g.AccountCapUSD(ctx, "a"); got != 2.5 {
		t.Errorf("account cap = %v, want 2.5", got)
	}
	if got := g.AccountCapUSD(ctx, "b"); got != DefaultAccountCapUSD {
		t.Errorf("another account's cap = %v, want the default %v", got, DefaultAccountCapUSD)
	}
	if got := g.GlobalCapUSD(ctx); got != 7 {
		t.Errorf("global cap = %v, want 7", got)
	}

	// Zero is a real choice: no hosted spend at all.
	if err := g.SetAccountCap(ctx, "a", 0); err != nil {
		t.Fatalf("a zero cap was refused: %v", err)
	}
	if got := g.AccountCapUSD(ctx, "a"); got != 0 {
		t.Errorf("a zero cap reads %v", got)
	}
}

// The cap the screen saves is the cap the gate refuses at, not a copy of it.
func TestASavedCapStopsTheNextCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newWorld(t)
	dbs := openStore(t)
	optIn(t, dbs, "a", true)
	g := gateFor(t, dbs, w, WithGateClock(func() time.Time { return gateEpoch }))

	if err := g.SetAccountCap(ctx, "a", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Embed(ctx, embedReq("a")); !errors.Is(err, ErrCapReached) {
		t.Fatalf("embed under a zero cap = %v, want ErrCapReached", err)
	}
	if n := len(w.Calls()); n != 0 {
		t.Errorf("the provider received %d requests under a zero cap", n)
	}
}

func TestACapThatMakesNoSenseIsRefusedAndChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := NewGate(openStore(t))
	if err := g.SetAccountCap(ctx, "a", 3); err != nil {
		t.Fatal(err)
	}
	for name, usd := range map[string]float64{
		"negative": -1, "NaN": math.NaN(), "infinite": math.Inf(1), "over the ceiling": MaxCapUSD + 1,
	} {
		if err := g.SetAccountCap(ctx, "a", usd); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s account cap: err = %v, want ErrBadRequest", name, err)
		}
		if err := g.SetGlobalCap(ctx, usd); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s global cap: err = %v, want ErrBadRequest", name, err)
		}
	}
	if err := g.SetAccountCap(ctx, "", 3); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a cap for no account: err = %v, want ErrBadRequest", err)
	}
	if got := g.AccountCapUSD(ctx, "a"); got != 3 {
		t.Errorf("a refused cap changed the stored one to %v", got)
	}
}

func TestAFeatureSwitchIsPerAccountAndShipsDark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := NewGate(openStore(t))

	if g.FeatureEnabled(ctx, "a", "needs_me") {
		t.Error("a feature that ships dark is on before anyone turned it on")
	}
	if !g.FeatureEnabled(ctx, "a", "search") {
		t.Error("meaning search is off by default, but it shipped on")
	}
	if err := g.SetFeatureEnabled(ctx, "a", "needs_me", true); err != nil {
		t.Fatal(err)
	}
	if err := g.SetFeatureEnabled(ctx, "a", "search", false); err != nil {
		t.Fatal(err)
	}
	if !g.FeatureEnabled(ctx, "a", "needs_me") || g.FeatureEnabled(ctx, "a", "search") {
		t.Error("the saved switches did not read back")
	}
	if g.FeatureEnabled(ctx, "b", "needs_me") || !g.FeatureEnabled(ctx, "b", "search") {
		t.Error("one account's switch reached another")
	}
	for _, bad := range []string{"mystery", ""} {
		if err := g.SetFeatureEnabled(ctx, "a", bad, true); !errors.Is(err, ErrBadRequest) {
			t.Errorf("feature %q: err = %v, want ErrBadRequest", bad, err)
		}
	}
	if err := g.SetFeatureEnabled(ctx, "", "search", true); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a switch for no account: err = %v, want ErrBadRequest", err)
	}
}

// "Needs you" is asked in the shared Jev call, which runs under classify, and the
// safety checks stage two relies on ride that call too. The screen has no switch for
// classify, so turning "Needs you" on must be enough on its own.
func TestNeedsYouSwitchesOnTheSharedCallItRidesOn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := NewGate(openStore(t))

	if g.FeatureEnabled(ctx, "a", "classify") {
		t.Fatal("the shared call is on before anyone asked for anything")
	}
	if err := g.SetFeatureEnabled(ctx, "a", "needs_me", true); err != nil {
		t.Fatal(err)
	}
	if !g.FeatureEnabled(ctx, "a", "classify") {
		t.Error("Needs you is on but the call it rides on is still off")
	}
	if g.FeatureEnabled(ctx, "b", "classify") {
		t.Error("one account's switch turned on another account's shared call")
	}
	if err := g.SetFeatureEnabled(ctx, "a", "needs_me", false); err != nil {
		t.Fatal(err)
	}
	if g.FeatureEnabled(ctx, "a", "classify") {
		t.Error("turning Needs you back off left the shared call on")
	}
}

// Junk rescue is asked in the same shared call, so its switch has to be enough too.
func TestJunkRescueSwitchesOnTheSharedCallItRidesOn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := NewGate(openStore(t))

	if err := g.SetFeatureEnabled(ctx, "a", "junk_rescue", true); err != nil {
		t.Fatal(err)
	}
	if !g.FeatureEnabled(ctx, "a", "classify") {
		t.Error("Junk rescue is on but the call it rides on is still off")
	}
	if err := g.SetFeatureEnabled(ctx, "a", "junk_rescue", false); err != nil {
		t.Fatal(err)
	}
	if g.FeatureEnabled(ctx, "a", "classify") {
		t.Error("turning Junk rescue back off left the shared call on")
	}
}

// The screen lists only features that exist for the operator; the dark ones join as
// their stages land.
func TestOnlyShippedFeaturesAreListedForTheScreen(t *testing.T) {
	t.Parallel()
	var names []string
	for _, f := range UserFeatures() {
		if f.Label == "" {
			t.Errorf("feature %q is listed without a label", f.Name)
		}
		names = append(names, f.Name)
	}
	if !slices.Contains(names, "search") {
		t.Errorf("listed = %v, want meaning search among them", names)
	}
	for _, shipped := range []string{"needs_me", "needs_me_stage2", "junk_rescue"} {
		if !slices.Contains(names, shipped) {
			t.Errorf("listed = %v, want %s: it has a screen behind it", names, shipped)
		}
	}
	if slices.Contains(names, "classify") {
		t.Errorf("listed = %v: classify has no screen behind it yet", names)
	}
}

func TestTheChosenModelsAreSavedAndValidated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := NewGate(openStore(t))

	if got := g.ChosenChatModel(ctx); got != DefaultChatModelID {
		t.Fatalf("nothing chosen reads %q, want the built-in %q", got, DefaultChatModelID)
	}
	other := otherChatModel(t)
	if err := g.SetChatModel(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if got := g.ChosenChatModel(ctx); got != other.ID {
		t.Errorf("chosen = %q, want %q", got, other.ID)
	}
	for _, bad := range []string{"retired", JevModelID, EmbedSmallModelID, ""} {
		if err := g.SetChatModel(ctx, bad); !errors.Is(err, ErrBadRequest) {
			t.Errorf("chat model %q: err = %v, want ErrBadRequest", bad, err)
		}
	}
	if got := g.ChosenChatModel(ctx); got != other.ID {
		t.Errorf("a refused choice changed the model to %q", got)
	}

	if err := g.SetFeatureModel(ctx, "digest", DefaultChatModelID); err != nil {
		t.Fatal(err)
	}
	if got := g.ChosenFeatureModel(ctx, "digest"); got != DefaultChatModelID {
		t.Errorf("override = %q, want %q", got, DefaultChatModelID)
	}
	if got := g.ChosenFeatureModel(ctx, "summary"); got != "" {
		t.Errorf("an override on digest reached summary: %q", got)
	}
	// An empty id removes the override, and the feature follows the default again.
	if err := g.SetFeatureModel(ctx, "digest", ""); err != nil {
		t.Fatal(err)
	}
	if got := g.ChosenFeatureModel(ctx, "digest"); got != "" {
		t.Errorf("a cleared override reads %q", got)
	}
	if m, _ := g.ModelFor(ctx, "digest"); m.ID != other.ID {
		t.Errorf("digest after clearing runs on %q, want the default choice %q", m.ID, other.ID)
	}
}

func TestAnOverrideMustFitItsFeature(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := NewGate(openStore(t))
	blind := blindChatModel(t)

	cases := map[string]struct{ feature, model string }{
		"vision with a model that cannot see": {"vision", blind.ID},
		"an unknown feature":                  {"mystery", DefaultChatModelID},
		"an embedding feature":                {"search", DefaultChatModelID},
		"a Jev feature":                       {"needs_me", DefaultChatModelID},
		"an unknown model":                    {"digest", "retired"},
		"the Jev model for a chat feature":    {"digest", JevModelID},
	}
	for name, c := range cases {
		if err := g.SetFeatureModel(ctx, c.feature, c.model); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v, want ErrBadRequest", name, err)
		}
	}
	if err := g.SetFeatureModel(ctx, "digest", blind.ID); err != nil {
		t.Errorf("a text-only model for a text feature was refused: %v", err)
	}
}

// A model the catalog retired is not reported as the operator's choice: the screen
// shows what the feature will actually run on.
func TestAStoredModelThatWasRetiredReadsAsTheDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openStore(t)
	g := NewGate(dbs)
	if err := dbs.SetSetting(ctx, "", SettingChatModel, "retired"); err != nil {
		t.Fatal(err)
	}
	if err := dbs.SetSetting(ctx, "", FeatureModelKey("digest"), "retired"); err != nil {
		t.Fatal(err)
	}
	if got := g.ChosenChatModel(ctx); got != DefaultChatModelID {
		t.Errorf("chosen = %q, want the default", got)
	}
	if got := g.ChosenFeatureModel(ctx, "digest"); got != "" {
		t.Errorf("override = %q, want none", got)
	}
}

// Categories ride the same shared call as Needs you, so switching them on must be
// enough on its own, and they are on the screen.
func TestCategoriesAreListedAndSwitchOnTheSharedCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var listed bool
	for _, f := range UserFeatures() {
		if f.Name == "category" {
			listed = f.Label == "Categories" && !f.DefaultOn
		}
	}
	if !listed {
		t.Error("category is not listed as Categories, dark")
	}
	g := NewGate(openStore(t))
	if g.FeatureEnabled(ctx, "a", "classify") {
		t.Fatal("the shared call is on with nothing asked for")
	}
	if err := g.SetFeatureEnabled(ctx, "a", "category", true); err != nil {
		t.Fatal(err)
	}
	if !g.FeatureEnabled(ctx, "a", "classify") {
		t.Error("Categories is on but the call it rides on is still off")
	}
}
