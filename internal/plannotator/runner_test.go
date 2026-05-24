package plannotator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeStub installs a tiny shell script that pretends to be plannotator.
// Behavior is controlled by env vars set by the test. The stub:
//
//   - prints $STUB_STDOUT to stdout
//   - prints $STUB_STDERR to stderr
//   - exits with $STUB_EXIT (default 0)
//   - if $STUB_SESSION_FILE is set, writes the path to a fake session file
//     before exiting
func writeStub(t *testing.T, sessionsDir string) string {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "plannotator")
	script := fmt.Sprintf(`#!/bin/bash
[[ -n "$STUB_SESSION_FILE" ]] && mkdir -p %q && echo "$STUB_SESSION_FILE" > %q/$$.json
[[ -n "$STUB_STDOUT" ]] && printf '%%s' "$STUB_STDOUT"
[[ -n "$STUB_STDERR" ]] && printf '%%s' "$STUB_STDERR" >&2
exit ${STUB_EXIT:-0}
`, sessionsDir, sessionsDir)
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub
}

func TestResolveBinOverride(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-bin")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveBin(bin)
	if err != nil {
		t.Fatal(err)
	}
	if got != bin {
		t.Errorf("resolveBin = %q, want %q", got, bin)
	}
}

func TestResolveBinNotFound(t *testing.T) {
	_, err := resolveBin("/path/that/does/not/exist/plannotator")
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestHealthCheckPasses(t *testing.T) {
	sessionsDir := t.TempDir()
	bin := writeStub(t, sessionsDir)
	r := &Runner{BinaryPath: bin, SessionsDir: sessionsDir}
	if err := r.HealthCheck(context.Background()); err != nil {
		t.Errorf("HealthCheck: %v", err)
	}
}

func TestHealthCheckFails(t *testing.T) {
	sessionsDir := t.TempDir()
	bin := writeStub(t, sessionsDir)
	t.Setenv("STUB_EXIT", "2")
	r := &Runner{BinaryPath: bin, SessionsDir: sessionsDir}
	if err := r.HealthCheck(context.Background()); err == nil {
		t.Error("expected error from non-zero exit")
	}
}

func TestRunCapturesStdoutStderrExit(t *testing.T) {
	sessionsDir := t.TempDir()
	bin := writeStub(t, sessionsDir)
	t.Setenv("STUB_STDOUT", `{"hello":"world"}`)
	t.Setenv("STUB_STDERR", "an error")
	t.Setenv("STUB_EXIT", "3")
	r := &Runner{BinaryPath: bin, SessionsDir: sessionsDir}
	res := r.Run(context.Background(), []string{"annotate"}, nil)
	if string(res.Stdout) != `{"hello":"world"}` {
		t.Errorf("Stdout = %q", res.Stdout)
	}
	if string(res.Stderr) != "an error" {
		t.Errorf("Stderr = %q", res.Stderr)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
	if res.Err != nil {
		t.Errorf("Err = %v, want nil (non-zero exit should not produce Err)", res.Err)
	}
}

func TestDiscoverSessionURLPresent(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{SessionsDir: dir}
	pid := 42424
	contents, _ := json.Marshal(map[string]any{"pid": pid, "port": 9001, "url": "http://localhost:9001"})
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	got := r.DiscoverSessionURL(context.Background(), pid, 100*time.Millisecond)
	if got != "http://localhost:9001" {
		t.Errorf("DiscoverSessionURL = %q, want %q", got, "http://localhost:9001")
	}
}

func TestDiscoverSessionURLMissing(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{SessionsDir: dir}
	got := r.DiscoverSessionURL(context.Background(), 9999, 100*time.Millisecond)
	if got != "" {
		t.Errorf("DiscoverSessionURL = %q, want empty", got)
	}
}

func TestDiscoverSessionURLCtxCancel(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{SessionsDir: dir}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	got := r.DiscoverSessionURL(ctx, 9999, 5*time.Second)
	if got != "" {
		t.Errorf("DiscoverSessionURL = %q", got)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("ctx cancel ignored: took %v", time.Since(start))
	}
}
