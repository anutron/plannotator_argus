# plannotator-argus

Argus plugin that wraps [Plannotator](https://github.com/anutron/plannotator_argus) so sandboxed argus tasks can drive annotation/review flows. The daemon runs outside any argus task sandbox, registers MCP tools with argus, and shells out to the user's existing `plannotator` binary on tool invocation.

## Why

Plannotator's local browser session needs to write to `~/.plannotator/sessions/<pid>.json`. Inside an argus task sandbox, that write is EPERM-blocked, breaking every Plannotator-based flow including the ExitPlanMode hook that drives plan review. This daemon moves the actual Plannotator process outside the sandbox while letting sandboxed Claude drive it via MCP.

## Install

```bash
make build
cp ./bin/plannotator-argus ~/.local/bin/

# One-time setup
argus token mint --scope plannotator > ~/.plannotator/argus-api-token
chmod 600 ~/.plannotator/argus-api-token
# The daemon parses either the full `argus token mint` output or a single
# bare token line — pipe the command's stdout verbatim.

# Start the daemon (foreground; Ctrl+C to stop)
plannotator-argus start --foreground

# OR install as a LaunchAgent (starts at login, restarts on crash)
./deploy/install.sh
```

The daemon registers five MCP tools (`plannotator_annotate`, `plannotator_review`, `plannotator_setup_goal`, `plannotator_last`, `plannotator_session_result`) and serves a `POST /hook` HTTP endpoint for ExitPlanMode hook integration. With the LaunchAgent installed, stdout/stderr land at `~/.plannotator/argus-plugin.log`.

## Hook wrapper

`./deploy/install.sh` also installs `~/.local/bin/plannotator-hook`, a small bash wrapper that routes Claude Code stop-hook payloads through the daemon when reachable and falls back to invoking `plannotator` directly when it isn't. The wrapper is environment-invariant — point your existing hook config at `plannotator-hook` instead of `plannotator` and ExitPlanMode behaves identically inside and outside argus task sandboxes. Overridable via `PLANNOTATOR_HOOK_URL`, `PLANNOTATOR_HOOK_TOKEN`, `PLANNOTATOR_HOOK_TIMEOUT`.

## Wiring the Claude Code ExitPlanMode hook

The upstream Plannotator installer (`curl -fsSL https://plannotator.ai/install.sh | bash`) only writes the Claude Code ExitPlanMode hook into the marketplace plugin path (`~/.claude/plugins/marketplaces/plannotator/apps/hook/hooks/hooks.json`) — and only when that file already exists, i.e. when you've installed Plannotator as a marketplace plugin. On a fresh machine with no marketplace plugin, no hook gets wired.

To get one hook config that works both in vanilla Claude Code and inside an argus task sandbox, add the hook directly to `~/.claude/settings.json` and point it at the wrapper:

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "ExitPlanMode",
        "hooks": [
          {
            "type": "command",
            "command": "/Users/aaron/.local/bin/plannotator-hook",
            "timeout": 345600
          }
        ]
      }
    ]
  }
}
```

Why this location:

- **Survives `install.sh` re-runs.** The upstream installer never touches `~/.claude/settings.json` for Claude Code — it only modifies `~/.codex/hooks.json` and `~/.gemini/settings.json`. Re-running the installer refreshes the binary, skills, and slash commands but leaves your hook config alone.
- **Works both sandboxed and non-sandboxed.** The wrapper tries the daemon first (required inside argus sandboxes), falls back to invoking `plannotator` directly when the daemon is down.
- **Avoid the marketplace plugin path.** If you ever install Plannotator as a marketplace plugin, it will write its own ExitPlanMode hook pointing at the bare `plannotator` binary, which will fire alongside the wrapper-routed hook in `settings.json`. Pick one mechanism, not both.

The upstream installer also wires a `PreToolUse: EnterPlanMode → plannotator improve-context` pre-hook. The daemon's `/hook` endpoint only handles the no-args ExitPlanMode flow, so the wrapper has nothing to do with `improve-context` — leave that pre-hook out unless and until the daemon grows an endpoint for it.

## Design

See `openspec/changes/plannotator-argus-plugin/design.md`.
