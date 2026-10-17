package jev

import "github.com/AutumnsGrove/Ivy/store"

// overwhelmingSpam is how many times over the provider's own bar a score must be to
// skip a Junk message. The provider files everything at or above its bar into Junk, so
// "flagged" describes the whole folder; the mail worth rescuing sits just over it.
const overwhelmingSpam = 2.0

// CondemnedByProvider reports whether the signals the provider already attached say a
// Junk message is not worth a model call: a score far past its bar, a failed DMARC, or
// SPF and DKIM both failing. Each verdict is trusted only when it exists (the mirror
// keeps them solely from the provider's own authserv-id), so missing data never skips.
func CondemnedByProvider(a store.AuthResults) bool {
	if a.SpamPresent && a.SpamRequired > 0 && a.SpamScore >= overwhelmingSpam*a.SpamRequired {
		return true
	}
	return a.DMARC == "fail" || (a.SPF == "fail" && a.DKIM == "fail")
}

// JunkRescueQuestion is the id of the Junk rescue question, and LooksReal the one
// answer that may show a chip.
const (
	JunkRescueQuestion = "junk_rescue"
	LooksReal          = "looks_real"
)
