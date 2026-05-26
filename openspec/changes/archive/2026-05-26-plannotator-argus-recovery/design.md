## Context

Plannotator-argus is a thin plugin client. Its only interaction with argus is HTTP: POST tool registrations, periodic re-POST as a heartbeat, DELETE on shutdown. There is no socket connection to keep alive, no streaming subscription, no in-flight RPC mid-call. The base URL is captured at startup from `config.ArgusBaseURL` (default `http://127.0.0.1:7743`, env override `PLANNOTATOR_ARGUS_BASE_URL`) and never re-resolved.

Hera's spec (see user message in the parent thread) addresses a much larger surface: a coordinated worker fleet that needs to keep the argus link healthy mid-task. It introduces a link state machine, a `Daemon.Ports` socket RPC plus port-scan rescue, a watcher polling pid-file mtime and a socket ping at one-second cadence, force-reregister callbacks, and an MCP tool-call gate that returns `isError: true` during recovery.

This change deliberately does not replicate that shape. Plannotator-argus has three properties that make hera's complexity unwarranted:

- **No state worth preserving across restart.** The session store is in-memory and the existing spec explicitly says "Restart drops in-flight sessions." A daemon restart costs nothing beyond what argus restart already costs.
- **No fleet of dependents.** Plannotator-argus serves Claude tasks via argus's MCP layer. When argus is down, the tools 404 at argus's edge regardless of what plannotator-argus does. No worker is parked on a tool call expecting a response.
- **launchd already provides supervision.** `deploy/com.anutron.plannotator-argus.plist` sets `KeepAlive.SuccessfulExit=false` with a 60-second `ThrottleInterval`. A non-zero exit triggers automatic restart with a bounded retry rate.

The cheapest recovery that closes the silent-breakage gap is therefore: detect that argus is gone, exit non-zero, let launchd restart with a fresh discovered URL.

## Goals / Non-Goals

**Goals:**

- After argus restarts (on any port), plannotator-argus eventually reaches a healthy state without operator intervention, bounded by a worst-case outage of roughly two minutes.
- The discovery path prefers argus's `Daemon.Ports` RPC so a port change is invisible to the operator, with an env-var override and hardcoded fallback so older argus builds and overridden configurations still work.
- Heartbeat failure classification is conservative: a single transient blip does not bring the daemon down. Only a sustained failure (or an unmistakable signal like 401) triggers exit.
- Existing behavior is preserved when argus is healthy. The change adds discovery and a fatal path; it does not modify the steady-state register/heartbeat/unregister flow.

**Non-Goals:**

- **No mid-flight transparent recovery.** The daemon does not attempt to re-discover and re-register without restarting. Implementing that would require everything hera spec'd (watcher, mutex-protected baseURL setter, force-reregister callbacks, link state machine) for almost no win, because there is no in-flight state to preserve.
- **No MCP tool-call gating during the gap.** During the disconnect-to-restart window, argus has already lost the registrations, so callbacks cannot reach plannotator-argus regardless. No need for the daemon to return `isError: true` from anything; the calls do not arrive.
- **No port-scan rescue.** YAGNI. If `Daemon.Ports` is unavailable, the operator can set `PLANNOTATOR_ARGUS_BASE_URL` once.
- **No changes to the MCP tool surface, session store, hook endpoint, or authentication.** This change is strictly about base URL acquisition and link liveness.

## Decisions

### D1. Discovery order: env override → Daemon.Ports → hardcoded default

When the operator sets `PLANNOTATOR_ARGUS_BASE_URL`, that value wins unconditionally. This preserves today's escape hatch (used by tests, by operators pinning to a non-default port, and by anyone running plannotator-argus against a remote argus instance).

When the env var is unset, the daemon attempts `Daemon.Ports` discovery. If it succeeds, the returned URL is used. If it fails (RPC unavailable on the running argus build, socket missing, parse error), the daemon falls back to the hardcoded `http://127.0.0.1:7743` and continues startup. Startup never blocks on discovery returning a "no, really, try again" answer; either it succeeds, fails to one fallback, or the daemon proceeds with the default and surfaces health via the normal startup-registration path.

**Alternatives considered:**

- **Daemon.Ports always, env as escape hatch only.** Rejected because the env var is the lowest-friction override for development and CI scenarios; flipping the precedence would require operators to unset the env var to opt into discovery.
- **Port-scan as a third fallback.** Rejected. Hera's spec keeps it for symmetry with the watcher; for plannotator-argus it is dead code once `Daemon.Ports` ships and the env var is documented.

### D2. Liveness signal: failure classification in the heartbeat loop

Every heartbeat does the same work it does today (one re-POST per registered tool). The classification lives in the loop and reads each call's outcome:

- **HTTP 200 / 201** – success, reset the failure counter.
- **HTTP 401** – fatal. Token revoked or scope mismatch; restarting will not fix this, but exiting and surfacing the error in the launchd log is still better than logging warnings forever. The operator notices the restart-loop and re-mints the token. This matches today's startup behavior at `internal/argus/client.go:71`.
- **Transport-level failure** (connection refused, DNS, request timeout, EOF) – schedule a fast retry in 30 seconds. If the next attempt also fails the same way, propagate fatal. This is the "argus restarted somewhere else" signal.
- **HTTP 5xx, other 4xx, response parse error** – log warning, do not count toward the fast-retry threshold. Argus is reachable but having a moment; we keep heartbeating on the normal cadence.

The "two consecutive transport failures" threshold is deliberately small. One blip every 30 seconds, sustained, is a clear "argus is gone" signal; anything noisier than that argues for a longer threshold but produces a longer outage window. Two retries balance both directions.

**Alternatives considered:**

- **Single failure → fatal.** Rejected. A 200 ms network blip during a launchd reload or sleep/wake transition would otherwise bring the daemon down for no reason.
- **Exponential backoff.** Rejected. The fast-retry case is already bounded (30 seconds, single attempt), and an exponential schedule mostly delays the inevitable exit. Launchd's throttle is the right place for backoff, not the daemon.
- **HTTP 5xx counts toward fatal.** Rejected. A 5xx means argus is up and responding; throwing the daemon away on transient internal errors is overreaction.

### D3. Fatal signal: context cancellation, not panic

When the heartbeat loop classifies a failure as fatal, it sends the error onto a `chan error` exposed by the registrar. `Daemon.Run` selects on that channel alongside `ctx.Done()` and the OS signal channel; receiving a fatal error logs it, triggers the existing `Stop` path (so tool unregisters are still attempted, even though they will likely fail too), and returns an error from `Run`. The CLI propagates that to the process exit code.

This keeps cleanup orderly. The alternative (panic, or `os.Exit(1)` from the heartbeat goroutine) skips the GC stop, the session-wg drain, and the MCP server shutdown. Those finalizers run quickly today; running them on the way out costs nothing and keeps the shutdown path identical to SIGTERM.

**Alternatives considered:**

- **`os.Exit(1)` directly from the heartbeat loop.** Rejected for the cleanup reasons above, and because it bypasses the test seam (tests want to assert the error, not crash the test binary).
- **A dedicated `Health()` method on the registrar that `Daemon.Run` polls.** Rejected. The information lives in the heartbeat loop already; an extra polling layer adds latency and complexity for no win.

### D4. No watcher process

Hera spec'd a one-second poll on pid-file mtime and a socket ping. Plannotator-argus does not need this. The heartbeat is its watcher: every five minutes (configurable via `PLANNOTATOR_MCP_HEARTBEAT`) a real registration POST goes out and either succeeds or fails. The 30-second fast retry on first transport failure tightens the worst-case detection window to roughly the heartbeat interval plus 30 seconds.

If a tighter detection bound matters in the future (e.g., the operator wants sub-minute recovery), the right knob is to drop `PLANNOTATOR_MCP_HEARTBEAT` to one minute. No new watcher subsystem is needed.

### D5. Discovery protocol: defer to argus's implementation

This change does not lock in the exact wire protocol for `Daemon.Ports`. The argus PR (#630) defines it; this spec asserts only that plannotator-argus calls into argus's defined discovery surface, parses the returned plugin-API base URL, and uses it. The implementation lives in `internal/argus/discover.go` and is tested against a fake of argus's RPC.

If argus's discovery surface changes shape later, only `internal/argus/discover.go` and its tests change; the registrar, daemon, and CLI are insulated.

## Risks / Trade-offs

- **Restart loops if argus is permanently down.** Launchd's 60-second throttle bounds the loop to one start per minute. The logs will be noisy but not catastrophic. Aligned with hera's "fail fast at startup, retry in flight" choice from the parent message.
- **A revoked token now produces a launchd loop instead of a single startup failure.** Today, an invalid token fails startup and the daemon stays dead. With this change, a token that becomes invalid mid-life (extremely unlikely; tokens are operator-minted and long-lived) also produces a restart loop. The launchd log makes the cause visible. Acceptable trade because the recovery shape is the same as for any other fatal heartbeat error.
- **Discovery latency on startup.** A failed `Daemon.Ports` call (socket missing, RPC not implemented yet on older argus) adds a small startup delay. Bound the discovery attempt to a short timeout (suggested: 500 ms) so an unreachable RPC does not noticeably slow startup.
- **Test surface for the new failure-classifier branches.** The existing registrar tests do not exercise transport failures. The change adds a fake `argus.Client` (or `http.RoundTripper`) that can be programmed to fail with connection-refused/401/5xx on demand. This is mostly net-new test infrastructure but reuses Go's `httptest` patterns.

## Migration Plan

1. Implement `internal/argus/discover.go` with the `Daemon.Ports` RPC client and a unit test against a fake. Discovery returns `(baseURL, ok)` and never errors fatally.
2. Update `config.Default()` to call discovery when `PLANNOTATOR_ARGUS_BASE_URL` is unset. The env-var override path is preserved as a strict precedence.
3. Add the failure-classifier and fatal-channel plumbing to `internal/mcp/registrar.go`. Update `Registrar.Start` to return the fatal channel (or expose it via a `Fatal() <-chan error` accessor).
4. Update `internal/daemon/run.go` to select on the fatal channel in `Run`, log the error, and return it from `Run`.
5. Update `cmd/plannotator-argus/start.go` to propagate the non-zero exit code.
6. Smoke test on the host: start plannotator-argus, `argus stop`, observe heartbeat fast-retry, observe exit; `argus start` (on a different port if possible), observe launchd restart, observe plannotator-argus discover the new port and re-register.

Rollback: revert the change. No state migration is required; the daemon's on-disk surface (pidfile, tokens, state dir) is unchanged.

## Open Questions

- **Exact `Daemon.Ports` API.** Resolved in argus PR #630; this design defers the wire shape to that PR. The plannotator-argus implementation will match whatever shape lands there.
- **Should `PLANNOTATOR_MCP_HEARTBEAT` default change?** Today it is 5 minutes. With the fast-retry path, that is fine. If operators report the 5-minute detection window feels too long, drop it to 2 minutes. Not in scope for v1; revisit if operators ask.
- **Does plannotator-argus need to retry the in-shutdown `UnregisterTool` calls when it knows argus is down?** Today `Stop` runs the deletes regardless and logs failures as warnings. With this change, on a fatal-driven exit, the deletes are almost certain to fail. Leave them in place; they cost a 10-second context timeout at most and produce a clear "during exit, also failed to unregister" log line. Not worth a code branch to suppress.
