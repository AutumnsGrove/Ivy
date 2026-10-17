package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// mailTo files one message addressed to the given recipients and returns nothing; the
// tests ask about it by content key.
func mailTo(t *testing.T, dbs *DBs, account, key string, to, cc, delivered []string) {
	t.Helper()
	ctx := context.Background()
	seedAccount(t, dbs, account)
	folder := account + "-inbox"
	if _, err := dbs.GetFolderByID(ctx, folder); err != nil {
		if err := dbs.UpsertFolder(ctx, Folder{ID: folder, AccountID: account, Name: "INBOX", Role: RoleInbox}); err != nil {
			t.Fatal(err)
		}
	}
	addrs := func(list []string) []Address {
		var out []Address
		for _, a := range list {
			out = append(out, Address{Address: a})
		}
		return out
	}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "m-" + account + "-" + key, AccountID: account, FolderID: folder, UID: uint32(len(key)) + 1, ContentKey: key,
		Subject: "s", Date: now, InternalDate: now, From: Address{Address: "them@example.org"},
		To: addrs(to), CC: addrs(cc), DeliveredTo: addrs(delivered),
	}); err != nil {
		t.Fatal(err)
	}
}

func setType(t *testing.T, dbs *DBs, account, address string, kind MailType, llm bool) {
	t.Helper()
	if err := dbs.SetAddressType(context.Background(), AddressTypeInput{
		AccountID: account, Address: address, Kind: kind, LLMOn: llm, Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func blocked(t *testing.T, dbs *DBs, account, key string) bool {
	t.Helper()
	b, err := dbs.ModelsBlockedFor(context.Background(), account, key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// 5d.5: mail sent to a security or abuse address is kept from every model until the
// operator turns that address on. The address is matched wherever the message names it,
// and case never decides.
func TestMailToASecurityAddressIsBlockedFromModelsUntilItIsTurnedOn(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	mailTo(t, dbs, "a", "to", []string{"Abuse@Example.com"}, nil, nil)
	mailTo(t, dbs, "a", "cc", []string{"me@example.com"}, []string{"abuse@example.com"}, nil)
	mailTo(t, dbs, "a", "bcc", []string{"list@example.org"}, nil, []string{"abuse@example.com"})
	mailTo(t, dbs, "a", "plain", []string{"me@example.com"}, nil, []string{"me@example.com"})

	for _, k := range []string{"to", "cc", "bcc", "plain"} {
		if blocked(t, dbs, "a", k) {
			t.Fatalf("%s blocked before any address was marked", k)
		}
	}

	setType(t, dbs, "a", "abuse@example.com", MailSecurity, false)
	for k, want := range map[string]bool{"to": true, "cc": true, "bcc": true, "plain": false} {
		if got := blocked(t, dbs, "a", k); got != want {
			t.Errorf("%s: blocked = %v, want %v (recipient, Cc and Delivered-To all count, in any case)", k, got, want)
		}
	}

	setType(t, dbs, "a", "abuse@example.com", MailSecurity, true)
	if blocked(t, dbs, "a", "to") {
		t.Error("an address the operator turned on is still blocked")
	}
	setType(t, dbs, "a", "abuse@example.com", MailSecurity, false)
	if !blocked(t, dbs, "a", "to") {
		t.Error("turning the address back off did not block it again")
	}
}

// A mail type belongs to one account's address; another account that receives mail at the
// same address is unaffected, and so is a type that is not security.
func TestAMailTypeIsPerAccountAndOnlySecurityBlocks(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	mailTo(t, dbs, "a", "k", []string{"abuse@example.com"}, nil, nil)
	mailTo(t, dbs, "b", "k", []string{"abuse@example.com"}, nil, nil)

	setType(t, dbs, "a", "abuse@example.com", MailSecurity, false)
	if !blocked(t, dbs, "a", "k") || blocked(t, dbs, "b", "k") {
		t.Fatal("a mail type reached the wrong account")
	}
	setType(t, dbs, "a", "abuse@example.com", MailContactForm, false)
	if blocked(t, dbs, "a", "k") {
		t.Error("a contact-form address blocks models; only security and abuse addresses do")
	}
}

// "Normal" is the absence of a type, so setting it removes the row.
func TestSettingNormalForgetsTheType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	setType(t, dbs, "a", "hello@example.com", MailContactForm, false)
	setType(t, dbs, "a", "Abuse@example.com", MailSecurity, false)
	got, err := dbs.ListAddressTypes(ctx, "a")
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %+v, %v", got, err)
	}
	setType(t, dbs, "a", "ABUSE@example.com", MailNormal, false)
	got, err = dbs.ListAddressTypes(ctx, "a")
	if err != nil || len(got) != 1 || got[0].Address != "hello@example.com" || got[0].Kind != MailContactForm {
		t.Fatalf("after normal, list = %+v, %v", got, err)
	}
}

func TestAnAddressTypeIsValidatedAndBounded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	for _, bad := range []string{"", "no-at-sign", "a@b\r\nBcc: x@y.z", "über@example.com"} {
		err := dbs.SetAddressType(ctx, AddressTypeInput{AccountID: "a", Address: bad, Kind: MailSecurity, Now: time.Now()})
		if !errors.Is(err, ErrIdentityAddress) {
			t.Errorf("address %q: err = %v, want ErrIdentityAddress", bad, err)
		}
	}
	if err := dbs.SetAddressType(ctx, AddressTypeInput{AccountID: "a", Address: "x@example.com", Kind: "mystery", Now: time.Now()}); !errors.Is(err, ErrBadMailType) {
		t.Errorf("unknown kind: err = %v, want ErrBadMailType", err)
	}
	for i := 0; i < MaxAddressTypesPerAccount; i++ {
		setType(t, dbs, "a", fmt.Sprintf("u%d@example.com", i), MailContactForm, false)
	}
	err := dbs.SetAddressType(ctx, AddressTypeInput{AccountID: "a", Address: "one-more@example.com", Kind: MailSecurity, Now: time.Now()})
	if !errors.Is(err, ErrAddressTypeLimit) {
		t.Errorf("over the limit: err = %v, want ErrAddressTypeLimit", err)
	}
	// An edit of an existing address is still allowed at the ceiling.
	setType(t, dbs, "a", "u0@example.com", MailSecurity, false)
}

// A message the mirror does not know, with no security address marked, is not blocked
// and costs no mirror read; with one marked, the answer for an unknown message fails
// closed rather than guessing it is fine.
func TestAnUnknownMessageFailsClosedOnlyWhenAddressesAreGuarded(t *testing.T) {
	t.Parallel()
	dbs := openTemp(t)
	if blocked(t, dbs, "a", "nope") {
		t.Fatal("blocked with nothing guarded")
	}
	setType(t, dbs, "a", "abuse@example.com", MailSecurity, false)
	if !blocked(t, dbs, "a", "nope") {
		t.Fatal("a message the mirror cannot find was allowed while a security address is guarded: its recipients are unknown")
	}
}

// Attachment text is embedded under the file's hash, not a message key, and one file
// can arrive on several messages. A file carried by any guarded message is guarded.
func TestAFileCarriedByAGuardedMessageIsGuardedToo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	mailTo(t, dbs, "a", "guarded", []string{"abuse@example.com"}, nil, nil)
	mailTo(t, dbs, "a", "plain", []string{"me@example.com"}, nil, nil)
	for msg, hash := range map[string]string{"m-a-guarded": "h-shared", "m-a-plain": "h-shared"} {
		if err := dbs.ReplaceMessageAttachments(ctx, msg, []Attachment{{ID: "att-" + msg, MessageID: msg, Filename: "f.pdf", ContentHash: hash}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := dbs.ReplaceMessageAttachments(ctx, "m-a-plain", []Attachment{
		{ID: "att-p1", MessageID: "m-a-plain", Filename: "f.pdf", ContentHash: "h-shared"},
		{ID: "att-p2", MessageID: "m-a-plain", Filename: "g.pdf", ContentHash: "h-own"},
	}); err != nil {
		t.Fatal(err)
	}
	setType(t, dbs, "a", "abuse@example.com", MailSecurity, false)

	if !blocked(t, dbs, "a", "h-shared") {
		t.Error("a file that also arrived on a guarded message was left readable")
	}
	if blocked(t, dbs, "a", "h-own") {
		t.Error("a file only an ordinary message carried was blocked")
	}
	if blocked(t, dbs, "a", "plain") {
		t.Error("an ordinary message was blocked because it shares a file")
	}
}
