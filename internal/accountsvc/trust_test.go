package accountsvc

import (
	"slices"
	"testing"

	"github.com/AutumnsGrove/Ivy/config"
)

// The sync worker is built from SyncAccount, so this is where a provider's own
// authserv-id has to arrive, or the score and verdicts would be dropped as untrusted.
func TestSyncAccountTrustsAKnownProvidersAuthservID(t *testing.T) {
	t.Parallel()
	got := SyncAccount(config.Account{ID: "a", IMAPHost: "imap.purelymail.com"}).TrustedAuthservIDs
	if !slices.Equal(got, []string{"purelymail.com"}) {
		t.Errorf("TrustedAuthservIDs = %v, want [purelymail.com]", got)
	}
	if got := SyncAccount(config.Account{ID: "b", IMAPHost: "127.0.0.1"}).TrustedAuthservIDs; len(got) != 0 {
		t.Errorf("the dev fake trusts %v", got)
	}
}
