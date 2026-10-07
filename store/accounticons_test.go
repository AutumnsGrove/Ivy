package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Issue #14: the picker used to save an emoji. The accounts already saved that
// way (the real board has one) are renamed to the Lucide names deliberately, so
// the strict list never has to accept a glyph.
func TestLegacyEmojiAccountIconsBecomeNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	before := len(stateMigrations) - 1 // everything up to, but not including, the icon migration
	old := rawPool(t, filepath.Join(dir, "state.db"))
	if err := migrate(context.Background(), old, stateMigrations[:before]); err != nil {
		t.Fatalf("apply old schema: %v", err)
	}
	legacy := map[string]string{
		"a1": "🌿", "a2": "🌙", "a3": "🌻", "a4": "📬", "a5": "🌊",
		"a6": "🍂", "a7": "🪴", "a8": "☀️", "a9": "🌸", "a10": "🐦",
		"a11": "", "a12": "leaf", "a13": "🦄",
	}
	for id, icon := range legacy {
		if _, err := old.ExecContext(context.Background(),
			`INSERT INTO account_profiles (account_id, icon) VALUES (?, ?)`, id, icon); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	dbs, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open upgrade: %v", err)
	}
	defer dbs.Close()

	want := map[string]string{
		"a1": "leaf", "a2": "moon", "a3": "flower-2", "a4": "mail", "a5": "droplets",
		"a6": "tree-deciduous", "a7": "sprout", "a8": "sun", "a9": "flower", "a10": "bird",
		"a11": "", "a12": "leaf",
		"a13": "", // a glyph the picker never offered has no name, so it falls back to the initial
	}
	for id, icon := range want {
		var got string
		if err := dbs.State.Read.QueryRowContext(context.Background(),
			`SELECT icon FROM account_profiles WHERE account_id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if got != icon {
			t.Errorf("%s icon = %q, want %q", id, got, icon)
		}
		if !ValidAccountIcon(got) {
			t.Errorf("%s icon %q is not on the closed list", id, got)
		}
	}
}
