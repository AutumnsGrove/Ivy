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
| `fast` | The seeder writes the SQLite DB directly, through the **sync's own store path** (`StoreRaw`, `RecordFolder`, `Settle`; never raw SQL), skipping IMAP | Same rows as `full`, no network. It is not quicker to build (parsing and writes dominate both); the speed comes from the build cache below |

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

Fast resets: each profile's built databases are cached in `.dev/cache/<key>` (the key covers profile,
seed, accounts, both schema versions and `DerivedVersion`), and an empty data directory is restored by
file copy, so `ivy-dev reset` then `up` is quick: measured `demo` 9 ms, `large` (100k, 2.8 GB) 3.5 s.
Only a clean build is cached, never a directory the operator has used; a schema or parser change
changes the key and rebuilds; an unreadable cache is discarded with a message and rebuilt. The cache
doubles the disk use (delete `.dev/cache` to reclaim it). `.dev/snapshots/` still holds the named launch
recipes (`ivy-dev snapshot`). The first build of a profile is slow (`large` about 3 minutes).

## 4. Named states (drive the edge-case screens)

`ivy-dev state <name>` flips mailworld and Ivy into a condition so every mockup in
`docs/design/canvas/` can be built and screenshotted against the real app:

`sync-auth-failed`, `unreachable`, `backfilling` (with progress), `fetch-failed` (message and
attachment), `send-too-large`, `send-transient-4xx`, `llm-cap-reached`, `llm-provider-down`,
`offline` (server stopped), `mirror-healthy`. Each is a mailworld fault plus a clock/state tweak,
defined once and reused by E2E and visual-baseline tests.

The list lives in `internal/devstack/states.json` (name, description, and the frontend `?scenario=`
screens each one corresponds to). Go embeds it for `ivy-dev state`, and `web/e2e/named-states.spec.ts`
reads the same file, so a state added there fails the spec until its screen is described. A state
with no designed screen is listed as an explicit gap (today `send-transient-4xx`). **Until sync and
the LLM gate run continuously (chunks 3 and 5), a mailworld fault changes no screen of the real app**,
so the specs reach each state through the mock `?scenario=` instead; each grows a real-stack twin when
its consumer exists.

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
- This is also a standing integration/E2E test from Milestone 3: A sends to B, B receives, B
  replies, threading, Sent copies, undo-send, Reply-To handling, send-as per address.
- Compose isn't built until Milestone 3, but the pair and local delivery are part of Milestone 0 so
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
- [x] `full` and `fast` agree for `demo` (`TestFastAndFullAgreeForDemo`, every table); `reset` then
      `up` restores in under 5 s (2026-10-04: `demo` 9 ms, `large` 3.5 s from the cache).
- [ ] Every named state works and has a Playwright test and visual baseline. (Tests: done, through
      the mock scenarios, `named-states.spec.ts`; one explicit gap, `send-transient-4xx`. Baselines:
      open, they need the CI harness regenerated. Real-stack twins arrive with chunks 3 and 5.)
- [ ] The rails in section 6 are tested.
- [ ] Hot reload works for Go (restart) and Svelte/CSS (HMR) edits.
