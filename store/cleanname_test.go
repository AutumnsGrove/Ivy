package store_test

import (
	"testing"

	"github.com/AutumnsGrove/Ivy/store"
)

// Issue #12: some servers hand back the display name with the header's quotes
// still on it ("claude[bot]" because `[` is not legal unquoted). A name that
// really contains quotes keeps them.
func TestCleanNameDropsWrappingQuotes(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{`"claude[bot]"`, "claude[bot]"},
		{`claude[bot]`, "claude[bot]"},
		{`  "Autumn"  `, "Autumn"},
		{`"Autumn \"Storm\" Brown"`, `Autumn "Storm" Brown`},
		{`"back\\slash"`, `back\slash`},
		{`"Brown, Autumn"`, "Brown, Autumn"},
		{`""`, ""},
		{`"`, `"`},
		{`"unterminated`, `"unterminated`},
		{`He said "hi"`, `He said "hi"`},
		{`"a" "b"`, `"a" "b"`},
		{"", ""},
	}
	for _, tc := range cases {
		if got := store.CleanName(tc.in); got != tc.want {
			t.Errorf("CleanName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
