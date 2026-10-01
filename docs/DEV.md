# Local dev stack

Status: DRAFT (2026-10-01), requirement from round 22. Goal: one command gives a fully working Ivy
with a believable seeded mailbox, **no internet, no credentials, no real mail account**, so UI and
logic iteration is instant and safe. It is built in Milestone 0 on top of `internal/mailworld`
(`STANDARDS.md` section 3) and is the same machinery the tests use.

## 1. Commands

```
make dev                   # = ivy-dev up --profile demo   (the everyday command)
ivy-dev up [--profile demo|minimal|empty|large] [--seed N] [--mode full|fast] [--expose]
ivy-dev reset              # throw away dev state, restore the profile's snapshot (seconds)
ivy-dev snapshot save|restore <name>
ivy-dev state <name>       # put the app into a named condition (section 4)
ivy-dev deliver|flag|move|expunge|fault|advance-clock ...   # play "the other mail client"
ivy-dev seed --profile ... # (re)generate seed data only
```

`ivy-dev up` starts, in one process group with prefixed, colour-coded logs and clean Ctrl-C shutdown:
1. **mailworld**: fake IMAP, SMTP, OpenRouter (Jev/chat/vision) and Ollama on loopback ports.
2. **Ivy** (real code, rebuilt and restarted on Go file changes) with a generated dev config pointing
   at mailworld, a dev data dir under `.dev/` (git-ignored) and the clock under dev control.
3. **Vite dev server** with hot module reload, proxying `/api` and SSE to Ivy, so the CSS/Svelte
   edit-save-see loop is instant. (`--no-web` serves the embedded build instead, to check the real
   artefact.)
4. A printed URL, plus a QR code for the phone when `--expose` is set.

## 2. Modes

| Mode | What happens | Use it for |
|---|---|---|
| `full` (default) | Mailworld is seeded; Ivy **syncs from it over real IMAP** | Anything touching sync, write path, send, banners, SSE: the real pipeline |
| `fast` | The seeder writes the SQLite DB directly, using the **real store/migration code** (never raw SQL), skipping sync | Pure UI work: starts in about a second even for large profiles |

Both modes produce the same visible mailbox; a test asserts the `full` result equals the `fast`
result for the `demo` profile, so `fast` can never silently drift.

## 3. Seed profiles

All profiles are **deterministic** (`--seed`, default fixed; printed on start) and contain no real
personal data. Profiles are code plus a small hand-written corpus in `testdata/`, so they can be
reviewed and tested.

| Profile | Contents |
|---|---|
| `empty` | Configured accounts, no mail: first-run Welcome and empty states |
| `minimal` | About 15 messages, one account: fastest smoke runs |
| `demo` | About 300-500 messages across 3 addresses: threads of every shape (long, forked, subject-changed), newsletters with `List-Unsubscribe`, receipts and invoices (with and without JSON-LD), contact-form mail with Reply-To, security/abuse reports, personal correspondence, calendar invites, Junk with spam scores, attachments of every type, inline images, remote-image and tracking-pixel bait, nasty HTML and XSS samples, non-UTF-8 charsets, RTL and emoji, very long threads and huge bodies. Also seeded: tags, rules, snoozes, a populated stats ledger (a month of LLM calls and costs) and Jev decisions, so every screen has real content. |
| `large` | 100k+ synthetic messages (realistic size distribution) for performance and scroll work |

Fast resets: each profile's built state is cached as a snapshot (`.dev/snapshots/`, keyed by profile,
seed and schema version), so `ivy-dev reset` restores in seconds and a schema change rebuilds it.

## 4. Named states (drive the edge-case screens)

`ivy-dev state <name>` flips mailworld and Ivy into a condition so every mockup in
`docs/design/canvas/` can be built and screenshotted against the real app:

`sync-auth-failed`, `unreachable`, `backfilling` (with progress), `fetch-failed` (message and
attachment), `send-too-large`, `send-transient-4xx`, `llm-cap-reached`, `llm-provider-down`,
`offline` (server stopped), `mirror-healthy`. Each is a mailworld fault plus a clock/state tweak,
defined once and reused by E2E and visual-baseline tests.

## 5. The fake LLM in dev

The fake OpenRouter answers deterministically from simple rules over the seeded mail (a message
from a receipts sender is "receipt", contact-form mail "needs attention", and so on), so triage,
the Reading feed, the digest, receipts ledger and Ask Ivy all show plausible content with **zero
spend**. Its answers are fixtures, not a judgment of real accuracy: accuracy is the eval suite's
job against real Jev.

## 6. Safety rails

- The dev config **refuses to start against a non-loopback IMAP/SMTP/LLM host**, and `ivy-dev`
  refuses to read a real `.env`; a test asserts both. Dev mode can never touch real mail by accident.
- Dev data lives only in `.dev/` (git-ignored along with `.env`); nothing is shared with
  `~/.config` or a production data dir.
- `--expose` binds the tailnet interface only, for trying the fake mailbox on the real phone and
  iPad (Safari), and prints a reminder that it is fake data with no auth.

## 7. Using it in tests

Go tests call the same seeder (`mailworld.Seed(w, profile)`) in-process; Playwright starts
`ivy-dev up --profile minimal --mode fast` (or `full` for sync flows) on free ports and uses
`ivy-dev state` / `deliver` as test steps. One mechanism for humans, tests and CI, so the dev stack
is exercised constantly and cannot rot.

## 8. Definition of done for the dev stack (Milestone 0)

- [ ] `make dev` goes from clean checkout to a working seeded inbox in the browser with no network,
      credentials or manual steps.
- [ ] Profiles are deterministic (same seed, same data; asserted by hash) and contain no real data.
- [ ] `full` and `fast` agree for `demo`; `reset` restores in under 5 s.
- [ ] Every named state works and has a Playwright test and visual baseline.
- [ ] The rails in section 6 are tested.
- [ ] Hot reload works for Go (restart) and Svelte/CSS (HMR) edits.
