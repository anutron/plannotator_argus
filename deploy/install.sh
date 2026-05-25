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
mkdir -p "$HOME/.local/bin"

# Install the hook wrapper so ExitPlanMode / other plannotator hook callers
# can route through the daemon (when reachable) and fall back to direct
# plannotator otherwise.
HOOK_SRC="$(cd "$(dirname "$0")" && pwd)/plannotator-hook.sh"
HOOK_DST="$HOME/.local/bin/plannotator-hook"
if [[ -f "$HOOK_SRC" ]]; then
    cp "$HOOK_SRC" "$HOOK_DST"
    chmod 0755 "$HOOK_DST"
    echo "installed $HOOK_DST"
fi

# Install the PreToolUse(Bash) guard that forces the MCP path for plannotator
# verb invocations inside argus task sandboxes. Wire it into Claude Code via
# ~/.claude/settings.json — see README for the stanza.
GUARD_SRC="$(cd "$(dirname "$0")" && pwd)/plannotator-bash-guard.sh"
GUARD_DST="$HOME/.local/bin/plannotator-bash-guard"
if [[ -f "$GUARD_SRC" ]]; then
    cp "$GUARD_SRC" "$GUARD_DST"
    chmod 0755 "$GUARD_DST"
    echo "installed $GUARD_DST"
fi

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
