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

## Forcing the MCP path for plannotator inside argus

The upstream Plannotator skills (`/plannotator-annotate`, `/plannotator-review`, `/plannotator-last`, `/plannotator-setup-goal`) instruct Claude to call `Bash(plannotator <verb> ...)` directly. Inside an argus task sandbox that EPERMs on the session-file write — the daemon's MCP tools are the only working path. The skills can't be patched in place because the Plannotator installer refreshes them.

`./deploy/install.sh` ships a second helper, `~/.local/bin/plannotator-bash-guard`, intended as a `PreToolUse(Bash)` hook. It inspects each Bash invocation, and when (a) the command directly runs `plannotator annotate|review|last|setup-goal` AND (b) `$PWD` is under `~/.argus/worktrees/`, it denies the call with a verb-specific message telling Claude exactly which `mcp__argus__plannotator_*` tool to use instead. Anywhere outside an argus worktree the guard is a silent no-op, so the same hook is safe on the host shell.

Wire it into `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/Users/aaron/.local/bin/plannotator-bash-guard",
            "timeout": 5
          }
        ]
      }
    ]
  }
}
```

Same rationale as the ExitPlanMode hook above: `~/.claude/settings.json` is operator-owned and survives Plannotator installer refreshes. The guard does not match `plannotator --version`, `plannotator-argus`, or `plannotator-hook` — it only fires on the four verb invocations the skills generate.

## Argus reconnection

The daemon resolves argus's plugin-API base URL at startup with a deterministic precedence:

1. `PLANNOTATOR_ARGUS_BASE_URL` env var, if set, wins unconditionally.
2. Otherwise argus's `Daemon.Ports` RPC (JSON-RPC over `~/.argus/daemon.sock`) is queried with a 500 ms timeout.
3. Otherwise the hardcoded fallback `http://127.0.0.1:7743` is used.

Discovery is best-effort: any failure (socket missing, RPC error, timeout, malformed response) falls through to the next step without logging at error level. The daemon never refuses to start because discovery returned nothing.

After startup, the heartbeat loop (default 5-minute interval, configurable via `PLANNOTATOR_MCP_HEARTBEAT`) classifies each registration round:

- **HTTP 200/201** – resets failure tracking.
- **HTTP 401** – fatal immediately; the scope token has been revoked or rotated.
- **HTTP 5xx or non-401 4xx** – logged as a warning; argus is reachable but responding poorly, so we keep heartbeating on the normal cadence.
- **Transport failure** (connection refused, DNS, timeout, EOF) – a single fast retry is scheduled 30 seconds later. If that retry also fails, the daemon exits non-zero.

On fatal exit, launchd's `KeepAlive.SuccessfulExit=false` (with `ThrottleInterval=60`) restarts the daemon, which re-runs discovery and picks up argus's new plugin-API URL automatically. Worst-case outage after an `argus restart` is bounded by the heartbeat interval plus 30 s plus the launchd throttle (under two minutes with defaults).

To pin the daemon to a specific argus instance and skip discovery entirely, set `PLANNOTATOR_ARGUS_BASE_URL` in the launchd plist's `EnvironmentVariables`.

## Design

See `openspec/changes/plannotator-argus-plugin/design.md`.
