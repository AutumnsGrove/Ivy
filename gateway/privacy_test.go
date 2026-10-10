package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/jev"
	"github.com/AutumnsGrove/Ivy/store"
)

type fakePrivacy struct {
	status  jev.VetStatus
	err     error
	account string
	key     string
}

func (f *fakePrivacy) Status(_ context.Context, account, key string) (jev.VetStatus, error) {
	f.account, f.key = account, key
	return f.status, f.err
}

func privacyServer(t *testing.T, src PrivacySource) (base string, dbs *store.DBs) {
	t.Helper()
	srv, dbs := newConfiguredServer(t, func(s *Server) {
		if src != nil {
			s.WithPrivacy(src)
		}
	})
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	mustMessage(t, dbs, inboxMessage("m1", "acct-1", "inbox-1", testNow, false))
	return srv.URL + "/api/v1", dbs
}

// The reader asks one cheap question per message: is it withheld, and why. It is
// read by account and content key, the same key the cached answers use.
func TestPrivacyReportsWhyAMessageIsWithheld(t *testing.T) {
	t.Parallel()
	src := &fakePrivacy{status: jev.VetStatus{State: jev.VetWithheld, Reasons: []string{"injection", "financial"}}}
	base, _ := privacyServer(t, src)

	var got api.MessagePrivacy
	if code := getJSON(t, base+"/messages/m1/privacy", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if src.account != "acct-1" || src.key != "ck:m1" {
		t.Fatalf("asked for %q/%q", src.account, src.key)
	}
	if got.State != api.Withheld || got.Overridden || len(got.Reasons) != 2 || got.Reasons[0] != "injection" {
		t.Fatalf("privacy = %+v", got)
	}
}

// Without the Jev layer, or on a read error, the reader must see "nothing to say",
// never a failure: this is a quiet marker on the reading path.
func TestPrivacyIsQuietWhenNothingIsWired(t *testing.T) {
	t.Parallel()
	base, _ := privacyServer(t, nil)
	var got api.MessagePrivacy
	if code := getJSON(t, base+"/messages/m1/privacy", &got); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if got.State != api.Unchecked || got.Reasons == nil || len(got.Reasons) != 0 {
		t.Fatalf("want unchecked with an empty (not null) list, got %+v", got)
	}

	failing, _ := privacyServer(t, &fakePrivacy{err: errors.New("disk")})
	if code := getJSON(t, failing+"/messages/m1/privacy", &got); code != http.StatusInternalServerError {
		t.Fatalf("a read error = %d, want 500 so it is visible and not silently clear", code)
	}
}

func TestPrivacyForAnUnknownMessageIsNotFound(t *testing.T) {
	t.Parallel()
	base, _ := privacyServer(t, &fakePrivacy{})
	if code := getJSON(t, base+"/messages/nope/privacy", nil); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

// Letting helpers read a message is the operator's click: it is stored against the
// message's content key, repeatable, and undone by a delete.
func TestPrivacyOverrideIsSetAndCleared(t *testing.T) {
	t.Parallel()
	base, dbs := privacyServer(t, &fakePrivacy{})
	ctx := context.Background()
	url := base + "/messages/m1/privacy/override"

	for i := 0; i < 2; i++ { // a double tap is not an error
		if code := doJSON(t, http.MethodPut, url, nil, nil, nil); code != http.StatusNoContent {
			t.Fatalf("PUT #%d = %d, want 204", i+1, code)
		}
	}
	if on, _ := dbs.VettingOverridden(ctx, "acct-1", "ck:m1"); !on {
		t.Fatal("the override was not stored against the account and content key")
	}
	for i := 0; i < 2; i++ {
		if code := doJSON(t, http.MethodDelete, url, nil, nil, nil); code != http.StatusNoContent {
			t.Fatalf("DELETE #%d = %d, want 204", i+1, code)
		}
	}
	if on, _ := dbs.VettingOverridden(ctx, "acct-1", "ck:m1"); on {
		t.Fatal("the override survived a delete")
	}
	if code := doJSON(t, http.MethodPut, base+"/messages/nope/privacy/override", nil, nil, nil); code != http.StatusNotFound {
		t.Fatalf("override of an unknown message = %d, want 404", code)
	}
}
