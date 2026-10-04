package sync_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AutumnsGrove/Ivy/internal/mailworld"
	"github.com/AutumnsGrove/Ivy/store"
	ivysync "github.com/AutumnsGrove/Ivy/sync"
)

var variants = []struct {
	name      string
	condStore bool
}{
	{"condstore", true},
	{"no-condstore", false}, // the UID/flags fallback of a server without QRESYNC
}

// seedsToRun is the seeds of one run. IVY_SYNC_SEED pins one seed to reproduce a
// failure; IVY_SYNC_SEEDS sets how many; IVY_SYNC_SEED_BASE moves the window, so
// a nightly job can cover fresh seeds. -short runs fewer.
func seedsToRun(t *testing.T) []uint64 {
	t.Helper()
	if s := os.Getenv("IVY_SYNC_SEED"); s != "" {
		seed, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			t.Fatalf("IVY_SYNC_SEED=%q: %v", s, err)
		}
		return []uint64{seed}
	}
	count, base := 24, uint64(1)
	if testing.Short() {
		count = 6
	}
	if s := os.Getenv("IVY_SYNC_SEEDS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			t.Fatalf("IVY_SYNC_SEEDS=%q is not a positive number", s)
		}
		count = n
	}
	if s := os.Getenv("IVY_SYNC_SEED_BASE"); s != "" {
		b, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			t.Fatalf("IVY_SYNC_SEED_BASE=%q: %v", s, err)
		}
		base = b
	}
	seeds := make([]uint64, count)
	for i := range seeds {
		seeds[i] = base + uint64(i)
	}
	return seeds
}

const shrinkBudget = 150

// TestSyncConvergesToTheServer is the one invariant of the mirror: after sync
// quiesces, the mirror equals the server, whatever the other mail client did in
// between (TESTING.md 2). Random scripts of move, flag, unflag, expunge, copy,
// append, folder create/rename/delete and UIDVALIDITY bumps run against the fake
// server with sync points between them; every sync point is checked, on a server
// with QRESYNC and on one without. A failure is shrunk to a minimal script.
func TestSyncConvergesToTheServer(t *testing.T) {
	t.Parallel()
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultConfig(v.condStore)
			seeds := seedsToRun(t)
			ran, total := map[opKind]int{}, 0
			for _, seed := range seeds {
				script := generate(seed, defaultMix)
				total += len(script)
				out := cfg.replay(script)
				for k, n := range out.ran {
					ran[k] += n
				}
				if out.fail != nil {
					t.Fatal(reportFailure(cfg, v.name, seed, script, out))
				}
			}
			t.Logf("%d sequences, %d operations, all converged. Executed: %s", len(seeds), total, mixSummary(ran))
		})
	}
}

func mixSummary(ran map[opKind]int) string {
	var parts []string
	for k := range numOpKinds {
		parts = append(parts, fmt.Sprintf("%s %d", k, ran[k]))
	}
	return strings.Join(parts, ", ")
}

// reportFailure shrinks a failing script and renders what a reviewer needs: the
// seed, the minimal script as concrete steps, and where it fails.
func reportFailure(cfg config, variant string, seed uint64, script []op, first outcome) string {
	kind := first.fail.kind
	minimal, replays, exhausted := shrink(script, shrinkBudget, func(s []op) bool {
		o := cfg.replay(s)
		return o.fail != nil && o.fail.kind == kind
	})
	final := cfg.replay(minimal)
	if final.fail == nil { // cannot happen: the shrinker only keeps scripts that fail
		final = first
	}
	note := ""
	if exhausted {
		note = fmt.Sprintf(" (budget of %d replays spent, so not necessarily minimal)", shrinkBudget)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mirror did not converge to the server: variant=%s seed=%d, %d operations shrunk to %d in %d replays%s\n",
		variant, seed, len(script), len(minimal), replays, note)
	fmt.Fprintf(&b, "  kind:   %s\n  detail: %s\n  minimal script (the server's own steps, then a sync check):\n", final.fail.kind, final.fail.detail)
	for _, line := range final.trace {
		fmt.Fprintf(&b, "    %s\n", line)
	}
	fmt.Fprintf(&b, "  reproduce: IVY_SYNC_SEED=%d go test -run 'TestSyncConvergesToTheServer/%s' ./sync", seed, variant)
	return b.String()
}

// ----------------------------------------------------- tests of the harness

// If the model of the server drifts from the real fake, every oracle verdict
// after it is meaningless. This runs real scripts with no sync at all and checks
// the model against the server's own answer after every operation.
func TestHarnessModelMatchesTheServer(t *testing.T) {
	t.Parallel()
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			cfg := config{condStore: v.condStore, checkModelEachOp: true}
			ran := map[opKind]int{}
			for seed := uint64(1); seed <= 12; seed++ {
				out := cfg.replay(generate(seed, defaultMix))
				if out.fail != nil {
					t.Fatalf("seed %d: %s: %s\n%s", seed, out.fail.kind, out.fail.detail, strings.Join(out.trace, "\n"))
				}
				for k, n := range out.ran {
					ran[k] += n
				}
			}
			for k := range numOpKinds {
				if k != opSync && ran[k] == 0 {
					t.Errorf("no seed ever executed %s: the generator and the fake are not both being exercised", k)
				}
			}
		})
	}
}

func TestGeneratorIsDeterministicAndCoversEveryOperation(t *testing.T) {
	t.Parallel()
	if a, b := generate(42, defaultMix), generate(42, defaultMix); !slices.Equal(a, b) {
		t.Error("the same seed gave two different scripts, so a printed seed cannot reproduce a failure")
	}
	if slices.Equal(generate(1, defaultMix), generate(2, defaultMix)) {
		t.Error("different seeds gave the same script")
	}
	seen := map[opKind]bool{}
	for seed := uint64(1); seed <= 100; seed++ {
		script := generate(seed, defaultMix)
		if n := len(script); n < 12 || n > 40 {
			t.Fatalf("seed %d: %d operations, want 12 to 40", seed, n)
		}
		for _, o := range script {
			seen[o.kind] = true
		}
	}
	for k := range numOpKinds {
		if !seen[k] {
			t.Errorf("the default mix never generates %s", k)
		}
	}
	for seed := uint64(1); seed <= 50; seed++ {
		for _, o := range generate(seed, appendOnlyMix) {
			if o.kind != opAppend && o.kind != opSync && o.kind != opCreateFolder {
				t.Fatalf("append-only mix produced %s", o.kind)
			}
		}
	}
}

func TestShrinkFindsTheMinimalPair(t *testing.T) {
	t.Parallel()
	flag, move := op{kind: opFlag, a: 7, b: 3}, op{kind: opMove, a: 9, b: 4}
	script := make([]op, 0, 30)
	for i := range 30 {
		script = append(script, op{kind: opAppend, n: i, a: i, b: i})
	}
	script[4], script[21] = flag, move
	fails := func(s []op) bool { // the bug needs a flag and, later, a move
		fi := slices.Index(s, flag)
		return fi >= 0 && slices.Index(s[fi+1:], move) >= 0
	}
	orig := slices.Clone(script)

	got, replays, exhausted := shrink(script, 1000, fails)
	if !slices.Equal(got, []op{flag, move}) {
		t.Errorf("shrunk to %v, want just the flag and the move", got)
	}
	if exhausted {
		t.Error("reported an exhausted budget on a tiny problem")
	}
	if !slices.Equal(script, orig) {
		t.Error("shrink changed the script it was given")
	}
	if replays > 200 {
		t.Errorf("took %d replays for 30 ops; delta debugging should need far fewer", replays)
	}

	partial, _, exhausted := shrink(script, 3, fails)
	if !exhausted || !fails(partial) {
		t.Errorf("a spent budget must say so and still return a failing script: exhausted=%v fails=%v", exhausted, fails(partial))
	}
}

// The oracle must accept a correct outcome as readily as it rejects a wrong one.
// The one-shot fetch handles a history of appends correctly, so any failure here
// is the oracle being wrong.
func TestHarnessAcceptsAHistoryTheOneShotFetchGetsRight(t *testing.T) {
	t.Parallel()
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultConfig(v.condStore)
			for seed := uint64(1); seed <= 10; seed++ {
				out := cfg.replay(generate(seed, appendOnlyMix))
				if out.fail != nil {
					t.Fatalf("seed %d: the oracle rejected a correct append-only history: %s: %s\n%s",
						seed, out.fail.kind, out.fail.detail, strings.Join(out.trace, "\n"))
				}
			}
		})
	}
}

// staleFlagSync is a deliberately broken system under test, kept so the harness
// self-test still has teeth now that the real runner refreshes flags: it mirrors
// new messages but restores the flags of every message it already held, exactly
// as the chunk 2b one-shot fetch did. The C2 demonstration uses it.
func staleFlagSync(ctx context.Context, dbs *store.DBs, f *ivysync.Fetcher, acct ivysync.Account) error {
	type flagState struct {
		flags string
		seen  bool
	}
	before := map[string]flagState{}
	rows, err := dbs.Mirror.Read.QueryContext(ctx, `SELECT id, COALESCE(flags_json, ''), seen FROM messages`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var st flagState
		if err := rows.Scan(&id, &st.flags, &st.seen); err != nil {
			_ = rows.Close()
			return err
		}
		before[id] = st
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := oneShotFetch(ctx, dbs, f, acct); err != nil {
		return err
	}
	for id, st := range before {
		if _, err := dbs.Mirror.Write.ExecContext(ctx,
			`UPDATE messages SET flags_json = ?, seen = ? WHERE id = ?`, st.flags, st.seen, id); err != nil {
			return err
		}
	}
	return nil
}

// The runner refreshes flags, so a sync that leaves them stale must still be
// reported, and the shrinker must cut a padded script down to the cause.
func TestHarnessCatchesStaleFlagsAndShrinksToTheCause(t *testing.T) {
	t.Parallel()
	cfg := defaultConfig(true)
	cfg.sut = staleFlagSync
	script := []op{
		{kind: opAppend, n: 0},
		{kind: opAppend, n: 1, a: 3},
		{kind: opSync},
		{kind: opCreateFolder, b: 2},
		{kind: opAppend, n: 2, a: 5},
		{kind: opSync},
		{kind: opFlag, a: 1, b: 4},
		{kind: opAppend, n: 3, a: 2},
		{kind: opSync},
	}
	first := cfg.replay(script)
	if first.fail == nil || first.fail.kind != "mirror differs from server" {
		t.Fatalf("outcome = %+v, want a \"mirror differs from server\" failure", first.fail)
	}
	if !strings.Contains(first.fail.detail, `\flagged`) && !strings.Contains(first.fail.detail, `\seen`) &&
		!strings.Contains(first.fail.detail, `$ivy`) && !strings.Contains(first.fail.detail, `\answered`) &&
		!strings.Contains(first.fail.detail, `\draft`) && !strings.Contains(first.fail.detail, `\deleted`) {
		t.Errorf("the report does not show the differing flags: %s", first.fail.detail)
	}

	minimal, _, exhausted := shrink(script, shrinkBudget, func(s []op) bool {
		o := cfg.replay(s)
		return o.fail != nil && o.fail.kind == first.fail.kind
	})
	if exhausted {
		t.Error("the shrink budget was spent on a 9-op script")
	}
	kinds := make([]opKind, len(minimal))
	for i, o := range minimal {
		kinds[i] = o.kind
	}
	// Append, sync (so the message is mirrored without the flag), flag; the end
	// of every script is itself a sync check.
	if want := []opKind{opAppend, opSync, opFlag}; !slices.Equal(kinds, want) {
		t.Errorf("minimal script = %v, want %v", kinds, want)
	}
}

// The system-under-test seam really is the thing being judged: a sync that
// loses the raw bytes, or one that is not idempotent, must be caught on the real
// path, not just in the oracle's unit tests below.
func TestHarnessCatchesASyncThatErasesRawBytes(t *testing.T) {
	t.Parallel()
	cfg := defaultConfig(true)
	cfg.sut = func(ctx context.Context, dbs *store.DBs, f *ivysync.Fetcher, acct ivysync.Account) error {
		if err := oneShotFetch(ctx, dbs, f, acct); err != nil {
			return err
		}
		_, err := dbs.Mirror.Write.ExecContext(ctx, `UPDATE messages SET raw_blob = NULL, raw_path = NULL`)
		return err
	}
	out := cfg.replay([]op{{kind: opAppend, n: 0}, {kind: opAppend, n: 1}})
	if out.fail == nil || out.fail.kind != "raw message lost" {
		t.Errorf("outcome = %+v, want \"raw message lost\"", out.fail)
	}
}

func TestHarnessCatchesASyncThatIsNotIdempotent(t *testing.T) {
	t.Parallel()
	cfg := defaultConfig(true)
	var calls atomic.Int64
	cfg.sut = func(ctx context.Context, dbs *store.DBs, f *ivysync.Fetcher, acct ivysync.Account) error {
		if err := oneShotFetch(ctx, dbs, f, acct); err != nil {
			return err
		}
		_, err := dbs.Mirror.Write.ExecContext(ctx, `UPDATE messages SET thread_id = ?`, fmt.Sprintf("call-%d", calls.Add(1)))
		return err
	}
	out := cfg.replay([]op{{kind: opAppend, n: 0}})
	if out.fail == nil || out.fail.kind != "second sync changed the mirror" {
		t.Errorf("outcome = %+v, want \"second sync changed the mirror\"", out.fail)
	}
}

func keyOf(id string) string { return store.ContentKey(id, nil) }

func serverMsgs(n int) []mailworld.ServerMessage {
	return []mailworld.ServerMessage{{UID: 1, MessageID: messageID(n)}}
}

func TestOracleChecksHaveTeeth(t *testing.T) {
	t.Parallel()
	good := func() row {
		return row{id: "r1", folder: "INBOX", uid: 1, msgID: messageID(1), key: keyOf(messageID(1)), thread: "t1", flags: []string{`\Seen`}, seen: true}
	}
	h := func() *run {
		return &run{seenRows: map[string]bool{}, everMirrored: map[string]bool{}, prev: map[string]rowState{}, raw: map[int][]byte{1: []byte("original")}}
	}

	t.Run("a wrong content key", func(t *testing.T) {
		t.Parallel()
		r := good()
		r.key = "deadbeef"
		if f := h().checkRowInvariants([]row{r}); f == nil || f.kind != "content key wrong" {
			t.Errorf("got %+v", f)
		}
	})
	t.Run("a seen column that disagrees with the flags", func(t *testing.T) {
		t.Parallel()
		r := good()
		r.seen = false
		if f := h().checkRowInvariants([]row{r}); f == nil || f.kind != "seen column disagrees with flags" {
			t.Errorf("got %+v", f)
		}
	})
	t.Run("a visible message with no thread", func(t *testing.T) {
		t.Parallel()
		r := good()
		r.thread = ""
		if f := h().checkRowInvariants([]row{r}); f == nil || f.kind != "visible message without a thread" {
			t.Errorf("got %+v", f)
		}
	})
	t.Run("a disabled message needs no thread", func(t *testing.T) {
		t.Parallel()
		r := good()
		r.thread, r.disabled, r.reason = "", true, "removed"
		if f := h().checkRowInvariants([]row{r}); f != nil {
			t.Errorf("a disabled row is out of every thread, got %+v", f)
		}
	})
	t.Run("a deleted row", func(t *testing.T) {
		t.Parallel()
		x := h()
		x.seenRows["r1"] = true
		if f := x.checkNothingErased(nil); f == nil || f.kind != "message row deleted" {
			t.Errorf("got %+v", f)
		}
	})
	t.Run("raw bytes that no row holds", func(t *testing.T) {
		t.Parallel()
		x := h()
		r := good()
		r.blob = []byte("something else")
		if f := x.checkNothingErased([]row{r}); f == nil || f.kind != "raw message lost" {
			t.Errorf("got %+v", f)
		}
	})
	t.Run("raw bytes kept by a disabled row", func(t *testing.T) {
		t.Parallel()
		x := h()
		r := good()
		r.blob, r.disabled, r.reason = []byte("original"), true, "removed"
		if f := x.checkNothingErased([]row{r}); f != nil {
			t.Errorf("a disabled row holding the bytes keeps the message, got %+v", f)
		}
	})
	t.Run("a move that is not labelled moved", func(t *testing.T) {
		t.Parallel()
		r := good()
		r.disabled, r.reason = true, "removed"
		snap := []serverFolder{{name: "Archive", msgs: serverMsgs(1)}}
		if f := h().checkDisabledReasons(snap, []row{r}); f == nil || f.kind != "wrong disabled reason" {
			t.Errorf("got %+v", f)
		}
	})
	t.Run("a removal that is labelled moved", func(t *testing.T) {
		t.Parallel()
		r := good()
		r.disabled, r.reason = true, "moved"
		if f := h().checkDisabledReasons(nil, []row{r}); f == nil || f.kind != "wrong disabled reason" {
			t.Errorf("got %+v", f)
		}
	})
	t.Run("a removal with any other reason than server_removed", func(t *testing.T) {
		t.Parallel()
		r := good()
		r.disabled, r.reason = true, "removed"
		if f := h().checkDisabledReasons(nil, []row{r}); f == nil || f.kind != "wrong disabled reason" {
			t.Errorf("got %+v", f)
		}
		r.reason = "server_removed"
		if f := h().checkDisabledReasons(nil, []row{r}); f != nil {
			t.Errorf("server_removed is the removal reason, got %+v", f)
		}
	})
	t.Run("an old disabled row is not judged again", func(t *testing.T) {
		t.Parallel()
		x := h()
		r := good()
		r.disabled, r.reason = true, "moved"
		x.prev[r.id] = rowState{disabled: true, reason: "moved"}
		if f := x.checkDisabledReasons(nil, []row{r}); f != nil {
			t.Errorf("only rows disabled in this pass are judged, got %+v", f)
		}
	})
}
