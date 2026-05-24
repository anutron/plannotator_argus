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

# Start the daemon
plannotator-argus start --foreground
```

The daemon registers five MCP tools (`plannotator_annotate`, `plannotator_review`, `plannotator_setup_goal`, `plannotator_last`, `plannotator_session_result`) and serves a `POST /hook` HTTP endpoint for ExitPlanMode hook integration.

## Design

See `openspec/changes/plannotator-argus-plugin/design.md`.
