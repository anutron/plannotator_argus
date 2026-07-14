package daemon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// PIDLock is an OS-level advisory lock (flock) held on the daemon's pidfile
// for its lifetime. Unlike a PID-number-based liveness check, the kernel
// releases this lock automatically on process exit — clean or crashed — so
// a stale pidfile can never be mistaken for a live daemon, and a PID number
// later recycled by an unrelated process can never be mistaken for one
// either.
type PIDLock struct {
	f    *os.File
	path string
}

// AcquirePIDLock creates (or opens) the pidfile at path and takes a
// non-blocking exclusive flock on it. On success the current PID is written
// into the file for user-facing display only (status/stop read it); the
// returned PIDLock must be held for the daemon's lifetime and released via
// Release. On failure, another process already holds the lock — a genuine
// running daemon, not merely a process that happens to share the recorded
// PID number.
func AcquirePIDLock(path string) (*PIDLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open pidfile %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		pid := readPID(f)
		f.Close()
		if pid > 0 {
			return nil, fmt.Errorf("another plannotator-argus daemon is already running (pid=%d, pidfile=%s)", pid, path)
		}
		return nil, fmt.Errorf("another plannotator-argus daemon is already running (pidfile=%s)", path)
	}
	if err := f.Truncate(0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("truncate pidfile %s: %w", path, err)
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("write pidfile %s: %w", path, err)
	}
	return &PIDLock{f: f, path: path}, nil
}

// Release unlocks the pidfile, removes it, and closes the descriptor. Safe
// to call on a nil *PIDLock.
func (l *PIDLock) Release() error {
	if l == nil {
		return nil
	}
	defer l.f.Close()
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	return os.Remove(l.path)
}

// PIDFileStatus is the result of probing a pidfile's liveness.
type PIDFileStatus struct {
	Exists  bool // the pidfile exists on disk
	Running bool // its advisory lock is held by a live process
	PID     int  // best-effort PID recorded in the file, for display only; 0 if unreadable
}

// ProbePIDFile reports whether the daemon holding path's advisory lock is
// alive. Liveness is decided purely by whether the flock is held — never by
// whether some process happens to exist at the recorded PID number — so a
// stale pidfile whose PID number has been reassigned to an unrelated
// process is correctly reported as not running.
func ProbePIDFile(path string) (PIDFileStatus, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return PIDFileStatus{}, nil
	}
	if err != nil {
		return PIDFileStatus{}, fmt.Errorf("open pidfile %s: %w", path, err)
	}
	defer f.Close()
	pid := readPID(f)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return PIDFileStatus{Exists: true, Running: true, PID: pid}, nil
	}
	// We were able to take the lock, meaning nothing holds it — release it
	// immediately since this call is only a probe.
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return PIDFileStatus{Exists: true, Running: false, PID: pid}, nil
}

// readPID best-effort parses the PID recorded in f, for display purposes
// only — it is never used to determine liveness.
func readPID(f *os.File) int {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	return pid
}
