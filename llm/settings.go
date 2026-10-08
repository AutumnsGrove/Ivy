package llm

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// The write side of the settings the gate reads. The Smart features screen goes
// through these, so the closed sets (features, models, a sane cap) are checked in
// the package that owns them, and a value the screen saves is exactly the value
// admission applies. Each setter has a pure Check twin so a caller with several
// changes can validate all of them before writing any.

// MaxCapUSD is the ceiling on a monthly cap. It is a typo guard and not a policy:
// the operator's own limit on the provider key is the real backstop.
const MaxCapUSD = 1000.0

// BadValue is a setting the operator asked for that Ivy will not store. Its message
// is fit to show; it matches ErrBadRequest.
type BadValue struct{ Reason string }

func (e *BadValue) Error() string { return e.Reason }

// Is makes a BadValue match ErrBadRequest, so callers can branch on the sentinel and
// still read the reason.
func (e *BadValue) Is(target error) bool { return target == ErrBadRequest }

func badValue(format string, args ...any) error {
	return &BadValue{Reason: fmt.Sprintf(format, args...)}
}

// FeatureInfo describes a feature the operator can switch, for the screen.
type FeatureInfo struct {
	Name      string
	Label     string
	DefaultOn bool
}

// UserFeatures lists the features that have code behind them, in a stable order.
// A feature joins the list when its stage lands (it gets a label then); the rest
// of the table still exists so the gate fails closed on them.
func UserFeatures() []FeatureInfo {
	var out []FeatureInfo
	for name, spec := range features {
		if spec.label != "" {
			out = append(out, FeatureInfo{Name: name, Label: spec.label, DefaultOn: spec.defaultOn})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CheckCap is whether a monthly cap in dollars can be stored. Zero is a real
// choice and means no hosted spend.
func CheckCap(usd float64) error {
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 || usd > MaxCapUSD {
		return badValue("A cap must be between $0 and $%.0f", MaxCapUSD)
	}
	return nil
}

// CheckFeature is whether a name is a feature the gate knows.
func CheckFeature(name string) error {
	if _, ok := features[name]; !ok {
		return badValue("%q is not a feature Ivy has", name)
	}
	return nil
}

// CheckChatModel is whether an id is a generating model in the catalog.
func CheckChatModel(id string) error {
	if m, ok := LookupModel(id); !ok || (m.Kind != EndpointChat && m.Kind != EndpointVision) {
		return badValue("%q is not a chat model Ivy offers", id)
	}
	return nil
}

// CheckFeatureModel is whether a model can serve a feature. An empty id clears the
// override and is always fine for a feature that runs on a chat model.
func CheckFeatureModel(name, id string) error {
	spec, ok := features[name]
	if !ok || (spec.endpoint != EndpointChat && spec.endpoint != EndpointVision) {
		return badValue("%q does not run on a chat model", name)
	}
	if id == "" {
		return nil
	}
	if m, found := LookupModel(id); !found || !fitsFeature(m, spec) {
		return badValue("%q cannot serve %s", id, name)
	}
	return nil
}

// FeatureEnabled is whether a feature will run for an account, as admission decides
// it: the stored switch, or the feature's own default. It does not say the account
// is opted in; that is a separate switch.
func (g *Gate) FeatureEnabled(ctx context.Context, accountID, name string) bool {
	spec, ok := features[name]
	return ok && g.featureOn(ctx, accountID, name, spec)
}

// SetFeatureEnabled turns one feature on or off for one account.
func (g *Gate) SetFeatureEnabled(ctx context.Context, accountID, name string, on bool) error {
	if accountID == "" {
		return badValue("No account named")
	}
	if err := CheckFeature(name); err != nil {
		return err
	}
	value := "off"
	if on {
		value = "on"
	}
	return g.store.SetSetting(ctx, accountID, featureSwitchKey(name), value)
}

// SetAccountCap sets one account's monthly cap in dollars.
func (g *Gate) SetAccountCap(ctx context.Context, accountID string, usd float64) error {
	if accountID == "" {
		return badValue("No account named")
	}
	if err := CheckCap(usd); err != nil {
		return err
	}
	return g.store.SetSetting(ctx, accountID, SettingAccountCapUSD, formatUSD(usd))
}

// SetGlobalCap sets the one monthly cap across every account.
func (g *Gate) SetGlobalCap(ctx context.Context, usd float64) error {
	if err := CheckCap(usd); err != nil {
		return err
	}
	return g.store.SetSetting(ctx, "", SettingGlobalCapUSD, formatUSD(usd))
}

func formatUSD(usd float64) string { return strconv.FormatFloat(usd, 'f', -1, 64) }

// ChosenChatModel is the registry id chat features run on unless overridden: the
// stored choice if the catalog still holds it, else the built-in. It is what
// ModelFor falls back to, so the screen shows what will really run.
func (g *Gate) ChosenChatModel(ctx context.Context) string {
	id, ok, err := g.store.GetSetting(ctx, "", SettingChatModel)
	if err == nil && ok && CheckChatModel(id) == nil {
		return id
	}
	return DefaultChatModelID
}

// ChosenFeatureModel is a feature's override, or "" when it follows the default
// (nothing stored, cleared, or a model the catalog no longer holds).
func (g *Gate) ChosenFeatureModel(ctx context.Context, name string) string {
	id, set, err := g.store.GetSetting(ctx, "", FeatureModelKey(name))
	if err != nil || !set || id == "" || CheckFeatureModel(name, id) != nil {
		return ""
	}
	return id
}

// SetChatModel chooses the default chat model, by registry id.
func (g *Gate) SetChatModel(ctx context.Context, id string) error {
	if err := CheckChatModel(id); err != nil {
		return err
	}
	return g.store.SetSetting(ctx, "", SettingChatModel, id)
}

// SetFeatureModel overrides the model one chat or vision feature runs on. An empty
// id clears the override, so the feature follows the default again.
func (g *Gate) SetFeatureModel(ctx context.Context, name, id string) error {
	if err := CheckFeatureModel(name, id); err != nil {
		return err
	}
	return g.store.SetSetting(ctx, "", FeatureModelKey(name), id)
}
