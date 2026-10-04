package devstack

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

// statesJSON is the single list of named states (DEV.md section 4): their names,
// what they do, and the ?scenario= screens the frontend shows for them. The
// Playwright suite reads the same file, so the dev stack, the mock app and the
// tests cannot disagree about what a state is.
//
//go:embed states.json
var statesJSON []byte

// State is one named dev condition from DEV.md section 4: a mailworld fault
// plus any tweak needed to reproduce a screen. It is defined once so E2E and
// visual-baseline tests drive the same condition as a human.
type State struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Scenarios are the frontend ?scenario= names that show this state's screens
	// while the backend is mocked. Empty when no scenario exists for it.
	Scenarios []string `json:"scenarios"`
	apply     func(*mailworld.World)
}

// faults is what each state does to the world, keyed by name. Every name in
// states.json needs an entry (checked when the registry loads).
var faults = map[string]func(*mailworld.World){
	"sync-auth-failed":   func(w *mailworld.World) { w.Fault(mailworld.AuthFail{}) },
	"unreachable":        func(w *mailworld.World) { w.Fault(mailworld.Unreachable{}) },
	"backfilling":        func(w *mailworld.World) { w.Fault(mailworld.Latency{Delay: 100 * time.Millisecond}) },
	"fetch-failed":       func(w *mailworld.World) { w.Fault(mailworld.FailFetch{}) },
	"send-too-large":     func(w *mailworld.World) { w.Fault(mailworld.SMTPReject{Code: 552, Message: "message too large"}) },
	"send-transient-4xx": func(w *mailworld.World) { w.Fault(mailworld.SMTPReject{Code: 450, Message: "try again later"}) },
	"llm-cap-reached":    func(w *mailworld.World) { w.Fault(mailworld.LLMCapReached{}) },
	"llm-provider-down":  func(w *mailworld.World) { w.Fault(mailworld.LLMDown{}) },
	"offline":            func(w *mailworld.World) { w.Fault(mailworld.Unreachable{}) },
	"mirror-healthy":     func(*mailworld.World) {},
}

// states is the registry, in the order states.json lists them.
var states = loadStates()

func loadStates() []State {
	var list []State
	if err := json.Unmarshal(statesJSON, &list); err != nil {
		panic(fmt.Sprintf("devstack: states.json: %v", err))
	}
	if len(list) != len(faults) {
		panic(fmt.Sprintf("devstack: states.json has %d states but %d faults are defined", len(list), len(faults)))
	}
	for i := range list {
		apply, ok := faults[list[i].Name]
		if !ok {
			panic(fmt.Sprintf("devstack: state %q has no fault defined", list[i].Name))
		}
		list[i].apply = apply
	}
	return list
}

// States returns every named state in DEV.md section 4.
func States() []State { return states }

// ApplyState clears any previous condition and applies the named one, so states
// do not stack across calls.
func ApplyState(w *mailworld.World, name string) error {
	for _, s := range states {
		if s.Name == name {
			w.ClearFaults()
			s.apply(w)
			return nil
		}
	}
	return fmt.Errorf("devstack: unknown state %q", name)
}
