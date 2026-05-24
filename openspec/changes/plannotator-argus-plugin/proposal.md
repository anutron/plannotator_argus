## Why

Plannotator is unusable from inside argus task sandboxes because writes to `~/.plannotator/sessions/<pid>.json` are EPERM-blocked and the browser handshake fails as a result. This breaks Aaron's primary review workflow — the ExitPlanMode stop hook that drives plan annotation — and every Plannotator-based skill (`/plannotator-annotate`, `/plannotator-review`, `/plannotator-setup-goal`, `/plannotator-last`). Wrapping Plannotator as an argus plugin moves the actual work outside the sandbox while letting sandboxed Claude drive it via MCP.

## What Changes

- New Go daemon `plannotator-argus`, scaffolded in this repo, registered with argus as a `plannotator`-scope plugin.
- Five MCP tools registered on startup, all under the `plannotator_` prefix:
  - `plannotator_annotate(cwd, path)` — wraps `plannotator annotate`
  - `plannotator_review(cwd, [pr_url], [git])` — wraps `plannotator review`
  - `plannotator_setup_goal(cwd, mode, bundle_path)` — wraps `plannotator setup-goal`
  - `plannotator_last(cwd)` — wraps `plannotator last`
  - `plannotator_session_result(cwd, session_id, [wait_seconds])` — polls for completion (handles argus's 30s callback ceiling via long-poll)
- New `POST /hook` HTTP endpoint on the daemon (non-MCP) for ExitPlanMode hook integration. Authenticated with a long-lived token at `~/.plannotator/argus-plugin-token`.
- Per-process random `auth_header` secret for MCP callbacks (mirrors Ludwig's pattern).
- 5-minute MCP tool re-registration heartbeat to stay alive in argus's 10-minute idle sweep.
- CLI verbs: `plannotator-argus start [--foreground]`, `stop`, `status`.

## Capabilities

### New Capabilities

- `plannotator-argus-plugin`: the argus-plugin daemon that exposes Plannotator to sandboxed argus tasks via MCP tools and a hook-mode HTTP endpoint.

### Modified Capabilities

(none — this repo has no existing specs)

## Impact

- **New code**: `cmd/plannotator-argus/`, `internal/{argus,config,daemon,hook,mcp,plannotator}/`, `Makefile`, `go.mod`, `README.md`.
- **No changes to Plannotator's binary or installer in this change.** The skill-file and hook-config rewrites that route Claude Code through the daemon are owned by Plannotator's installer and ship separately.
- **New external dependency**: argus daemon must be running on `127.0.0.1:7743` with a valid `plannotator`-scope token minted via `argus token mint --scope plannotator`.
- **Filesystem touch points**:
  - `~/.plannotator/argus-api-token` (scope token, written manually by operator)
  - `~/.plannotator/argus-plugin-token` (hook auth token, written by daemon on first startup, mode 0600)
  - `~/.plannotator/argus-plugin.pid` (PID file, written by daemon)
- **HTTP listener** on `127.0.0.1:7745` (configurable). No external network egress.
