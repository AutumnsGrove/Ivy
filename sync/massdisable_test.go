package sync

import "testing"

// The threshold is a judgement call, not a measurement, so it is a small table
// with the exact boundaries pinned: strictly more than the count, strictly more
// than the fraction, and never for a folder that held fewer than the floor.
func TestMassDisablesTripsOnCountOrFraction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		held   int
		hidden int
		want   bool
	}{
		{"a small folder hiding everything is below the floor", 9, 9, false},
		{"exactly the floor at 20% is not more", 10, 2, false},
		{"just over the fraction in a floor-sized folder", 10, 3, true},
		{"a big folder just over the absolute count", 300, 51, true},
		{"a big folder at the absolute count and under the fraction", 300, 50, false},
		{"the count rule fires even when the fraction does not", 200, 51, true},
		{"nothing hidden never alerts", 500, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := massDisables([]folderChurn{{name: "INBOX", held: tc.held, hidden: tc.hidden}})
			if (len(got) == 1) != tc.want {
				t.Errorf("held=%d hidden=%d: got %+v, want alert=%v", tc.held, tc.hidden, got, tc.want)
			}
		})
	}
}
