package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
	"github.com/AutumnsGrove/Ivy/waiting"
)

type fakeWaiting struct {
	items   []waiting.Item
	err     error
	account string
	calls   int
}

func (f *fakeWaiting) List(_ context.Context, account string) ([]waiting.Item, error) {
	f.calls++
	f.account = account
	return f.items, f.err
}

func waitingServer(t *testing.T, src WaitingSource) (string, *store.DBs) {
	t.Helper()
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		if src != nil {
			s.WithWaiting(src)
		}
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "sent-1", AccountID: "acct-1", Name: "Sent", Role: store.RoleSent})
	mustMessage(t, dbs, inboxMessage("s1", "acct-1", "sent-1", testNow, false))
	return srv.URL + "/api/v1", dbs
}

func TestWaitingOnIsServedWithPlainRecipientsAndTheAccountFilter(t *testing.T) {
	t.Parallel()
	src := &fakeWaiting{items: []waiting.Item{{
		AccountID: "acct-1", MessageID: "s1", ContentKey: "ck", Subject: "Figures <b>now</b>",
		To:     []store.Address{{Name: "Sam", Address: "sam@example.com"}, {Address: "kim@example.com"}},
		SentAt: testNow.Add(-5 * 24 * time.Hour), Days: 5,
	}}}
	base, _ := waitingServer(t, src)

	var got api.WaitingOn
	if code := getJSON(t, base+"/waiting?account_id=acct-1", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if src.account != "acct-1" {
		t.Errorf("account = %q", src.account)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %+v", got.Items)
	}
	it := got.Items[0]
	if it.Id != "s1" || it.Days != 5 || it.Subject != "Figures <b>now</b>" {
		t.Errorf("item = %+v", it)
	}
	if len(it.To) != 2 || it.To[0] != "Sam" || it.To[1] != "kim@example.com" {
		t.Errorf("to = %v, want a name when there is one and the address when not", it.To)
	}
	if !it.SentAt.Equal(testNow.Add(-5 * 24 * time.Hour)) {
		t.Errorf("sentAt = %v", it.SentAt)
	}
}

// A list that cannot be built is an empty list: the screen is a nicety, so a failure
// behind it is logged and never shown as an error.
func TestWaitingOnFailingOrAbsentIsAnEmptyListNotAnError(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]WaitingSource{
		"not wired": nil,
		"nothing":   &fakeWaiting{},
		"failed":    &fakeWaiting{err: errors.New("provider down")},
	} {
		base, _ := waitingServer(t, src)
		var got api.WaitingOn
		if code := getJSON(t, base+"/waiting", &got); code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", name, code)
		}
		if len(got.Items) != 0 {
			t.Errorf("%s: items = %+v, want none", name, got.Items)
		}
	}
}

func TestStoppingAWaitIsKeptForTheMessageAndRepeatingItIsFine(t *testing.T) {
	t.Parallel()
	base, dbs := waitingServer(t, &fakeWaiting{})
	for i := 0; i < 2; i++ {
		if code := doJSON(t, http.MethodPost, base+"/waiting/s1/stop", nil, nil, nil); code != http.StatusNoContent {
			t.Fatalf("stop #%d status = %d, want 204", i+1, code)
		}
	}
	refs, err := dbs.StoppedWaitingRefs(context.Background(), "acct-1")
	if err != nil || len(refs) != 1 || refs[0].ContentKey != "ck:s1" {
		t.Fatalf("stopped = %v, %v", refs, err)
	}
	if code := doJSON(t, http.MethodPost, base+"/waiting/nope/stop", nil, nil, nil); code != http.StatusNotFound {
		t.Fatalf("missing message status = %d, want 404", code)
	}
}
