#!/usr/bin/env bash
#
# Re-syncs the watcher's own systemd unit files from this checkout into
# /etc/systemd/system/ whenever they have drifted, so a fix landed in
# compose/watcher/*.service|.path|.timer reaches an installed host through the
# normal `ivy update` flow instead of needing a fresh install.sh run.
#
# This runs as root (it writes into /etc/systemd/system and calls systemctl), so
# it is only ever invoked through the hash-pinned watcher-sync-verify.sh wrapper;
# see that script's header for why. Always called with sudo by update.sh.
set -euo pipefail

INSTALL_DIR="$1"
DEPLOY_USER="$2"
WATCHER_SRC="$INSTALL_DIR/compose/watcher"
UNIT_DIR="/etc/systemd/system"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

changed=()
# The same fixed unit list install.sh templates at install time, kept in sync by
# hand since both are short and rarely change.
for unit in ivy-update.service ivy-update.path ivy-update.timer; do
	sed -e "s|@INSTALL_DIR@|$INSTALL_DIR|g" -e "s|@USER@|$DEPLOY_USER|g" \
		"$WATCHER_SRC/$unit" >"$tmp_dir/$unit"
	if ! cmp -s "$tmp_dir/$unit" "$UNIT_DIR/$unit" 2>/dev/null; then
		cp "$tmp_dir/$unit" "$UNIT_DIR/$unit"
		changed+=("$unit")
	fi
done

if [ "${#changed[@]}" -eq 0 ]; then
	exit 0
fi

echo "watcher unit files changed, re-syncing: ${changed[*]}"
systemctl daemon-reload

# .service units are oneshots, so daemon-reload is enough: the next trigger
# reads the updated file off disk. .path/.timer units stay active, so they need
# an explicit restart to pick up a changed condition; neither holds any
# in-progress state of its own, so restarting is safe.
for unit in "${changed[@]}"; do
	case "$unit" in
	*.path | *.timer)
		systemctl restart "$unit"
		;;
	esac
done
