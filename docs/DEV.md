# Local dev stack

Status: DRAFT (2026-10-01), requirement from round 22. Goal: one command gives a fully working Ivy
with a believable seeded mailbox, **no internet, no credentials, no real mail account**, so UI and
logic iteration is instant and safe. It is built in Milestone 0 on top of `internal/mailworld`
(`STANDARDS.md` section 3) and is the same machinery the tests use.

## 1. Commands

```
make dev                   # = ivy-dev up --profile demo   (the everyday command)
ivy-dev up [--profile demo|minimal|empty|large] [--seed N] [--mode full|fast]
           [--llm live|fake] [--accounts N | --pair] [--expose]
ivy-dev reset              # throw away dev state, restore the profile's snapshot (seconds)
ivy-dev snapshot save|restore <name>
ivy-dev state <name>       # put the app into a named condition (section 4)
ivy-dev deliver|flag|move|expunge|fault|advance-clock ...   # play "the other mail client"
ivy-dev seed --profile ... # (re)generate seed data only
```

`ivy-dev up` starts, in one process group with prefixed, colour-coded logs and clean Ctrl-C shutdown:
1. **mailworld**: fake IMAP and SMTP (with local delivery between its mailboxes) on loopback ports,
   plus the fake OpenRouter/Ollama used only when `--llm fake` or no key is present.
2. **Ivy** (real code, rebuilt and restarted on Go file changes) with a generated dev config pointing
   at mailworld, a dev data dir under `.dev/` (git-ignored) and the clock under dev control.
3. **Vite dev server** with hot module reload, proxying `/api` and SSE to Ivy, so the CSS/Svelte
   edit-save-see loop is instant. (`--no-web` serves the embedded build instead, to check the real
   artefact; run `make web-assets` first to build and precompress it into the binary.)
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

## 5. LLM in dev: live OpenRouter is the default (round 23)

`--llm live|fake` (default **`live`**). The seeded mail is synthetic, so sending it to OpenRouter
leaks nothing real, and the UI is judged against real Jev/chat/vision output.

- **live (default):** real OpenRouter (`/systemone` for Jev, chat, vision). The key comes from the
  git-ignored `.env` (`OPENROUTER_API_KEY`). Everything still goes through the one gate, so the
  ledger, per-account opt-in and **a low dev spend cap (default $1/month, `--llm-cap`)** apply and
  the stats panel shows real dev spend. If no key is found, `ivy-dev up` starts with the fake, says
  so loudly in the log and in the UI footer, and never fails.
- **Record/replay cache:** live responses are cached in `.dev/llmcache/` keyed by a hash of
  (endpoint, model, request), so `reset`, restarts and repeated seeds don't re-spend. `--llm-fresh`
  bypasses the cache; `--llm-offline` serves only the cache.
- **fake:** the deterministic fake OpenRouter answers from simple rules over the seeded mail
  (receipts sender is "receipt", contact-form mail "needs attention", ...), zero spend, no network.
  Its answers are fixtures, not a judgment of accuracy.
- **Tests and CI always pass `--llm fake`** (Playwright, benchmarks); live is for the human dev
  loop and the on-demand evals. A test asserts that no test path can reach the live provider.
- Embeddings follow the same switch: `--llm fake` uses the deterministic fake embedder (also
  counting calls, so the embed-once tests can assert on it); `live` uses OpenRouter, capped and
  ledgered like every other remote call; a local Ollama is used only if configured for the account.

## 5b. Two-account pair (test sending between accounts)

`--accounts N` (default 3 in `demo`) and a preset **`--pair`**: two accounts, e.g. `ivy-a@grove.test`
and `ivy-b@grove.test`, both configured in Ivy and both backed by mailworld. Mailworld's SMTP
**delivers locally** when a recipient is one of its mailboxes (it lands in that account's INBOX and
fires IDLE/EXISTS like a real provider) and only records mail addressed to outside domains. So you
can compose in Ivy as A, send to B, see it arrive in B's inbox (and in the combined view with B's
badge), reply as B, and watch the thread join up across accounts.
- Mailworld models whether the provider files a copy in Sent itself or expects the client to
  APPEND (`SentCopy: auto|client`), so both behaviours are testable once spike S1 settles which one
  Purelymail does.
- This is also a standing integration/E2E test from Milestone 4: A sends to B, B receives, B
  replies, threading, Sent copies, undo-send, Reply-To handling, send-as per address.
- Compose isn't built until Milestone 4, but the pair and local delivery are part of Milestone 0 so
  `ivy-dev deliver`/the fake SMTP can already exercise cross-account mail.

## 6. Safety rails

- The dev config **refuses to start against a non-loopback IMAP or SMTP host**, and the only
  external host it allows is OpenRouter (for `--llm live`, key from `.env`, capped); `ivy-dev` will
  not read real mail credentials from `.env`; a test asserts all of this. Dev mode can never touch
  real mail by accident.
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
