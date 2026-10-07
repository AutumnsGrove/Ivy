package store

import "slices"

// AccountIcons is the closed list of Lucide icon names an account badge may
// use. The icon is drawn on every screen and lives in state.db, so the server
// refuses anything else. The frontend keeps the same list in
// web/src/lib/accountIcons.ts, and a test there fails if the two drift.
var AccountIcons = []string{
	"leaf", "moon", "sun", "flower", "flower-2", "sprout",
	"tree-deciduous", "bird", "mail", "droplets", "cloud", "star",
}

// ValidAccountIcon reports whether icon may be stored. Empty means none.
func ValidAccountIcon(icon string) bool {
	return icon == "" || slices.Contains(AccountIcons, icon)
}
