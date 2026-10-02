package devstack

import (
	"fmt"
	"time"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
)

// State is one named dev condition from DEV.md section 4: a mailworld fault
// plus any tweak needed to reproduce a screen. It is defined once so E2E and
// visual-baseline tests drive the same condition as a human.
type State struct {
	Name        string
	Description string
	apply       func(*mailworld.World)
}

// states is the DEV.md section 4 registry, in the order the doc lists them.
var states = []State{
	{"sync-auth-failed", "IMAP login is rejected", func(w *mailworld.World) {
		w.Fault(mailworld.AuthFail{})
	}},
	{"unreachable", "connections drop immediately", func(w *mailworld.World) {
		w.Fault(mailworld.DropConnection{After: 0})
	}},
	{"backfilling", "IMAP responses are slow, as while a sync runs", func(w *mailworld.World) {
		w.Fault(mailworld.Latency{Delay: 100 * time.Millisecond})
	}},
	{"fetch-failed", "FETCH fails, for message and attachment errors", func(w *mailworld.World) {
		w.Fault(mailworld.FailFetch{})
	}},
	{"send-too-large", "SMTP rejects with 552", func(w *mailworld.World) {
		w.Fault(mailworld.SMTPReject{Code: 552, Message: "message too large"})
	}},
	{"send-transient-4xx", "SMTP rejects with a transient 450", func(w *mailworld.World) {
		w.Fault(mailworld.SMTPReject{Code: 450, Message: "try again later"})
	}},
	{"llm-cap-reached", "LLM calls answer 429", func(w *mailworld.World) {
		w.Fault(mailworld.LLMCapReached{})
	}},
	{"llm-provider-down", "LLM calls answer 503", func(w *mailworld.World) {
		w.Fault(mailworld.LLMDown{})
	}},
	{"offline", "the provider is unreachable", func(w *mailworld.World) {
		w.Fault(mailworld.DropConnection{After: 0})
	}},
	{"mirror-healthy", "clear every fault, the happy path", func(*mailworld.World) {}},
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
