package store

import (
	"context"
	"testing"
	"time"
)

func TestFTSQueryIsSafeAndPhraseAware(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"domain renewal", `"domain" "renewal"`},
		{"  spaced   out  ", `"spaced" "out"`},
		{"renew*", `"renew"*`},
		{"domain renew*", `"domain" "renew"*`},
		{`"domain renewal"`, `"domain renewal"`},
		{`he said "a b" c`, `"he" "said" "a b" "c"`},
		{`quote"inside`, `"quoteinside"`},
		{`OR NEAR(`, `"OR" "NEAR("`},
		{"*", ""},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := FTSQuery(tc.in); got != tc.want {
			t.Errorf("FTSQuery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSearchFindsSubjectBodyAndAttachment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct")
	seedFolder(t, dbs, "acct", "f1")

	uid := 0
	put := func(id, key, subject, body string) {
		t.Helper()
		uid++
		if err := dbs.UpsertMessage(ctx, Message{
			ID: id, AccountID: "acct", FolderID: "f1", UID: uint32(uid),
			ContentKey: key, Subject: subject, BodyText: body, Date: time.Now(),
		}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
		if err := dbs.IndexSearchDoc(ctx, SearchDoc{
			AccountID: "acct", ContentKey: key, Subject: subject, Body: body,
		}); err != nil {
			t.Fatalf("index %s: %v", id, err)
		}
	}
	put("m1", "ck1", "Domain renewal", "Your domain invoice is attached")
	put("m2", "ck2", "Lunch", "See you at noon")

	for _, tc := range []struct{ query, want string }{
		{"invoice", "ck1"},
		{"domain", "ck1"},
		{"renew*", "ck1"},
		{"lunch", "ck2"},
		{"zzz", ""},
	} {
		hits, err := dbs.SearchFTS(ctx, tc.query, nil, 10)
		if err != nil {
			t.Fatalf("search %q: %v", tc.query, err)
		}
		if tc.want == "" {
			if len(hits) != 0 {
				t.Errorf("search %q returned %d hits, want none", tc.query, len(hits))
			}
			continue
		}
		if len(hits) == 0 || hits[0].ContentKey != tc.want {
			t.Errorf("search %q hits = %+v, want %s first", tc.query, hits, tc.want)
		}
	}
}

func TestSearchFoldsDiacritics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct")
	seedFolder(t, dbs, "acct", "f1")
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "m1", AccountID: "acct", FolderID: "f1", UID: 1,
		ContentKey: "ck1", Subject: "Café meeting", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.IndexSearchDoc(ctx, SearchDoc{
		AccountID: "acct", ContentKey: "ck1", Subject: "Café meeting",
	}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"café", "cafe"} {
		hits, err := dbs.SearchFTS(ctx, q, nil, 10)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if len(hits) != 1 {
			t.Errorf("search %q returned %d hits, want 1", q, len(hits))
		}
	}
}

func TestSearchExcludesHiddenMail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct")
	seedFolder(t, dbs, "acct", "f1")
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "m1", AccountID: "acct", FolderID: "f1", UID: 1,
		ContentKey: "ck1", Subject: "Secret invoice", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.IndexSearchDoc(ctx, SearchDoc{AccountID: "acct", ContentKey: "ck1", Subject: "Secret invoice"}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := dbs.SearchFTS(ctx, "secret", nil, 10); len(hits) != 1 {
		t.Fatalf("before disable: %d hits, want 1", len(hits))
	}
	if err := dbs.DisableMessage(ctx, "m1", "server_removed", time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if hits, _ := dbs.SearchFTS(ctx, "secret", nil, 10); len(hits) != 0 {
		t.Fatalf("after disable: %d hits, want 0", len(hits))
	}
}

func TestSearchReturnsOneHitForACopyInTwoFolders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct")
	seedFolder(t, dbs, "acct", "f1")
	seedFolder(t, dbs, "acct", "f2")
	for i, folder := range []string{"f1", "f2"} {
		if err := dbs.UpsertMessage(ctx, Message{
			ID: "m" + folder, AccountID: "acct", FolderID: folder, UID: uint32(i + 1),
			ContentKey: "shared", Subject: "Shared meeting", Date: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := dbs.IndexSearchDoc(ctx, SearchDoc{AccountID: "acct", ContentKey: "shared", Subject: "Shared meeting"}); err != nil {
		t.Fatal(err)
	}
	hits, err := dbs.SearchFTS(ctx, "shared", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1 (a copy in two folders is one message)", len(hits))
	}
}

func TestSearchScopesToAccounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "a")
	seedAccount(t, dbs, "b")
	seedFolder(t, dbs, "a", "fa")
	seedFolder(t, dbs, "b", "fb")
	for _, tc := range []struct{ id, acct, folder, key string }{
		{"m1", "a", "fa", "ka"}, {"m2", "b", "fb", "kb"},
	} {
		if err := dbs.UpsertMessage(ctx, Message{
			ID: tc.id, AccountID: tc.acct, FolderID: tc.folder, UID: 1,
			ContentKey: tc.key, Subject: "quarterly report", Date: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := dbs.IndexSearchDoc(ctx, SearchDoc{AccountID: tc.acct, ContentKey: tc.key, Subject: "quarterly report"}); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := dbs.SearchFTS(ctx, "quarterly", []string{"b"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].AccountID != "b" {
		t.Fatalf("hits = %+v, want only account b", hits)
	}
}

func TestSearchIndexIsReplaceable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	seedAccount(t, dbs, "acct")
	seedFolder(t, dbs, "acct", "f1")
	if err := dbs.UpsertMessage(ctx, Message{
		ID: "m1", AccountID: "acct", FolderID: "f1", UID: 1,
		ContentKey: "ck1", Subject: "First subject", Date: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.IndexSearchDoc(ctx, SearchDoc{AccountID: "acct", ContentKey: "ck1", Subject: "First subject"}); err != nil {
		t.Fatal(err)
	}
	if err := dbs.IndexSearchDoc(ctx, SearchDoc{AccountID: "acct", ContentKey: "ck1", Subject: "Second subject"}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := dbs.SearchFTS(ctx, "first", nil, 10); len(hits) != 0 {
		t.Errorf("stale index row survived a re-index: %+v", hits)
	}
	if hits, _ := dbs.SearchFTS(ctx, "second", nil, 10); len(hits) != 1 {
		t.Errorf("re-index not searchable: %+v", hits)
	}
}

func TestExtractedTextRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	in := ExtractedText{Ref: "hash1", Kind: ExtractKindAttachment, Tier: 1, Status: "ok", Text: "invoice total 42", DerivedVersion: 1}
	if err := dbs.UpsertExtractedText(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := dbs.GetExtractedText(ctx, "hash1", ExtractKindAttachment)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != in.Text || got.Tier != 1 || got.Status != "ok" {
		t.Fatalf("got %+v, want %+v", got, in)
	}
	in.Text = "revised"
	if err := dbs.UpsertExtractedText(ctx, in); err != nil {
		t.Fatal(err)
	}
	if got, _ := dbs.GetExtractedText(ctx, "hash1", ExtractKindAttachment); got.Text != "revised" {
		t.Fatalf("update not applied: %+v", got)
	}
	if _, err := dbs.GetExtractedText(ctx, "missing", ExtractKindAttachment); err != ErrNotFound {
		t.Fatalf("missing ref error = %v, want ErrNotFound", err)
	}
}
