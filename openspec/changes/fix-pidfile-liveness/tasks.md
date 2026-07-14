**Design doc:** `openspec/changes/fix-pidfile-liveness/design.md`

**Spec deltas:** `openspec/changes/fix-pidfile-liveness/specs/plannotator-argus-plugin/spec.md`

## 1. Shared lock primitive

- [x] 1.1 Add `internal/daemon/pidfile.go` with `AcquirePIDLock(path string) (*PIDLock, error)` using `syscall.Flock(fd, LOCK_EX|LOCK_NB)`
- [x] 1.2 `AcquirePIDLock` writes the current PID into the (truncated) file on success, for display purposes only
- [x] 1.3 `AcquirePIDLock` returns a clear "already running" error naming the recorded PID and pidfile path on lock failure
- [x] 1.4 Add `(*PIDLock).Release() error` — unlocks, closes the fd, removes the pidfile
- [x] 1.5 Add `ProbePIDFile(path string) (PIDFileStatus, error)` — non-blocking lock attempt to determine `Exists`/`Running`/`PID` without holding the lock afterward

## 2. Wire into run.go

- [x] 2.1 Remove `writePidfile()` and its `O_EXCL` + `signal(0)` logic from `internal/daemon/run.go`
- [x] 2.2 `Run()` calls `AcquirePIDLock(d.Cfg.PIDPath())` after `Start()` succeeds, in place of `writePidfile`
- [x] 2.3 `Run()` defers `lock.Release()` in place of the old `defer os.Remove(d.Cfg.PIDPath())`
- [x] 2.4 Clean up now-unused imports (`errors`, `strconv`, `strings`, `syscall`) in `run.go`

## 3. Fix status and stop

- [x] 3.1 `cmd/plannotator-argus/status.go` calls `daemon.ProbePIDFile` instead of `os.FindProcess` + `signal(0)`
- [x] 3.2 `cmd/plannotator-argus/stop.go` calls `daemon.ProbePIDFile` to decide whether to signal at all, and to poll for exit, instead of `signal(0)`
- [x] 3.3 Remove/replace the false comment in `stop.go` ("Avoids signalling an unrelated process if the PID has been recycled") with an accurate description of the lock-based guarantee

## 4. Tests

- [x] 4.1 `internal/daemon`: stale pidfile recording a PID that is alive but does not hold the lock (simulated PID reuse) → `AcquirePIDLock` succeeds
- [x] 4.2 `internal/daemon`: a genuinely running daemon (holding the lock) → a second `AcquirePIDLock` on the same path fails with "already running"
- [x] 4.3 `internal/daemon`: `ProbePIDFile` reports not-running for a stale/unlocked pidfile and running for a locked one
- [x] 4.4 `internal/daemon`: `Run()` end-to-end still refuses a second concurrent start and recovers from a stale pidfile (update/replace `TestWritePidfileRefusesLivePidfile` / `TestWritePidfileRemovesStalePidfile`)
- [x] 4.5 `cmd/plannotator-argus`: `status` reports "not running" (not "running") against a pidfile whose recorded PID is alive but unlocked (PID-reuse simulation), and "running" against one whose daemon actually holds the lock
- [x] 4.6 `cmd/plannotator-argus`: `stop` against a pidfile whose recorded PID is alive but unlocked never sends SIGTERM to that process (assert the process survives) and reports the stale-pidfile outcome instead

## 5. Verification and hygiene

- [x] 5.1 `go build ./...` and `go vet ./...` pass
- [x] 5.2 `go test ./...` passes
- [x] 5.3 `openspec validate fix-pidfile-liveness --strict` passes
- [x] 5.4 Commit deltas, tests, and code together; open a PR via `iris_gh_pr_create` (do not merge) — https://github.com/anutron/plannotator_argus/pull/5
