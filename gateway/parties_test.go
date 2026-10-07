package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

// Issue #15: the reader's sender sheet needs the real addresses, who else was on
// the message, the identity the People page uses, and an honest auth verdict.
func TestMessageCarriesItsPartiesAndAuth(t *testing.T) {
	t.Parallel()
	srv, dbs := newSeededServer(t)
	mustAccount(t, dbs, store.Account{ID: "acct-1", Address: "me@example.com"})
	mustFolder(t, dbs, store.Folder{ID: "inbox-1", AccountID: "acct-1", Name: "INBOX", Role: store.RoleInbox})

	signed := inboxMessage("signed", "acct-1", "inbox-1", testNow, false)
	signed.From = store.Address{Name: `"Mara Linden"`, Address: "Mara@Example.com"}
	signed.To = []store.Address{{Address: "me@example.com"}, {Name: "Taro", Address: "taro@example.org"}}
	signed.CC = []store.Address{{Name: "Ops", Address: "ops@example.net"}}
	signed.AuthResults = store.AuthResults{AuthservID: "mx.example.com", SPF: "pass", DKIM: "pass", DMARC: "pass"}
	mustMessage(t, dbs, signed)

	bare := inboxMessage("bare", "acct-1", "inbox-1", testNow, false)
	bare.From = store.Address{Name: "me@example.com", Address: "phish@evil.test"} // an address as the name
	mustMessage(t, dbs, bare)

	failing := inboxMessage("failing", "acct-1", "inbox-1", testNow, false)
	failing.AuthResults = store.AuthResults{AuthservID: "mx.example.com", SPF: "pass", DKIM: "fail", DMARC: "fail"}
	mustMessage(t, dbs, failing)

	// The operator merged Mara's second address into her canonical one.
	if err := dbs.LinkPerson(context.Background(), "mara@example.com", "mara.linden@example.com", testNow); err != nil {
		t.Fatalf("LinkPerson: %v", err)
	}

	get := func(id string) api.MailMessage {
		t.Helper()
		var m api.MailMessage
		if code := getJSON(t, srv.URL+"/api/v1/messages/"+id, &m); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		return m
	}

	m := get("signed")
	if m.Sender.Name != "Mara Linden" || m.Sender.Address != "mara@example.com" {
		t.Errorf("sender = %+v, want the unquoted name and the lower-cased address", m.Sender)
	}
	if m.Sender.PersonId == nil || *m.Sender.PersonId != "mara.linden@example.com" {
		t.Errorf("sender person = %v, want the merged canonical address", m.Sender.PersonId)
	}
	if len(m.To) != 2 || m.To[0].PersonId != nil {
		t.Errorf("to = %+v; the operator's own address has no People page", m.To)
	}
	if m.To[1].Name != "Taro" || m.To[1].PersonId == nil || *m.To[1].PersonId != "taro@example.org" {
		t.Errorf("to[1] = %+v", m.To[1])
	}
	if len(m.Cc) != 1 || m.Cc[0].Address != "ops@example.net" {
		t.Errorf("cc = %+v", m.Cc)
	}
	if m.Auth.State != api.Pass || m.Auth.AuthservId == nil || *m.Auth.AuthservId != "mx.example.com" {
		t.Errorf("auth = %+v, want pass from mx.example.com", m.Auth)
	}

	// No header means "unknown", never a green tick.
	b := get("bare")
	if b.Auth.State != api.None {
		t.Errorf("auth with no header = %+v, want none", b.Auth)
	}
	if b.Sender.Name != "me@example.com" || b.Sender.Address != "phish@evil.test" {
		t.Errorf("sender = %+v, want both the claimed name and the real address, untouched", b.Sender)
	}
	if len(b.To) != 0 || len(b.Cc) != 0 {
		t.Errorf("to/cc = %+v/%+v, want empty lists, not null", b.To, b.Cc)
	}

	if f := get("failing"); f.Auth.State != api.Fail {
		t.Errorf("auth = %+v, want fail", f.Auth)
	}
}
