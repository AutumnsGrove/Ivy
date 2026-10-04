package sync_test

import (
	"os"
	"slices"
	"testing"
)

// churnMix leans on the operations that change which folders exist: the default
// mix runs about three renames in six hundred operations, which is how a folder
// renamed away and back (papercuts #58) passed the first gate. Messages are
// still appended, flagged and moved so there is always something to lose.
var churnMix = []weight{
	{opAppend, 20},
	{opSync, 18},
	{opCreateFolder, 12},
	{opRenameFolder, 12},
	{opDeleteFolder, 8},
	{opBumpValidity, 6},
	{opMove, 10},
	{opFlag, 8},
	{opCopy, 6},
}

// churnRegressionSeeds always run, whatever window the run asks for: seed 153
// shrinks to a folder renamed away and back (papercuts #58), which the default
// 24 seeds do not reach and a 200-seed sweep takes over a minute to find.
var churnRegressionSeeds = []uint64{153}

// TestSyncConvergesUnderFolderChurn is the convergence property over scripts
// that mostly create, rename, delete and rebuild folders. It shares the oracle,
// the seeds and the variants with TestSyncConvergesToTheServer; only the
// weights differ, so a failure reproduces with the same IVY_SYNC_SEED.
func TestSyncConvergesUnderFolderChurn(t *testing.T) {
	t.Parallel()
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultConfig(v.condStore)
			seeds := seedsToRun(t)
			if os.Getenv("IVY_SYNC_SEED") == "" {
				seeds = append(slices.Clone(churnRegressionSeeds), seeds...)
			}
			ran, total := map[opKind]int{}, 0
			for _, seed := range seeds {
				script := generate(seed, churnMix)
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
			if ran[opRenameFolder] < len(seeds) {
				t.Errorf("only %d renames ran in %d sequences: the mix no longer churns folders", ran[opRenameFolder], len(seeds))
			}
		})
	}
}
