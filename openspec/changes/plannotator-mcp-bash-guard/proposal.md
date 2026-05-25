## Why

The daemon and its five MCP tools work, but Claude in a fresh argus task still calls `Bash(plannotator annotate ...)` directly when following the upstream Plannotator skills (`/plannotator-annotate`, `/plannotator-review`, `/plannotator-last`, `/plannotator-setup-goal`). That EPERMs on `~/.plannotator/sessions/<pid>.json` inside the sandbox and breaks the flow before the MCP tools ever get used. The skill files can't be patched in place — the upstream Plannotator installer (`curl -fsSL https://plannotator.ai/install.sh | bash`) overwrites them on every update.

The fix has to live somewhere the installer doesn't touch: `~/.claude/settings.json` and `~/.local/bin/`. Specifically, a `PreToolUse(Bash)` hook that intercepts direct `plannotator <verb>` invocations inside argus worktrees and redirects Claude to the corresponding `mcp__argus__plannotator_*` tool. Outside argus worktrees the hook is a no-op, so it's safe to install globally.

## What Changes

- New helper script `~/.local/bin/plannotator-bash-guard` — a `PreToolUse(Bash)` hook that reads `tool_input.command`, matches `plannotator annotate|review|last|setup-goal`, checks `$PWD` against `~/.argus/worktrees/`, and either denies with a verb-specific redirect message (exit 2 with `hookSpecificOutput.permissionDecision = "deny"`) or passes through (exit 0).
- `deploy/install.sh` and `deploy/uninstall.sh` copy/remove the guard alongside the existing `plannotator-hook` wrapper.
- README documents the `~/.claude/settings.json` `PreToolUse → Bash` stanza that wires the guard into Claude Code.
- The daemon's MCP tools, hook endpoint, and authentication surfaces are unchanged.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `plannotator-argus-plugin`: adds a new "Bash invocation guard" requirement covering the PreToolUse hook's intercept-and-redirect behavior inside argus sandboxes and its silent pass-through behavior elsewhere.

## Impact

- **New code**: `deploy/plannotator-bash-guard.sh` (~80 lines, bash + jq).
- **Install footprint**: one additional file at `~/.local/bin/plannotator-bash-guard`. The install scripts already touch `~/.local/bin/plannotator-hook` so the pattern is established.
- **Configuration**: one new stanza in `~/.claude/settings.json`. Operator-owned, survives Plannotator installer refreshes.
- **No daemon changes**, no Go code changes, no MCP surface changes.
- **No effect outside argus**: the guard is a no-op when `$PWD` is not under `~/.argus/worktrees/`, so installing the hook globally doesn't break direct `plannotator` use on the host shell.
