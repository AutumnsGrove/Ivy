package store

import (
	"context"
	"testing"
)

func countRows(t *testing.T, dbs *DBs, query string, args ...any) int {
	t.Helper()
	var n int
	if err := dbs.State.Read.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestUpsertTagUpdatesInPlace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if err := dbs.UpsertTag(ctx, Tag{ID: "t1", Slug: "receipts", Name: "receipts", Color: "sky"}); err != nil {
		t.Fatalf("UpsertTag: %v", err)
	}
	if err := dbs.UpsertTag(ctx, Tag{ID: "t1", Slug: "receipts", Name: "Receipts", Color: "teal"}); err != nil {
		t.Fatalf("second UpsertTag: %v", err)
	}
	if n := countRows(t, dbs, `SELECT count(*) FROM tags`); n != 1 {
		t.Fatalf("%d tags, want 1", n)
	}
	var name, color string
	if err := dbs.State.Read.QueryRow(`SELECT name, color FROM tags WHERE id='t1'`).Scan(&name, &color); err != nil {
		t.Fatal(err)
	}
	if name != "Receipts" || color != "teal" {
		t.Errorf("tag = %q/%q, want Receipts/teal", name, color)
	}
}

func TestTagMessageIsIdempotentAndNeedsARealTag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)
	if err := dbs.UpsertTag(ctx, Tag{ID: "t1", Slug: "receipts", Name: "receipts"}); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := dbs.TagMessage(ctx, "acct-1", "key-1", "t1", "manual"); err != nil {
			t.Fatalf("TagMessage: %v", err)
		}
	}
	if n := countRows(t, dbs, `SELECT count(*) FROM message_tags`); n != 1 {
		t.Errorf("%d memberships after tagging twice, want 1", n)
	}
	if err := dbs.TagMessage(ctx, "acct-1", "key-1", "no-such-tag", "manual"); err == nil {
		t.Error("TagMessage accepted a tag that does not exist")
	}
}

func TestSettingsRoundTripPerAccountAndGlobal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbs := openTemp(t)

	if _, ok, err := dbs.GetSetting(ctx, "", "dev.seeded"); err != nil || ok {
		t.Fatalf("unset setting: ok=%v err=%v", ok, err)
	}
	if err := dbs.SetSetting(ctx, "", "dev.seeded", "1"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := dbs.SetSetting(ctx, "", "dev.seeded", "2"); err != nil {
		t.Fatalf("second SetSetting: %v", err)
	}
	if err := dbs.SetSetting(ctx, "acct-1", "dev.seeded", "mine"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := dbs.GetSetting(ctx, "", "dev.seeded"); err != nil || !ok || v != "2" {
		t.Errorf("global = %q ok=%v err=%v, want 2", v, ok, err)
	}
	if v, ok, err := dbs.GetSetting(ctx, "acct-1", "dev.seeded"); err != nil || !ok || v != "mine" {
		t.Errorf("account = %q ok=%v err=%v, want mine", v, ok, err)
	}
}
