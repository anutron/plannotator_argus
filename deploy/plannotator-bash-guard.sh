#!/bin/bash
# plannotator-bash-guard — Claude Code PreToolUse(Bash) hook.
#
# Force the MCP path for Plannotator inside argus task sandboxes.
#
# Inside an argus task worktree, invoking `plannotator <verb>` directly EPERMs
# on the session-file write to ~/.plannotator/sessions/<pid>.json. The upstream
# Plannotator skills still tell Claude to call `Bash(plannotator annotate ...)`
# because they're shared across non-sandboxed environments. We can't edit those
# skill files — the Plannotator installer refreshes them.
#
# So we install this guard as a PreToolUse hook on Bash. When Claude runs a
# direct `plannotator annotate|review|last|setup-goal` from inside an argus
# worktree, the guard denies the call and tells Claude exactly which MCP tool
# to use instead. Outside an argus worktree the guard is a silent no-op.
#
# Hook contract (Claude Code PreToolUse):
#   - stdin = JSON with at least { "tool_input": { "command": "..." } }
#   - allow = exit 0, empty stdout/stderr
#   - deny  = exit 2 with JSON on stderr:
#       {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"},
#        "systemMessage":"<reason shown to Claude>"}

set -uo pipefail

# Read the entire hook payload (don't fail on parse — fall through to allow).
PAYLOAD=$(cat 2>/dev/null || true)
COMMAND=$(printf '%s' "$PAYLOAD" | jq -r '.tool_input.command // empty' 2>/dev/null || true)

# No command? Allow.
if [[ -z "$COMMAND" ]]; then
    exit 0
fi

# Match `plannotator <verb>` with `<verb>` in {annotate,review,last,setup-goal}.
# Anchored so it doesn't match `plannotator-argus`, `plannotator-hook`, or
# `plannotator --version`. Allowed prefixes are start-of-string, whitespace, or
# `/` (for absolute paths like `/usr/local/bin/plannotator annotate foo`).
VERB_RE='(^|[[:space:]/])plannotator[[:space:]]+(annotate|review|last|setup-goal)([[:space:]]|$)'
if [[ ! "$COMMAND" =~ $VERB_RE ]]; then
    exit 0
fi
VERB="${BASH_REMATCH[2]}"

# Only enforce inside an argus task worktree. Outside, the direct path works.
ARGUS_ROOT="${HOME}/.argus/worktrees/"
if [[ "$PWD" != "$ARGUS_ROOT"* ]]; then
    exit 0
fi

# Build a verb-specific redirect message so Claude can swap with no thinking.
case "$VERB" in
    annotate)
        REDIRECT='mcp__argus__plannotator_annotate
  Args: cwd=$PWD, path=<the path/URL you passed to `plannotator annotate`>'
        ;;
    review)
        REDIRECT='mcp__argus__plannotator_review
  Args: cwd=$PWD, plus optional pr_url=<url> and/or git=true if you passed `--git`'
        ;;
    last)
        REDIRECT='mcp__argus__plannotator_last
  Args: cwd=$PWD'
        ;;
    setup-goal)
        REDIRECT='mcp__argus__plannotator_setup_goal
  Args: cwd=$PWD, mode=<interview|facts>, bundle_path=<path to bundle.json>'
        ;;
esac

REASON="Direct invocation of \`plannotator $VERB\` is blocked inside argus task sandboxes — the session file write to ~/.plannotator/sessions/<pid>.json EPERMs. Use the MCP tool instead:

$REDIRECT

The tool returns {session_id, status: \"pending\"} immediately. Poll mcp__argus__plannotator_session_result(cwd=\$PWD, session_id=<id>) until status=\"complete\" to get the result."

# jq builds the deny JSON so newlines and quotes in REASON are escaped safely.
jq -nc --arg reason "$REASON" '
  {
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "deny"
    },
    systemMessage: $reason
  }
' >&2
exit 2
