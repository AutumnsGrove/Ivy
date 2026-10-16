package mime_test

import (
	"strings"
	"testing"

	ivymime "github.com/AutumnsGrove/Ivy/mime"
)

func TestParseSpamStatusReadsTheProvidersScore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		values []string
		want   ivymime.SpamStatus
	}{
		{
			name:   "a clean message as Purelymail writes it",
			values: []string{"No, score=-3.03 required=5 tests=DKIM_SIGNED=0.1,DKIM_VALID=-0.1 autolearn=ham autolearn_force=no version=4.0.0"},
			want:   ivymime.SpamStatus{Present: true, Score: -3.03, Required: 5},
		},
		{
			name:   "flagged as spam",
			values: []string{"Yes, score=9.4 required=5.0 tests=BAYES_99=3.5"},
			want:   ivymime.SpamStatus{Present: true, Flagged: true, Score: 9.4, Required: 5},
		},
		{
			name:   "the older hits= spelling",
			values: []string{"No, hits=1.2 required=5.0 tests=X"},
			want:   ivymime.SpamStatus{Present: true, Score: 1.2, Required: 5},
		},
		{
			name:   "case and spacing vary",
			values: []string{"  YES ,  score=6  required=5"},
			want:   ivymime.SpamStatus{Present: true, Flagged: true, Score: 6, Required: 5},
		},
		{
			// The receiving server prepends its own header, so the topmost is the one
			// that counts; a sender's forged header sits below it.
			name:   "the topmost header wins over a forged one below it",
			values: []string{"Yes, score=8 required=5", "No, score=-99 required=5"},
			want:   ivymime.SpamStatus{Present: true, Flagged: true, Score: 8, Required: 5},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ivymime.ParseSpamStatus(tc.values); got != tc.want {
				t.Errorf("ParseSpamStatus = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A score is a hint shown beside a message, never an all-clear, so anything that is
// not clearly the SpamAssassin form reads as "no score" and never as a number.
func TestParseSpamStatusRefusesWhatItCannotReadAndNeverGuesses(t *testing.T) {
	t.Parallel()
	for name, values := range map[string][]string{
		"no header":              nil,
		"empty":                  {""},
		"not the form":           {"looks fine to me"},
		"no verdict word":        {"score=1 required=5"},
		"a verdict but no score": {"No, required=5"},
		"a score that is words":  {"No, score=lots required=5"},
		"not finite":             {"No, score=NaN required=5"},
		"infinite":               {"No, score=Inf required=5"},
		"absurdly large":         {"No, score=99999999 required=5"},
		"absurd threshold":       {"No, score=1 required=99999999"},
		"exponent form":          {"No, score=1e9 required=5"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := ivymime.ParseSpamStatus(values); got.Present {
				t.Errorf("ParseSpamStatus(%q) = %+v, want no score", values, got)
			}
		})
	}
}

// The header can list hundreds of rule names; only its start is read.
func TestParseSpamStatusReadsOnlyTheStartOfAHugeHeader(t *testing.T) {
	t.Parallel()
	huge := "No, score=0.5 required=5 tests=" + strings.Repeat("RULE=0.1,", 200_000)
	got := ivymime.ParseSpamStatus([]string{huge})
	if want := (ivymime.SpamStatus{Present: true, Score: 0.5, Required: 5}); got != want {
		t.Errorf("ParseSpamStatus = %+v, want %+v", got, want)
	}
}

// A sender can write any header, so the provider's score is believed only when the
// same message also carries a verdict line from a server we trust (decision 1).
func TestTheScoreIsBelievedOnlyWhenATrustedServerVouchedForTheMessage(t *testing.T) {
	t.Parallel()
	raw := func(authserv string) []byte {
		return []byte("From: a@example.com\r\nTo: me@example.com\r\nSubject: hi\r\n" +
			"X-Spam-Status: No, score=-3.03 required=5 tests=X\r\n" +
			"Authentication-Results: " + authserv + "; spf=pass; dkim=pass; dmarc=pass\r\n\r\nbody\r\n")
	}
	trusted := ivymime.Parse(raw("purelymail.com"), "purelymail.com")
	if want := (ivymime.SpamStatus{Present: true, Score: -3.03, Required: 5}); trusted.Spam != want {
		t.Errorf("with a trusted server: %+v, want %+v", trusted.Spam, want)
	}
	if got := ivymime.Parse(raw("purelymail.com")).Spam; got.Present {
		t.Errorf("with no trust configured the score was believed: %+v", got)
	}
	if got := ivymime.Parse(raw("evil.example"), "purelymail.com").Spam; got.Present {
		t.Errorf("an untrusted server's line made the score believable: %+v", got)
	}
	streamed := ivymime.ParseStream(strings.NewReader(string(raw("purelymail.com"))), "purelymail.com")
	if !streamed.Spam.Present {
		t.Error("the streaming parser does not read the score")
	}
}
