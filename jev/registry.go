// Package jev is the question layer over the gate's Decide: a registry of typed
// questions as data, the state a message is reduced to, the cache of answers and
// the worker that fills it. It owns no provider client; every call goes through
// llm.Gate.
package jev

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"

	"github.com/goccy/go-yaml"

	"github.com/AutumnsGrove/Ivy/llm"
)

// Bounds on the registry. A question set is operator-sized, but it is parsed from a
// file and a settings row, so each limit has a defined refusal above it.
const (
	// MaxQuestions is the operator's limit per Jev call (docs/chunk5/5b: 100). Only
	// about 20 are measured for quality and latency, so the larger sets wait on a spike.
	MaxQuestions = 100
	// MaxOptions is Jev's own limit on a question's option set.
	MaxOptions = 255
	// MaxInstructionBytes keeps one question near the ~100 tokens the cost model assumes.
	MaxInstructionBytes = 4 << 10
	// MaxFileBytes bounds a YAML document before it is parsed.
	MaxFileBytes = 1 << 20

	maxFieldBytes = 512
)

// Folders a question may run on. Eligible mail is the Inbox, plus Junk for a
// question that names it (junk_rescue); nothing else is classified automatically.
const (
	FolderInbox = "inbox"
	FolderJunk  = "junk"
)

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_:]{0,63}$`)

// Question is one thing Ivy asks Jev about a message. It is data: the wording lives
// in YAML or in the operator's settings, never in Go.
type Question struct {
	ID           string            `yaml:"id"`
	Instructions string            `yaml:"instructions"`
	Criteria     map[string]string `yaml:"criteria"`
	// QuietOption is the answer when nothing fires. Acting on it is never allowed.
	QuietOption string  `yaml:"quiet_option"`
	Threshold   float64 `yaml:"threshold"`
	// Scope lists the accounts the question runs for; empty means every account.
	Scope []string `yaml:"scope,omitempty"`
	// Folders lists where it runs; empty means the Inbox only.
	Folders []string `yaml:"folders,omitempty"`
	// Enabled is a pointer so an absent key means on.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Suppresses names questions whose result is held back when this one fires.
	Suppresses []string `yaml:"suppresses,omitempty"`
	// Feature names the per-account switch that must be on for the question to be
	// asked (for example junk_rescue). Empty means it rides on the shared call's own
	// feature. It never changes what an answer means, so it is not in the hash.
	Feature string `yaml:"feature,omitempty"`
}

// IsEnabled reports whether the question is switched on (the default).
func (q Question) IsEnabled() bool { return q.Enabled == nil || *q.Enabled }

// Hash identifies what Jev is actually asked: the instructions and the option set.
// Threshold, scope, folders, the switch, the quiet option and suppression are
// deliberately left out. They tune how an answer is used, not what it is, and a
// cached answer must survive them or every threshold nudge would re-bill the mailbox.
func (q Question) Hash() string {
	h := sha256.New()
	field := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	field(q.Instructions)
	keys := make([]string, 0, len(q.Criteria))
	for k := range q.Criteria {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		field(k)
		field(q.Criteria[k])
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// ToLLM is the wire form the gate sends: always a choice, since yes/no is modelled
// as a choice until noul and score are calibrated on real mail.
func (q Question) ToLLM() llm.Question {
	crit := make(map[string]string, len(q.Criteria))
	for k, v := range q.Criteria {
		crit[k] = v
	}
	return llm.Question{Type: "choice", Instructions: q.Instructions, Criteria: crit}
}

func (q Question) clone() Question {
	c := q
	c.Criteria = make(map[string]string, len(q.Criteria))
	for k, v := range q.Criteria {
		c.Criteria[k] = v
	}
	c.Scope = append([]string(nil), q.Scope...)
	c.Folders = append([]string(nil), q.Folders...)
	c.Suppresses = append([]string(nil), q.Suppresses...)
	if q.Enabled != nil {
		on := *q.Enabled
		c.Enabled = &on
	}
	return c
}

func (q Question) runsOn(account, folder string) bool {
	if !q.IsEnabled() {
		return false
	}
	if len(q.Scope) > 0 && !contains(q.Scope, account) {
		return false
	}
	if len(q.Folders) == 0 {
		return folder == FolderInbox
	}
	return contains(q.Folders, folder)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

type file struct {
	Questions *[]Question `yaml:"questions"`
}

// Parse reads and validates a question file. Unknown keys are refused, as in the
// config loader, so a typo cannot silently drop a threshold.
func Parse(data []byte) ([]Question, error) {
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("question file is %d bytes, over the %d limit", len(data), MaxFileBytes)
	}
	var f file
	if err := yaml.UnmarshalWithOptions(data, &f, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parse question file: %w", err)
	}
	if f.Questions == nil {
		return nil, errors.New("question file has no questions list")
	}
	if err := Validate(*f.Questions); err != nil {
		return nil, err
	}
	return *f.Questions, nil
}

// Validate checks a whole set: each question alone, then the references between
// them. A merged registry is validated as one set, so an edit cannot leave a
// built-in's suppression dangling.
func Validate(qs []Question) error {
	if len(qs) > MaxQuestions {
		return fmt.Errorf("%d questions is over the limit of %d", len(qs), MaxQuestions)
	}
	seen := make(map[string]bool, len(qs))
	for _, q := range qs {
		if err := validateOne(q); err != nil {
			return fmt.Errorf("question %q: %w", q.ID, err)
		}
		if seen[q.ID] {
			return fmt.Errorf("question %q is defined twice", q.ID)
		}
		seen[q.ID] = true
	}
	for _, q := range qs {
		for _, s := range q.Suppresses {
			switch {
			case s == q.ID:
				return fmt.Errorf("question %q suppresses itself", q.ID)
			case !seen[s]:
				return fmt.Errorf("question %q suppresses %q, which does not exist", q.ID, s)
			}
		}
	}
	return nil
}

func validateOne(q Question) error {
	switch {
	case !idPattern.MatchString(q.ID):
		return errors.New("id must be lowercase letters, digits, _ or : and start with a letter (64 at most)")
	case q.Instructions == "":
		return errors.New("no instructions")
	case len(q.Instructions) > MaxInstructionBytes:
		return fmt.Errorf("instructions are %d bytes, over %d", len(q.Instructions), MaxInstructionBytes)
	case len(q.Criteria) < 2:
		return errors.New("needs at least two options, one of them quiet")
	case len(q.Criteria) > MaxOptions:
		return fmt.Errorf("%d options is over %d", len(q.Criteria), MaxOptions)
	case q.Threshold <= 0 || q.Threshold > 1:
		return fmt.Errorf("threshold %v is outside (0, 1]", q.Threshold)
	}
	for k, v := range q.Criteria {
		if k == "" || len(k) > maxFieldBytes || len(v) > maxFieldBytes {
			return errors.New("an option has an empty name or a name or description over the limit")
		}
	}
	if _, ok := q.Criteria[q.QuietOption]; !ok {
		return fmt.Errorf("quiet option %q is not one of the options", q.QuietOption)
	}
	for _, a := range q.Scope {
		if a == "" || len(a) > maxFieldBytes {
			return errors.New("scope names an empty or oversized account id")
		}
	}
	if q.Feature != "" && !llm.IsJevFeature(q.Feature) {
		return fmt.Errorf("feature %q is not a Jev feature", q.Feature)
	}
	for _, f := range q.Folders {
		if f != FolderInbox && f != FolderJunk {
			return fmt.Errorf("folder %q is not %s or %s", f, FolderInbox, FolderJunk)
		}
	}
	return nil
}

//go:embed builtin.yaml
var builtinYAML []byte

// Builtin returns the questions that ship in the binary. The set is empty until
// 5c to 5g add theirs.
func Builtin() ([]Question, error) { return Parse(builtinYAML) }

// Registry is the live question set: the built-ins with the operator's edits and
// own questions laid over them by id. It is safe for concurrent use; Set swaps the
// whole set atomically so a worker never sees half of an edit.
type Registry struct {
	mu  sync.RWMutex
	set map[string]Question
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{set: map[string]Question{}} }

// Set replaces the live registry with builtin overlaid by user, matched by id. The
// merged whole is validated first; on any error the live registry is untouched, so
// a bad edit can never leave the worker without questions.
func (r *Registry) Set(builtin, user []Question) error {
	merged := make(map[string]Question, len(builtin)+len(user))
	for _, q := range builtin {
		merged[q.ID] = q.clone()
	}
	for _, q := range user {
		merged[q.ID] = q.clone()
	}
	all := make([]Question, 0, len(merged))
	for _, q := range merged {
		all = append(all, q)
	}
	if err := Validate(all); err != nil {
		return err
	}
	r.mu.Lock()
	r.set = merged
	r.mu.Unlock()
	return nil
}

// Get returns one question by id.
func (r *Registry) Get(id string) (Question, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	q, ok := r.set[id]
	return q.clone(), ok
}

// All returns every question, enabled or not, ordered by id.
func (r *Registry) All() []Question {
	return r.collect(func(Question) bool { return true })
}

// Active returns the questions to ask about a message in this account and folder,
// ordered by id so a call's question set is stable.
func (r *Registry) Active(account, folder string) []Question {
	return r.collect(func(q Question) bool { return q.runsOn(account, folder) })
}

func (r *Registry) collect(keep func(Question) bool) []Question {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Question, 0, len(r.set))
	for _, q := range r.set {
		if keep(q) {
			out = append(out, q.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
