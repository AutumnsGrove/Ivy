---
name: review-deepseek
description: Audit a range of commits written by another model (DeepSeek via the harness) commit by commit, fixing every defect test-first, logging each in papercuts.md, and committing each fix on a review branch. Use when the operator says to review, audit or second-opinion the other model's work.
---

# Review the other model's work

The operator develops the backend fast and cheaply with DeepSeek and uses this workflow to get a
careful second pass. The reviewer finds real defects, proves them with a failing test, fixes them,
and leaves a paper trail. It does not rewrite working code for taste.

## Inputs to get before starting

- **The first commit to review** (a GitHub commit URL or SHA, *inclusive*). If it is missing, ask;
  do not guess a boundary from `git log`.
- **The stopping point.** Default: the current tip of `origin/main`, frozen at the start. Later
  commits are the next review's job.

## Setup (do this once, in order)

1. **Make the clone complete.** `git rev-parse --is-shallow-repository`; if `true`, run
   `git fetch --unshallow origin`. A shallow clone shows its oldest commit as "adds everything",
   which hides what that commit really changed.
2. **Branch off the tip.** `git checkout -B review/<topic> origin/main`. Never commit the review
   to `main` unless the operator says so. Keep any stale local `main` on a `backup/` branch rather
   than discarding it.
3. **Install the tools the project's own checks need** (`pnpm install --frozen-lockfile`, adding
   `--config.engine-strict=false` if the container's Node is slightly old). The preinstalled
   `golangci-lint` may be too old for the module's Go version; build the version CI pins with
   `GOBIN=<scratchpad>/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<pin>`.
4. **Record a baseline** before reading any code: `go build`, `go vet`, `staticcheck`,
   `gofumpt -l`, the full test suite, then `-race`. Anything already broken is finding #1.
   Run the project's actual commands (`make check`, the CI workflow's steps) rather than your own
   guesses; a step that can never pass (for example `-race` under `CGO_ENABLED=0`) is a defect.
5. **Create `papercuts.md`** at the repo root (format below) and commit it with the first fix.

## The loop

List the range oldest first (`git log --reverse --format='%h %s' <first>^..<tip>`) and take the
commits in order, one file at a time. For each commit:

1. **Read the diff, then the file as it stands now.** Later commits often change a file; judge
   behaviour at the tip but attribute the finding to the commit that introduced it.
2. **Read tests as evidence.** A test that only exercises the first connection, sleeps to dodge a
   race, or asserts the current behaviour verbatim is often hiding the bug.
3. **For every defect: reproduce first.** Write the test (or a throwaway experiment), run it, and
   confirm it fails *for the stated reason*. This is non-negotiable and is the project's own rule
   (CLAUDE.md, "test first, and watch it fail"). If the failure is a hang or a crash, bound it
   with a timeout so the suite still ends.
4. **Make the smallest fix** that turns it green. Follow the surrounding code's idiom; comments
   explain *why*. No new abstractions or options for cases that cannot happen.
5. **Run the affected packages, then the whole suite** (`go test ./...`; `-race` with
   `CGO_ENABLED=1`), plus `gofumpt -l`, `go vet`, and the frontend checks if `web/` changed.
   A fix that breaks a sibling package (strict parsing breaking a generated config, for example)
   is caught here, not in CI.
6. **Append the entry to `papercuts.md`, then commit** the fix, the test and the entry together.
   One logical fix per commit; independent findings get separate commits even in the same file
   area. If two fixes are entangled in the same lines, one commit describing both is acceptable.
7. Never commit secrets, real mail, `.env`, or generated build output.

Use `Edit`/`Write` for file changes, never `sed` or Python scripts (CLAUDE.md). Appending to
`papercuts.md` or a test file with a heredoc is fine; rewriting source with `sed` is not.

## What to hunt for

These are the defect classes that actually turned up reviewing the first 25 commits. Check each
file against them.

- **Silent failure.** `_ = f()` on something that matters, unknown config or scenario keys
  ignored, an empty scan reporting "0, passed". Fail loudly; reject unknown YAML (`yaml.Strict()`).
- **Fault and state models that only work once.** A "sticky" condition implemented with a
  consumed, one-shot fault; tests that check only the first attempt. Always test attempts 2 and 3.
- **Cancellation.** Any function taking `ctx` whose I/O cannot be interrupted (clients whose
  calls take no context), `Dial` without a context or timeout, goroutines that `Wait()` forever.
- **Crash paths.** `panic` helpers called from server goroutines with operator-controlled input;
  `recover()` does not catch stack overflow or `fatal error`.
- **Secure by default.** Zero values must be the safe choice (TLS on, loopback only, no
  cache). A boolean named for the safe thing (`TLS`) fails open; name it for the unsafe thing
  (`Insecure`).
- **Hostile input boundaries.** Anything parsing mail or headers: trust of sender-supplied
  headers (`Authentication-Results` without an `authserv-id` check), depth and size bombs,
  duplicated identifiers (same `Message-ID`), header and filename injection.
- **HTTP.** Missing server timeouts (not `WriteTimeout` on SSE routes), response transformations
  that corrupt semantics (compressing `206` or `no-transform`), missing `Referrer-Policy` /
  `nosniff` / framing headers, `Cache-Control` on private data.
- **Ordering and pagination.** Nullable sort keys in keyset pagination, clock-skewed dates,
  tie-breaks, rows that can fall out of every page.
- **Races in tooling.** Change detection that baselines after the work it is watching; child
  processes that outlive their parent (process groups, orphaned dev servers on a fixed port).
- **Localisation of lookups.** Tables keyed ASCII-folded but looked up un-folded, case handling,
  accented folder names.
- **Docs that disagree with enforcement.** A linter list in STANDARDS.md that `.golangci.yml`
  does not enable, a rule in CLAUDE.md the Makefile contradicts. Reconcile them, and run the
  documented linter set once to see what the gap hides.
- **Tests that cannot fail.** Assertions satisfied by an empty result, `t.Parallel()` with shared
  environment, `os.Unsetenv` without `t.Setenv`, sleeps instead of conditions.

## Severity labels

`bug` wrong behaviour today · `risk` works now, fails under a plausible condition · `standards`
violates CLAUDE.md or `docs/STANDARDS.md` · `nit` clarity or consistency. Use `N<number>` with
`(open)` for findings *not* fixed, and always say why: it needs a design decision, it needs a
measurement on the target hardware (the Le Potato), or it belongs to a later chunk.

## When not to fix

Do not silently implement a design decision. Log it as open with a concrete recommendation and
tell the operator in the summary. Examples: which `authserv-id` to trust, the writer-connection
model for SQLite, security-header policy that depends on a later chunk's CSP. Do not "optimise"
without a number; add it to the open list as a measure-first item.

## `papercuts.md` format

```
## `<sha>` <subject>   (or a range, or "Baseline")

- **#N** · `<sha>` · `<file>` · **<severity>** · what was wrong, how it was reproduced (name the
  test and say it failed before the fix), and what changed.
- **N<number> (open, <reason>)** · `<sha>` · `<file>` · what, why it is not fixed, what to do.
```

Numbers are sequential and never reused. The commit that introduced the defect is the `<sha>`,
not the review commit. Keep entries factual; no blame.

## Commit messages

Follow CLAUDE.md: present tense, subject under 50 characters, the body explains *why*, then the
attribution trailers from the session's system reminder. The body names the symptom, not the
mechanism only. Example: "Fold diacritics when classifying folders / The role table is ASCII but
real German and French folders are accented, so they all fell through to other."

## Reporting

Work in the open: when the operator is away, keep going, but every long stretch should be
followed by a one-line status. At the end, report:

1. Commits reviewed and commits skipped (and why).
2. Counts of findings fixed versus open, grouped by severity.
3. The open items that need *their* decision, most serious first, each with the recommendation.
4. Anything that could not be run in this environment (browsers, the target hardware, live
   mailboxes) and therefore remains unverified.
5. The branch name and how to merge it (the operator decides; do not open a PR unless asked).

## Pitfalls seen so far

- A background test run that hangs keeps a process alive; kill the stray processes
  (`pkill -f "sleep 300"`) and bound every experiment with `-timeout`.
- A throwaway experiment file left in the tree (`zz_*_test.go`) must be deleted before the next
  commit. Check `git status` after every experiment.
- goleak failures at exit usually mean a fake is sleeping without a way to be cancelled; fix the
  fake, not the assertion.
- `go test` caching hides flakiness; use `-count=1`, and `-count=3` under `-race` for anything
  involving goroutines.
- Do not let a green suite end the review of a file: several defects here (never-healing fault,
  vacuous budget, stale precompressed siblings) passed every existing test.

## Added after the first audit

- **Run the project's own whole gate at the end**: `make check`, the documented linter set at
  the CI-pinned version, `govulncheck` on the newest patch toolchain
  (`GOTOOLCHAIN=go1.26.N go run golang.org/x/vuln/cmd/govulncheck@<pin> ./...`; older patches
  report stdlib CVEs that are not the code's), and a smoke run of the real binary.
- **Measure growth, not one point**, when a parser looks slow: run depths or sizes
  1, 4, 8, 12, 16, 20 with a hard `timeout`. Super-linear growth is the finding.
- **A fuzz target without a time bound cannot find a denial of service.** Add a duration
  assertion and seed the shape you just found.
- **Generated files**: regenerate with the tool, then diff. If your local tool version rewrites
  unrelated metadata (pnpm's `libc:` selectors), restore it before committing; never hand-merge
  a patch into a lockfile, verify with a frozen install.
- **Lint staging**: change the config first and list every finding; fix real defects at the
  source, keep `//nolint` for deliberate code with a same-line reason, and exclude a linter for
  tests only with the reason written in the config.
- **A change to a core API** (the store's writer model) is worth doing early at the operator's
  request, but write the failing concurrency test first against the old API so the gain is shown.
