## MODIFIED Requirements

### Requirement: Daemon CLI verbs

The daemon SHALL expose `start [--foreground]`, `stop`, and `status` CLI verbs. Liveness of a previously started daemon SHALL be determined by whether an OS-level advisory lock (`flock`) on the pidfile is held, never by whether some process happens to exist at the recorded PID number — a PID number match SHALL NOT by itself be treated as proof that the recorded process is the plannotator-argus daemon.

#### Scenario: Start --foreground brings the daemon up in the current shell

- **WHEN** the user runs `plannotator-argus start --foreground`
- **THEN** the daemon runs in the foreground, logs to stderr, and exits cleanly on SIGINT or SIGTERM

#### Scenario: Start without --foreground returns an explanatory error

- **WHEN** the user runs `plannotator-argus start` without `--foreground`
- **THEN** the binary exits non-zero with a message pointing at `nohup` or launchd for background daemonization

#### Scenario: Start acquires the pidfile lock for its lifetime

- **WHEN** `plannotator-argus start --foreground` succeeds
- **THEN** the daemon holds an exclusive, non-blocking advisory lock (`flock`) on the pidfile at `~/.plannotator/argus-plugin.pid` for as long as it runs, and the kernel releases that lock automatically on process exit — clean or crashed — without depending on cleanup code running

#### Scenario: Stop terminates a running daemon

- **WHEN** the user runs `plannotator-argus stop` and a daemon is running
- **THEN** the binary opens the PID file at `~/.plannotator/argus-plugin.pid`, confirms the daemon is alive via a failed non-blocking lock attempt on that file (proving a live holder, not merely a PID-number match), sends SIGTERM to the recorded PID, waits for the lock to become acquirable (confirming exit), and removes the PID file

#### Scenario: Stop removes a stale PID file without signalling a recycled PID

- **WHEN** the PID file exists but nothing holds its advisory lock (the recorded PID was never the daemon, has since exited, or its PID number has since been reassigned to an unrelated process)
- **THEN** the binary removes the stale PID file and reports the situation, without ever sending SIGTERM — liveness is decided by the lock, not by whether some process currently owns the recorded PID number

#### Scenario: Status reports liveness

- **WHEN** the user runs `plannotator-argus status`
- **THEN** the binary reports `running (pid=<n>, since=<ts>)` (where `<ts>` is the PID file's mtime in RFC3339) if the pidfile's advisory lock is held by a live daemon, or `not running` otherwise — regardless of whether the recorded PID number happens to belong to some other running process

#### Scenario: Concurrent start refuses to clobber a live pidfile

- **WHEN** a second `plannotator-argus start --foreground` runs while a daemon is already running and holding the pidfile's lock
- **THEN** the second invocation's non-blocking lock attempt fails, so it exits with a clear error naming the existing PID and pidfile path, and does NOT overwrite the file

#### Scenario: Start succeeds despite a stale pidfile referencing a live, unrelated process

- **WHEN** the pidfile records a PID number that is alive (for example, reassigned by the OS to an unrelated process after an unclean previous exit) but that process does not hold the pidfile's advisory lock
- **THEN** `plannotator-argus start --foreground` acquires the lock successfully and starts normally, instead of refusing to start because a same-numbered process happens to exist
