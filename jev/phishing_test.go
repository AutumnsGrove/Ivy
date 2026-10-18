package jev

import (
	"testing"

	"github.com/AutumnsGrove/Ivy/store"
)

func phishingVerdict(choice string, fires, suppressed bool) Verdict {
	return Verdict{QuestionID: PhishingRiskQuestion, Choice: choice, Fires: fires, Suppressed: suppressed}
}

// The warning can only add caution. The model's answer is what creates it; the provider's
// verdicts may raise a suspicion to a likelihood but never invent one and never lower one.
func TestPhishingLevelOnlyEverAddsCaution(t *testing.T) {
	t.Parallel()
	failed := store.AuthResults{DMARC: "fail"}
	bothFailed := store.AuthResults{SPF: "fail", DKIM: "fail"}
	passed := store.AuthResults{SPF: "pass", DKIM: "pass", DMARC: "pass"}
	cases := []struct {
		name string
		v    Verdict
		auth store.AuthResults
		want string
	}{
		{"none shows nothing", phishingVerdict("none", false, false), store.AuthResults{}, ""},
		{"none stays nothing even when auth fails", phishingVerdict("none", false, false), failed, ""},
		{"suspicious", phishingVerdict("suspicious", true, false), store.AuthResults{}, "suspicious"},
		{"likely", phishingVerdict("likely", true, false), store.AuthResults{}, "likely"},
		{"a pass never lowers likely", phishingVerdict("likely", true, false), passed, "likely"},
		{"a pass never lowers suspicious", phishingVerdict("suspicious", true, false), passed, "suspicious"},
		{"a DMARC fail raises suspicious", phishingVerdict("suspicious", true, false), failed, "likely"},
		{"SPF and DKIM both failing raises suspicious", phishingVerdict("suspicious", true, false), bothFailed, "likely"},
		{"one failing check alone does not raise", phishingVerdict("suspicious", true, false), store.AuthResults{SPF: "fail"}, "suspicious"},
		{"below its bar shows nothing", phishingVerdict("suspicious", false, false), failed, ""},
		{"a suppressed answer shows nothing", phishingVerdict("likely", true, true), failed, ""},
		{"another question's answer is ignored", Verdict{QuestionID: "category", Choice: "likely", Fires: true}, failed, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := PhishingLevel([]Verdict{c.v}, c.auth); got != c.want {
				t.Fatalf("level = %q, want %q", got, c.want)
			}
		})
	}
}
