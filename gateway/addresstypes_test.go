package gateway

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

const addressTypesPath = "/api/v1/accounts/acct-1/address-types"

func boolp(b bool) *bool { return &b }

func TestMarkingAnAddressListsItAndAnUntouchedOneIsNotListed(t *testing.T) {
	t.Parallel()
	srv, _ := identityServer(t)

	var empty api.AddressTypeList
	if code := getJSON(t, srv.URL+addressTypesPath, &empty); code != http.StatusOK || empty.Types == nil || len(empty.Types) != 0 {
		t.Fatalf("empty list = %+v (status %d), want an empty list, not null", empty, code)
	}

	var saved api.AddressType
	body := api.AddressTypeInput{Address: "Abuse@Example.com", Kind: api.Security}
	if code := doJSON(t, http.MethodPut, srv.URL+addressTypesPath, body, &saved, nil); code != http.StatusOK {
		t.Fatalf("save status = %d", code)
	}
	if saved.Kind != api.Security || saved.Llm {
		t.Fatalf("saved = %+v: a security address must start with models off", saved)
	}
	var list api.AddressTypeList
	getJSON(t, srv.URL+addressTypesPath, &list)
	if len(list.Types) != 1 || list.Types[0].Address != "Abuse@Example.com" {
		t.Fatalf("list = %+v", list.Types)
	}
}

func TestTurningASecurityAddressOnAndClearingIt(t *testing.T) {
	t.Parallel()
	srv, dbs := identityServer(t)
	put := func(in api.AddressTypeInput) api.AddressType {
		var out api.AddressType
		if code := doJSON(t, http.MethodPut, srv.URL+addressTypesPath, in, &out, nil); code != http.StatusOK {
			t.Fatalf("status = %d for %+v", code, in)
		}
		return out
	}
	if got := put(api.AddressTypeInput{Address: "abuse@example.com", Kind: api.Security, Llm: boolp(true)}); !got.Llm {
		t.Fatalf("turned on = %+v", got)
	}
	blocked, _ := dbs.ModelsBlockedFor(t.Context(), "acct-1", "nope")
	if blocked {
		t.Fatal("with the only guarded address turned on, nothing should be guarded")
	}
	// llm means nothing for a contact form: it is stored off whatever was sent.
	if got := put(api.AddressTypeInput{Address: "hello@example.com", Kind: api.ContactForm, Llm: boolp(true)}); got.Llm || got.Kind != api.ContactForm {
		t.Fatalf("contact form = %+v, want llm false", got)
	}
	cleared := put(api.AddressTypeInput{Address: "abuse@example.com", Kind: api.Normal})
	if cleared.Kind != api.Normal || cleared.Llm {
		t.Fatalf("cleared = %+v", cleared)
	}
	var list api.AddressTypeList
	getJSON(t, srv.URL+addressTypesPath, &list)
	if len(list.Types) != 1 || list.Types[0].Address != "hello@example.com" {
		t.Fatalf("list = %+v, want only the contact form", list.Types)
	}
}

func TestAnAddressTypeRefusesBadInput(t *testing.T) {
	t.Parallel()
	srv, _ := identityServer(t)
	for name, in := range map[string]api.AddressTypeInput{
		"a header injection": {Address: "evil@example.com\r\nBcc: x@example.com", Kind: api.Security},
		"no address":         {Address: "", Kind: api.Security},
		"an unknown kind":    {Address: "x@example.com", Kind: "mystery"},
	} {
		var e api.Error
		if code := doJSON(t, http.MethodPut, srv.URL+addressTypesPath, in, &e, nil); code != http.StatusBadRequest || e.Code != "bad_request" {
			t.Errorf("%s: status %d code %q, want 400 bad_request", name, code, e.Code)
		}
	}
	var e api.Error
	if code := doJSON(t, http.MethodPut, srv.URL+"/api/v1/accounts/nope/address-types",
		api.AddressTypeInput{Address: "x@example.com", Kind: api.Security}, &e, nil); code != http.StatusNotFound {
		t.Errorf("unknown account: status %d, want 404", code)
	}
	if code := getJSON(t, srv.URL+"/api/v1/accounts/nope/address-types", &e); code != http.StatusNotFound {
		t.Errorf("unknown account list: status %d, want 404", code)
	}
}

func TestTheMarkedAddressCeilingIs409(t *testing.T) {
	t.Parallel()
	srv, _ := identityServer(t)
	for i := 0; i < store.MaxAddressTypesPerAccount; i++ {
		in := api.AddressTypeInput{Address: fmt.Sprintf("u%d@example.com", i), Kind: api.ContactForm}
		if code := doJSON(t, http.MethodPut, srv.URL+addressTypesPath, in, nil, nil); code != http.StatusOK {
			t.Fatalf("seeding %d: status %d", i, code)
		}
	}
	var e api.Error
	code := doJSON(t, http.MethodPut, srv.URL+addressTypesPath, api.AddressTypeInput{Address: "more@example.com", Kind: api.Security}, &e, nil)
	if code != http.StatusConflict || e.Code != "too_many_addresses" {
		t.Fatalf("over the ceiling: status %d code %q, want 409 too_many_addresses", code, e.Code)
	}
}
