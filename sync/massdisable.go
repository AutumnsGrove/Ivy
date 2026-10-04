package sync

// A mass-disable sweep is a folder losing so much mail in one completed pass
// that it looks like a mistake rather than an edit: a provider wiping a mailbox,
// a UIDVALIDITY reset, or a bug. Sync does not stop, because disabling is
// reversible, but it raises a Mirror health alert with a one-click restore
// (ARCHITECTURE.md 4). The numbers are tunable constants, not a design call
// (qa-log round 37).
const (
	// MassDisableCount is the absolute number of messages hidden in one pass.
	MassDisableCount = 50
	// MassDisableFraction is how much of a folder may be hidden before it is news.
	MassDisableFraction = 0.20
	// MassDisableFloor is the smallest folder the fraction rule applies to, so a
	// three-message folder losing one is never an alarm.
	MassDisableFloor = 10
)

// MassDisable names a folder whose last completed pass hid enough mail to alert
// on. Hidden and Held let the UI word the count ("12 of 14 hidden").
type MassDisable struct {
	Folder string
	Hidden int
	Held   int
}

// folderChurn is what one folder's reconcile did in a pass. Kept separate from
// the Result so the threshold stays a pure function over a small value.
type folderChurn struct {
	name   string
	held   int
	hidden int
}

// massDisables returns the folders whose sweep tripped either rule. It reads
// only counts, so it is trivial to reason about and to test at the boundaries.
func massDisables(rows []folderChurn) []MassDisable {
	var out []MassDisable
	for _, r := range rows {
		if r.hidden == 0 {
			continue
		}
		if r.hidden > MassDisableCount ||
			(r.held >= MassDisableFloor && float64(r.hidden) > MassDisableFraction*float64(r.held)) {
			out = append(out, MassDisable{Folder: r.name, Hidden: r.hidden, Held: r.held})
		}
	}
	return out
}
