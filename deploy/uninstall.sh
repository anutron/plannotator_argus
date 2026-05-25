#!/bin/bash
# Uninstall the plannotator-argus LaunchAgent.
set -euo pipefail

LABEL="com.anutron.plannotator-argus"
PLIST_DST="$HOME/Library/LaunchAgents/${LABEL}.plist"
DOMAIN="gui/$(id -u)"
SERVICE_TARGET="${DOMAIN}/${LABEL}"

if launchctl print "$SERVICE_TARGET" >/dev/null 2>&1; then
    echo "stopping $LABEL..."
    launchctl bootout "$SERVICE_TARGET" || true
fi

if [[ -f "$PLIST_DST" ]]; then
    rm -f "$PLIST_DST"
    echo "removed $PLIST_DST"
fi

HOOK_DST="$HOME/.local/bin/plannotator-hook"
if [[ -f "$HOOK_DST" ]]; then
    rm -f "$HOOK_DST"
    echo "removed $HOOK_DST"
fi

GUARD_DST="$HOME/.local/bin/plannotator-bash-guard"
if [[ -f "$GUARD_DST" ]]; then
    rm -f "$GUARD_DST"
    echo "removed $GUARD_DST"
fi

echo "done."
