package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// identityServer is one account with an inbox message in a deterministic store.
func identityServer(t *testing.T) (*httptest.Server, *store.DBs) {
	t.Helper()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com", DisplayName: "Autumn"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})
	return srv, dbs
}

func replyMessage() store.Message {
	return store.Message{
		ID: "m1", AccountID: "acct-1", FolderID: "inbox-1",
		UID: 1, ContentKey: "ck:m1", MessageID: "<mara-1@example.com>",
		InReplyTo: "<root@example.com>", References: "<root@example.com>",
		Subject: "Moving my blog over", Snippet: "Can you help?",
		From:        store.Address{Name: "Mara", Address: "mara@example.com"},
		To:          []store.Address{{Address: "me@example.com"}},
		CC:          []store.Address{{Address: "friend@example.com"}},
		ReplyTo:     []store.Address{{Address: "visitor@example.com"}},
		DeliveredTo: []store.Address{{Address: "feedback@example.com"}},
		BodyText:    "Hi Autumn,\n\nCan you help?",
		Date:        testNow, Flags: []string{},
	}
}

// The account's own address is always an identity, even before any row exists.
func TestListIdentitiesMergesThePrimary(t *testing.T) {
	t.Parallel()
	srv, _ := identityServer(t)

	var list api.IdentityList
	if code := getJSON(t, srv.URL+"/api/v1/accounts/acct-1/identities", &list); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(list.Identities) != 1 {
		t.Fatalf("identities = %+v, want one synthetic primary", list.Identities)
	}
	primary := list.Identities[0]
	if !primary.Primary || primary.Address != "me@example.com" || primary.Name != "Autumn" || primary.Id != "" {
		t.Errorf("primary = %+v, want the account's own address", primary)
	}
}

// Saving an alias creates it; saving the same address again edits it in place.
func TestSaveIdentityCreatesAndEdits(t *testing.T) {
	t.Parallel()
	srv, _ := identityServer(t)

	body := api.IdentityInput{Address: "feedback@example.com", Name: strPtr("Grove"), Signature: strPtr("— The Grove")}
	var saved api.Identity
	if code := doJSON(t, http.MethodPut, srv.URL+"/api/v1/accounts/acct-1/identities", body, &saved, nil); code != http.StatusOK {
		t.Fatalf("save status = %d, want 200", code)
	}
	if saved.Id == "" || saved.Address != "feedback@example.com" || saved.Primary {
		t.Fatalf("saved = %+v, want a stored non-primary identity", saved)
	}

	edit := api.IdentityInput{Address: "feedback@example.com", Name: strPtr("Feedback"), Signature: strPtr("— Grove")}
	if code := doJSON(t, http.MethodPut, srv.URL+"/api/v1/accounts/acct-1/identities", edit, &saved, nil); code != http.StatusOK {
		t.Fatalf("edit status = %d, want 200", code)
	}
	if saved.Name != "Feedback" || saved.Signature != "— Grove" {
		t.Errorf("edited = %+v, want the new fields", saved)
	}

	var list api.IdentityList
	if code := getJSON(t, srv.URL+"/api/v1/accounts/acct-1/identities", &list); code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", code)
	}
	if len(list.Identities) != 2 {
		t.Fatalf("identities = %+v, want the primary and the alias", list.Identities)
	}
}

func TestSaveIdentityRejectsAHostileAddress(t *testing.T) {
	t.Parallel()
	srv, _ := identityServer(t)

	var e api.Error
	code := doJSON(t, http.MethodPut, srv.URL+"/api/v1/accounts/acct-1/identities",
		api.IdentityInput{Address: "evil\r\nBcc: x@example.com"}, &e, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if e.Code != "bad_request" {
		t.Errorf("code = %q, want bad_request", e.Code)
	}
}

// The account's own address is never deletable, and an unknown id is a 404.
func TestDeleteIdentityRefusesThePrimary(t *testing.T) {
	t.Parallel()
	srv, _ := identityServer(t)

	var saved api.Identity
	doJSON(t, http.MethodPut, srv.URL+"/api/v1/accounts/acct-1/identities",
		api.IdentityInput{Address: "me@example.com", Name: strPtr("Me")}, &saved, nil)
	var e api.Error
	if code := doJSON(t, http.MethodDelete, srv.URL+"/api/v1/accounts/acct-1/identities/"+saved.Id, nil, &e, nil); code != http.StatusConflict {
		t.Fatalf("primary delete status = %d, want 409", code)
	}
	if e.Code != "primary_identity" {
		t.Errorf("code = %q, want primary_identity", e.Code)
	}

	var alias api.Identity
	doJSON(t, http.MethodPut, srv.URL+"/api/v1/accounts/acct-1/identities",
		api.IdentityInput{Address: "feedback@example.com"}, &alias, nil)
	if code := doJSON(t, http.MethodDelete, srv.URL+"/api/v1/accounts/acct-1/identities/"+alias.Id, nil, nil, nil); code != http.StatusNoContent {
		t.Fatalf("alias delete status = %d, want 204", code)
	}
	if code := doJSON(t, http.MethodDelete, srv.URL+"/api/v1/accounts/acct-1/identities/"+alias.Id, nil, nil, nil); code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", code)
	}
}

// A send may go from a configured identity, and no further.
func TestSendAcceptsAConfiguredIdentity(t *testing.T) {
	t.Parallel()
	srv, _ := identityServer(t)
	doJSON(t, http.MethodPut, srv.URL+"/api/v1/accounts/acct-1/identities",
		api.IdentityInput{Address: "feedback@example.com", Name: strPtr("Grove")}, nil, nil)

	good := sendRequest("s1")
	good.From = "feedback@example.com"
	if code := postJSON(t, srv.URL+"/api/v1/send", good, nil); code != http.StatusAccepted {
		t.Fatalf("configured identity status = %d, want 202", code)
	}

	bad := sendRequest("s2")
	bad.From = "stranger@example.com"
	var e api.Error
	if code := postJSON(t, srv.URL+"/api/v1/send", bad, &e); code != http.StatusBadRequest {
		t.Fatalf("stranger status = %d, want 400", code)
	}
	if e.Code != "bad_from" {
		t.Errorf("code = %q, want bad_from", e.Code)
	}
}

// The reply prefill sends as the delivered alias, targets Reply-To, threads onto
// the message and applies the alias signature.
func TestReplyPrefillUsesTheDeliveredIdentity(t *testing.T) {
	t.Parallel()
	srv, dbs := identityServer(t)
	mustMessage(t, dbs, replyMessage())
	doJSON(t, http.MethodPut, srv.URL+"/api/v1/accounts/acct-1/identities",
		api.IdentityInput{Address: "feedback@example.com", Name: strPtr("Grove"), Signature: strPtr("— The Grove")}, nil, nil)

	var p api.ComposePrefill
	if code := getJSON(t, srv.URL+"/api/v1/messages/m1/reply", &p); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if p.From != "feedback@example.com" || p.FromName == nil || *p.FromName != "Grove" {
		t.Errorf("from = %q/%v, want the delivered alias", p.From, p.FromName)
	}
	if p.AccountId != "acct-1" {
		t.Errorf("accountId = %q, want acct-1 so the compose screen loads the right identities", p.AccountId)
	}
	if len(p.To) != 1 || p.To[0] != "visitor@example.com" {
		t.Errorf("to = %v, want the Reply-To", p.To)
	}
	if p.Subject != "Re: Moving my blog over" {
		t.Errorf("subject = %q", p.Subject)
	}
	if p.InReplyTo == nil || *p.InReplyTo != "<mara-1@example.com>" {
		t.Errorf("inReplyTo = %v", p.InReplyTo)
	}
	if len(p.References) != 2 || p.References[1] != "<mara-1@example.com>" {
		t.Errorf("references = %v", p.References)
	}
	if !strings.Contains(p.Text, "— The Grove") {
		t.Errorf("text = %q, want the signature applied", p.Text)
	}
	if p.MissingIdentity != nil {
		t.Errorf("missingIdentity = %v, want none", *p.MissingIdentity)
	}
}

// A reply-all Ccs the others, minus the operator's own addresses and the direct
// target. The message has a Reply-To, so the sender is one of the others.
func TestReplyAllPrefillKeepsTheOthers(t *testing.T) {
	t.Parallel()
	srv, dbs := identityServer(t)
	mustMessage(t, dbs, replyMessage())

	var p api.ComposePrefill
	if code := getJSON(t, srv.URL+"/api/v1/messages/m1/reply?all=true", &p); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(p.Cc) != 2 || p.Cc[0] != "mara@example.com" || p.Cc[1] != "friend@example.com" {
		t.Errorf("cc = %v, want the sender then the other Cc", p.Cc)
	}
}

// An unconfigured Delivered-To is reported so the screen can offer to add it.
func TestReplyPrefillReportsAMissingIdentity(t *testing.T) {
	t.Parallel()
	srv, dbs := identityServer(t)
	mustMessage(t, dbs, replyMessage())

	var p api.ComposePrefill
	if code := getJSON(t, srv.URL+"/api/v1/messages/m1/reply", &p); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if p.From != "me@example.com" {
		t.Errorf("from = %q, want the account default", p.From)
	}
	if p.MissingIdentity == nil || *p.MissingIdentity != "feedback@example.com" {
		t.Errorf("missingIdentity = %v, want feedback@example.com", p.MissingIdentity)
	}
}

func TestForwardPrefillCarriesTheOriginal(t *testing.T) {
	t.Parallel()
	srv, dbs := identityServer(t)
	mustMessage(t, dbs, replyMessage())

	var p api.ComposePrefill
	if code := getJSON(t, srv.URL+"/api/v1/messages/m1/forward", &p); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if p.Subject != "Fwd: Moving my blog over" {
		t.Errorf("subject = %q", p.Subject)
	}
	if len(p.To) != 0 || len(p.Cc) != 0 {
		t.Errorf("to/cc = %v/%v, want both empty", p.To, p.Cc)
	}
	for _, want := range []string{"---------- Forwarded message ----------", "From: Mara <mara@example.com>", "Hi Autumn,"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("forward text missing %q:\n%s", want, p.Text)
		}
	}
}

func TestReplyPrefillUnknownMessageIsNotFound(t *testing.T) {
	t.Parallel()
	srv, _ := identityServer(t)
	if code := getJSON(t, srv.URL+"/api/v1/messages/missing/reply", nil); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

// A display name with a line break would be saved and then fail every send as
// that address, so it is refused when it is entered.
func TestSaveIdentityRejectsAHostileName(t *testing.T) {
	t.Parallel()
	srv, _ := identityServer(t)

	name := "Autumn\r\nBcc: x@example.com"
	code := doJSON(t, http.MethodPut, srv.URL+"/api/v1/accounts/acct-1/identities",
		api.IdentityInput{Address: "alias@example.com", Name: &name}, nil, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}
