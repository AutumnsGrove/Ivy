# Penguin Mail: what it is, and what it teaches

Reconnaissance notes, 2026-10-22. Evidence for the plan in `README.md` beside this file. Nothing
here is a commitment; it is the reading that the plan is based on.

## Which Penguin Mail

Two projects share the name, and only one exists:

- **`penguin-mail.com`** — mail and calendar for Linux and macOS, written in Rust on GTK 4 and
  libadwaita 1.8, GPL-3.0-or-later, `github.com/c9dev/penguin-mail`, version 1.0.10 at the time of
  writing. Made by one developer (Pivotd / `kavourias`), who says openly that parts were built with
  AI assistance. **This is the one worth reading.**
- `penguinmail.app` — a landing page for an "alpha test starting soon". No product, no source.

The real one, measured on 2026-10-22: created 2026-09-19, 1.0 released 2026-10-07, 498 stars, 28
forks, 19 open issues, 1360 files and 894 `.rs` files, and a 185-comment Hacker News thread. Five
weeks from empty repository to a 1.0 with a package in six formats. That velocity is the thing to
understand, not to copy.

## Shape, and why the code is not the lesson

Penguin Mail is a **desktop client**: no server, no mirror, no daemon of its own. Gmail and
Microsoft accounts talk to their own APIs, everything else over IMAP/POP3/JMAP, and all state lives
in the user's home folder. About 55 MB resident in the tray.

Ivy is the opposite shape: one server binary, a mirror in SQLite, one mailbox, reached from a phone
over Tailscale. So Penguin's architecture transfers almost nothing. What transfers is its
**interface craft**, its **assistant contract**, and a handful of **product decisions** it got right
or wrong.

Per-provider modules (`gmail/`, `graph/`, `imap/`, `jmap/`, `pop3/`, `dav/`, `sieve/`, `pgp/`,
`smime/`), a `domain/` crate, an `ai/` crate, and a `testmail/` crate used by the sync tests. The
last one is the same instinct as `internal/mailworld`: **the fake outside world is a first-class
module, not a test fixture**. Ivy already has that.

## The screenshots, one by one

Sixteen are published under `docs/screenshots/` in their repository (raw links below). Light and
dark of the same window, because they show the whole product in one view.

| Shot | What it teaches |
|---|---|
| `inbox.png` | Three panes: a favourites/mailboxes/accounts rail, a list with a **facet row** (`All 8 · unread 3 · 📎 1 · flagged 1 · 👤 1`), and a reader whose older thread messages are one-line rows with the newest expanded and a `Details` toggle. Rows carry a coloured account dot. |
| `dark.png` | Same window, dark: the whole app inverts from one token set, no bespoke dark artwork. |
| `phone.png` | Phone is a single column: back chevron, four toolbar actions and an overflow. No third pane, no persistent bulk bar. |
| `assistant.png` | The assistant pane: the answer grouped under bold subheadings, numbered items, one folded row per tool (`Thought for 1 second`, `Reading a mailbox — follow_up ✓`), a live line at the bottom, input pinned below, `+` for a new thread. A `Summarize` chip sits in the reader header. |
| `categories.png` | The category bar with per-category counts; a mailing-list strip across the reader (`This message is from a mailing list — [Unsubscribe]`); weekday labels (`Sunday`, `Saturday`) instead of dates for mail a few days old. |
| `selection.png` | Three conversations selected: the **reader pane becomes the bulk surface** — "3 Conversations Selected / Actions and shortcuts apply to all of them. Esc clears the selection" with large buttons. |
| `composer.png` | Subject in the title bar, `From` collapsing to one line, recipients as chips, Cc/Bcc at the right, a **split Send button** (Send + chevron), sign/encrypt in the title bar, signature as a removable block with its own menu. |
| `vips.png` | A VIP mailbox with one child per person, a star on their rows. |
| `flags.png` | Seven flag colours as child mailboxes under Flagged, the colour kept locally while only the star syncs. |
| `rules.png` | The Rules dialog: **"Gmail runs these on new mail as it arrives, even when this computer is off"** above the list, then each rule as one plain sentence with a trash button. |
| `preferences.png` | A search field, a bottom tab bar, per-account toggles, and a **disabled row that says why**: "dana@fastmail.example — Fastmail's contacts come in a later version." |
| `hide-my-email.png` | One plus-address per site, with the site and creation date as the secondary line, a copy button and an on/off switch whose off state sends the mail to the Trash. |
| `send-later.png` | A scheduling mailbox where every row says when it goes (`Sends tomorrow at 08:00`). |
| `outlook.png` | Focused/Other on a Microsoft account, folders and labels distinguished by glyph, the account colour dot repeated in the list. |
| `calendar-week.png` | A calendar, out of scope for Ivy. Noted only for its month strip, per-account calendar toggles with a padlock for read-only, and locked rows explaining the future. |
| `invitation.png` | An invitation card inside the message: title, when and where, recurrence, organiser and RSVP count, a strip showing the rest of that afternoon and "Nothing else at that hour", a note field, Yes/Maybe/No, and the `.ics` as a normal attachment chip. |

Raw base URL: `https://raw.githubusercontent.com/c9dev/penguin-mail/main/docs/screenshots/<name>.png`

## Their assistant contract

From `docs/assistant.md` (~1200 lines, written as a manual for the operator). The parts worth
studying:

1. **The assistant uses the app's own tools, not a private database path.** On an account from
   another provider it files mail into folders; when you ask for something only one provider has, it
   says why that account cannot.
2. **You see the work.** Each answer shows the model's thinking (folded), one row per tool it ran
   with a done/failed mark, then the reply; a line at the bottom says what it is doing now.
3. **It asks before acting,** and the list is explicit and long: sending, turning on an away reply,
   creating or deleting a rule, blocking a sender, **moving more than 25 conversations to the Trash**,
   deleting forever, rescheduling or cancelling a waiting send, unsubscribing, calendar changes,
   renaming or deleting a label, changing a setting, opening a web page (the whole address shown),
   running an MCP tool. Each question is a card with Allow / Don't Allow.
4. **Bulk is the trigger, not the category.** Small reversible things are free; a *volume* threshold
   asks. That is a cleaner rule than a per-action allow-list.
5. **Everything it does lands on the same undo stack as the UI's own actions** (Ctrl+Z), so "undo
   that" covers the assistant's work and yours, newest first. This is why it can safely act at all.
6. **Drafts always open in a composer.** It never sends without a confirmation that names every
   recipient, the blind copies, the files, and whether it is signed or encrypted.
7. **A tool result is data.** Its instructions say never to follow instructions inside an email, and
   the approval cards are the backstop if it does. Pages it reads are fetched by the app (2 MB, 20 s,
   no proxy, DNS checked, redirects re-checked, local addresses refused) and the address is shown
   before each fetch, because *"a message the model read could have told it to put your mail there"*.
8. **What is written down is what gets tested.** Every behaviour above is a sentence an operator can
   check.

## What Hacker News said

Praise: it looks like Mail.app, it is fast, and it is more polished than the old Linux clients
(GNOME-flavoured, native widgets, dark and light both designed).

Criticism, in the order it appeared:

- **Security of the HTML view.** "How well do they sanitize the full-content view?" — the top
  comment. Ivy already answers this better (sanitise server-side, sandbox the frame, block remote
  content twice, per-sender allow-list).
- **No sender identity.** "can't choose a sender email?! This is major dealbreaker, if you have many
  aliases or domains" — the second-most-upvoted complaint, twice. Ivy has identities (4e).
- **Menu discoverability.** Hamburger plus three-dots means "spelunking submenu tunnels" for an app
  with this many actions.
- **No per-row site icons**, folder ordering that fights the user, "server doesn't support that" when
  colouring a folder.
- **It advertises Fastmail but has no JMAP.**
- **"Vibe-coded projects get abandoned within a year"** — the strongest scepticism, and the one the
  author answers by saying it is his daily driver.
- **"Why stuff an AI chatbox into my email?"** — the hive's reflex, and exactly what Ivy is not
  doing (quiet chips, plain text, never labelled as AI).

## What we deliberately do not copy

| Their thing | Why not for Ivy |
|---|---|
| Calendar views, CalDAV, invitations with RSVP | Out of scope. `PLAN.md` 1 makes RSVP a v1 non-goal, and the operator confirmed on 2026-10-22: no calendar. The one survivor is the existing backlog item: an invite mail may still offer its `.ics` for the phone's calendar app. |
| OpenPGP and S/MIME | Non-goal in `PLAN.md` 1. |
| Gmail/Microsoft Graph/JMAP providers | Settled: IMAP/SMTP, Purelymail first. |
| Tray icon, desktop notifications | Settled: no push; Apple Mail keeps that job. |
| MCP servers, skills with sandboxes, "Claude Subscription" | A desktop extension ecosystem for a multi-user tool. Ivy is one operator, one binary, one gate. |
| Seven flag colours, VIPs | Ivy has 12 tag colours and a derived People view; a second axis of colour-coded state would be noise. |
| Categories from Gmail's own API, Focused/Other | 5d's categories are local tags answered by Jev, and must work on any IMAP server. |
| Send Later | Worth a line in the backlog, not a stage: the operator mostly receives, and a scheduler is a real subsystem (a clock, resumption across restarts, its own view). Their split Send button only makes sense with it. |
| "Hide My Email" plus-addresses | Gmail-specific, and Purelymail handles aliases at the catch-all anyway. |

## Process notes worth stealing

- **`AGENTS.md` and `CONTEXT.md` at the repository root**, next to a `CHANGELOG.md` written for users.
  Ivy has `CLAUDE.md` and a doc map; a user-facing "what changed" surface is still missing, and
  `ivy update` makes it feel needed.
- **`docs/` written for the operator, not the maintainer** (`setup.md`, `assistant.md`,
  `accessibility.md`, `privacy-policy.md`). Ivy's docs are engineering-facing. One operator-readable
  page stating **what each helper does and when it asks** would be both documentation and an
  acceptance checklist for chunks 5 and 6.
- **Idle memory treated as a feature** ("a minute after you close the window it restarts itself in
  the background to give back the memory the window used"). The potato deserves the same honesty:
  state the idle number, not only the working set.
- **A per-package table of what works where** (Flatpak/snap lose skills and external commands). Ivy
  has one deploy path, so this is only a reminder to keep `DEPLOY.md` telling the truth.
