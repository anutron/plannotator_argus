#!/bin/bash
# Install the plannotator-argus LaunchAgent so the daemon starts at login
# and restarts on crash. Idempotent: a previous install is unloaded first.
#
# Usage:
#   ./deploy/install.sh             # install + load
#   ./deploy/install.sh --reload    # unload (if loaded), reinstall, load again
set -euo pipefail

LABEL="com.anutron.plannotator-argus"
PLIST_SRC="$(cd "$(dirname "$0")" && pwd)/${LABEL}.plist"
PLIST_DST="$HOME/Library/LaunchAgents/${LABEL}.plist"
DOMAIN="gui/$(id -u)"
SERVICE_TARGET="${DOMAIN}/${LABEL}"

if [[ ! -f "$PLIST_SRC" ]]; then
    echo "error: plist source not found at $PLIST_SRC" >&2
    exit 1
fi

if [[ ! -x "$HOME/.local/bin/plannotator-argus" ]]; then
    echo "error: $HOME/.local/bin/plannotator-argus not found or not executable" >&2
    echo "       run \`make install-dev\` from the repo root first" >&2
    exit 1
fi

mkdir -p "$HOME/Library/LaunchAgents"
mkdir -p "$HOME/.plannotator"

# If already loaded, unload first so we pick up plist changes.
if launchctl print "$SERVICE_TARGET" >/dev/null 2>&1; then
    echo "unloading existing $LABEL..."
    launchctl bootout "$SERVICE_TARGET" || true
fi

cp "$PLIST_SRC" "$PLIST_DST"
chmod 0644 "$PLIST_DST"

echo "loading $LABEL from $PLIST_DST..."
launchctl bootstrap "$DOMAIN" "$PLIST_DST"
launchctl enable "$SERVICE_TARGET"

echo
echo "installed. Logs: ~/.plannotator/argus-plugin.log"
echo "check status:    launchctl print $SERVICE_TARGET | head -20"
echo "stop service:    ./deploy/uninstall.sh"
