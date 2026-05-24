## Context

Plannotator is a single Bun-compiled binary at `~/.local/bin/plannotator` that drives interactive review/annotation flows via a local browser session. It writes session metadata to `~/.plannotator/sessions/<pid>.json` and opens a browser to a localhost port. Aaron uses it heavily:

- **Primary path** — Claude Code's ExitPlanMode stop hook invokes `plannotator` (no args) with session JSON on stdin. The hook opens a browser, Aaron annotates, and the hook's output feeds back into Claude as user feedback.
- **Secondary path** — Skills like `/plannotator-annotate`, `/plannotator-review`, `/plannotator-setup-goal`, `/plannotator-last` shell out to `plannotator <verb> <args>`.

Argus orchestrates parallel argus tasks, each running inside a filesystem sandbox scoped to its worktree. The sandbox blocks writes to `~/.plannotator/sessions/<pid>.json` (EPERM) and also breaks Plannotator's localhost browser handshake (the browser opens but loads nothing). Net effect: Aaron's primary review workflow is unusable from any argus task.

Meanwhile, the argus plugin substrate (drn/argus PRs 1-9) is shipped and exercised. A complete reference implementation of the wrap-as-plugin pattern landed tonight as Ludwig (`anutron/ludwig`, branch `argus/ludwig-argus-coordinator`). Same shape applies here: a daemon outside the sandbox registers MCP tools with argus, sandboxed Claude invokes the tools, the daemon does the actual work using Plannotator's existing binary.

This change creates the `anutron/plannotator_argus` repo's first real implementation: a Go daemon that wraps Plannotator as an argus plugin.

## Goals / Non-Goals

**Goals:**

- Make Plannotator usable from inside argus task sandboxes without rewriting Plannotator.
- Cover both invocation paths — explicit MCP calls (annotate / review / setup-goal / last) and the ExitPlanMode hook.
- Mirror Ludwig's daemon shape so the codebase stays uniform across argus plugins.
- Keep the design forward-compatible with future Plannotator verbs without per-verb daemon code changes being painful.
- Stay headless in v1 — no plugin view, no settings section, no `plannotator-argus install` ergonomics.

**Non-Goals:**

- **No skill file rewrites.** The Plannotator installer owns `~/.claude/skills/plannotator-*/SKILL.md` and will overwrite anything we ship here. Updates to those skills happen in Plannotator's installer, not in this change.
- **No new MCP tools for ops verbs** (`plannotator archive`, `plannotator sessions`, `plannotator improve-context`). Those are local-only operator workflows that don't need a sandbox-aware path.
- **No multi-user / multi-host operation.** One daemon per user, talking to one argus daemon on `127.0.0.1`.
- **No production-mode hardening of MCP callback secret.** Same model as Ludwig — secret is per-process random, restart-resets, trust scoped to a single-user machine.
- **No settings section, no plugin view, no `plannotator-argus install` (launchd).** Deferred to follow-up changes once headless surface is stable.
- **No fix for Plannotator's existing sandbox-detection logic.** Plannotator's own binary stays as-is. If we ever want it to self-detect "I'm sandboxed, route through the daemon," that's a Plannotator change, not an argus plugin change.

## Decisions

### D1. Wrap, don't rewrite

The daemon shells out to the existing `plannotator` binary on `$PATH` (or a configured path) and pipes stdin/stdout/stderr appropriately. Plannotator's internals — session files, browser handshake, annotation parser — stay untouched.

**Why:** Plannotator is actively developed by Aaron. Forking its internals into a Go reimplementation creates a maintenance burden and a divergence risk every time Plannotator's CLI changes. Shelling out is one syscall and Plannotator's `--json` / `--hook` output modes are already structured for programmatic consumption.

**Alternatives considered:**

- **Rewrite Plannotator's session-file write to use a relative path.** Rejected — would require modifying Plannotator and still wouldn't fix the browser-handshake failure inside the sandbox.
- **Run Plannotator as a long-running subprocess per session.** Rejected — Plannotator's per-PID session-file convention already isolates each invocation, and a fresh process per call is simpler to reason about.

### D2. Curated MCP tool surface (5 tools) with polling for long ops

The daemon registers five MCP tools under the `plannotator_` prefix:

- `plannotator_annotate(cwd, path)` — wraps `plannotator annotate <abs(cwd, path)>`. Path can be a file (md/html), folder, or URL.
- `plannotator_review(cwd, [pr_url], [git])` — wraps `plannotator review [--git] [PR_URL]`.
- `plannotator_setup_goal(cwd, mode, bundle_path)` — wraps `plannotator setup-goal <interview|facts> <bundle.json|->`.
- `plannotator_last(cwd)` — wraps `plannotator last`.
- `plannotator_session_result(cwd, session_id, wait_seconds?)` — poll/long-poll for a session's completion result.

The first four return a `{session_id, url, status: "pending"}` envelope within argus's 30-second callback window. The agent then polls `plannotator_session_result(session_id)` until it returns `{status: "complete", result: <annotations>}` or `{status: "cancelled" | "failed", error: <msg>}`.

`plannotator_session_result` accepts a `wait_seconds` parameter (default 20, max 25, capped under argus's 30s ceiling). The handler blocks until the session resolves OR the deadline hits, then returns whatever state it has. Most annotations resolve in 1-2 polls.

**Why polling:** Argus caps plugin callback responses at 30 seconds; interactive Plannotator sessions are minutes. This is the standard long-running-operation (LRO) pattern — Google Cloud / Kubernetes / etc. all use it.

**Why curated verbs over an omnibus `plannotator_run` tool:** Per-verb tools give Claude typed input schemas and per-tool descriptions. The cost — daemon code update when Plannotator adds a verb — is small. The benefit — Claude actually understands what each tool does — is significant.

**Alternatives considered:**

- **Block synchronously in `plannotator_annotate`, accept argus timeouts.** Rejected — 30s is way under typical annotation time, so timeouts would be the norm.
- **Omnibus `plannotator_run(cwd, args[])` instead of curated verbs.** Rejected — agents perform measurably worse with generic exec-style tools than with typed verb tools.
- **Single unified `plannotator_session_result` shared across all verbs vs. one polling tool per verb.** Chose shared. Session ID is the unit of work; the verb is bookkeeping.

### D3. Direct HTTP endpoint for hook integration

The daemon exposes a separate `POST /hook` endpoint on its HTTP listener (same listener as MCP callbacks, different path/auth namespace). This endpoint:

- Accepts JSON on the request body, identical to what `plannotator` reads from stdin in hook mode.
- Pipes the body into `plannotator < stdin` (no args) executed outside the sandbox.
- Holds the HTTP request open until Plannotator returns (typically minutes; bounded only by the user closing the browser).
- Returns Plannotator's stdout as the response body.

**Why a separate endpoint, not an MCP tool:** ExitPlanMode hooks are invoked by Claude Code (the parent process), not by Claude (the model). MCP tools are only callable by the model. The hook needs a direct HTTP path the parent's stop-hook command can `curl` to.

**Why no 30-second wall:** Argus's MCP proxy isn't in the middle. The sandboxed Claude Code's hook curls 127.0.0.1:<plannotator-port> directly; the daemon and the hook share a single HTTP connection that can stay open as long as both endpoints agree. No proxy timeout applies.

The shim/wrapper that actually rewrites the ExitPlanMode hook config to call this endpoint instead of `plannotator` directly is **owned by Plannotator's installer**, not by this change. The argus plugin's responsibility ends at the daemon exposing the endpoint and documenting the contract.

### D4. Two-channel authentication

Two separate auth surfaces because the two callers have different trust profiles:

- **MCP callbacks** (`/mcp/plannotator_*`): the daemon generates a per-process random `auth_header` secret on startup, registers it with argus as part of each tool registration, and checks incoming requests with constant-time comparison. On daemon restart, the secret regenerates and re-registers. Identical to Ludwig's pattern.
- **Hook endpoint** (`/hook`): the daemon writes a long-lived token to `~/.plannotator/argus-plugin-token` (mode 0600) on first startup. Sandboxed Claude Code's hook reads this file and presents it as `Authorization: Bearer <token>`. The token persists across daemon restarts.

**Why a persistent token for the hook but not for MCP:** MCP tool registrations carry their `auth_header` via the argus registry — argus knows what header to expect. Hooks have no such central registry; the hook config in `~/.claude/settings.json` needs a stable secret to send. A file at a known path under `~/.plannotator/` is the simplest answer.

**Sandbox-readability of `~/.plannotator/`:** Argus's sandbox blocks WRITES to `~/.plannotator/`; reads are expected to work. If this turns out not to be true, the token file moves to a path the sandbox does permit (open question, surfaces in testing).

### D5. Argus worktree paths are unsandboxed-readable from the daemon

The MCP tools take `cwd` from the calling argus task. Argus worktrees live at `/Users/aaron/.argus/worktrees/<Project>/<branch>/...`. From the daemon's perspective (running outside any sandbox), these paths are normal filesystem paths.

For each MCP tool:

1. Sandboxed Claude calls `plannotator_annotate(cwd=$PWD, path="design.md")`.
2. Daemon resolves `abspath = filepath.Join(cwd, path)` and enforces that the resolved path is a descendant of `cwd` (with symlink resolution to catch escape via links). HTTP(S) URLs pass through unmodified.
3. Daemon shells `plannotator annotate <abspath>`.

**No bytes-in mode.** Inputs are paths or URLs. Plannotator already handles those natively; we don't re-implement file streaming.

### D6. Daemon process model and state

The daemon is a single long-running Go process. Per-call lifecycle:

1. MCP tool handler receives a request, generates a fresh `session_id`, records `{session_id, cmd, started_at, status: "running"}` in an in-memory map.
2. Goroutine spawns `plannotator` outside any sandbox and waits.
3. When `plannotator` exits, the goroutine parses stdout JSON and updates the map: `{status: "complete", result: <annotations>, completed_at: <now>}` or `{status: "failed", error: <stderr summary>}`.
4. `plannotator_session_result(session_id)` reads from this map. Completed sessions stay in memory for 10 minutes (configurable) before GC.

State is purely in-memory. Daemon restart drops all in-flight and recently-completed sessions; users would need to re-trigger their annotation. Acceptable for v1 — restarts are rare and operationally obvious.

**Why no SQLite:** Plannotator's own session files at `~/.plannotator/sessions/` provide persistence for Plannotator's own bookkeeping. The daemon's in-memory map only tracks the MCP-call → Plannotator-process correspondence, which is ephemeral by nature.

### D7. Daemon configuration

- `ARGUS_BASE_URL` (default `http://127.0.0.1:7743`) — argus daemon location.
- `PLANNOTATOR_BIN` (default: first `plannotator` on `$PATH`) — Plannotator binary path.
- `ARGUS_TOKEN_PATH` (default `~/.plannotator/argus-api-token`) — scope token file (separate from the hook token, since the hook token belongs to clients, not the daemon).
- `LISTEN_ADDR` (default `127.0.0.1:7745`) — daemon HTTP listener.
- `STATE_DIR` (default `~/.plannotator/`) — for hook token file, PID file, log.
- `MCP_HEARTBEAT` (default 5min) — half of argus's 10min idle window.
- `SESSION_TTL` (default 10min) — how long completed session results stay in memory.

CLI verbs (mirroring Ludwig): `plannotator-argus start [--foreground]`, `stop`, `status`. Background daemonization is deferred — `start` without `--foreground` returns a hint pointing at `nohup` and launchd.

### D8. Mirror Ludwig's shape

Project layout, code patterns, and test conventions follow Ludwig directly:

- `cmd/plannotator-argus/` — CLI entry points (start, stop, status).
- `internal/argus/` — HTTP client for argus daemon (`client.go`, `mcp.go`, `events.go` if subscribing — likely not in v1).
- `internal/config/` — config loading.
- `internal/mcp/` — callback HTTP server, registrar (with 5min heartbeat), tool handlers, shared session-state map.
- `internal/hook/` — hook endpoint handler.
- `internal/plannotator/` — shell-out helper that finds the Plannotator binary, runs it with the right args, parses output.
- `internal/daemon/` — main loop, Start/Stop pattern.

Go module name: `github.com/anutron/plannotator_argus`. Single binary, `make build` → `./bin/plannotator-argus`.

Heartbeat re-registration runs every 5 minutes per registered tool. Idempotent (argus's substrate refreshes `LastSeenAt`).

### D9. Headless v1; deferrals match Ludwig's pattern

Deferred to follow-up changes:

- **Plugin view.** Plannotator's UI is already a browser; an embedded TUI view doesn't help.
- **Settings section.** Possible v1.1 candidates: `PLANNOTATOR_BIN` override, session TTL, log level. Easy to add when needed.
- **`plannotator-argus install` (launchd).** Same as Ludwig — defer until `start --background` story is settled.
- **Skill updates.** Owned by Plannotator's installer, not this change.

## Risks / Trade-offs

- **`~/.plannotator/` write-blocked but expected to be readable from sandbox.** → Verify in testing. If reads also blocked, hook token moves to a daemon-side endpoint that exchanges a known argus task ID for a fresh token (more code, but doable).
- **Token rotation has no graceful path.** → Same as Ludwig: revoking the scope token 401s the daemon, which logs and exits. User re-mints and restarts.
- **Daemon restart drops in-flight session state.** → Acceptable. Annotation is human-driven; the human just re-triggers.
- **Plannotator binary not on `$PATH` or not installed.** → Daemon fails health check on startup with explicit "Plannotator binary not found, set PLANNOTATOR_BIN" error. The health check is `plannotator --version`; Plannotator's contract here is that `--version` returns exit 0. If a future Plannotator release renames or drops the flag, the daemon refuses to start until the contract is restored or the health check is loosened.
- **MCP callback secret in process memory.** → Same as Ludwig — restart resets, re-registration carries new header. Trust scoped to a single-user machine.
- **Plannotator output format changes underneath us.** → We pin to `--json` output and document the expected shape; output drift fails the parse cleanly with a clear error. Acceptable maintenance cost.
- **Polling cadence vs. argus's callback budget.** → `plannotator_session_result` long-polls up to 25s per call; the agent typically wakes once and the session is done. Worst case: an idle agent polls every 25s for the duration of the annotation. Acceptable, well under argus's 30s ceiling.

## Migration Plan

Greenfield repo (`anutron/plannotator_argus` is empty besides a stub README). Bootstrap steps for first install:

1. `make build` → `./bin/plannotator-argus`.
2. `cp ./bin/plannotator-argus ~/.local/bin/`.
3. Mint scope token: `argus token mint --scope plannotator > ~/.plannotator/argus-api-token; chmod 600 ~/.plannotator/argus-api-token`.
4. First run: `plannotator-argus start --foreground`. Daemon creates `~/.plannotator/argus-plugin-token` (the hook token), registers 5 MCP tools, ready.
5. Verify: `argus token list` shows the `plannotator` scope; daemon log shows five tool registrations.

The Claude Code hook config change (replacing `plannotator` with a wrapper that calls `127.0.0.1:7745/hook`) ships in Plannotator's installer, not here.

Rollback: revoke the scope token (`argus token revoke <id>`). Argus's cascade drops all five MCP tool registrations. Delete `~/.plannotator/argus-api-token` and `~/.plannotator/argus-plugin-token`. Daemon process can be killed via `plannotator-argus stop` or `kill $(cat ~/.plannotator/argus-plugin.pid)`.

## Open Questions

- **Sandbox readability of `~/.plannotator/argus-plugin-token`.** Pending empirical verification once the daemon is built. If the sandbox blocks reads of `~/.plannotator/`, we need a different token-delivery channel. Surfaces in early hook testing.
- **`plannotator_session_result` polling cadence.** Default 20s long-poll seems right; Aaron may want tuning. Easy to expose as a configurable.
- **Should the daemon validate that `cwd` is actually an argus task worktree** before running Plannotator? Currently we just resolve and shell out. A stricter check (must be `/Users/aaron/.argus/worktrees/<*>/`) is one line of code and might prevent confusion. Lean toward strict; revisit if it gets in the way.
- **Should `plannotator_setup_goal` accept the bundle as inline JSON (passed via stdin) instead of a path?** Today the CLI accepts `-` for stdin, but the MCP tool doesn't have a natural way to pass arbitrary JSON. For v1: path-only. Revisit if the path mode becomes painful.

## Discovery findings

- **Plannotator source is not present on this machine.** The binary at `~/.local/bin/plannotator` is a Bun-compiled bundle. The `anutron/plannotator_argus` repo is the only Plannotator-related repo on GitHub under `anutron`, and it's empty besides a stub README. This is fine — the wrapper only needs the binary, not the source.
- **Plannotator CLI surface** (from `plannotator --help`): `annotate`, `review`, `setup-goal`, `last`, `archive`, `sessions`, `improve-context`, plus a bare-invocation hook mode that reads JSON from stdin. Only the first four are wrapped in v1.
- **Plannotator session file format**: `{pid, port, url, mode, project, startedAt, label}` at `~/.plannotator/sessions/<pid>.json`. The argus plugin daemon doesn't read these directly — it just observes Plannotator's process exit and parses stdout.
- **Argus substrate caps callback at 30 seconds.** Confirmed in `docs/plugins.md`. Drives the polling design.
- **Ludwig reference** (`~/.argus/worktrees/Ludwig/ludwig-argus-coordinator/`): provides the daemon skeleton, MCP server pattern, registrar pattern, and Start/Stop wiring. Mirrored 1:1 where applicable.
- **No native event subscription needed for v1.** Ludwig uses argus's SSE event stream to auto-adopt worker tasks; Plannotator has no equivalent — it's purely call-driven.

## Acceptance criteria

Per-section behavioral criteria, will become OpenSpec scenarios in the delta spec.

### D2 (MCP tool surface)

- it should register exactly five MCP tools under the `plannotator_` prefix when the daemon starts and successfully authenticates with argus.
- it should return `{session_id, url, status: "pending"}` from `plannotator_annotate`, `plannotator_review`, `plannotator_setup_goal`, and `plannotator_last` within argus's 30-second callback window.
- it should return the same `session_id` from a subsequent `plannotator_session_result` call until the session resolves or the TTL expires.
- it should return `{status: "complete", result: <parsed-json>}` from `plannotator_session_result` once Plannotator exits with success and `--json` output parses cleanly.
- it should return `{status: "failed", error: <message>}` from `plannotator_session_result` when Plannotator exits non-zero or `--json` output fails to parse.
- it should long-poll up to `wait_seconds` (default 20, max 25) in `plannotator_session_result` before returning `pending` if the session is still running.
- it should reject MCP tool calls whose `cwd` cannot be resolved to a readable absolute path (400 with explanatory error).

### D3 (Hook endpoint)

- it should accept `POST /hook` requests with a body matching Plannotator's hook-mode stdin JSON shape.
- it should authenticate `/hook` requests with `Authorization: Bearer <token-from-~/.plannotator/argus-plugin-token>`.
- it should pipe the request body into a freshly-spawned `plannotator` (no args) process and return its stdout as the response body.
- it should keep the HTTP connection open for the full duration of the Plannotator process (no artificial timeout).
- it should return 401 when the bearer token is missing or does not match.
- it should return 500 with the Plannotator stderr summary when the subprocess exits non-zero.

### D4 (Authentication)

- it should generate a fresh random secret on startup and include it as the `auth_header` in each tool registration sent to argus.
- it should reject MCP callback POSTs whose `Authorization` header does not match the per-process secret (constant-time comparison).
- it should create `~/.plannotator/argus-plugin-token` with mode 0600 on first startup if it does not exist.
- it should preserve `~/.plannotator/argus-plugin-token` across restarts (do not overwrite if already present).

### D5 (Path handling)

- it should resolve `path` arguments to absolute paths by joining with `cwd`.
- it should reject paths that escape the calling task's worktree via `..` traversal.
- it should accept absolute URLs (`http://` or `https://`) as `path` and pass them through unmodified.

### D6 (Daemon process model)

- it should garbage-collect completed session entries after `SESSION_TTL` (default 10 minutes).
- it should drop all in-flight session state on restart (no persistence across restarts).
- it should run each `plannotator` invocation in its own subprocess (no reuse).

### D8 (MCP registration lifecycle)

- it should POST registrations for all five tools to argus's `/api/mcp/tools` on daemon startup.
- it should re-POST each registration on a 5-minute heartbeat to stay alive in argus's MCP idle sweep.
- it should unregister all tools on graceful daemon shutdown (SIGTERM / SIGINT).
- it should log a clear error and exit if argus returns 401 (revoked or invalid scope token).
