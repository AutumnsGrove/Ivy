package config

import (
	"slices"
	"testing"
)

// The provider vouches for a message with an Authentication-Results line carrying its
// own authserv-id. Which id that is belongs to the provider, so a known one is trusted
// without the operator having to look it up (5d decision 1).
func TestAKnownProvidersAuthservIDIsTrustedByDefault(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		acct Account
		want []string
	}{
		{"Purelymail by its IMAP host", Account{IMAPHost: "imap.purelymail.com"}, []string{"purelymail.com"}},
		{"the host's case does not matter", Account{IMAPHost: "IMAP.PureLymail.com"}, []string{"purelymail.com"}},
		{"an explicit list wins", Account{IMAPHost: "imap.purelymail.com", TrustedAuthservIDs: []string{"mx.example.net"}}, []string{"mx.example.net"}},
		{"an unknown provider trusts nothing", Account{IMAPHost: "imap.example.net"}, nil},
		{"the loopback dev fake trusts nothing", Account{IMAPHost: "127.0.0.1"}, nil},
		{"no host trusts nothing", Account{}, nil},
		// A look-alike must not borrow Purelymail's trust.
		{"a look-alike host", Account{IMAPHost: "imap.purelymail.com.evil.example"}, nil},
		{"a host that only ends the same", Account{IMAPHost: "notpurelymail.com"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TrustedAuthservIDs(tc.acct); !slices.Equal(got, tc.want) {
				t.Errorf("TrustedAuthservIDs = %v, want %v", got, tc.want)
			}
		})
	}
}
