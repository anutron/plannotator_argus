package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anutron/plannotator_argus/internal/daemon"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestStatusReportsNotRunningWhenNoPidfile(t *testing.T) {
	t.Setenv("PLANNOTATOR_STATE_DIR", t.TempDir())

	out := captureStdout(t, func() {
		if err := newStatusCmd().RunE(nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "not running" {
		t.Errorf("out = %q, want %q", out, "not running")
	}
}

// TestStatusReportsNotRunningForRecycledPID is the status-facing version of
// the incident: the pidfile names a PID that is genuinely alive (a spawned
// process, standing in for the reassigned Dropbox PID) but that process
// never acquired the pidfile's lock. status must not report "running".
func TestStatusReportsNotRunningForRecycledPID(t *testing.T) {
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

	out := captureStdout(t, func() {
		if err := newStatusCmd().RunE(nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "not running") {
		t.Errorf("out = %q, want it to report not running despite a live, unrelated process at the recorded pid", out)
	}
}

func TestStatusReportsRunningWhenLockHeld(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLANNOTATOR_STATE_DIR", dir)
	pidPath := filepath.Join(dir, "argus-plugin.pid")

	lock, err := daemon.AcquirePIDLock(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	out := captureStdout(t, func() {
		if err := newStatusCmd().RunE(nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "running (pid=") || strings.Contains(out, "not running") {
		t.Errorf("out = %q, want it to report running", out)
	}
}
