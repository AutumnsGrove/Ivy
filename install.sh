#!/usr/bin/env bash
#
# Installs Ivy's host-side update watcher: the systemd path/timer units, the
# hash-pinned root wrapper that keeps unit re-sync from becoming a
# root-execution path for an unreviewed commit, and the sudoers rule that allows
# exactly that one wrapper. Run it once from the checkout, with sudo:
#
#   sudo ./install.sh
#
# Re-run it after reviewing a change to compose/watcher/sync-units.sh, to move
# the pinned hash forward. The container image itself is pulled by docker
# compose; nothing here builds or compiles anything.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
	echo "install.sh must run as root (sudo ./install.sh)" >&2
	exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="${IVY_INSTALL_DIR:-$SCRIPT_DIR}"
DEPLOY_USER="${SUDO_USER:-${IVY_DEPLOY_USER:-}}"
if [ -z "$DEPLOY_USER" ] || [ "$DEPLOY_USER" = "root" ]; then
	echo "could not determine the deploy user; re-run with sudo from your own account," >&2
	echo "or set IVY_DEPLOY_USER=<user>." >&2
	exit 1
fi

UNIT_DIR="/etc/systemd/system"
IVY_ETC="/etc/ivy"
WATCHER_SRC="$INSTALL_DIR/compose/watcher"
SUDOERS_FILE="/etc/sudoers.d/ivy-watcher"

echo "installing the Ivy update watcher"
echo "  install dir: $INSTALL_DIR"
echo "  deploy user: $DEPLOY_USER"

# --- filesystem the container and watcher share --------------------------------
# Ensure the checkout we are actually installing exists.
if [ ! -d "$WATCHER_SRC" ]; then
	echo "no compose/watcher under $INSTALL_DIR — is this the Ivy checkout?" >&2
	exit 1
fi

# The container writes the signal file through a bind mount, so the host
# directory must be writable regardless of the container's uid inside it.
mkdir -p "$INSTALL_DIR/update-signal"
chmod 777 "$INSTALL_DIR/update-signal"

# The data directory (config, both databases, blobs, backups) is a bind mount
# too. The container runs as the deploy user's uid, so give them ownership.
mkdir -p "$INSTALL_DIR/data"
chown "$DEPLOY_USER" "$INSTALL_DIR/data"
chmod 700 "$INSTALL_DIR/data"

# Record the uid/gid docker-compose.yml uses for the container, and the image
# the watcher pins, in the compose .env. Both are the deploy user's.
ENV_FILE="$INSTALL_DIR/.env"
ensure_env_key() {
	local key="$1" value="$2"
	if grep -q "^${key}=" "$ENV_FILE" 2>/dev/null; then
		return
	fi
	printf '%s=%s\n' "$key" "$value" >>"$ENV_FILE"
}
touch "$ENV_FILE"
ensure_env_key IVY_UID "$(id -u "$DEPLOY_USER")"
ensure_env_key IVY_GID "$(id -g "$DEPLOY_USER")"
ensure_env_key IVY_IMAGE "ghcr.io/autumnsgrove/ivy:latest"
chown "$DEPLOY_USER" "$ENV_FILE"

# --- systemd units -------------------------------------------------------------
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
for unit in ivy-update.service ivy-update.path ivy-update.timer; do
	sed -e "s|@INSTALL_DIR@|$INSTALL_DIR|g" -e "s|@USER@|$DEPLOY_USER|g" \
		"$WATCHER_SRC/$unit" >"$tmp_dir/$unit"
	install -m 0644 "$tmp_dir/$unit" "$UNIT_DIR/$unit"
done

# --- the hash-pinned root wrapper ----------------------------------------------
install -d -m 0755 "$IVY_ETC"
install -m 0755 "$WATCHER_SRC/watcher-sync-verify.sh" "$IVY_ETC/watcher-sync-verify.sh"
sha256sum "$WATCHER_SRC/sync-units.sh" | awk '{print $1}' >"$IVY_ETC/watcher-sync.sha256"
chmod 0644 "$IVY_ETC/watcher-sync.sha256"

# --- sudoers -------------------------------------------------------------------
# The deploy user may run exactly the wrapper, nothing else. visudo -c validates
# the fragment before it lands in /etc/sudoers.d, so a typo cannot lock sudo out.
cat >"$tmp_dir/ivy-watcher" <<EOF
$DEPLOY_USER ALL=(root) NOPASSWD: $IVY_ETC/watcher-sync-verify.sh
EOF
if command -v visudo >/dev/null 2>&1; then
	visudo -c -f "$tmp_dir/ivy-watcher" >/dev/null
	install -m 0440 "$tmp_dir/ivy-watcher" "$SUDOERS_FILE"
else
	echo "visudo not found; skipping the watcher unit re-sync sudoers rule." >&2
	echo "The update flow still works; unit changes just will not auto-apply." >&2
fi

# --- enable --------------------------------------------------------------------
systemctl daemon-reload
systemctl enable --now ivy-update.path ivy-update.timer

echo "done. Ivy's Update button (or 'ivy update' in the container) now works."
