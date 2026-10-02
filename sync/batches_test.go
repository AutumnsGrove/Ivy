package sync

import (
	"reflect"
	"testing"
)

func TestUIDBatchesDescendAndTile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		highest uint32
		size    int
		want    []uidRange
	}{
		{"empty", 0, 200, nil},
		{"single", 1, 200, []uidRange{{1, 1}}},
		{"exact", 4, 2, []uidRange{{3, 4}, {1, 2}}},
		{"remainder", 5, 2, []uidRange{{4, 5}, {2, 3}, {1, 1}}},
		{"size clamps", 3, 0, []uidRange{{3, 3}, {2, 2}, {1, 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := uidBatches(tc.highest, tc.size); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("uidBatches(%d, %d) = %+v, want %+v", tc.highest, tc.size, got, tc.want)
			}
		})
	}
}

// Every UID in 1..highest must appear exactly once, newest batch first.
func TestUIDBatchesCoverRange(t *testing.T) {
	t.Parallel()
	const highest uint32 = 1000
	const size = 7
	seen := map[uint32]int{}
	prev := highest + 1
	for _, b := range uidBatches(highest, size) {
		if b.hi >= prev || b.hi < b.lo {
			t.Fatalf("batch %+v is not descending after %d", b, prev)
		}
		for u := b.lo; u <= b.hi; u++ {
			seen[u]++
		}
		prev = b.lo
	}
	for u := uint32(1); u <= highest; u++ {
		if seen[u] != 1 {
			t.Fatalf("UID %d seen %d times, want once", u, seen[u])
		}
	}
}
