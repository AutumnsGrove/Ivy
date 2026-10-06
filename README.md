<p align="center">
  <img src="docs/design/brand/ivy-icon.png" alt="Ivy" width="120">
</p>

# Ivy

A self-hosted web mail client for one person. Ivy is a single Go binary with an embedded SvelteKit
frontend. It mirrors an existing IMAP/SMTP mailbox into SQLite, then adds fast search, tags, rules
and careful LLM features on top. It is built to run on a small board and be reached from a phone
over Tailscale.

It does not replace your mail host. Your provider stays the source of truth, and Ivy follows it.

> **Status: built, not yet deployed.** Reading, search, tags, rules, snooze, people, backups and
> the update path all work against a fake mail world and the test suite. Composing and sending
> (chunk 4) and the LLM triage features (chunk 5) are not built yet. It has not run on the target
> board or a real mailbox for long, so expect rough edges. Live status is in
> [`next_steps.md`](next_steps.md).

## What it does

- **Mirrors your mailbox.** Full history, threaded conversations, many accounts with a combined
  inbox. IMAP IDLE and QRESYNC keep it current.
- **Never erases anything.** Mail deleted on the server is hidden, not destroyed, and its bytes are
  kept.
- **Writes go to IMAP first.** Archive, delete, flag, junk and tag all go through a durable outbox
  and are applied to the server, so other mail clients see them. Moves and deletes ask first.
- **Search.** Keyword (FTS5) and meaning-based (embeddings) search merged into one ranked list.
  Attachment text is indexed too.
- **Tags, rules, snooze and People.** Tags are stored locally and as IMAP keywords. Rules run
  locally. Snooze hides mail until a time you pick. People is derived from your mail.
- **Treats email as hostile.** HTML is sanitized server-side and sandboxed in the browser, remote
  content is blocked, and server-side fetches are guarded against SSRF.
- **Careful with LLMs.** Everything that costs money sits behind one gate: per-account opt-in, spend
  caps and a ledger row for every call. It is off by default.
- **Built for a phone.** It is designed mostly for Safari on an iPhone and iPad, with a night-garden
  theme and a light theme that follows the system.
- **Small and fast.** Precompressed embedded assets, paged lists, and budgets that are tested. It
  targets about 800 MB of free RAM on an aarch64 Le Potato.

## Try it locally

You need Go and pnpm. The dev stack is offline and seeded with a believable fake mailbox, with no
credentials and no real account.

```bash
make dev
```

This starts a fake IMAP/SMTP world, Ivy, and the Vite dev server together, and prints a URL.
[`docs/DEV.md`](docs/DEV.md) covers the profiles, named states and failure injection.

```bash
make check                       # format, vet, lint, Go tests with -race, frontend checks
cd web && pnpm exec playwright test   # phone and desktop end-to-end
```

## Running it for real

CI publishes a multi-arch image to `ghcr.io/autumnsgrove/ivy`. The target board only pulls it, and
nothing compiles there.

```bash
git clone https://github.com/AutumnsGrove/Ivy.git ~/ivy
cd ~/ivy && sudo ./install.sh
```

Then write `data/ivy.yaml` and your secrets, and start it with Docker Compose. Ivy has no login, so
being reachable only over Tailscale is the access control. Do not expose it to the internet.
[`docs/DEPLOY.md`](docs/DEPLOY.md) has the full runbook for install, update, rollback and restore.

Day to day:

| Command | What it does |
|---|---|
| `ivy run` | Serve the app and sync |
| `ivy doctor` | Check the config, data directory and databases |
| `ivy backup` | Take a snapshot of `state.db` now (also runs daily, 15 days kept) |
| `ivy restore <snapshot>` | Restore `state.db` from a snapshot |
| `ivy update` | Ask the host watcher to deploy the latest image |

Only locally owned state (tags, rules, snoozes, settings, the cost ledger and the outbox) is backed
up. The mirror is rebuilt from IMAP.

## How it is built

Go with pure-Go SQLite (`modernc.org/sqlite`, no cgo), SvelteKit 3 with Svelte 5 and plain CSS custom
properties, JSON REST plus SSE. Two databases: `mirror.db` follows the server, and `state.db` holds
everything that is yours. Libraries and the reasons for them are in [`docs/STACK.md`](docs/STACK.md).

Start with:

- [`docs/PLAN.md`](docs/PLAN.md): the product and milestones
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md): how it fits together
- [`docs/PERFORMANCE.md`](docs/PERFORMANCE.md): compression and budgets
- [`docs/JEV.md`](docs/JEV.md): the cheap decision engine behind the LLM features

## Roadmap

| | |
|---|---|
| Read, sync, outbox, tags, search, rules, snooze, People, backups, update path | done |
| First deploy and live checks on the board | next |
| Compose and send, with undo send and drafts | planned |
| Triage: needs-attention, newsletters, receipts, vision, Ask Ivy, spend stats | planned |

## Contributing

Read [`CLAUDE.md`](CLAUDE.md) and [`docs/STANDARDS.md`](docs/STANDARDS.md) first. Tests are written
first and watched failing, and most are integration tests against the fake mail world in
`internal/mailworld`. Report security issues as described in [`SECURITY.md`](SECURITY.md).

## License

[AGPL-3.0](LICENSE).
