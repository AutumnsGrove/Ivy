package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/store"
)

func sendJSON(t *testing.T, method, url string, body, out any) int {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, rd)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func mustTag(t *testing.T, dbs *store.DBs, id, name string) store.Tag {
	t.Helper()
	tag, err := dbs.CreateTag(context.Background(), id, name, "sky")
	if err != nil {
		t.Fatalf("CreateTag(%s): %v", name, err)
	}
	return tag
}

func TestCreateListAndUpdateTags(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)

	var created api.UserTag
	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/tags", map[string]string{"name": " Café ", "color": "rose"}, &created); code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", code)
	}
	if created.Id == "" || created.Name != "Café" || created.Slug != "cafe" || created.Color != api.Rose || created.Count != 0 {
		t.Errorf("created = %+v", created)
	}
	// No colour asked for gets the accent, so a tag is never colourless.
	var plain api.UserTag
	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/tags", map[string]string{"name": "Cafe"}, &plain); code != http.StatusCreated || plain.Color != api.Lilac {
		t.Errorf("default colour: status %d, tag %+v, want 201 and lilac", code, plain)
	}
	// A clashing name gets its own slug, not an error.
	stored, err := dbs.GetTag(context.Background(), plain.Id)
	if err != nil || stored.Slug != "cafe-2" {
		t.Errorf("second slug = %q, %v; want cafe-2", stored.Slug, err)
	}
	if err := dbs.TagMessage(context.Background(), "acct-1", "ck:m1", created.Id, "operator"); err != nil {
		t.Fatal(err)
	}

	var overview api.TagsOverview
	if code := getJSON(t, srv.URL+"/api/v1/tags", &overview); code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	if len(overview.Mine) != 2 || overview.Mine[0].Name != "Cafe" || overview.Mine[1].Name != "Café" || overview.Mine[1].Count != 1 {
		t.Errorf("mine = %+v, want Cafe(0) and Café(1) by name", overview.Mine)
	}
	if overview.Placed == nil || len(overview.Placed) != 0 || overview.ActiveRules != 0 {
		t.Errorf("placed/activeRules = %+v/%d, want an empty list and 0 until chunk 5 and 3g", overview.Placed, overview.ActiveRules)
	}

	var renamed api.UserTag
	code := sendJSON(t, http.MethodPatch, srv.URL+"/api/v1/tags/"+created.Id, map[string]string{"name": "Coffee"}, &renamed)
	if code != http.StatusOK || renamed.Name != "Coffee" || renamed.Color != api.Rose || renamed.Count != 1 {
		t.Errorf("rename: status %d, tag %+v; want 200, a new name, the colour kept and the count", code, renamed)
	}
	if got, _ := dbs.GetTag(context.Background(), created.Id); got.Slug != "cafe" {
		t.Errorf("slug after rename = %q, want cafe kept", got.Slug)
	}
}

func TestTagRequestsAreValidated(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	tag := mustTag(t, dbs, "t1", "Work")

	for name, body := range map[string]any{
		"blank name":     map[string]string{"name": "  "},
		"missing name":   map[string]string{},
		"unknown colour": map[string]string{"name": "x", "color": "chartreuse"},
		"not an object":  []int{1},
	} {
		var e api.Error
		if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/tags", body, &e); code != http.StatusBadRequest || e.Code != "bad_request" {
			t.Errorf("create with %s: %d/%s, want 400/bad_request", name, code, e.Code)
		}
	}
	var e api.Error
	if code := sendJSON(t, http.MethodPatch, srv.URL+"/api/v1/tags/"+tag.ID, map[string]string{"color": "chartreuse"}, &e); code != http.StatusBadRequest {
		t.Errorf("patch with a bad colour = %d, want 400", code)
	}
	if code := sendJSON(t, http.MethodPatch, srv.URL+"/api/v1/tags/nope", map[string]string{"name": "x"}, &e); code != http.StatusNotFound || e.Code != "not_found" {
		t.Errorf("patch of a missing tag = %d/%s, want 404/not_found", code, e.Code)
	}
	if code := sendJSON(t, http.MethodDelete, srv.URL+"/api/v1/tags/nope", nil, &e); code != http.StatusNotFound {
		t.Errorf("delete of a missing tag = %d, want 404", code)
	}
}

func TestCreatingTooManyTagsIsRefused(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	for i := range store.MaxTags {
		mustTag(t, dbs, fmt.Sprintf("t%d", i), fmt.Sprintf("tag %d", i))
	}
	var e api.Error
	if code := sendJSON(t, http.MethodPost, srv.URL+"/api/v1/tags", map[string]string{"name": "one more"}, &e); code != http.StatusConflict || e.Code != "too_many_tags" {
		t.Errorf("past the limit = %d/%s, want 409/too_many_tags", code, e.Code)
	}
}

// Tagging is the keyword written through the outbox; the membership waits for the
// server, so the response is an op, not a changed tag.
func TestEnqueueTagCreatesAKeywordOp(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	tag := mustTag(t, dbs, "t1", "Work")

	var item api.OutboxItem
	code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: api.OutboxActionTag, TagId: &tag.ID}, &item)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if item.Kind != api.OutboxKindFlags || item.FlagsAdd == nil || !slices.Equal(*item.FlagsAdd, []string{"$ivy-work"}) {
		t.Errorf("op = %s %v, want flags adding $ivy-work", item.Kind, item.FlagsAdd)
	}
	members, _ := dbs.TagMembers(context.Background(), tag.ID)
	if len(members) != 0 {
		t.Errorf("membership %v was written before the server took the keyword", members)
	}

	var e api.Error
	missing := "nope"
	for name, id := range map[string]*string{"unknown": &missing, "absent": nil} {
		code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: api.OutboxActionTag, TagId: id}, &e)
		if code != http.StatusConflict || e.Code != "unknown_tag" {
			t.Errorf("tag with an %s id = %d/%s, want 409/unknown_tag", name, code, e.Code)
		}
	}
}

// Untagging clears the keyword from every live copy that carries it (N8), and
// always queues the targeted row, so a local-only tag still loses its membership.
func TestEnqueueUntagClearsEveryCopy(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	tag := mustTag(t, dbs, "t1", "Work")
	mustFolder(t, dbs, store.Folder{ID: "archive-1", AccountID: "acct-1", Name: "Archive", Role: store.RoleArchive})
	copyRow := inboxMessage("m2", "acct-1", "archive-1", testNow, false)
	copyRow.ContentKey = "ck:m1"
	copyRow.Flags = []string{"$IVY-Work"}
	mustMessage(t, dbs, copyRow)
	other := inboxMessage("m3", "acct-1", "inbox-1", testNow, false)
	other.Flags = []string{"$ivy-work"}
	mustMessage(t, dbs, other)

	var item api.OutboxItem
	code := postJSON(t, srv.URL+"/api/v1/outbox", api.OutboxAction{MessageId: "m1", Action: api.OutboxActionUntag, TagId: &tag.ID}, &item)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	if item.MessageId != "m1" || item.FlagsClear == nil || !slices.Equal(*item.FlagsClear, []string{"$ivy-work"}) {
		t.Errorf("returned op = %+v, want m1 clearing $ivy-work", item)
	}
	ops, err := dbs.OutboxByAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	var folders []string
	for _, op := range ops {
		folders = append(folders, op.SourceFolderID+"/"+op.ContentKey)
	}
	slices.Sort(folders)
	if want := []string{"archive-1/ck:m1", "inbox-1/ck:m1"}; !slices.Equal(folders, want) {
		t.Errorf("queued ops = %v, want %v (the other message is untouched)", folders, want)
	}
}

// Deleting a tag clears its keyword from the server first (through the outbox),
// then drops the tag and its memberships.
func TestDeleteTagQueuesKeywordRemovalThenDeletes(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	ctx := context.Background()
	tag := mustTag(t, dbs, "t1", "Work")
	carried := inboxMessage("m2", "acct-1", "inbox-1", testNow, false)
	carried.Flags = []string{"$ivy-work"}
	mustMessage(t, dbs, carried)
	for _, key := range []string{"ck:m1", "ck:m2"} {
		if err := dbs.TagMessage(ctx, "acct-1", key, tag.ID, "operator"); err != nil {
			t.Fatal(err)
		}
	}

	if code := sendJSON(t, http.MethodDelete, srv.URL+"/api/v1/tags/"+tag.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", code)
	}
	if _, err := dbs.GetTag(ctx, tag.ID); err == nil {
		t.Error("the tag survived its deletion")
	}
	ops, err := dbs.OutboxByAccount(ctx, "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	// m1 never carried the keyword (a local-only tag), so only m2 needs the server.
	if len(ops) != 1 || ops[0].ContentKey != "ck:m2" || !slices.Equal(ops[0].Expect.FlagsClear, []string{"$ivy-work"}) {
		t.Errorf("ops = %+v, want one op clearing the keyword from m2", ops)
	}
}

// If the keyword cannot be cleared everywhere because the outbox is nearly full,
// nothing is deleted: a half-deleted tag would leave orphans.
func TestDeleteTagRefusedWhenTheOutboxCannotHoldTheClears(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	ctx := context.Background()
	tag := mustTag(t, dbs, "t1", "Work")
	for i := range 2 {
		m := inboxMessage(fmt.Sprintf("c%d", i), "acct-1", "inbox-1", testNow, false)
		m.Flags = []string{"$ivy-work"}
		mustMessage(t, dbs, m)
		if err := dbs.TagMessage(ctx, "acct-1", m.ContentKey, tag.ID, "operator"); err != nil {
			t.Fatal(err)
		}
	}
	for i := range store.MaxQueuedOps - 1 {
		if _, _, err := dbs.EnqueueOutbox(ctx, store.OutboxOp{
			ID: fmt.Sprintf("fill-%d", i), AccountID: "acct-1", Kind: store.OutboxFlags,
			ContentKey: fmt.Sprintf("fill-%d", i), SourceFolderID: "inbox-1",
			Expect: store.OutboxExpect{FlagsAdd: []string{`\Seen`}}, CreatedAt: testNow,
		}); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}

	var e api.Error
	if code := sendJSON(t, http.MethodDelete, srv.URL+"/api/v1/tags/"+tag.ID, nil, &e); code != http.StatusConflict || e.Code != "outbox_full" {
		t.Fatalf("delete = %d/%s, want 409/outbox_full", code, e.Code)
	}
	if _, err := dbs.GetTag(ctx, tag.ID); err != nil {
		t.Errorf("the tag was deleted although its keywords could not be cleared: %v", err)
	}
	ops, _ := dbs.OutboxByAccount(ctx, "acct-1")
	if len(ops) != store.MaxQueuedOps-1 {
		t.Errorf("%d ops queued, want the %d fillers only", len(ops), store.MaxQueuedOps-1)
	}
}

// Messages carry their first tag's name, by name, so the list and the reader can
// show it.
func TestMessagesCarryTheirTag(t *testing.T) {
	t.Parallel()
	srv, dbs := outboxServer(t)
	ctx := context.Background()
	work := mustTag(t, dbs, "t1", "Work")
	bills := mustTag(t, dbs, "t2", "Bills")
	for _, id := range []string{work.ID, bills.ID} {
		if err := dbs.TagMessage(ctx, "acct-1", "ck:m1", id, "operator"); err != nil {
			t.Fatal(err)
		}
	}

	var inbox api.Inbox
	if code := getJSON(t, srv.URL+"/api/v1/inbox", &inbox); code != http.StatusOK || len(inbox.Items) != 1 {
		t.Fatalf("inbox = %d, %+v", code, inbox)
	}
	if inbox.Items[0].Tag == nil || *inbox.Items[0].Tag != "Bills" {
		t.Errorf("list tag = %v, want Bills (first by name)", inbox.Items[0].Tag)
	}
	var msg api.MailMessage
	if code := getJSON(t, srv.URL+"/api/v1/messages/m1", &msg); code != http.StatusOK || msg.Tag == nil || *msg.Tag != "Bills" {
		t.Errorf("message tag = %v (status %d), want Bills", msg.Tag, code)
	}
	if msg.TagIds == nil || !slices.Equal(*msg.TagIds, []string{bills.ID, work.ID}) {
		t.Errorf("message tagIds = %v, want [%s %s] for the picker", msg.TagIds, bills.ID, work.ID)
	}

	untagged := inboxMessage("m9", "acct-1", "inbox-1", testNow, false)
	mustMessage(t, dbs, untagged)
	var bare api.MailMessage
	if code := getJSON(t, srv.URL+"/api/v1/messages/m9", &bare); code != http.StatusOK || bare.Tag != nil {
		t.Errorf("untagged message tag = %v, want none", bare.Tag)
	}
}
