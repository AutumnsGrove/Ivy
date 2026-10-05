package gateway

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
)

func TestListAndGetPeople(t *testing.T) {
	t.Parallel()
	srv, dbs := rulesServer(t)
	if err := dbs.RebuildPeople(context.Background(), "acct-1"); err != nil {
		t.Fatalf("RebuildPeople: %v", err)
	}

	var listed api.PeoplePage
	if code := getJSON(t, srv.URL+"/api/v1/people", &listed); code != http.StatusOK || len(listed.Items) != 3 {
		t.Fatalf("people = %d %+v, want 3", code, listed.Items)
	}
	people := listed.Items
	var billing *api.Person
	for i := range people {
		if people[i].Email == "billing@cloudflare.com" {
			billing = &people[i]
		}
	}
	if billing == nil || billing.Latest != "Receipt for your renewal" || billing.Count != 1 {
		t.Fatalf("billing = %+v", billing)
	}

	var person api.Person
	if code := getJSON(t, srv.URL+"/api/v1/people/"+url.PathEscape("billing@cloudflare.com"), &person); code != http.StatusOK {
		t.Fatalf("get person status = %d", code)
	}
	if len(person.Conversations) != 1 || person.Conversations[0].Subject != "Receipt for your renewal" {
		t.Errorf("conversations = %+v", person.Conversations)
	}
	if len(person.Addresses) != 1 || person.Addresses[0] != "billing@cloudflare.com" {
		t.Errorf("addresses = %v", person.Addresses)
	}
}

func TestMergeAndSplitAddresses(t *testing.T) {
	t.Parallel()
	srv, dbs := rulesServer(t)
	if err := dbs.RebuildPeople(context.Background(), "acct-1"); err != nil {
		t.Fatalf("RebuildPeople: %v", err)
	}
	canonical := "billing@cloudflare.com"
	merged := "news@wildflowers.test"

	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/people/"+url.PathEscape(canonical)+"/addresses",
		map[string]string{"address": merged}, nil); code != http.StatusNoContent {
		t.Fatalf("merge status = %d, want 204", code)
	}

	var listed api.PeoplePage
	if code := getJSON(t, srv.URL+"/api/v1/people", &listed); code != http.StatusOK {
		t.Fatalf("list after merge = %d", code)
	}
	people := listed.Items
	if len(people) != 2 {
		t.Fatalf("after merge there are %d people, want 2", len(people))
	}
	var found *api.Person
	for i := range people {
		if people[i].Id == canonical {
			found = &people[i]
		}
	}
	if found == nil || len(found.Addresses) != 2 || found.Count != 2 {
		t.Fatalf("merged person = %+v, want 2 addresses and 2 messages", found)
	}

	// The person page now spans both addresses' conversations.
	var person api.Person
	if code := getJSON(t, srv.URL+"/api/v1/people/"+url.PathEscape(canonical), &person); code != http.StatusOK || len(person.Conversations) != 2 {
		t.Errorf("merged person page = %d, %d conversations, want 2", code, len(person.Conversations))
	}

	if code := sendJSON(t, http.MethodDelete, srv.URL+"/api/v1/people/"+url.PathEscape(canonical)+"/addresses/"+url.PathEscape(merged), nil, nil); code != http.StatusNoContent {
		t.Fatalf("split status = %d, want 204", code)
	}
	listed = api.PeoplePage{}
	if code := getJSON(t, srv.URL+"/api/v1/people", &listed); code != http.StatusOK || len(listed.Items) != 3 {
		t.Errorf("after split there are %d people, want 3", len(listed.Items))
	}
}
