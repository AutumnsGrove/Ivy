# CLAUDE.md

Guidance for Claude Code (or any agent) working in this repo. Takes precedence over generic/global
instructions.

## What this is

Ivy is a self-hosted web mail client: a single Go binary (SvelteKit frontend embedded) that sits in
front of an existing IMAP/SMTP provider (first target: Purelymail on `autumn@grove.place`), mirrors
the mailbox into a local SQLite database, and layers fast search, tagging and LLM features on top.
Single-operator tool, primarily used from a phone over Tailscale. Sibling project to Polaris
(`~/Documents/Projects/Polaris`) in shape and philosophy, but fully independent of it.

## Current phase: PLANNING

**No implementation until the operator approves `docs/PLAN.md`.** Work right now is a Q&A session
that produces the plan. Record every answer in `docs/qa-log.md` and fold settled decisions into
`docs/PLAN.md` as they land. Don't create code, a Go module or a frontend scaffold yet.

## Settled decisions (don't re-litigate without asking)

- Name: **Ivy** (grove.place lore). Standalone repo, nothing shared with Polaris.
- Backend: **Go**. Database: **SQLite** (D1 is SQLite, keeping a far-off, maybe-never Cloudflare
  "Grove Ivy" cheap; Postgres was considered and rejected for that reason).
- Frontend: **SvelteKit**, built with `adapter-static`; all backend access through one typed API
  client module so the frontend stays portable.
- Mirror model: sync IMAP into SQLite, render from SQLite, the DB follows the server including
  deletes/moves. **Writes go to IMAP first**, never DB-only. Tags/sorting are the only locally
  owned state and need a backup path.
- Embeddings: Ollama `nomic-embed-text` by default (already on the potato), optional remote
  provider for other people, kept behind an interface (D1 can't load extensions).
- Easy for strangers to run: single binary + first-run `init`. **Deployment is bare metal**: the
  target builds the Go binary; the frontend build output is committed and embedded via `go:embed`;
  updates via `ivy update` (also an in-app settings button). Docker is a maybe-later path.
- License: AGPL-3.0.
- Design: fresh from the ground up. Vendored copy of Grove's design tokens is welcome; no runtime
  dependency on Lattice. The old Ivy (`Lattice/_junkdrawer/apps/ivy`) is reference only.
- LLM layer: `decide()` (classification; default **Jev** via OpenRouter's **`/systemone`** endpoint,
  model `jev-latest`, confirmed live in Polaris's `jev/jev.go`; typed answers + probabilities, no
  explanations, only `choice` questions proven, so yes/no is a `choice`), `complete()` (configurable
  OpenAI-compatible chat model for extraction/summaries/drafts), and `see()` (vision model). All
  calls go through ONE gate (per-account opt-in, caps, ledger). See `docs/JEV.md`.
- Pure-Go SQLite (`modernc.org/sqlite`), pure CSS with custom properties (no Tailwind), pnpm.
- **Testing is a hard requirement: test absolutely everything**; see `docs/TESTING.md`'s definition
  of done. Bugfix tests must be seen failing without the fix.
- Undo-send delay is a setting; there is a Polaris-style stats panel (per-call ledger of all LLM
  spend).
- Email content is untrusted input to any LLM: read-only tools, nothing sends without an explicit
  confirmation.

## Production target

The potato (Le Potato SBC, aarch64, 1.9GB RAM, ~800MB free; `ssh potato-remote`) is the real
deployment target, so RAM matters. Verify on real hardware, not just mocks — same culture as Polaris.

## Conventions

- `uv`/Python instructions in global CLAUDE.md files don't apply: Go + SvelteKit. Use `go build`,
  `go test ./...`, `go vet ./...`, and **pnpm** (not npm) for the frontend.
- Use the Edit/Write tools for file changes, never python/sed scripts.
- Comments explain *why*, not *what*.
- Commit at each stage rather than batching; present-tense messages, first line under 50 chars.
