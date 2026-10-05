package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTagSlug(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("ab ", 40)
	cases := []struct{ name, want string }{
		{"receipts", "receipts"},
		{"Contact Form", "contact-form"},
		{"Café", "cafe"},
		{"Ünïcödé Ñame", "unicode-name"},
		{"  --Hello__World!! ", "hello-world"},
		{"A/B", "a-b"},
		{"日本語", ""},
		{"日本 notes", "notes"},
		{"", ""},
		{long, strings.TrimSuffix(strings.Repeat("ab-", 16), "-")},
	}
	for _, c := range cases {
		if got := TagSlug(c.name); got != c.want {
			t.Errorf("TagSlug(%q) = %q, want %q", c.name, got, c.want)
		}
		if got := TagSlug(c.name); len(got) > MaxTagSlugLen {
			t.Errorf("TagSlug(%q) = %d bytes, over the %d limit", c.name, len(got), MaxTagSlugLen)
		}
	}
}

func TestTagKeywordRoundTripAndHostileKeywords(t *testing.T) {
	t.Parallel()
	if got := TagKeyword("work"); got != "$ivy-work" {
		t.Errorf("TagKeyword = %q", got)
	}
	for _, flag := range []string{"$ivy-work", "$IVY-Work", "$Ivy-work"} {
		if slug, ok := SlugFromKeyword(flag); !ok || slug != "work" {
			t.Errorf("SlugFromKeyword(%q) = %q, %v; want work, true", flag, slug, ok)
		}
	}
	hostile := []string{
		`\Seen`, "$ivy-", "$ivy", "ivy-work", "$ivy-wör", "$ivy-a b", "$ivy-a/b",
		"$ivy-" + strings.Repeat("a", MaxTagSlugLen+1),
	}
	for _, flag := range hostile {
		if slug, ok := SlugFromKeyword(flag); ok {
			t.Errorf("SlugFromKeyword(%q) = %q, true; want it refused", flag, slug)
		}
	}
	if _, ok := SlugFromKeyword("$ivy-" + strings.Repeat("a", MaxTagSlugLen)); !ok {
		t.Error("a slug at the limit was refused")
	}
}

func TestCreateTagNumbersCollidingSlugs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	var slugs []string
	for _, name := range []string{"Café", "Cafe", "CAFE", "日本語", "中文"} {
		tag, err := dbs.CreateTag(ctx, "id-"+name, name, "sky")
		if err != nil {
			t.Fatalf("CreateTag(%q): %v", name, err)
		}
		slugs = append(slugs, tag.Slug)
	}
	if want := "cafe cafe-2 cafe-3 tag tag-2"; strings.Join(slugs, " ") != want {
		t.Errorf("slugs = %v, want %s", slugs, want)
	}

	// A slug already at the limit still has room for its suffix.
	full := strings.Repeat("x", MaxTagSlugLen)
	a, err := dbs.CreateTag(ctx, "long-1", full, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := dbs.CreateTag(ctx, "long-2", full, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Slug != full || b.Slug != full[:MaxTagSlugLen-2]+"-2" || len(b.Slug) > MaxTagSlugLen {
		t.Errorf("slugs = %q, %q", a.Slug, b.Slug)
	}
}

func TestCreateTagRefusesBadNamesAndTooManyTags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	for _, name := range []string{"", "   ", strings.Repeat("a", MaxTagNameLen+1)} {
		if _, err := dbs.CreateTag(ctx, "x", name, ""); !errors.Is(err, ErrTagName) {
			t.Errorf("CreateTag(%d-byte name) = %v, want ErrTagName", len(name), err)
		}
	}
	if _, err := dbs.CreateTag(ctx, "ok", strings.Repeat("a", MaxTagNameLen), ""); err != nil {
		t.Errorf("a name at the limit was refused: %v", err)
	}
	for i := 1; i < MaxTags; i++ {
		if _, err := dbs.CreateTag(ctx, fmt.Sprintf("t%d", i), fmt.Sprintf("tag %d", i), ""); err != nil {
			t.Fatalf("tag %d: %v", i, err)
		}
	}
	if _, err := dbs.CreateTag(ctx, "over", "one too many", ""); !errors.Is(err, ErrTagLimit) {
		t.Errorf("tag past the limit = %v, want ErrTagLimit", err)
	}
}

func TestUpdateTagRenamesWithoutChangingTheSlug(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	created, err := dbs.CreateTag(ctx, "t1", "Receipts", "sky")
	if err != nil {
		t.Fatal(err)
	}

	got, err := dbs.UpdateTag(ctx, "t1", "Bills & receipts", "coral")
	if err != nil {
		t.Fatalf("UpdateTag: %v", err)
	}
	if got.Name != "Bills & receipts" || got.Color != "coral" || got.Slug != created.Slug {
		t.Errorf("tag = %+v, want a new name and colour with slug %q kept", got, created.Slug)
	}
	if _, err := dbs.UpdateTag(ctx, "nope", "x", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateTag of a missing tag = %v, want ErrNotFound", err)
	}
	if _, err := dbs.UpdateTag(ctx, "t1", " ", ""); !errors.Is(err, ErrTagName) {
		t.Errorf("UpdateTag with a blank name = %v, want ErrTagName", err)
	}
}

func TestTagMembershipListingCountsAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	work, _ := dbs.CreateTag(ctx, "work", "Work", "sky")
	home, _ := dbs.CreateTag(ctx, "home", "Home", "rose")
	for _, m := range []struct{ acct, key, tag string }{
		{"a1", "k1", work.ID}, {"a1", "k2", work.ID}, {"a2", "k1", work.ID}, {"a1", "k1", home.ID},
	} {
		if err := dbs.TagMessage(ctx, m.acct, m.key, m.tag, "operator"); err != nil {
			t.Fatal(err)
		}
	}

	list, err := dbs.ListTags(ctx)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(list) != 2 || list[0].ID != "home" || list[0].Count != 1 || list[1].ID != "work" || list[1].Count != 3 {
		t.Errorf("tags = %+v, want home(1) then work(3) by name", list)
	}

	byKey, err := dbs.TagsForMessages(ctx, "a1", []string{"k1", "k2", "k3"})
	if err != nil {
		t.Fatalf("TagsForMessages: %v", err)
	}
	if len(byKey["k1"]) != 2 || byKey["k1"][0].ID != "home" || len(byKey["k2"]) != 1 || len(byKey["k3"]) != 0 {
		t.Errorf("by key = %+v", byKey)
	}

	members, err := dbs.TagMembers(ctx, work.ID)
	if err != nil || len(members) != 3 {
		t.Fatalf("members = %+v, %v; want 3", members, err)
	}

	if err := dbs.UntagMessage(ctx, "a1", "k1", work.ID); err != nil {
		t.Fatalf("UntagMessage: %v", err)
	}
	if err := dbs.UntagMessage(ctx, "a1", "k1", work.ID); err != nil {
		t.Errorf("untagging twice = %v, want a no-op", err)
	}
	if n := countRows(t, dbs, `SELECT count(*) FROM message_tags WHERE tag_id = 'work'`); n != 2 {
		t.Errorf("%d work memberships after an untag, want 2", n)
	}

	if err := dbs.DeleteTag(ctx, "work"); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	if n := countRows(t, dbs, `SELECT count(*) FROM message_tags WHERE tag_id = 'work'`); n != 0 {
		t.Errorf("%d memberships left after deleting the tag", n)
	}
	if n := countRows(t, dbs, `SELECT count(*) FROM message_tags WHERE tag_id = 'home'`); n != 1 {
		t.Errorf("deleting one tag touched another's memberships: %d", n)
	}
	if err := dbs.DeleteTag(ctx, "work"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteTag = %v, want ErrNotFound", err)
	}
}

func TestTagBySlugIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if _, err := dbs.CreateTag(ctx, "t1", "Work", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := dbs.TagBySlug(ctx, "work"); err != nil || got.ID != "t1" {
		t.Errorf("TagBySlug = %+v, %v", got, err)
	}
	if _, err := dbs.TagBySlug(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("TagBySlug(missing) = %v, want ErrNotFound", err)
	}
}
