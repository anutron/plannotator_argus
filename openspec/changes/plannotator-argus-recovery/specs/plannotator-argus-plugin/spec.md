## ADDED Requirements

### Requirement: Argus base URL discovery

The daemon SHALL acquire its argus plugin-API base URL at startup with a deterministic precedence: an explicit operator override via `PLANNOTATOR_ARGUS_BASE_URL` wins unconditionally; otherwise the daemon SHALL attempt argus's `Daemon.Ports` discovery RPC; otherwise the daemon SHALL fall back to the hardcoded default `http://127.0.0.1:7743`. Discovery SHALL be best-effort and bounded by a short timeout so an unavailable RPC never blocks startup.

#### Scenario: Env override wins unconditionally

- **WHEN** the daemon starts with `PLANNOTATOR_ARGUS_BASE_URL=http://127.0.0.1:9999` exported
- **THEN** the daemon uses `http://127.0.0.1:9999` as the argus plugin-API base URL and does NOT attempt `Daemon.Ports` discovery

#### Scenario: Daemon.Ports discovery succeeds

- **WHEN** the daemon starts with `PLANNOTATOR_ARGUS_BASE_URL` unset and argus's `Daemon.Ports` RPC returns a plugin-API URL of `http://127.0.0.1:7841`
- **THEN** the daemon uses `http://127.0.0.1:7841` as the argus plugin-API base URL

#### Scenario: Daemon.Ports discovery is unavailable, falls back to default

- **WHEN** the daemon starts with `PLANNOTATOR_ARGUS_BASE_URL` unset and argus's `Daemon.Ports` socket is missing or the RPC errors
- **THEN** the daemon uses the hardcoded default `http://127.0.0.1:7743` and proceeds with normal startup registration

#### Scenario: Discovery is bounded by a short timeout

- **WHEN** the daemon starts and argus's `Daemon.Ports` RPC hangs without responding
- **THEN** the daemon abandons the discovery attempt within 500 ms, treats it as a failure, and falls back to the hardcoded default

#### Scenario: Discovery failure is not fatal

- **WHEN** discovery returns any error (socket missing, dial refused, malformed response, timeout)
- **THEN** the daemon does NOT exit, does NOT log at error level, and proceeds to use the fallback URL

### Requirement: Argus link liveness detection

The daemon's heartbeat loop SHALL classify each registration result and treat sustained loss of the argus link as fatal so launchd can restart the daemon onto a freshly discovered URL. A single transient failure SHALL NOT bring the daemon down; only a confirmed sustained disconnect or an unambiguous authentication failure SHALL trigger exit.

#### Scenario: Successful heartbeat resets failure tracking

- **WHEN** a heartbeat receives HTTP 200 or 201 from argus
- **THEN** any pending fast-retry timer is cancelled and the consecutive-failure counter is reset to zero

#### Scenario: First transport failure schedules a fast retry

- **WHEN** a heartbeat returns a transport-level error (connection refused, DNS failure, request timeout, EOF) for the first time since the last success
- **THEN** the daemon logs the error at warn level, marks the first-failure timestamp, and schedules a single fast retry to fire 30 seconds later; it does NOT exit

#### Scenario: Second consecutive transport failure is fatal

- **WHEN** the fast-retry heartbeat also returns a transport-level error
- **THEN** the daemon propagates the error onto its fatal channel, triggering `Daemon.Stop` and a non-zero exit from `Daemon.Run`

#### Scenario: HTTP 401 from argus is immediately fatal

- **WHEN** any heartbeat receives HTTP 401 from argus
- **THEN** the daemon propagates the error onto its fatal channel without a fast-retry attempt, because the token is no longer valid and restarting will not change that

#### Scenario: HTTP 5xx is a warning, not fatal

- **WHEN** a heartbeat receives HTTP 500, 502, 503, or 504 from argus
- **THEN** the daemon logs the error at warn level, does NOT schedule a fast retry, does NOT increment the consecutive-failure counter, and waits for the next normal heartbeat interval

#### Scenario: HTTP 4xx other than 401 is a warning, not fatal

- **WHEN** a heartbeat receives HTTP 400, 403, 404, 422, or any non-401 4xx from argus
- **THEN** the daemon logs the error at warn level and continues on the normal heartbeat cadence

#### Scenario: Recovery from a single transport failure

- **WHEN** a heartbeat fails with a transport-level error, the fast-retry fires 30 seconds later, and that retry succeeds with HTTP 200
- **THEN** the daemon clears the failure state, resumes the normal heartbeat cadence, and does NOT exit

#### Scenario: Fatal exit triggers orderly shutdown

- **WHEN** the registrar's fatal channel emits an error
- **THEN** `Daemon.Run` logs the error at error level, invokes `Daemon.Stop` (running the GC stop, the MCP server shutdown, and the tool-unregister attempt against the now-unreachable argus), and returns the fatal error so the CLI exits non-zero

#### Scenario: Launchd restart after fatal exit

- **WHEN** the daemon exits non-zero from a fatal heartbeat error and `KeepAlive.SuccessfulExit` is `false` in the launchd plist
- **THEN** launchd restarts the daemon after its `ThrottleInterval` (60 seconds), and the restarted daemon re-runs the discovery order from "Argus base URL discovery" so a new argus port is picked up automatically
