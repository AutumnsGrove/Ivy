package config

import "strings"

// providerAuthservIDs maps a provider's domain to the authserv-id it writes into the
// Authentication-Results header it adds (RFC 8601). Only a provider whose header we
// have read on real mail belongs here; an id that is guessed would trust a forgery.
var providerAuthservIDs = map[string]string{
	// Seen on the operator's own mail: "Authentication-Results: purelymail.com; spf=...".
	"purelymail.com": "purelymail.com",
}

// TrustedAuthservIDs is the list of authserv-ids whose verdicts, and whose spam score,
// may be believed for an account. An explicit trusted_authserv_ids wins. Otherwise a
// known provider is trusted by its IMAP host, which is matched on a whole domain
// boundary so a look-alike host cannot borrow the trust. An unknown provider, and the
// loopback dev fake, trust nothing: a sender can write any header, so the default is
// to believe none of them.
func TrustedAuthservIDs(a Account) []string {
	if len(a.TrustedAuthservIDs) > 0 {
		return a.TrustedAuthservIDs
	}
	host := strings.ToLower(strings.TrimSpace(a.IMAPHost))
	for domain, id := range providerAuthservIDs {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return []string{id}
		}
	}
	return nil
}
