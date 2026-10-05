# Ivy

A self-hosted web mail client. One Go binary with an embedded SvelteKit frontend that mirrors an
existing IMAP/SMTP mailbox into SQLite, then adds fast search, tags and careful LLM features on top.
Built for a single operator on a small server, reached over Tailscale.

**Status: planning.** There is no application code yet. The plan, architecture, standards and
design mockups are in [`docs/`](docs/); start with [`docs/PLAN.md`](docs/PLAN.md).

## Principles

- Pure Go (no cgo) and SQLite; tests written first and watched failing.
- Your mail stays yours: the mirror never erases anything, and email is treated as hostile input.
- Fast on modest hardware, with compression and bounded data from the start.

## Running it

Installing on a Linux board, updating and restoring: [`docs/DEPLOY.md`](docs/DEPLOY.md).

## Contributing

Read [`CLAUDE.md`](CLAUDE.md) and [`docs/STANDARDS.md`](docs/STANDARDS.md) first. Security issues:
see [`SECURITY.md`](SECURITY.md).

## License

[AGPL-3.0](LICENSE).
