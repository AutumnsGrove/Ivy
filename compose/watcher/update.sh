#!/usr/bin/env bash
#
# Runs on the host (not inside any container) as a systemd oneshot service,
# triggered by ivy-update.path the instant update-signal/requested appears, or
# by ivy-update.timer every hour as a backstop against a missed inotify event.
# Never invoked by the Ivy container itself: Docker control deliberately stays
# on this side of the container boundary, so the one process that talks to the
# open internet never holds the Docker socket.
#
# update-signal/requested holds the exact image reference (by digest, not a
# floating tag) the settings panel's Update button showed. Pulling that exact
# reference — by rewriting IVY_IMAGE in .env, which docker-compose.yml's
# `image:` field interpolates — instead of re-resolving :latest closes the gap
# where a second commit landing between the click and this script running could
# silently ship something the user never saw.
set -euo pipefail

# This script lives at <install_dir>/compose/watcher/update.sh, two levels below
# the install root where .env and update-signal/ actually are.
INSTALL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_FILE="$INSTALL_DIR/.env"
SIGNAL_DIR="$INSTALL_DIR/update-signal"
LOCK_FILE="$INSTALL_DIR/.update-watcher.lock"
REQUESTED_FILE="$SIGNAL_DIR/requested"
RESULT_FILE="$SIGNAL_DIR/result"
SERVICE="ivy"
# Reused across every risky command below so a failure's detail is the real
# output, not a fixed generic string. Overwritten per-command, never appended.
CMD_LOG="$SIGNAL_DIR/.last-command.log"

cd "$INSTALL_DIR"

# json_escape makes a value safe inside a JSON string. Backslash and the quote
# are escaped; every control character (docker prints tabs, carriage returns and
# colour escapes in its errors) becomes a space, because JSON forbids them raw
# and an invalid result file shows the operator no failure reason at all.
json_escape() {
	printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g' | tr '\000-\037' ' '
}

# truncate_detail keeps the result readable; docker output can run to dozens of
# per-layer lines and only the last few ever contain the actual error.
truncate_detail() {
	local text="$1" max=400
	if [ "${#text}" -gt "$max" ]; then
		printf '...%s' "${text: -$max}"
	else
		printf '%s' "$text"
	fi
}

RESULT_WRITTEN=0
write_result() {
	local status="$1" detail="$2"
	RESULT_WRITTEN=1
	printf '{"status":"%s","detail":"%s","target":"%s","finished_at":"%s"}\n' \
		"$(json_escape "$status")" "$(json_escape "$detail")" \
		"$(json_escape "${TARGET_IMAGE:-}")" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		>"$RESULT_FILE.tmp"
	mv "$RESULT_FILE.tmp" "$RESULT_FILE"
}

# set_pinned_image sets IVY_IMAGE=<value> in .env, replacing any existing line.
set_pinned_image() {
	local value="$1"
	if grep -q '^IVY_IMAGE=' "$ENV_FILE" 2>/dev/null; then
		sed -i.bak "s|^IVY_IMAGE=.*|IVY_IMAGE=$value|" "$ENV_FILE" && rm -f "$ENV_FILE.bak"
	else
		printf 'IVY_IMAGE=%s\n' "$value" >>"$ENV_FILE"
	fi
}

current_pinned_image() {
	# `|| true`: no IVY_IMAGE line yet is a normal first-update case, not a
	# failure — without it pipefail plus set -e would kill the script here.
	grep '^IVY_IMAGE=' "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2- || true
}

# rollback_container re-pins .env to the previous image and tries to get a
# running container back. Returns non-zero if the rollback itself also failed,
# so the caller can tell "update failed, old version still fine" apart from
# "update failed AND rollback failed — Ivy may be down right now".
rollback_container() {
	set_pinned_image "$PREVIOUS_IMAGE"
	if timeout 60 docker compose up -d --force-recreate --no-deps "$SERVICE" 2>&1 | tee "$CMD_LOG"; then
		return 0
	fi
	return 1
}

# flock -n: a second trigger arriving while an update is in flight (a stray
# timer tick right after a manual click) just no-ops instead of running twice.
exec 200>"$LOCK_FILE"
if ! flock -n 200; then
	echo "update already in progress, skipping this run"
	exit 0
fi

if [ ! -f "$REQUESTED_FILE" ]; then
	# The timer's backstop tick with nothing pending — normal, not an error.
	exit 0
fi

TARGET_IMAGE="$(cat "$REQUESTED_FILE")"
if [ -z "$TARGET_IMAGE" ]; then
	echo "update-signal/requested is empty, ignoring" >&2
	rm -f "$REQUESTED_FILE"
	exit 0
fi

# The value is always resolved server-side, never typed by a person, so a
# malformed one is a bug upstream; failing fast beats discovering it three steps
# later as an opaque docker compose error, or writing garbage into .env. The
# repository is pinned (update/update.go DefaultRepo) because the signal
# directory is world-writable for the container's bind mount: any local user can
# write this file, and this script runs docker as the deploy user.
if ! [[ "$TARGET_IMAGE" =~ ^ghcr\.io/autumnsgrove/ivy@sha256:[0-9a-f]{64}$ ]]; then
	write_result "failed" "update-signal/requested is not a ghcr.io/autumnsgrove/ivy digest reference, refusing to use it: $TARGET_IMAGE"
	rm -f "$REQUESTED_FILE"
	exit 1
fi

# From here on the request is ours, and it must not outlive this run on any exit,
# including a command that fails under `set -e`: ivy-update.path fires whenever
# the file exists, so a leftover would re-run the whole pull-and-recreate in a
# loop. If nothing wrote a result, say that the script died rather than leave the
# settings panel to wait on a result that never comes.
finish() {
	local status=$?
	if [ "$RESULT_WRITTEN" -eq 0 ]; then
		write_result "failed" "update.sh stopped early (exit $status); see: journalctl -u ivy-update.service" || true
	fi
	rm -f "$REQUESTED_FILE"
}
trap finish EXIT

PREVIOUS_IMAGE="$(current_pinned_image)"
echo "updating ivy: $PREVIOUS_IMAGE -> $TARGET_IMAGE"

# A previous run that hit a merge conflict could have left the checkout
# mid-merge, which would make every later run fail identically. MERGE_HEAD only
# exists before a merge commit is made, so aborting cannot discard committed work.
if [ -f "$INSTALL_DIR/.git/MERGE_HEAD" ]; then
	echo "found a leftover merge in progress from a previous run — aborting it" >&2
	git merge --abort || true
fi

# Sync the host checkout before touching the container: docker-compose.yml,
# ivy.yaml templates and this script all live here, so the image alone is not
# the whole deployment. Bounded so a stalled remote cannot hold the lock forever.
if ! timeout 60 git pull --no-rebase origin main 2>&1 | tee "$CMD_LOG"; then
	if [ -f "$INSTALL_DIR/.git/MERGE_HEAD" ]; then
		git merge --abort || true
	fi
	write_result "failed" "git pull origin main failed: $(truncate_detail "$(cat "$CMD_LOG")")"
	rm -f "$REQUESTED_FILE"
	exit 1
fi

# Re-sync the watcher's own systemd unit files from the checkout git pull just
# updated, through the hash-pinned root wrapper (see watcher-sync-verify.sh for
# why a malicious commit must not be able to get itself root-executed). Best
# effort and non-fatal: a failure just means the watcher keeps its current units.
timeout 60 sudo /etc/ivy/watcher-sync-verify.sh "$INSTALL_DIR" "$(whoami)" 2>&1 | tee "$CMD_LOG" || \
	echo "watcher unit re-sync failed (non-fatal, continuing update): $(truncate_detail "$(cat "$CMD_LOG")")" >&2

set_pinned_image "$TARGET_IMAGE"

# Scoped to the ivy service so an update can never disturb another compose
# service. Bounded so a stalled pull cannot block updates forever.
if ! timeout 300 docker compose pull "$SERVICE" 2>&1 | tee "$CMD_LOG"; then
	write_result "failed" "docker compose pull failed: $(truncate_detail "$(cat "$CMD_LOG")")"
	# Pull failing means the running container was never touched, so just
	# re-pin .env to the previous image; no recreate needed.
	set_pinned_image "$PREVIOUS_IMAGE"
	rm -f "$REQUESTED_FILE"
	exit 1
fi

# --force-recreate: without it, `up -d` is a no-op when the desired image
# already matches what is running, so a "restart" would never actually cycle the
# container.
if ! timeout 60 docker compose up -d --force-recreate --no-deps "$SERVICE" 2>&1 | tee "$CMD_LOG"; then
	write_result "failed" "docker compose up failed: $(truncate_detail "$(cat "$CMD_LOG")")"
	if ! rollback_container; then
		write_result "failed" "update failed AND rollback to the previous image also failed — Ivy may be down right now: $(truncate_detail "$(cat "$CMD_LOG")")"
	fi
	rm -f "$REQUESTED_FILE"
	exit 1
fi

# `docker compose up -d` returning success only means the container was told to
# start, not that it is serving. The image's own HEALTHCHECK is what confirms
# that, so poll it: a bad image (a config bug, a broken migration, a crash loop)
# must not report "ok" while the service is unusable. The first start after an
# update can run migrations over a large mirror on a slow board, and the check
# reports `starting` until it first passes, so allow five minutes: a slow start
# is not a failed one, and a rollback throws a good update away.
CONTAINER_ID="$(docker compose ps -q "$SERVICE")"
HEALTH="unknown"
for _ in $(seq 1 100); do
	HEALTH="$(docker inspect --format '{{.State.Health.Status}}' "$CONTAINER_ID" 2>/dev/null || echo "unknown")"
	if [ "$HEALTH" = "healthy" ] || [ "$HEALTH" = "unhealthy" ]; then
		break
	fi
	sleep 3
done

if [ "$HEALTH" != "healthy" ]; then
	# The container's own recent logs, not the up command's, explain an
	# unhealthy status (a crash on boot, a config parse error, ...).
	FAIL_LOG="$(docker compose logs --no-color --tail=30 "$SERVICE" 2>&1 || true)"
	write_result "failed" "new container started but never reported healthy (status: $HEALTH): $(truncate_detail "$FAIL_LOG")"
	if ! rollback_container; then
		write_result "failed" "new image failed its healthcheck AND rollback also failed — Ivy may be down right now: $(truncate_detail "$FAIL_LOG")"
	fi
	rm -f "$REQUESTED_FILE"
	exit 1
fi

write_result "ok" "updated to $TARGET_IMAGE"
rm -f "$REQUESTED_FILE"
