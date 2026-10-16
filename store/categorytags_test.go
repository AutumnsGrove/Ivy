package store

import (
	"context"
	"errors"
	"testing"
)

func TestACategoryPlacesItsOwnLocalTagOnceAndOnlyForTaggedCategories(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if err := dbs.PlaceCategoryTag(ctx, "a", "k1", "newsletter"); err != nil {
		t.Fatal(err)
	}
	if err := dbs.PlaceCategoryTag(ctx, "a", "k1", "newsletter"); err != nil { // placing twice is a no-op
		t.Fatalf("placing twice: %v", err)
	}
	got, err := dbs.TagsForMessages(ctx, "a", []string{"k1"})
	if err != nil {
		t.Fatal(err)
	}
	tags := got["k1"]
	if len(tags) != 1 {
		t.Fatalf("tags = %+v, want exactly one", tags)
	}
	tag := tags[0]
	if tag.Name != "Newsletter" || tag.ID != "t-cat-newsletter" || tag.Slug != "cat-newsletter" || tag.Color != "sky" {
		t.Errorf("tag = %+v", tag)
	}
	if !tag.LocalOnly() {
		t.Error("a placed category tag could be written to the server as a keyword")
	}
	if !IsReservedTag(tag.ID) {
		t.Error("a placed category tag can be deleted, so the next message would quietly recreate it")
	}

	// Personal mail is protected from automated handling, and "other" says nothing.
	for _, c := range []string{"personal", "other", "mystery", ""} {
		if err := dbs.PlaceCategoryTag(ctx, "a", "k2", c); !errors.Is(err, ErrNotFound) {
			t.Errorf("category %q: err = %v, want ErrNotFound (no tag is placed for it)", c, err)
		}
	}
	if left, _ := dbs.TagsForMessages(ctx, "a", []string{"k2"}); len(left["k2"]) != 0 {
		t.Errorf("a category with no tag still tagged the message: %+v", left["k2"])
	}
}

func TestEveryPlacedCategoryHasAReadableNameAndAColourTheUIKnows(t *testing.T) {
	t.Parallel()
	known := map[string]bool{"sky": true, "rose": true, "teal": true, "coral": true, "lilac": true, "mint": true,
		"gold": true, "sand": true, "orchid": true, "fern": true, "slate": true, "berry": true}
	names := map[string]bool{}
	for _, c := range PlacedCategories() {
		tag, ok := categoryTag(c)
		if !ok || tag.Name == "" || !known[tag.Color] {
			t.Errorf("category %q: %+v ok=%v", c, tag, ok)
		}
		if names[tag.Name] {
			t.Errorf("two categories share the name %q", tag.Name)
		}
		names[tag.Name] = true
	}
	if len(names) != 7 {
		t.Errorf("%d placed categories, want the seven that are not personal or other", len(names))
	}
}
