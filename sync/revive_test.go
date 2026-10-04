package sync_test

import (
	"strings"
	"testing"
)

// A folder renamed away and then back keeps its UIDVALIDITY and UIDs, so a
// QRESYNC server answers the stored modseq with "nothing changed". The messages
// the runner disabled while the name was gone must still reappear.
func TestRenamedAwayAndBackKeepsItsMessages(t *testing.T) {
	t.Parallel()
	script := []op{
		{kind: opCreateFolder},
		{kind: opAppend, n: 0, a: 0}, // into the new folder
		{kind: opAppend, n: 1, a: 0},
		{kind: opSync},
		{kind: opRenameFolder, a: 0}, // away
		{kind: opSync},
		{kind: opRenameFolder, a: 0}, // and back to the first free pool name
		{kind: opSync},
	}
	for _, condstore := range []bool{true, false} {
		cfg := defaultConfig(condstore)
		out := cfg.replay(script)
		if out.fail != nil {
			t.Errorf("condstore=%v: %s: %s\n%s", condstore, out.fail.kind, out.fail.detail, strings.Join(out.trace, "\n"))
		}
	}
}
