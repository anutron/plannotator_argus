## Why

The daemon's `ArgusBaseURL` is captured once at startup (`internal/config/config.go:43`, default `http://127.0.0.1:7743`) and never re-resolved. The heartbeat loop in `internal/mcp/registrar.go:125` swallows registration failures as `slog.Warn` and keeps reposting to the dead URL forever. If argus restarts on a different port (or even on the same port after a window long enough to flush its in-memory tool table), plannotator-argus stays connected to nothing: its five MCP tools silently disappear from argus's view, callbacks stop arriving, and the only fix today is a manual `plannotator-argus stop && start`.

The recovery shape that hera spec'd for itself (port-scan rescue, link state machine, in-flight tool-call gate) is overkill here. Plannotator-argus already discards all in-flight session state on restart (existing spec, "Session lifecycle and garbage collection" → "Restart drops in-flight sessions"), has no dependents waiting on its tools, and runs under a launchd plist that already sets `KeepAlive.SuccessfulExit=false` with a 60-second throttle. The cheapest viable recovery is "die when you notice argus is gone, let launchd bring you back."

This change adds two thin behaviors: discover argus's plugin-API base URL at startup (via argus's `Daemon.Ports` RPC when available, falling back to env override and the hardcoded default) and treat a sustained heartbeat failure as fatal so launchd restarts the daemon onto a freshly discovered URL.

## What Changes

- `internal/config/config.go` gains a discovery step: when `PLANNOTATOR_ARGUS_BASE_URL` is unset, attempt argus's `Daemon.Ports` discovery before defaulting to `http://127.0.0.1:7743`. When the env var is set, it wins unconditionally (explicit operator override).
- `internal/argus/client.go` (or a sibling) gains a `Discover` helper that speaks argus's `Daemon.Ports` protocol and returns the plugin-API URL.
- `internal/mcp/registrar.go` classifies heartbeat failures. A single transport-level failure (connection refused, DNS, request timeout) schedules a fast retry in 30 seconds instead of waiting the full heartbeat interval. Two consecutive transport failures, or any HTTP 401 from argus, propagate a fatal error out of the heartbeat loop. HTTP 5xx and other non-401 status codes stay as warnings and do not trip the fatal path.
- `internal/daemon/run.go` wires the fatal-error channel to context cancellation so `Run` exits non-zero on link loss, letting launchd's `KeepAlive` restart the daemon.
- `cmd/plannotator-argus/start.go` propagates the non-zero exit code.
- No changes to the MCP tool surface, session store, hook endpoint, or authentication paths.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `plannotator-argus-plugin`: adds two new requirements: "Argus base URL discovery" (covering env override, `Daemon.Ports`, fallback) and "Argus link liveness detection" (covering fast retry on first transport failure, fatal exit on sustained failure, fatal exit on 401, and pass-through behavior for transient HTTP errors).

## Impact

- **New code**: roughly 100 LOC across `internal/argus/discover.go`, the `Registrar` failure classifier, and the `Daemon.Run` exit-on-fatal wiring. Plus unit tests for each.
- **Behavioral change**: the daemon now exits non-zero on sustained heartbeat failure where it previously logged warnings forever. Launchd handles the restart (60-second throttle already configured at `deploy/com.anutron.plannotator-argus.plist:38`).
- **Operator experience**: after `argus restart` the operator no longer needs to manually restart plannotator-argus. Expected outage: one heartbeat interval plus the 30-second fast retry plus the 60-second launchd throttle, bounded under two minutes.
- **No effect on in-flight sessions**: any session running across an argus restart was already going to be lost by the existing "restart drops in-flight sessions" contract. This change does not regress that bound.
- **Dependency**: relies on argus exposing its plugin-API port via a `Daemon.Ports` RPC (argus PR #630). When that RPC is unavailable (older argus build, port-scan dead end), the env var and hardcoded fallback still work, so this change does not hard-block on PR #630 landing.
