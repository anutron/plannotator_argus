## Why

Today's production incident: the daemon's pidfile held a stale PID from an unclean previous exit. macOS later reassigned that PID number to an unrelated process (Dropbox). The liveness check in `writePidfile()`, `status`, and `stop` only verifies "does *some* process hold this PID number" via `os.FindProcess` + `proc.Signal(syscall.Signal(0))` — it never confirms the PID actually belongs to plannotator-argus. `start --foreground` treated the recycled PID as a live daemon and refused to start; launchd's `KeepAlive` kept retrying and kept failing the same false-positive check for hours until a human deleted the stale pidfile by hand. The same flawed check makes `stop` dangerous: it would send `SIGTERM` to whatever process now holds a recycled PID number, and its own comment falsely claims this "avoids signalling an unrelated process if the PID has been recycled."

## What Changes

- Replace the PID-file-plus-`signal(0)` liveness check with an OS-level advisory lock (`syscall.Flock` with `LOCK_EX|LOCK_NB`) on the pidfile itself, in all three call sites that duplicate the flawed pattern.
- `internal/daemon/run.go`: the daemon acquires and holds the flock for its lifetime instead of writing the pidfile with `O_EXCL`; the kernel releases the lock automatically on process exit (clean or crashed), so a stale pidfile from an unclean exit can never be mistaken for a live daemon.
- `cmd/plannotator-argus/status.go` and `cmd/plannotator-argus/stop.go`: determine "is the daemon actually running" via a non-blocking `flock` attempt on the same file (succeeds = nothing running, fails = the real daemon holds it) instead of `signal(0)`, while still reading/displaying the PID from the file for user-facing output.
- Remove the false "avoids signalling an unrelated process if the PID has been recycled" comment in `stop.go` — `signal(0)` never provided that guarantee; the new flock-based check does.
- **BREAKING** (internal only): the unexported `writePidfile` function in `internal/daemon` is replaced by exported `AcquirePIDLock` / `ProbePIDFile` helpers so `cmd/plannotator-argus` can share the same liveness primitive. No CLI-facing behavior changes beyond bug fixes.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `plannotator-argus-plugin`: the "Daemon CLI verbs" requirement's PID-liveness scenarios (`stop`, `status`, concurrent `start`) are modified to specify lock-based liveness detection instead of PID-number-plus-signal(0), closing the PID-reuse false-positive/false-signal hole.

## Impact

- **Changed code**: `internal/daemon/run.go` (`writePidfile` removed), new `internal/daemon/pidfile.go` (`AcquirePIDLock`, `ProbePIDFile`), `cmd/plannotator-argus/status.go`, `cmd/plannotator-argus/stop.go`.
- **No new dependency**: `syscall.Flock` is in Go's stdlib on darwin/linux, which is where this daemon deploys (macOS via launchd).
- **No wire-format or CLI-flag changes.** `start`, `stop`, `status` keep their existing invocations and output shapes; only the underlying liveness primitive changes.
- **Tests**: new coverage in `internal/daemon` (and `cmd/plannotator-argus` where reasonably testable) for stale-but-recycled-PID pidfiles and for genuinely-running daemons, replacing the now-removed `TestWritePidfileRefusesLivePidfile` / `TestWritePidfileRemovesStalePidfile`.
