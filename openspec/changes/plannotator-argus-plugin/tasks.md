**Design doc:** `openspec/changes/plannotator-argus-plugin/design.md`

## 1. Failing tests from the delta spec

- [ ] 1.1 Set up `go test ./...` toolchain and `internal/testutil/` with a stubbed argus HTTP server helper (mirror Ludwig's `internal/argus/server_stub.go` pattern)
- [ ] 1.2 Write failing tests for "MCP tool registration" requirement (5 tool names, heartbeat re-POST, graceful unregister, 401 fail-fast)
- [ ] 1.3 Write failing tests for "Verb-starter MCP tools return an async session envelope" requirement (one per verb, asserting envelope shape and that the subprocess is backgrounded)
- [ ] 1.4 Write failing tests for "Session result polling" requirement (pending, complete, failed, unknown, wait_seconds clamp)
- [ ] 1.5 Write failing tests for "Hook-mode HTTP endpoint" requirement (valid request, missing token, wrong token, subprocess failure, long-running connection)
- [ ] 1.6 Write failing tests for "Two-channel authentication" requirement (MCP secret regenerates, constant-time compare, hook token created on first startup, preserved on second)
- [ ] 1.7 Write failing tests for "Path resolution and safety" requirement (relative, traversal rejected, URL passthrough)
- [ ] 1.8 Write failing tests for "Session lifecycle and garbage collection" requirement (TTL retention, GC after TTL, restart drops state)
- [ ] 1.9 Write failing tests for "Daemon CLI verbs" requirement (start --foreground, start error message, stop, status)
- [ ] 1.10 Run `go test ./... -race` and confirm every test fails for the right reason (no false positives)

## 2. Project scaffold

**Depends on:** Stage 1

- [ ] 2.1 `go mod init github.com/anutron/plannotator_argus`; pin Go 1.22+
- [ ] 2.2 Create `cmd/plannotator-argus/main.go` with cobra-or-stdlib subcommand dispatch for `start`, `stop`, `status`
- [ ] 2.3 Create `internal/{argus,config,daemon,hook,mcp,plannotator,testutil}/doc.go` package stubs with short package comments
- [ ] 2.4 Create `Makefile` with `build`, `test`, `vet`, `lint` targets (mirror Ludwig's)
- [ ] 2.5 Update repo `README.md` from the stub to a real one-paragraph description plus install steps
- [ ] 2.6 Add `.gitignore` for `bin/`, `*.log`, OS-specific files

## 3. Argus HTTP client (`internal/argus`)

**Depends on:** Stage 2

- [ ] 3.1 Implement `argus.Client` with `Bearer` + `X-Argus-Plugin-Version: 1` headers on every request
- [ ] 3.2 Implement `client.RegisterTool(ctx, def)` POSTing to `/api/mcp/tools`, returning `201`/`200` as success
- [ ] 3.3 Implement `client.UnregisterTool(ctx, name)` DELETing `/api/mcp/tools/<name>`, treating `200`/`404` as idempotent success
- [ ] 3.4 Implement Bearer-auth error mapping: 401 returns a typed `ErrUnauthorized` so callers can fail fast
- [ ] 3.5 Make Stage 1.2 tests for tool registration pass (against the stubbed argus server)

## 4. Plannotator shell-out helper (`internal/plannotator`)

**Depends on:** Stage 2

- [ ] 4.1 Implement `plannotator.Resolve()` to find the binary on `$PATH` (or `PLANNOTATOR_BIN` env override) and verify it exists + is executable
- [ ] 4.2 Implement `plannotator.Run(ctx, args, stdin)` returning `(stdout, stderr, exitCode, error)`
- [ ] 4.3 Implement `plannotator.RunJSON(ctx, args)` for verb tools — invokes the binary with `--json`, parses stdout into a generic `json.RawMessage`
- [ ] 4.4 Implement startup health check: `plannotator --version` succeeds within 5 seconds, else fail daemon startup with a clear error
- [ ] 4.5 Unit tests for path-not-found, exit non-zero, JSON parse failure, stdin pipe close

## 5. Path resolution and safety (`internal/mcp/paths.go`)

**Depends on:** Stage 2

- [ ] 5.1 Implement `mcp.ResolvePath(cwd, raw)` returning either an absolute path under `cwd` OR a URL passed through unmodified
- [ ] 5.2 Reject `..` traversal: after `filepath.Clean(filepath.Join(cwd, raw))`, the result MUST have `cwd` as a strict prefix
- [ ] 5.3 Accept `http://` and `https://` URLs verbatim (no path resolution)
- [ ] 5.4 Make Stage 1.7 tests pass

## 6. MCP server, registrar, and session-state map (`internal/mcp`)

**Depends on:** Stages 3, 4, 5

- [ ] 6.1 Implement `mcp.Server` — HTTP listener with `/mcp/<tool>` routes and constant-time `Authorization` check
- [ ] 6.2 Implement `mcp.Registrar` — register-on-startup, 5-minute heartbeat goroutine, unregister-on-stop
- [ ] 6.3 Implement `mcp.GenerateAuthHeader()` — crypto/rand 32-byte secret base64-encoded with `Bearer ` prefix
- [ ] 6.4 Implement `mcp.SessionStore` — concurrent-safe map keyed by session_id, holds `{cmd, status, result, error, started_at, completed_at}`, supports `Put`, `Get`, `MarkComplete`, `MarkFailed`, `GC(ttl)`
- [ ] 6.5 Implement `mcp.SessionStore.WaitForResolution(ctx, session_id, timeout)` — long-poll primitive used by `plannotator_session_result`
- [ ] 6.6 GC goroutine runs every minute, drops sessions where `(now - completed_at) > SESSION_TTL`
- [ ] 6.7 Make Stage 1.2, 1.6 (MCP-half), 1.8 tests pass

## 7. Verb-starter tool handlers (`internal/mcp/handler_*.go`)

**Depends on:** Stage 6

- [ ] 7.1 `handler_annotate.go` — validates `cwd`/`path`, resolves via `ResolvePath`, creates session, spawns `plannotator annotate <path> --json` as background goroutine, returns envelope
- [ ] 7.2 `handler_review.go` — validates `cwd`, optional `pr_url`/`git`, spawns `plannotator review [--git] [pr_url] --json`, returns envelope
- [ ] 7.3 `handler_setup_goal.go` — validates `cwd`, `mode in {interview,facts}`, `bundle_path` resolves under `cwd`, spawns `plannotator setup-goal <mode> <bundle_path>`, returns envelope
- [ ] 7.4 `handler_last.go` — validates `cwd`, spawns `plannotator last`, returns envelope
- [ ] 7.5 Common envelope helper: returns `{session_id, url, status: "pending"}`; `url` populated from Plannotator session file at `~/.plannotator/sessions/<pid>.json` once the subprocess writes it (poll up to 5s)
- [ ] 7.6 Background goroutine pattern: capture stdout/stderr, on exit call `SessionStore.MarkComplete` or `MarkFailed`
- [ ] 7.7 Make Stage 1.3 tests pass

## 8. Session-result tool (`internal/mcp/handler_session_result.go`)

**Depends on:** Stage 6

- [ ] 8.1 Validate `session_id`, `cwd`, and `wait_seconds` (default 20, clamp [0, 25])
- [ ] 8.2 If session not found, return tool error with clear message
- [ ] 8.3 If session already resolved, return current state immediately
- [ ] 8.4 If still running, call `SessionStore.WaitForResolution(ctx, session_id, wait_seconds)` and return whatever state is current at the deadline
- [ ] 8.5 Make Stage 1.4 tests pass

## 9. Hook endpoint (`internal/hook`)

**Depends on:** Stages 2, 4

- [ ] 9.1 Implement `hook.Token` — read from `~/.plannotator/argus-plugin-token`, generate if missing, persist with mode 0600
- [ ] 9.2 Implement `hook.Handler` — HTTP handler for `POST /hook`, constant-time Bearer check
- [ ] 9.3 Pipe request body into `plannotator` stdin (no args), stream stdout back as response body
- [ ] 9.4 No timeout on the daemon side — let the user's annotation take as long as it takes (rely on client-side cancellation if needed)
- [ ] 9.5 Subprocess non-zero exit → 500 with last 4KB of stderr in the response body
- [ ] 9.6 Make Stage 1.5, 1.6 (hook-half) tests pass

## 10. Daemon main loop + CLI verbs (`internal/daemon`, `cmd/plannotator-argus`)

**Depends on:** Stages 6, 7, 8, 9

- [ ] 10.1 `internal/daemon/run.go` — mirror Ludwig: `Start(ctx, cfg, log)` wires every component, returns a `*Daemon`; `Stop(ctx)` cleans up in reverse order; `Run(ctx)` wraps Start, writes pidfile, blocks on ctx.Done()
- [ ] 10.2 `internal/config/` — `Config` struct, `Default()`, `EnsureStateDir()`, `LoadScopeToken()`, `LoadHookToken()` (creates if missing), env var overrides for every field documented in the design
- [ ] 10.3 `cmd/plannotator-argus/start.go` — handle `--foreground`; without it, exit with the explanatory error
- [ ] 10.4 `cmd/plannotator-argus/stop.go` — read pidfile, SIGTERM, wait up to 10s for exit, remove pidfile
- [ ] 10.5 `cmd/plannotator-argus/status.go` — report running/not-running with PID and startedAt
- [ ] 10.6 Smoke test (`internal/daemon/run_smoke_test.go`) — boot daemon against stubbed argus, assert all five tools register, assert hook endpoint accepts POST, assert all tools unregister on shutdown
- [ ] 10.7 Make Stage 1.9 and the remaining tests pass; `go test ./... -race -count=1` is fully green

## 11. Build, polish, manual smoke

**Depends on:** Stage 10

- [ ] 11.1 `make build` produces `./bin/plannotator-argus`; binary runs on first invocation against the user's live argus
- [ ] 11.2 Document install + bootstrap steps in `README.md` (mint token, start daemon, verify registrations)
- [ ] 11.3 Manual smoke: run `plannotator-argus start --foreground` against the user's argus; call `plannotator_annotate` from a sandboxed argus task; confirm browser opens on the user's desktop and annotation flows back through `plannotator_session_result`
- [ ] 11.4 Manual smoke: POST a hook-shaped JSON body to `127.0.0.1:7745/hook` from a sandboxed shell and confirm Plannotator opens + returns
- [ ] 11.5 `openspec validate plannotator-argus-plugin --strict` still passes; commit hand-off to `openspec archive` once Aaron signs off
