package mime_test

import (
	"strings"
	"testing"

	ivymime "github.com/AutumnsGrove/Ivy/mime"
)

// TestParseAuthResultsTrustsOnlyConfiguredAuthservID is the N9 fix. RFC 8601
// 4.1: a consumer must not interpret an Authentication-Results header unless
// configured to and its authserv-id is one the ADMD uses. Purelymail adds no
// SPF/DKIM/DMARC verdicts, so a sender-supplied header is the only one a naive
// parser sees; trusting it switches off the spoofed-sender discount.
func TestParseAuthResultsTrustsOnlyConfiguredAuthservID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		values  []string
		trusted []string
		want    ivymime.AuthResults
	}{
		{
			name:   "forged header with no allowlist trusts nothing",
			values: []string{"evil.example; spf=pass; dkim=pass; dmarc=pass"},
			want:   ivymime.AuthResults{Raw: []string{"evil.example; spf=pass; dkim=pass; dmarc=pass"}},
		},
		{
			name:    "forged header with a different authserv-id is ignored",
			values:  []string{"evil.example; spf=pass; dkim=pass; dmarc=pass"},
			trusted: []string{"mx.example.net"},
			want:    ivymime.AuthResults{Raw: []string{"evil.example; spf=pass; dkim=pass; dmarc=pass"}},
		},
		{
			name:    "a trusted header supplies its verdicts",
			values:  []string{"mx.example.net; spf=pass; dkim=pass; dmarc=pass"},
			trusted: []string{"mx.example.net"},
			want: ivymime.AuthResults{
				AuthservID: "mx.example.net", SPF: "pass", DKIM: "pass", DMARC: "pass",
				Raw: []string{"mx.example.net; spf=pass; dkim=pass; dmarc=pass"},
			},
		},
		{
			name:    "an untrusted top header does not hide the trusted one below",
			values:  []string{"evil.example; dmarc=pass", "mx.example.net; dmarc=fail"},
			trusted: []string{"mx.example.net"},
			want: ivymime.AuthResults{
				AuthservID: "mx.example.net", DMARC: "fail",
				Raw: []string{"evil.example; dmarc=pass", "mx.example.net; dmarc=fail"},
			},
		},
		{
			name:    "a later trusted header cannot add a verdict the top trusted one omits",
			values:  []string{"mx.example.net; dmarc=fail", "mx.example.net; dkim=pass"},
			trusted: []string{"mx.example.net"},
			want: ivymime.AuthResults{
				AuthservID: "mx.example.net", DMARC: "fail",
				Raw: []string{"mx.example.net; dmarc=fail", "mx.example.net; dkim=pass"},
			},
		},
		{
			name:    "authserv-id match ignores case and surrounding space",
			values:  []string{"MX.Example.NET ; SPF=Pass"},
			trusted: []string{"mx.example.net"},
			want: ivymime.AuthResults{
				AuthservID: "mx.example.net", SPF: "pass",
				Raw: []string{"MX.Example.NET ; SPF=Pass"},
			},
		},
		{
			name:    "a trusted header with no known method records only its id",
			values:  []string{"mail.purelymail.com; auth=pass"},
			trusted: []string{"mail.purelymail.com"},
			want: ivymime.AuthResults{
				AuthservID: "mail.purelymail.com",
				Raw:        []string{"mail.purelymail.com; auth=pass"},
			},
		},
		{
			name:    "blank trusted ids never match",
			values:  []string{"mx.example.net; dmarc=pass"},
			trusted: []string{"", "   "},
			want:    ivymime.AuthResults{Raw: []string{"mx.example.net; dmarc=pass"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ivymime.ParseAuthResults(tc.values, tc.trusted)
			if got.SPF != tc.want.SPF || got.DKIM != tc.want.DKIM || got.DMARC != tc.want.DMARC || got.AuthservID != tc.want.AuthservID {
				t.Errorf("verdicts = %+v, want %+v", got, tc.want)
			}
			if strings.Join(got.Raw, "\x00") != strings.Join(tc.want.Raw, "\x00") {
				t.Errorf("Raw = %q, want %q", got.Raw, tc.want.Raw)
			}
		})
	}
}
