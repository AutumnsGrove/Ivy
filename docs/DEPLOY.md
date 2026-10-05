# Deploying Ivy on the board

First install on the Le Potato (or any Linux host with Docker), then how an update, a rollback and a
restore work. The design is `ARCHITECTURE.md` section 9; this is the runbook. Steps marked
**(live check)** have been proved against stubs and in containers on a laptop but not yet on the
board; do them once and note what you saw in `next_steps.md`.

Nothing compiles on the board. CI builds the image on every merge to `main` and publishes it to
`ghcr.io/autumnsgrove/ivy` (public, no `docker login`); the board only pulls it.

## 0. Before you start

- The code you want is merged to `main` and the **Publish the container image** workflow has run
  green. The board gets whatever `:latest` is when it pulls.
- The host has Docker with the compose plugin, `git`, `systemd`, `sudo`, `flock` and `timeout`
  (util-linux and coreutils; both are on any normal distro), and your user is in the `docker` group.
- The host is on your tailnet and you know its name (`potato.your-tailnet.ts.net`). Ivy has no login:
  being reachable only over Tailscale is the access control.
- You have the mailbox password (Purelymail: a mailbox or app password) and, if you want meaning-based
  search, an OpenRouter key.

## 1. Install

```bash
git clone https://github.com/AutumnsGrove/Ivy.git ~/ivy
cd ~/ivy
sudo ./install.sh
```

The checkout **is** the install directory: the update watcher pulls `main` into it. `install.sh`
(run it from your own account with `sudo`, so it knows who the deploy user is):

- creates `data/` (owned by you, mode 700) and `update-signal/` (world-writable, because the container
  writes into it through the bind mount);
- writes `.env` with `IVY_UID`, `IVY_GID` (the container runs as you, so it can write `data/`) and
  `IVY_IMAGE` (the image reference the watcher rewrites on every update);
- installs the systemd units `ivy-update.path`, `.service` and `.timer`, the hash-pinned root wrapper
  `/etc/ivy/watcher-sync-verify.sh` and one sudoers rule allowing exactly that wrapper.

It does **not** write your configuration or start Ivy.

## 2. Configure

Two files, both in `data/` (the container sees them as `/data/...`).

`data/ivy.yaml`, the non-secret settings. Unknown keys are an error on purpose:

```yaml
# Names a browser may use to reach Ivy, with no scheme and no port. Without your tailnet name here
# every request from the phone is refused (loopback is always allowed, for the health check).
allowed_hosts:
  - potato.your-tailnet.ts.net

accounts:
  - id: purelymail            # also names the password variable: IVY_PURELYMAIL_PASSWORD
    address: you@yourdomain.com
    imap_host: imap.purelymail.com
    imap_port: 993            # implicit TLS
    smtp_host: smtp.purelymail.com
    smtp_port: 465            # implicit TLS
    username: you@yourdomain.com
    # Hosted embeddings send message text to OpenRouter, so they need both lines and a key.
    # Leave them out to run on keyword search alone.
    # llm_enabled: true
    # embed_provider: openrouter

# llm:
#   monthly_cap_usd: 5        # the default; embeddings stop at the cap

backup:
  at: "03:00"                 # local time; the snapshot goes to /data/backups unless you add targets
```

`data/.env`, the secrets (`chmod 600 data/.env`):

```
IVY_PURELYMAIL_PASSWORD=the-mailbox-password
OPENROUTER_API_KEY=sk-or-...        # only if embed_provider: openrouter
# GITHUB_TOKEN=...                  # optional: raises the GitHub API rate limit while an update waits on CI
```

If `ivy.yaml` is missing Ivy starts with defaults and **no accounts**, which looks like an empty app,
so check the log in the next step.

## 3. Start

```bash
cd ~/ivy
docker compose up -d
docker compose logs -f ivy          # look for: ivy <version> listening on 0.0.0.0:8418
curl -s http://127.0.0.1:8418/api/v1/health
docker compose ps                   # the image's own healthcheck should say "healthy"
```

Then open `http://potato.your-tailnet.ts.net:8418` on the phone. The first sync backfills the mailbox
in the background; mail appears newest first as it arrives. Watch for these lines in the log:

- `embeddings are off for this account: ...`: the provider is chosen but its key or address is
  missing; the message names the setting.
- `sync worker stopped` or an account banner saying it can't sign in: wrong password or host.

The port is published on all of the host's interfaces, and the host-name check is what keeps a request
from another network name out. If the board is on a LAN you don't trust, publish it on the tailnet
address only: add `IVY_BIND=100.x.y.z` (the board's `tailscale ip -4`) to `.env`, then
`docker compose up -d`. `IVY_PORT` changes the port the same way.

**(live check)** Archive a message, undo it, then empty the Trash on a throwaway message. Those are the
real MOVE/UIDPLUS/COPYUID/expunge paths against Purelymail that the fake mailbox only imitates.

## 4. Update

Settings, then Update, or from a shell `docker compose exec ivy /app/ivy update`. Either way Ivy:

1. waits (up to 6 minutes) for an in-flight publish of `main`, so a click right after a merge doesn't
   deliver the previous build;
2. asks GHCR for the digest of `:latest` and writes `ghcr.io/autumnsgrove/ivy@sha256:...` to
   `update-signal/requested`. The container never touches the Docker socket.

On the host `ivy-update.path` fires, and `compose/watcher/update.sh` (as you, not root) pulls `main`
into the checkout, pins that exact digest in `.env`, pulls it, recreates the container and waits up
to 5 minutes for the healthcheck. It writes `update-signal/result` (`ok` or `failed` with the reason)
and the Settings screen shows it. Ivy itself restarts during this, so the page reconnects.

If the new container is not healthy the watcher puts the previous image back and the result says
`failed`, with the container's last log lines. `systemctl status ivy-update.path` and
`journalctl -u ivy-update.service` show the watcher's own side.

**(live check)** Do one real update end to end, and one on purpose with nothing new, before relying
on it.

### Rolling back by hand

```bash
cd ~/ivy
git log --oneline -5                       # find the digest you want in a previous result, or tag by short SHA:
# edit .env: IVY_IMAGE=ghcr.io/autumnsgrove/ivy:<short-sha>   (every merge to main also publishes its short SHA)
docker compose up -d --force-recreate ivy
```

Ivy **refuses to open a database newer than itself** (`database is newer than this Ivy`). If an
update migrated the data and you then go back to an older image, it will say so instead of running on
a schema it doesn't know. Restore a backup taken before the update (below), or go forward.

## 5. Backups and restore

A daily snapshot of `state.db` (tags, rules, snoozes, the spend ledger, the outbox) goes to
`data/backups/` and the last 15 are kept. The mailbox mirror is not backed up: it is rebuilt from IMAP.
`data/backups/` is on the same disk as the data, so copy it somewhere off the board now and then
(a second backup target would need its folder mounted into the container in `docker-compose.yml`).

```bash
docker compose exec ivy /app/ivy backup --config /data/ivy.yaml        # a snapshot now
docker compose stop ivy
docker compose run --rm --no-deps ivy restore --config /data/ivy.yaml /data/backups/<snapshot>
docker compose up -d
```

`restore` refuses while a server holds the data directory, hence the `stop`.

## 6. Troubleshooting

| Symptom | Likely cause |
|---|---|
| The phone gets a refusal or a blank page | `allowed_hosts` is missing the name the browser used (no port, no scheme). `docker compose exec ivy /app/ivy doctor --config /data/ivy.yaml` prints what it allows. |
| Empty app, no accounts | `data/ivy.yaml` missing or not where the container sees it (`/data/ivy.yaml`). |
| `can't sign in` banner | Wrong password or host. The password variable is `IVY_<ID>_PASSWORD` with the account id upper-cased and punctuation turned into `_`. |
| Update says `ghcr token request failed (status 403)` | The package is private. In GitHub, Packages, ivy, set it public. |
| Update says `... did not report a result` | The watcher isn't running: `systemctl status ivy-update.path`, and was `install.sh` run with `sudo` from your own account? |
| Update `failed`: "not a ghcr.io/autumnsgrove/ivy digest" | Something other than Ivy wrote `update-signal/requested`; the watcher refuses any other image. |
| `database is newer than this Ivy` | An older image is running on data a newer one migrated. See rolling back. |

## 7. Things to check once, live

- [ ] A real update, end to end, and its result on the Settings screen.
- [ ] Archive, undo and empty Trash against the real mailbox (section 3).
- [ ] If embeddings are on: after the first pass, the spend screen shows calls with a cost (a row
      flagged "estimated" means OpenRouter reported none; tell me and the price table gets checked).
- [ ] The settle benchmark on the board, `docs/PERFORMANCE.md` "Settle at rest", to decide whether the
      whole-mailbox passes need to skip when nothing changed.
