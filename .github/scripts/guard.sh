#!/usr/bin/env bash
# Repo hygiene checks the `guard` CI job runs (CI.md section 2), kept as a
# script so `make guard` runs exactly the same commands locally. It only reads
# tracked paths and test sources: no secrets, no untrusted input.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
fail=0

echo "guard: generated assets stay out of the tree"
tracked=$(git ls-files internal/webui/build | grep -v '^internal/webui/build/.gitkeep$' || true)
if [ -n "$tracked" ]; then
	echo "  only .gitkeep may be committed under internal/webui/build/:"
	echo "$tracked" | sed 's/^/    /'
	fail=1
fi

echo "guard: no test can reach the live LLM provider"
if ! go test ./internal/mailworld -run TestNoTestReachesLiveProvider -count=1; then
	echo "  TestNoTestReachesLiveProvider failed; only the mailworld fake may appear in tests"
	fail=1
fi

# Licence headers are planned (CI.md section 6) but the tree does not carry
# them yet. Flip IVY_LICENCE_HEADERS=1 (and add the SPDX line to every source
# file) to make this blocking; it is off so the rest of the guard is usable now.
if [ "${IVY_LICENCE_HEADERS:-0}" = "1" ]; then
	echo "guard: AGPL-3.0 SPDX headers on source files"
	missing=$(git grep -L 'SPDX-License-Identifier: AGPL-3.0' -- \
		'*.go' '*.ts' '*.svelte' ':(exclude)api/api.gen.go' ':(exclude)web/src/lib/api/schema.d.ts' || true)
	if [ -n "$missing" ]; then
		echo "  files without an AGPL-3.0 SPDX header:"
		echo "$missing" | sed 's/^/    /'
		fail=1
	fi
else
	echo "guard: AGPL headers check skipped (IVY_LICENCE_HEADERS != 1; see CI.md section 6)"
fi

exit "$fail"
