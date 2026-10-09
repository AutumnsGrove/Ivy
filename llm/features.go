package llm

// Ledger endpoint names. Each is also one concurrency slot and one deadline.
const (
	EndpointEmbeddings  = "embeddings"
	EndpointOllamaEmbed = "ollama_embed"
	EndpointSystemOne   = "systemone"
	EndpointChat        = "chat"
	EndpointVision      = "vision"
)

// feature is one way Ivy uses a remote model. The table below is the only place a
// feature is described, so the policy a call meets is a property of its name and a
// new feature cannot forget a check. The architecture test also reads it.
type feature struct {
	endpoint string
	// vetted: the call carries mail text, so every message in it must have passed
	// the tripwire and sensitive checks and not been withheld (fail closed).
	vetted bool
	// vision: the account must also have the vision switch on.
	vision bool
	// multi: the call may name several accounts (Ask Ivy's operator-selected set).
	// Every other feature sees exactly one account's mail.
	multi bool
	// defaultOn: the feature runs once an account has opted in, unless its
	// per-feature switch says off. Features ship dark, so only the two that
	// already shipped are on; each later stage turns its own on.
	defaultOn bool
	// label is the operator-facing name. A feature with none is not listed on the
	// Smart features screen: it has no code behind it yet, and gains a label when
	// its stage ships.
	label string
}

var features = map[string]feature{
	"search": {endpoint: EndpointEmbeddings, defaultOn: true, label: "Meaning search"},
	"embed":  {endpoint: EndpointEmbeddings, defaultOn: true},

	// The tripwire and the sensitive check are the checks, so they cannot wait on
	// being vetted themselves.
	"injection_tripwire": {endpoint: EndpointSystemOne},
	"sensitive_content":  {endpoint: EndpointSystemOne},

	// Classifiers read clipped state only.
	"needs_me":    {endpoint: EndpointSystemOne},
	"classify":    {endpoint: EndpointSystemOne},
	"junk_rescue": {endpoint: EndpointSystemOne},

	"needs_me_stage2": {endpoint: EndpointChat, vetted: true},
	"summary":         {endpoint: EndpointChat, vetted: true},
	"digest":          {endpoint: EndpointChat, vetted: true},
	"extraction":      {endpoint: EndpointChat, vetted: true},
	// The rule compiler turns a sentence into a rule and sees no mail.
	"compiler": {endpoint: EndpointChat},

	"vision": {endpoint: EndpointVision, vetted: true, vision: true},

	"ask":         {endpoint: EndpointChat, vetted: true, multi: true},
	"claim_check": {endpoint: EndpointSystemOne, vetted: true, multi: true},
}

// FeatureNames lists every feature the gate knows, for the architecture test and
// the settings screen.
func FeatureNames() []string {
	out := make([]string, 0, len(features))
	for name := range features {
		out = append(out, name)
	}
	return out
}

// IsJevFeature reports whether a feature is answered by Jev (/systemone), so a
// question file can only name a switch that governs decision questions.
func IsJevFeature(name string) bool {
	f, ok := features[name]
	return ok && f.endpoint == EndpointSystemOne
}

// featureSwitchKey is the per-account setting that turns one feature off (or, for
// a feature that ships dark, on).
func featureSwitchKey(name string) string { return "llm.feature." + name }

// Settings keys for the caps. The per-account cap is a setting of the account; the
// global one lives in the global scope (an empty account id).
const (
	SettingAccountCapUSD = "llm.cap_usd"
	SettingGlobalCapUSD  = "llm.global_cap_usd"
)
