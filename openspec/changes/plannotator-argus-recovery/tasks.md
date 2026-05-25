**Design doc:** `openspec/changes/plannotator-argus-recovery/design.md`

**Spec deltas:** `openspec/changes/plannotator-argus-recovery/specs/plannotator-argus-plugin/spec.md`

## 1. Discovery

- [ ] 1.1 Write `internal/argus/discover.go` with a `Discover(ctx context.Context, timeout time.Duration) (baseURL string, ok bool)` helper that calls argus's `Daemon.Ports` RPC
- [ ] 1.2 Decide the exact wire path with argus PR #630 (socket location, request shape, response field for the plugin-API URL) and pin it in `discover.go`
- [ ] 1.3 Treat any error (socket missing, dial refused, parse error, timeout) as `ok=false` and return without logging at error level – discovery is best-effort
- [ ] 1.4 Unit test against a fake `Daemon.Ports` server using `httptest` or a unix-socket stub, covering success, missing socket, malformed response, and timeout

## 2. Config wiring

- [ ] 2.1 In `config.Default()`, call `argus.Discover` when `PLANNOTATOR_ARGUS_BASE_URL` is unset; on `ok=true` use the discovered URL, otherwise leave the hardcoded default
- [ ] 2.2 Preserve the existing `LoadFromEnv` override – when the env var is set, it wins unconditionally and skips discovery
- [ ] 2.3 Bound the discovery call to a short timeout (500 ms) so an unreachable RPC does not slow startup
- [ ] 2.4 Update `config_test.go` to cover: env override wins; discovery success path; discovery failure path falls back to hardcoded default

## 3. Heartbeat failure classifier

- [ ] 3.1 Add a `Fatal() <-chan error` accessor (or change `Start` to return the channel) on `Registrar` for fatal errors
- [ ] 3.2 Classify each heartbeat result: 200/201 = success, 401 = fatal, transport-level error = mark first-fail timestamp and schedule fast retry in 30s, 5xx and other 4xx = warning only
- [ ] 3.3 On second consecutive transport-level failure, send the error onto the fatal channel
- [ ] 3.4 Distinguish transport errors from HTTP errors by inspecting the `error` from `http.Client.Do` (URL error, connection refused, etc.) vs. the status code on a successful response
- [ ] 3.5 Reset the consecutive-failure counter on any 2xx response
- [ ] 3.6 Update `registrar_test.go` to cover: transport failure → fast retry, two transport failures → fatal, 401 → fatal, 5xx → warning (no fatal), recovery after a single transport failure
- [ ] 3.7 Use a programmable fake `http.RoundTripper` to inject failures into `argus.Client`

## 4. Daemon wiring

- [ ] 4.1 In `Daemon.Run`, select on `ctx.Done()`, OS signal channel, and `Registrar.Fatal()`
- [ ] 4.2 On fatal, log the error at error level, call `d.Stop`, and return the fatal error from `Run`
- [ ] 4.3 Update `cmd/plannotator-argus/start.go` to propagate the non-zero exit code from `Run`
- [ ] 4.4 Update `run_test.go` with a fake registrar that emits fatal; assert `Run` returns the error and `Stop` was called

## 5. Documentation

- [ ] 5.1 README section "Argus reconnection" explains the discovery order, the fast-retry threshold, and the launchd restart loop
- [ ] 5.2 Note that operators can pin `PLANNOTATOR_ARGUS_BASE_URL` to bypass discovery entirely
- [ ] 5.3 Document the expected outage window after `argus restart` (heartbeat interval + 30s fast retry + 60s launchd throttle)

## 6. Verification

- [ ] 6.1 Unit tests pass: `go test ./...`
- [ ] 6.2 Smoke test on host: start daemon, `argus stop`, observe fast-retry log line within 30 s of next heartbeat, observe exit on second failure
- [ ] 6.3 Smoke test on host: with daemon down and `argus start` (potentially on new port), confirm launchd restart and successful re-registration via `Daemon.Ports`
- [ ] 6.4 Confirm operator override still works: `PLANNOTATOR_ARGUS_BASE_URL=http://127.0.0.1:9999 plannotator-argus start --foreground` skips discovery and fails on a bad URL as expected
- [ ] 6.5 Confirm fatal-on-401 surfaces a clear log line and the launchd loop is visible in `/Users/aaron/.plannotator/argus-plugin.log`

## 7. OpenSpec hygiene

- [ ] 7.1 Scaffold under `openspec/changes/plannotator-argus-recovery/` with proposal, design, tasks, and delta spec (this change)
- [ ] 7.2 `openspec validate plannotator-argus-recovery --strict` passes
- [ ] 7.3 Commit on a feature branch (suggested: `argus/evaluate-means-recover`, already current)
- [ ] 7.4 After merge + smoke (6.2-6.5), run `openspec archive plannotator-argus-recovery`
