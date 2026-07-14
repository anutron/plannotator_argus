package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anutron/plannotator_argus/internal/daemon"
)

// pidHolderHelperEnv/PidfileEnv drive a re-exec of this test binary into
// "pidfile lock holder" mode (the standard Go stdlib trick used by
// os/exec_test.go for spawning a real, controllable child process). This
// lets TestStopTerminatesRunningDaemon signal an actual process that holds
// the pidfile's advisory lock, rather than faking liveness.
const (
	pidHolderHelperEnv = "PLANNOTATOR_TEST_PID_HOLDER"
	pidHolderPathEnv   = "PLANNOTATOR_TEST_PID_HOLDER_PATH"
)

func TestMain(m *testing.M) {
	if os.Getenv(pidHolderHelperEnv) == "1" {
		runPIDHolderHelper()
		return
	}
	os.Exit(m.Run())
}

// runPIDHolderHelper acquires the pidfile lock at the path named by
// pidHolderPathEnv and blocks until SIGTERM, simulating a genuinely running
// daemon that `stop` must be able to terminate.
func runPIDHolderHelper() {
	lock, err := daemon.AcquirePIDLock(os.Getenv(pidHolderPathEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer lock.Release()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	<-ch
}

// TestStopNeverSignalsRecycledPID is the stop-facing version of the
// incident's most dangerous consequence: the old signal(0) check would send
// SIGTERM to whatever process now holds a recycled PID number. Here the
// pidfile names a genuinely alive process that never acquired the lock;
// stop must refuse to signal it.
func TestStopNeverSignalsRecycledPID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLANNOTATOR_STATE_DIR", dir)

	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	pidPath := filepath.Join(dir, "argus-plugin.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}

	err := newStopCmd().RunE(nil, nil)
	if err == nil {
		t.Fatal("expected stop to report an error for a stale, unlocked pidfile")
	}
	if strings.Contains(err.Error(), "did not exit within") {
		t.Fatalf("stop appears to have signalled the recycled pid: %v", err)
	}

	// The process must still be alive — stop must never have sent it
	// SIGTERM.
	if procErr := cmd.Process.Signal(syscall.Signal(0)); procErr != nil {
		t.Errorf("recycled-pid process is gone; stop must have signalled it: %v", procErr)
	}
}

// TestStopTerminatesRunningDaemon covers the "genuinely running daemon"
// side of the fix: stop must still be able to terminate a real daemon that
// actually holds the pidfile's lock.
func TestStopTerminatesRunningDaemon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLANNOTATOR_STATE_DIR", dir)
	pidPath := filepath.Join(dir, "argus-plugin.pid")

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(exe, "-test.run=^$")
	helper.Env = append(os.Environ(),
		pidHolderHelperEnv+"=1",
		pidHolderPathEnv+"="+pidPath,
	)
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, err := daemon.ProbePIDFile(pidPath); err == nil && st.Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st, err := daemon.ProbePIDFile(pidPath); err != nil || !st.Running {
		t.Fatalf("helper process never acquired the pidfile lock (st=%+v, err=%v)", st, err)
	}

	if err := newStopCmd().RunE(nil, nil); err != nil {
		t.Fatalf("stop failed against a genuinely running daemon: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- helper.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("helper process did not exit after stop")
	}

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("expected pidfile removed after stop, stat err = %v", err)
	}
}
