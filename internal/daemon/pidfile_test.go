package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAcquirePIDLockSucceedsDespiteRecycledPID simulates the production
// incident directly: a stale pidfile names a PID that is alive (here, the
// test process itself) but the process holding that PID number never
// acquired the pidfile's lock — exactly what happens when a PID number is
// reassigned to an unrelated process (Dropbox, in the incident) after an
// unclean previous exit. Starting must succeed.
func TestAcquirePIDLockSucceedsDespiteRecycledPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "argus-plugin.pid")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}

	lock, err := AcquirePIDLock(path)
	if err != nil {
		t.Fatalf("AcquirePIDLock should succeed despite a live-but-unrelated recorded PID, got %v", err)
	}
	defer lock.Release()

	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), fmt.Sprintf("%d", os.Getpid())) {
		t.Errorf("pidfile not rewritten with current pid; got %q", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// TestAcquirePIDLockRefusesWhenAlreadyHeld covers the genuine-daemon case: a
// second start attempt while the first still holds the lock must refuse.
func TestAcquirePIDLockRefusesWhenAlreadyHeld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "argus-plugin.pid")

	lock, err := AcquirePIDLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	_, err = AcquirePIDLock(path)
	if err == nil {
		t.Fatal("expected a second AcquirePIDLock to refuse while the first is held")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("err = %v, want 'already running' message", err)
	}
}

func TestPIDLockReleaseUnlocksAndRemoves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "argus-plugin.pid")

	lock, err := AcquirePIDLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected pidfile removed after Release, stat err = %v", err)
	}

	// The lock must be free again — a fresh Acquire should succeed.
	lock2, err := AcquirePIDLock(path)
	if err != nil {
		t.Fatalf("expected lock to be free after Release, got %v", err)
	}
	lock2.Release()
}

func TestProbePIDFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "argus-plugin.pid")

	st, err := ProbePIDFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Exists || st.Running {
		t.Errorf("st = %+v, want Exists=false Running=false", st)
	}
}

// TestProbePIDFileRecycledPIDReportsNotRunning is the status/stop-facing
// equivalent of the recycled-PID scenario above: a live-but-unrelated PID
// recorded in the file must never read as "running".
func TestProbePIDFileRecycledPIDReportsNotRunning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "argus-plugin.pid")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := ProbePIDFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || st.Running {
		t.Errorf("st = %+v, want Exists=true Running=false (recorded pid is alive but not the lock holder)", st)
	}
	if st.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", st.PID, os.Getpid())
	}

	// The probe must not leave the file locked behind it.
	lock, err := AcquirePIDLock(path)
	if err != nil {
		t.Fatalf("probe left the pidfile locked: %v", err)
	}
	lock.Release()
}

func TestProbePIDFileRunningReportsTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "argus-plugin.pid")

	lock, err := AcquirePIDLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	st, err := ProbePIDFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || !st.Running {
		t.Errorf("st = %+v, want Exists=true Running=true", st)
	}
	if st.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", st.PID, os.Getpid())
	}
}
