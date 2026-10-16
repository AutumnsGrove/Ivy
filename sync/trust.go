package sync

import stdsync "sync"

// trustBook remembers which Authentication-Results servers each account trusts, as
// the last Fetch saw them. The re-derive pass runs without the account's connection
// details, and parsing with no trusted ids reads every verdict as none, so it needs
// this to rewrite verdicts and scores faithfully. An account it has not seen is
// unknown, and re-deriving leaves its stored verdicts alone.
type trustBook struct {
	mu  stdsync.Mutex
	ids map[string][]string
}

func (t *trustBook) set(account string, ids []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ids == nil {
		t.ids = map[string][]string{}
	}
	t.ids[account] = append([]string(nil), ids...)
}

func (t *trustBook) get(account string) ([]string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids, ok := t.ids[account]
	return ids, ok
}
