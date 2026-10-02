package sync

// uidRange is an inclusive UID interval.
type uidRange struct{ lo, hi uint32 }

// uidBatches splits 1..highest into descending batches of at most size, so a
// backfill makes the newest mail usable first (ARCHITECTURE.md 4.3). The
// batches tile the range: every UID from 1 to highest appears exactly once.
func uidBatches(highest uint32, size int) []uidRange {
	if size < 1 {
		size = 1
	}
	var out []uidRange
	for hi := highest; hi >= 1; {
		lo := uint32(1)
		if hi > uint32(size) {
			lo = hi - uint32(size) + 1
		}
		out = append(out, uidRange{lo: lo, hi: hi})
		if lo == 1 {
			break
		}
		hi = lo - 1
	}
	return out
}
