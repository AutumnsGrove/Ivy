package jev

import "github.com/AutumnsGrove/Ivy/store"

// PhishingRiskQuestion is the id of the phishing question; its answers are the levels.
const (
	PhishingRiskQuestion = "phishing_risk"
	PhishingSuspicious   = "suspicious"
	PhishingLikely       = "likely"
)

// PhishingLevel is the warning to show on a message, or "" for none. Only the model's
// acting answer creates a warning. The provider's verdicts (a failed DMARC, or SPF and
// DKIM both failing) may raise a suspicion to a likelihood, but they never create a
// warning on their own and never lower one: a pass proves little against a lookalike
// domain, so the banner only ever adds caution.
func PhishingLevel(answers []Verdict, a store.AuthResults) string {
	for _, v := range answers {
		if v.QuestionID != PhishingRiskQuestion || !v.Acts() {
			continue
		}
		switch v.Choice {
		case PhishingLikely:
			return PhishingLikely
		case PhishingSuspicious:
			if a.DMARC == "fail" || (a.SPF == "fail" && a.DKIM == "fail") {
				return PhishingLikely
			}
			return PhishingSuspicious
		}
	}
	return ""
}
