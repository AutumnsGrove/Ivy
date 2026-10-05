#!/usr/bin/env bash
#
# Root-side gatekeeper for sync-units.sh. This wrapper — not sync-units.sh
# itself — is what install.sh's sudoers rule grants NOPASSWD access to, and it
# is copied to a fixed, root-owned path outside the git checkout
# (/etc/ivy/watcher-sync-verify.sh) that update.sh's automated git-pull flow
# never touches.
#
# Why this exists: sync-units.sh runs as root. If the sudoers rule pointed at
# its path inside the checkout, ANY commit that reached main — including a
# malicious one — would get root-executed on every host the next time
# `ivy update` ran, with no human review. The fix: run sync-units.sh only if its
# current SHA-256 still matches the hash install.sh pinned the last time a human
# ran it. A changed script changes the hash, so this refuses and the automated
# flow degrades to "the watcher units stay as they are" instead of "run whatever
# main contains as root". Moving the pin forward needs a human to review the
# diff and re-run install.sh.
set -euo pipefail

INSTALL_DIR="$1"
SCRIPT="$INSTALL_DIR/compose/watcher/sync-units.sh"
PINNED_HASH_FILE="/etc/ivy/watcher-sync.sha256"

if [ ! -f "$SCRIPT" ]; then
	echo "sync-units.sh not found at $SCRIPT — nothing to verify or run" >&2
	exit 1
fi
if [ ! -f "$PINNED_HASH_FILE" ]; then
	echo "no pinned hash at $PINNED_HASH_FILE — run install.sh to approve sync-units.sh first" >&2
	exit 1
fi

actual_hash="$(sha256sum "$SCRIPT" | awk '{print $1}')"
pinned_hash="$(cat "$PINNED_HASH_FILE")"

if [ "$actual_hash" != "$pinned_hash" ]; then
	echo "compose/watcher/sync-units.sh has changed since it was last approved" >&2
	echo "by install.sh (pinned: $pinned_hash, current: $actual_hash) — refusing" >&2
	echo "to run it as root. Review the change, then re-run install.sh." >&2
	exit 1
fi

# $@ still holds this wrapper's own args (INSTALL_DIR, DEPLOY_USER), which are
# exactly what sync-units.sh expects, so they pass straight through.
exec "$SCRIPT" "$@"
