## Context

Three call sites share the same PID-liveness pattern: read a PID number out of `~/.plannotator/argus-plugin.pid`, call `os.FindProcess` (a no-op on Unix — it always succeeds), then `proc.Signal(syscall.Signal(0))` to check "does a process with this PID number exist." `signal(0)` answers exactly that question and nothing more — it cannot distinguish plannotator-argus from any other process that happens to hold the same PID number after the original process exited and the OS recycled the number. That gap caused today's incident: an unclean previous exit left a stale pidfile, macOS reassigned the PID to Dropbox, and every subsequent `start`, `status`, or `stop` treated Dropbox as "the daemon."

## Goals / Non-Goals

**Goals:**

- Liveness detection that cannot be fooled by PID reuse, in `run.go`'s startup guard, `status`, and `stop`.
- No new dependency — the daemon deploys to macOS via launchd; the fix must work with Go's stdlib on darwin.
- Preserve existing CLI output shapes (`running (pid=<n>, since=<ts>)`, `not running`, stale-pidfile messaging) so operators and the base spec's scenarios don't need to change beyond the detection mechanism.
- Correct the false safety claim in `stop.go`'s comment.

**Non-Goals:**

- Not building a general-purpose process-supervision library. This is a single pidfile's liveness check, used in three places in one binary.
- Not changing `start`/`stop`/`status` CLI flags, output format, or the pidfile's path/permissions.
- Not addressing background daemonization (still explicitly out of scope per the existing "Start without --foreground" scenario).

## Decisions

### D1. `flock(LOCK_EX|LOCK_NB)` on the pidfile itself, held for the daemon's lifetime

`internal/daemon/pidfile.go` adds `AcquirePIDLock(path)`, which opens (creating if needed) the pidfile and attempts a non-blocking exclusive `flock`. Success means no other process holds the lock: it truncates the file, writes the current PID for display purposes, and returns a `*PIDLock` the daemon holds until shutdown. Failure means another process — genuinely, not just a matching PID number — already holds the lock, so `start` refuses.

The kernel releases an `flock` automatically when the holding process exits for any reason, including SIGKILL or a crash, without requiring cleanup code to run. This is exactly the property `signal(0)` cannot provide: liveness stops depending on PID *numbers* and starts depending on whether the file descriptor's lock is actually held by a live holder, which the kernel itself guarantees is race-free.

**Alternatives considered:**

- **Compare `/proc`/`sysctl` process start time or command name against the recorded PID.** Rejected — no portable, dependency-free way to read a process's start time or argv on macOS from Go's stdlib; would need cgo or a third-party library (`gopsutil`) for a one-file fix.
- **Verify with `kill -0` plus a secondary check (e.g., re-read `/proc/<pid>/comm`).** Rejected for the same portability reason — macOS has no `/proc`.
- **Use a Unix domain socket as the liveness sentinel instead of a pidfile.** Rejected — bigger surface change than the bug warrants; `flock` reuses the existing pidfile path and requires no new listener.

### D2. `status`/`stop` probe liveness with their own non-blocking `flock` attempt, not by holding the daemon's lock

`ProbePIDFile(path)` opens the pidfile (if it exists), reads the recorded PID for display, then attempts the same non-blocking `flock`. If the attempt *succeeds*, nothing holds the lock, so the recorded PID is stale (or was never a live daemon) — `ProbePIDFile` releases the lock immediately (it was only a probe) and reports "not running." If the attempt *fails* (`EWOULDBLOCK`), the real daemon holds the lock, so it's reported as running, using the recorded PID for display only.

`stop` uses this same probe to decide whether to `SIGTERM` at all. Because the lock proves the target process is the daemon (not just "a process with this PID number"), signalling the recorded PID afterward is now actually safe — unlike the old comment's false claim. `stop` then polls the same probe (rather than `signal(0)`) to detect when the daemon has exited, since the kernel drops the lock on exit regardless of how the process died.

**Alternatives considered:**

- **Have `status`/`stop` try to acquire and hold the lock like the daemon does.** Rejected — that would let a `status` call transiently block a legitimate `start` racing it, and serves no purpose beyond a probe; a probe should release immediately.

### D3. Comment/message correction in `stop.go`

The existing comment ("Verify the target is alive before SIGTERM'ing. Avoids signalling an unrelated process if the PID has been recycled.") is removed since it describes what `signal(0)` doesn't actually guarantee. It's replaced with a comment describing what the flock check *does* guarantee — that the recorded PID is confirmed via the lock to belong to the actual daemon.

## Risks / Trade-offs

- **[Risk]** A pidfile on a filesystem that doesn't support `flock` (e.g., some network filesystems) would make the lock a no-op or error unpredictably. → **Mitigation**: the daemon's state directory (`~/.plannotator`) is a local macOS home-directory path in every supported deployment; not a concern in practice, and `AcquirePIDLock` surfaces any `flock` error rather than silently ignoring it.
- **[Risk]** Removing `writePidfile` and its two tests changes the daemon package's public surface (new exported `AcquirePIDLock`/`ProbePIDFile`). → **Mitigation**: these are internal implementation helpers within `internal/daemon`, not part of any external API; the CLI-facing contract (`start`/`stop`/`status` output) is unchanged.

## Migration Plan

No data migration. Existing stale pidfiles from before this change (plain PID-number files with no lock) are handled correctly by the new code on first use: `AcquirePIDLock` opens the same path, and since nothing holds a lock on it, it acquires cleanly and overwrites the contents — exactly the "stale pidfile" case this fix targets.
