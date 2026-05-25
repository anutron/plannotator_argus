#!/bin/bash
# plannotator-hook — Claude Code stop-hook adapter for Plannotator.
#
# Routes the hook payload through the plannotator-argus daemon when reachable
# (required inside argus task sandboxes where invoking `plannotator` directly
# would EPERM on the session-file write), and falls back to invoking the
# `plannotator` binary directly when the daemon is not up.
#
# Behaviour is idempotent across environments:
#   - inside argus sandbox        → daemon path wins (direct would fail anyway)
#   - outside argus, daemon up    → daemon path wins (one extra localhost hop)
#   - outside argus, daemon down  → direct `plannotator` (today's behaviour)
#
# Configurable via environment:
#   PLANNOTATOR_HOOK_TOKEN  path to the daemon's bearer-token file
#                           (default: ~/.plannotator/argus-plugin-token)
#   PLANNOTATOR_HOOK_URL    daemon endpoint
#                           (default: http://127.0.0.1:7745/hook)
#   PLANNOTATOR_HOOK_TIMEOUT  curl --connect-timeout in seconds
#                           (default: 1)

set -uo pipefail

TOKEN_FILE="${PLANNOTATOR_HOOK_TOKEN:-$HOME/.plannotator/argus-plugin-token}"
DAEMON_URL="${PLANNOTATOR_HOOK_URL:-http://127.0.0.1:7745/hook}"
CONNECT_TIMEOUT="${PLANNOTATOR_HOOK_TIMEOUT:-1}"

# Slurp stdin once so we can fall back without losing the payload.
PAYLOAD=$(cat)

# Try the daemon path.
if [[ -r "$TOKEN_FILE" ]]; then
    TOKEN=$(head -n1 "$TOKEN_FILE" | tr -d '[:space:]')
    if [[ -n "$TOKEN" ]]; then
        if printf '%s' "$PAYLOAD" | curl -sS \
                --connect-timeout "$CONNECT_TIMEOUT" \
                -H "Authorization: Bearer $TOKEN" \
                --data-binary @- \
                "$DAEMON_URL"; then
            exit 0
        fi
    fi
fi

# Daemon unreachable, token missing, or token empty. Fall back to direct
# plannotator if it's on the path.
if command -v plannotator >/dev/null 2>&1; then
    printf '%s' "$PAYLOAD" | plannotator
    exit $?
fi

echo "plannotator-hook: daemon at $DAEMON_URL unreachable and 'plannotator' not on \$PATH" >&2
exit 1
