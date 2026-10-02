# CI plan

Status: DRAFT (2026-10-01), round 26. GitHub Actions. **Written for a public repository** (the
operator plans to make `AutumnsGrove/Ivy` public), which gives free Actions minutes but also means
strangers can open PRs and read every workflow, log and doc, so the security rules in section 5 are
part of the plan, not an afterthought.

Principles: CI runs exactly what `make check` runs locally (no CI-only magic); every required check
must have been seen failing once (the red step of TDD applies to CI itself); fast feedback (target
under 5 minutes for a PR); secrets only reach manual, maintainer-approved workflows.

## 1. Phases

**Phase A, now (docs-only repo):** one workflow, `docs.yml`: secret scan (gitleaks, full history)
and a markdown link check. Written when the first workflow is added, with actions pinned by
verified commit SHA.

**Phase B, Milestone 0:** the full set below, built as part of the harness and each check proven
by a deliberately failing commit on a throwaway branch.

## 2. On every PR and push to main (`ci.yml`)

Parallel jobs, path-filtered, superseded runs cancelled (`concurrency`), caches for the Go build,
module cache, pnpm store and Playwright browsers.

| Job | What it runs |
|---|---|
| `go` | `CGO_ENABLED=0 go build ./...`, `gofumpt -l` (must be empty), `go vet`, `staticcheck`, `golangci-lint`, `go test -race ./...` (integration tests against `internal/mailworld`, fuzz seed corpora included), `govulncheck`, plus a `GOOS=linux GOARCH=arm64` compile check (the target architecture; nothing is published) |
| `nocgo` | Fails if any package or dependency has cgo files (`go list -deps -f '{{.CgoFiles}}'`) |
| `drift` | Regenerates OpenAPI (Go and TS types) and `sqlc` output, fails on any diff |
| `web` | `pnpm install --frozen-lockfile`, `svelte-check`, ESLint, Prettier, Vitest, build in a scratch dir, compressed-size budgets (`PERFORMANCE.md` 2) |
| `e2e` | Playwright on WebKit and Chromium against `ivy-dev up --llm fake` (never live), sharded; visual baselines, axe; traces, screenshots and videos uploaded on failure |
| `guard` | Rejects a PR that adds files under `web/build/` other than the placeholder, gitleaks on the diff, AGPL header check, a check that no test path can reach the live LLM provider |
| `deps` | `dependency-review-action` (new dependencies: licence must be AGPL-compatible, no known vulnerabilities) |
| `codeql` | CodeQL for Go and JavaScript/TypeScript (free on public repos), also on a weekly schedule |
| `bench` | Short benchmarks compared with the base commit via `benchstat` in the same job; advisory (comment only) until numbers are trusted, then a regression tripwire |

Required status checks on main (ruleset): `go`, `nocgo`, `drift`, `web`, `e2e`, `guard`, `deps`,
`codeql`. `bench` stays advisory.

## 3. On merge to main (`docker-publish.yml`)

Builds the multi-arch image (amd64, arm64) from the multi-stage `Dockerfile` (frontend with pinned
Node and pnpm, precompressed assets, and the pure-Go binary cross-compiled, all pinned to
`$BUILDPLATFORM` so only the final stage is per-arch) and pushes it to GHCR as `:latest` and the
short SHA. It runs only on `push` to main, with `contents: read` and `packages: write` and nothing
else, uses the GHA build cache, and queues instead of cancelling so a push is never interrupted
(an interrupted push can leave a partial manifest). No repository write access, no bot commits, no
ruleset bypass. The target pulls the image; it compiles nothing. Modelled on Polaris's workflow.

## 4. Nightly, manual and local

- **Nightly (`nightly.yml`):** Firefox E2E suite, time-boxed fuzzing of each fuzz target (about 5-10
  minutes each, corpus cached between runs and new crashers committed as regression seeds),
  `govulncheck` and `pnpm audit` against fresh advisories, longer benchmarks, link rot check.
- **Manual (`workflow_dispatch` only), run in protected GitHub Environments with required
  reviewer approval:** `live` (real `dev@` Purelymail tests, credentials as environment secrets)
  and `evals` (real Jev/chat on the labeled corpus, OpenRouter key as an environment secret). Never
  required, never triggered by a PR.
- **Local only:** `make potato-bench` over `ssh potato-remote` (the potato is never a CI runner;
  see 5), and `make check` (fast pre-commit: format, vet, lint, unit and integration tests).
- **Dependabot:** Go modules, pnpm, and GitHub Actions weekly, grouped, each update going through
  the normal required checks.

## 5. Public-repo security rules

1. **Workflow triggers:** `pull_request`, never `pull_request_target`; no workflow checks out or
   executes fork code with secrets. PRs from forks get no secrets and run only the fake-backed jobs.
2. **Least privilege:** default `permissions: contents: read` at workflow level; jobs add only what
   they need (`web-build` gets `contents: write`; `codeql` gets `security-events: write`).
3. **Pin third-party actions by full commit SHA** (Dependabot updates them); only a short allow-list
   of actions (GitHub-owned, `golangci-lint`, `gitleaks`, `pnpm/action-setup`, Playwright).
4. **No self-hosted runners.** A public repo plus a self-hosted runner lets any PR run code on that
   machine. The potato must never be a runner. Github-hosted runners only.
5. **Secrets:** none in repo or repository-level secrets. Live and eval credentials live in
   protected Environments with required reviewers and are only available to the manual workflows.
   Set "Require approval for all outside collaborators" for workflow runs from forks.
6. **Repo settings:** secret scanning and push protection on; branch ruleset on main (PRs
   required, required checks, no force-push, bot bypass only for `web-build`); `CODEOWNERS`;
   a `SECURITY.md` with a reporting address (never a public issue).
7. **Logs are public:** no mail content, addresses beyond the published role addresses, or tokens
   in test output; tests use `grove.test`-style fake domains and synthetic mail only (`DEV.md`).
8. **Untrusted inputs in workflows:** PR titles, branch names and commit messages are never
   interpolated into shell commands (use environment variables).

## 6. Before the repository goes public (checklist)

- [x] `LICENSE` (AGPL-3.0), `README.md` and `SECURITY.md` added (round 27). Still to add: licence
      headers per the guard job, a `CONTRIBUTING.md` (TDD rules from `STANDARDS.md`), and enabling
      GitHub's private vulnerability reporting (the `SECURITY.md` points to it).
- [ ] Run gitleaks over the **full git history** (not just the tip) and review the result; the
      current tree has no secrets, and history should be rechecked at that time.
- [ ] Review the docs for personal or operational detail you would rather not publish. As of this
      PR the role email addresses were removed from the docs, mockups and `CLAUDE.md` (they remain
      in git history; mockups now use `example.com`). What is left: the `potato-remote` SSH alias
      and the Purelymail migration notes in `qa-log.md`; none are credentials. `qa-log.md` is also
      a very candid working log, so decide whether to keep, trim or move it.
- [ ] Enable the settings in section 5 and confirm the ruleset with a test PR.
- [ ] Decide on the design mockups in `docs/design/canvas/` (large HTML files; fine to publish if
      the Grove assets and fonts used are licensed for it).
- [ ] Note for the dev stack: with a public repo, `ivy-dev` must stay safe by default (no real
      hosts, `.env` git-ignored), already enforced in `DEV.md` section 6.

## 7. Cost and speed

Public repos get free Actions minutes on GitHub-hosted runners, so the earlier minutes concern goes
away; speed still matters for the feedback loop. Budget: `go` under 3 min, `web` under 2 min,
`e2e` under 5 min sharded, all in parallel; track per-job time and the slowest tests in the job
summary, and treat a PR run over 10 minutes as a bug to fix.

## 8. Definition of done for CI (Milestone 0)

- [ ] Every required check has been seen failing on a purpose-built bad commit, then passing.
- [ ] `make check` and CI run the same commands.
- [ ] The image publish workflow works once end to end, and the board pulls and runs the arm64 image.
- [ ] Section 5 rules are in place and verified from a fork (no secrets reachable).
- [ ] Nightly and manual workflows run green once.
