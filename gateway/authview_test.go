package gateway

import (
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

func TestTheAuthViewCarriesTheProvidersSpamScoreAndNeverReadsItAsAnAllClear(t *testing.T) {
	t.Parallel()
	scored := store.AuthResults{
		AuthservID: "purelymail.com", SPF: "pass", DKIM: "pass", DMARC: "pass",
		SpamPresent: true, SpamScore: -3.03, SpamRequired: 5,
	}
	got := authView(scored)
	if got.State != api.Pass || got.Spam == nil || got.Spam.Score != -3.03 || got.Spam.Required != 5 || got.Spam.Flagged {
		t.Errorf("a scored, verified message = %+v spam %+v", got, got.Spam)
	}

	flagged := store.AuthResults{AuthservID: "purelymail.com", SpamPresent: true, SpamFlagged: true, SpamScore: 9.4, SpamRequired: 5}
	got = authView(flagged)
	if got.Spam == nil || !got.Spam.Flagged || got.Spam.Score != 9.4 {
		t.Errorf("a flagged message = %+v", got.Spam)
	}
	// A trusted header with a score but no verdicts is still "unknown", never a pass.
	if got.State != api.None {
		t.Errorf("state = %q with no verdicts, want none: a score is not a verdict", got.State)
	}

	// A real score of zero is a score, not an absence.
	zero := authView(store.AuthResults{AuthservID: "purelymail.com", SpamPresent: true, SpamRequired: 5})
	if zero.Spam == nil || zero.Spam.Score != 0 {
		t.Errorf("a score of 0 was dropped: %+v", zero.Spam)
	}
	if none := authView(store.AuthResults{SPF: "pass"}); none.Spam != nil {
		t.Errorf("a message with no score carries one: %+v", none.Spam)
	}
}
