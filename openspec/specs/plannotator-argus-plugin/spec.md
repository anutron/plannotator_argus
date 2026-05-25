# plannotator-argus-plugin Specification

## Purpose
TBD - created by archiving change plannotator-argus-plugin. Update Purpose after archive.
## Requirements
### Requirement: MCP tool registration

The daemon SHALL register exactly five MCP tools with argus on startup, all under the `plannotator_` prefix, and SHALL maintain those registrations for the daemon's lifetime.

#### Scenario: All five tools registered on startup

- **WHEN** the daemon starts with a valid scope token and argus reachable
- **THEN** it POSTs five tool registrations to `/api/mcp/tools` whose names are exactly `plannotator_annotate`, `plannotator_review`, `plannotator_setup_goal`, `plannotator_last`, and `plannotator_session_result`

#### Scenario: Registrations are re-POSTed on heartbeat

- **WHEN** five minutes have elapsed since the previous registration of any tool
- **THEN** the daemon re-POSTs that tool's registration, refreshing argus's `LastSeenAt` and keeping the tool alive under the 10-minute idle sweep

#### Scenario: Registrations are dropped on graceful shutdown

- **WHEN** the daemon receives SIGTERM or SIGINT
- **THEN** it DELETEs each of the five tool registrations from argus before exiting

#### Scenario: Startup fails fast on invalid scope token

- **WHEN** the daemon starts and argus returns 401 to the first registration POST
- **THEN** the daemon logs an explanatory error pointing at `argus token mint --scope plannotator` and exits non-zero

### Requirement: Verb-starter MCP tools return an async session envelope

Each verb-starter tool (`plannotator_annotate`, `plannotator_review`, `plannotator_setup_goal`, `plannotator_last`) SHALL return an envelope identifying a new Plannotator session within argus's 30-second callback window, never blocking on Plannotator's interactive flow.

#### Scenario: Annotate returns a session envelope synchronously

- **WHEN** Claude calls `plannotator_annotate(cwd, path)` with valid arguments
- **THEN** the daemon spawns `plannotator annotate <abs(cwd, path)>` as a background subprocess, generates a fresh `session_id`, and returns `{session_id, url, status: "pending"}` within five seconds

#### Scenario: Review returns a session envelope synchronously

- **WHEN** Claude calls `plannotator_review(cwd, [pr_url], [git])` with valid arguments
- **THEN** the daemon spawns `plannotator review [--git] [pr_url]` as a background subprocess and returns `{session_id, url, status: "pending"}` within five seconds

#### Scenario: Setup-goal returns a session envelope synchronously

- **WHEN** Claude calls `plannotator_setup_goal(cwd, mode, bundle_path)` with `mode` in `{"interview", "facts"}` and `bundle_path` resolving under `cwd`
- **THEN** the daemon spawns `plannotator setup-goal <mode> <bundle_path>` as a background subprocess and returns `{session_id, url, status: "pending"}` within five seconds

#### Scenario: Last returns a session envelope synchronously

- **WHEN** Claude calls `plannotator_last(cwd)`
- **THEN** the daemon spawns `plannotator last` as a background subprocess and returns `{session_id, url, status: "pending"}` within five seconds

#### Scenario: Subprocess spawn failure surfaces as a tool error

- **WHEN** `cmd.Start` fails (e.g. the configured Plannotator binary is missing or non-executable)
- **THEN** the daemon does NOT create a session and returns a tool error response with `isError: true` and a message identifying the spawn failure

### Requirement: Session result polling

The `plannotator_session_result` tool SHALL return the current state of a session, long-polling up to `wait_seconds` (default 20, max 25) before returning a `pending` status if the session is still running.

#### Scenario: Pending session within wait window returns pending

- **WHEN** Claude calls `plannotator_session_result(cwd, session_id, wait_seconds=20)` and the session is still running after 20 seconds
- **THEN** the daemon returns `{session_id, status: "pending"}` after blocking approximately `wait_seconds` seconds (subject to OS scheduling jitter)

#### Scenario: Completed session returns the result

- **WHEN** Claude calls `plannotator_session_result` after the underlying Plannotator subprocess has exited with status 0 and valid JSON on stdout
- **THEN** the daemon returns `{session_id, status: "complete", result: <parsed-json>}` and MAY include `url` if Plannotator's session file recorded one

#### Scenario: Completed session with non-JSON stdout is wrapped

- **WHEN** the underlying Plannotator subprocess exits with status 0 with stdout that is not valid JSON (e.g. `plannotator last` emitting prose)
- **THEN** the daemon stores the result as `{"raw": <stdout-as-string>}` so the polled `result` field is always a valid JSON object

#### Scenario: Completed session with empty stdout returns null result

- **WHEN** the underlying Plannotator subprocess exits with status 0 and writes no stdout
- **THEN** the daemon returns `{session_id, status: "complete", result: null}`

#### Scenario: Failed session returns the error

- **WHEN** Claude calls `plannotator_session_result` after the underlying Plannotator subprocess has exited non-zero
- **THEN** the daemon returns `{session_id, status: "failed", error: <stderr-summary>}`

#### Scenario: Unknown session_id returns 404-equivalent

- **WHEN** Claude calls `plannotator_session_result` with a `session_id` that the daemon has no record of (never created, GC'd after TTL, or lost on daemon restart)
- **THEN** the daemon returns a tool error response with a clear message that the session is unknown or expired

#### Scenario: Wait_seconds is clamped under argus ceiling

- **WHEN** Claude calls `plannotator_session_result` with `wait_seconds=60`
- **THEN** the daemon silently clamps to 25 seconds, leaving margin under argus's 30-second callback timeout

#### Scenario: Wait_seconds defaults to 20 when zero or omitted

- **WHEN** Claude calls `plannotator_session_result` with `wait_seconds=0`, a negative value, or the field omitted
- **THEN** the daemon uses a 20-second long-poll window

#### Scenario: Caller cancellation returns the current snapshot, not an error

- **WHEN** the MCP callback ctx is cancelled (e.g. argus hung up early) while the session is still pending
- **THEN** the daemon returns the current snapshot (typically `{session_id, status: "pending"}`) rather than a tool error, so the agent can resume polling with the same `session_id`

### Requirement: Hook-mode HTTP endpoint

The daemon SHALL expose a `POST /hook` endpoint that pipes the request body into a freshly spawned `plannotator` process (no args) and returns the subprocess's stdout as the response body, with no artificial timeout.

#### Scenario: Valid hook request proxies to Plannotator

- **WHEN** a client POSTs JSON to `/hook` with a valid `Authorization: Bearer <token>` header
- **THEN** the daemon writes the body to a fresh `plannotator` subprocess's stdin, holds the HTTP connection open until the subprocess exits, and returns the subprocess's stdout as the response body with the same content-type Plannotator emits

#### Scenario: Missing bearer token is rejected

- **WHEN** a client POSTs to `/hook` without an `Authorization` header
- **THEN** the daemon returns HTTP 401 with no body and does not spawn a subprocess

#### Scenario: Wrong bearer token is rejected via constant-time compare

- **WHEN** a client POSTs to `/hook` with `Authorization: Bearer <wrong-token>`
- **THEN** the daemon returns HTTP 401 after performing a constant-time comparison against the stored token

#### Scenario: Subprocess failure surfaces as 500

- **WHEN** the `plannotator` subprocess exits non-zero
- **THEN** the daemon returns HTTP 500 with a body containing the last 4KB of the subprocess's stderr

#### Scenario: Connection survives long annotation sessions

- **WHEN** a hook request takes ten minutes to resolve because the user is annotating
- **THEN** the daemon does not close or time out the connection from its side

### Requirement: Two-channel authentication

The daemon SHALL use a per-process random secret to authenticate MCP callbacks and a separate long-lived token to authenticate hook requests.

#### Scenario: MCP secret regenerates on each startup

- **WHEN** the daemon starts
- **THEN** it generates a fresh cryptographically random secret, embeds it as the `auth_header` of each MCP tool registration sent to argus, and does NOT persist the secret to disk

#### Scenario: MCP callbacks check auth_header constant-time

- **WHEN** an MCP callback arrives with a wrong `Authorization` header
- **THEN** the daemon rejects with HTTP 401 after a constant-time comparison

#### Scenario: Hook token is created on first startup

- **WHEN** the daemon starts and `~/.plannotator/argus-plugin-token` does not exist
- **THEN** the daemon generates a cryptographically random token and writes it to that path with mode 0600

#### Scenario: Hook token is preserved across restarts

- **WHEN** the daemon starts and `~/.plannotator/argus-plugin-token` already exists
- **THEN** the daemon reads the existing token and does NOT overwrite the file

#### Scenario: Scope token accepts raw `argus token mint` output

- **WHEN** the scope token file contains the verbatim multi-line output of `argus token mint --scope plannotator` (including `id:`, `scope:`, `label:`, `token: <value>`, and trailing prose)
- **THEN** the daemon extracts the `token: <value>` line and uses it as the bearer secret

#### Scenario: Scope token rejected when it would corrupt HTTP headers

- **WHEN** the scope token contains a byte outside the printable ASCII range (e.g., embedded newline or NUL) and no `token: <value>` line is present
- **THEN** the daemon refuses to start and surfaces an error pointing at the offending byte or whitespace

### Requirement: Path resolution and safety

The daemon SHALL resolve `path` arguments by joining with `cwd` and SHALL reject paths that escape the calling task's worktree via `..` traversal. HTTP and HTTPS URLs are passed through unmodified.

#### Scenario: Relative path resolves under cwd

- **WHEN** Claude calls `plannotator_annotate(cwd="/Users/aaron/.argus/worktrees/Plannotator/ask", path="design.md")`
- **THEN** the daemon resolves the absolute path to `/Users/aaron/.argus/worktrees/Plannotator/ask/design.md` and passes it to Plannotator

#### Scenario: Traversal outside cwd is rejected

- **WHEN** Claude calls `plannotator_annotate(cwd="/Users/aaron/.argus/worktrees/Plannotator/ask", path="../../../etc/passwd")`
- **THEN** the daemon returns a tool error and does not spawn a subprocess

#### Scenario: HTTP URL is passed through

- **WHEN** Claude calls `plannotator_annotate(cwd=<anything>, path="https://example.com/doc.md")`
- **THEN** the daemon passes the URL verbatim to `plannotator annotate https://example.com/doc.md`

#### Scenario: Absolute path under cwd is accepted

- **WHEN** Claude calls `plannotator_annotate(cwd="/Users/aaron/proj", path="/Users/aaron/proj/design.md")`
- **THEN** the daemon accepts the absolute path verbatim and passes it to Plannotator

#### Scenario: Absolute path outside cwd is rejected

- **WHEN** Claude calls `plannotator_annotate(cwd="/Users/aaron/proj", path="/etc/passwd")`
- **THEN** the daemon returns a tool error and does not spawn a subprocess

#### Scenario: Symlink-mediated escape is rejected

- **WHEN** `cwd` contains a symlink pointing outside `cwd` and Claude calls a verb tool with the link as the path
- **THEN** the daemon resolves the symlink, detects the escape, and returns a tool error

#### Scenario: Empty cwd is rejected

- **WHEN** Claude calls any verb tool with `cwd=""`
- **THEN** the daemon returns a tool error and does not spawn a subprocess

#### Scenario: Subprocess runs from the caller's cwd

- **WHEN** the daemon spawns Plannotator for any verb
- **THEN** the subprocess's working directory is set to the calling task's `cwd` (not the daemon's own working directory), so verbs that read `$PWD` (e.g. `plannotator review --git`) find the expected workspace

### Requirement: Session lifecycle and garbage collection

The daemon SHALL hold session state in process memory only, garbage-collect completed sessions after their TTL, and lose all in-flight session state on restart.

#### Scenario: Completed session is reachable until TTL

- **WHEN** a session has been in `complete` or `failed` state for less than the configured `SESSION_TTL` (default 10 minutes)
- **THEN** `plannotator_session_result` for that session_id returns the cached result

#### Scenario: Completed session is GC'd after TTL

- **WHEN** a session has been in `complete` or `failed` state for longer than `SESSION_TTL`
- **THEN** `plannotator_session_result` returns an unknown-or-expired error

#### Scenario: Restart drops in-flight sessions

- **WHEN** the daemon restarts while sessions are running or recently completed
- **THEN** all session state is lost and any subsequent `plannotator_session_result` for those session_ids returns an unknown-or-expired error

#### Scenario: Shutdown kills in-flight subprocesses

- **WHEN** the daemon receives SIGTERM or SIGINT with one or more Plannotator subprocesses still running
- **THEN** the daemon cancels the parent context (SIGKILLing each subprocess via `exec.CommandContext` semantics), waits up to a bounded grace period for the lifecycle goroutines to drain, then unregisters tools and exits

### Requirement: Daemon CLI verbs

The daemon SHALL expose `start [--foreground]`, `stop`, and `status` CLI verbs.

#### Scenario: Start --foreground brings the daemon up in the current shell

- **WHEN** the user runs `plannotator-argus start --foreground`
- **THEN** the daemon runs in the foreground, logs to stderr, and exits cleanly on SIGINT or SIGTERM

#### Scenario: Start without --foreground returns an explanatory error

- **WHEN** the user runs `plannotator-argus start` without `--foreground`
- **THEN** the binary exits non-zero with a message pointing at `nohup` or launchd for background daemonization

#### Scenario: Stop terminates a running daemon

- **WHEN** the user runs `plannotator-argus stop` and a daemon is running
- **THEN** the binary reads the PID file at `~/.plannotator/argus-plugin.pid`, verifies the target process is alive (signal 0), sends SIGTERM, waits for the process to exit, and removes the PID file

#### Scenario: Stop removes a stale PID file without signalling a recycled PID

- **WHEN** the PID file exists but the recorded PID is no longer alive (recycled or never restarted)
- **THEN** the binary removes the stale PID file and reports the situation, without ever sending SIGTERM to an unrelated process

#### Scenario: Status reports liveness

- **WHEN** the user runs `plannotator-argus status`
- **THEN** the binary reports `running (pid=<n>, since=<ts>)` (where `<ts>` is the PID file's mtime in RFC3339) if the PID file exists and the process is alive, or `not running` otherwise

#### Scenario: Concurrent start refuses to clobber a live pidfile

- **WHEN** a second `plannotator-argus start --foreground` runs while a daemon is already running with a live PID file
- **THEN** the second invocation fails with a clear error naming the existing PID and pidfile path, and does NOT overwrite the file

