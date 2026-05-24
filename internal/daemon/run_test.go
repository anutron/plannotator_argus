package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anutron/plannotator_argus/internal/config"
)

// stubArgus returns an httptest.Server that records registration and
// unregistration calls.
type stubArgus struct {
	mu       sync.Mutex
	posts    []string
	deletes  []string
	server   *httptest.Server
}

func newStubArgus(t *testing.T) *stubArgus {
	t.Helper()
	s := &stubArgus{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.Method {
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var reg struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(body, &reg)
			s.posts = append(s.posts, reg.Name)
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			path := r.URL.Path
			// path is /api/mcp/tools/<name>
			s.deletes = append(s.deletes, filepath.Base(path))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	return s
}

func (s *stubArgus) postedNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.posts))
	copy(out, s.posts)
	return out
}

func (s *stubArgus) deletedNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.deletes))
	copy(out, s.deletes)
	return out
}

func writePlannotatorStub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "plannotator")
	script := `#!/bin/bash
case "$1" in --version) echo "stub"; exit 0;; esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func setupCfg(t *testing.T, argusURL, plannotatorBin string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = dir
	cfg.ArgusTokenPath = filepath.Join(dir, "scope-token")
	cfg.HookTokenPath = filepath.Join(dir, "hook-token")
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.MCPHeartbeat = time.Hour // effectively disable heartbeat in tests
	cfg.PlannotatorBin = plannotatorBin
	cfg.ArgusBaseURL = argusURL
	if err := os.WriteFile(cfg.ArgusTokenPath, []byte("scope-token-xyz"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDaemonStartsRegistersAllFiveTools(t *testing.T) {
	stub := newStubArgus(t)
	defer stub.server.Close()
	bin := writePlannotatorStub(t)
	cfg := setupCfg(t, stub.server.URL, bin)

	ctx := context.Background()
	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())

	names := stub.postedNames()
	want := map[string]bool{
		"plannotator_annotate":       true,
		"plannotator_review":         true,
		"plannotator_setup_goal":     true,
		"plannotator_last":           true,
		"plannotator_session_result": true,
	}
	if len(names) != len(want) {
		t.Errorf("registered %d tools, want %d: %v", len(names), len(want), names)
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected registration: %q", n)
		}
	}
}

func TestDaemonUnregistersOnStop(t *testing.T) {
	stub := newStubArgus(t)
	defer stub.server.Close()
	bin := writePlannotatorStub(t)
	cfg := setupCfg(t, stub.server.URL, bin)

	ctx := context.Background()
	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.Stop(context.Background())

	deleted := stub.deletedNames()
	if len(deleted) != 5 {
		t.Errorf("deleted %d tools, want 5: %v", len(deleted), deleted)
	}
}

func TestDaemonHookEndpointReachable(t *testing.T) {
	stub := newStubArgus(t)
	defer stub.server.Close()
	bin := writePlannotatorStub(t)
	cfg := setupCfg(t, stub.server.URL, bin)

	ctx := context.Background()
	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop(context.Background())

	// Read the persistent hook token the daemon created.
	tok, err := os.ReadFile(cfg.HookTokenPath)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "http://"+d.MCPServer.Addr()+"/hook", nil)
	req.Header.Set("Authorization", "Bearer "+string(tok[:len(tok)-1]))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// We sent empty body to a stub that does nothing → exit 0 → 200 with empty stdout.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDaemonStopKillsInFlightSubprocesses(t *testing.T) {
	stub := newStubArgus(t)
	defer stub.server.Close()

	// Stub plannotator that sleeps until SIGKILL. Records its PID so the
	// test can verify it really did die.
	dir := t.TempDir()
	bin := filepath.Join(dir, "plannotator")
	pidFile := filepath.Join(dir, "running.pid")
	script := `#!/bin/bash
case "$1" in --version) echo "stub"; exit 0;; esac
echo $$ > ` + pidFile + `
sleep 60
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := setupCfg(t, stub.server.URL, bin)

	ctx := context.Background()
	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Drive a session via the annotate handler. Need a writable cwd with a
	// real file because ResolvePath rejects traversal.
	docDir := t.TempDir()
	doc := filepath.Join(docDir, "doc.md")
	if err := os.WriteFile(doc, []byte("# hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Use the runner to spawn a long-running stub child directly through
	// the daemon's parent context so we exercise the kill-on-shutdown path
	// without needing to drive the MCP HTTP server end-to-end.
	_ = docDir // kept for symmetry with the original handler invocation idea
	cmd := d.Runner.Spawn(d.parentCtx, []string{"sleep-test"})
	if err := cmd.Start(); err != nil {
		d.Stop(ctx)
		t.Fatal(err)
	}
	childPID := cmd.Process.Pid
	d.sessionWG.Add(1)
	go func() {
		defer d.sessionWG.Done()
		_ = cmd.Wait()
	}()

	// Give the stub time to write its pid file (best effort).
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	d.Stop(context.Background())
	if elapsed := time.Since(start); elapsed > SessionShutdownGrace+1*time.Second {
		t.Errorf("Stop took %v, want <= %v", elapsed, SessionShutdownGrace+time.Second)
	}

	// Confirm the child process is gone.
	if proc, err := os.FindProcess(childPID); err == nil {
		if err := proc.Signal(syscall.Signal(0)); err == nil {
			t.Errorf("subprocess pid %d still alive after Stop", childPID)
		}
	}
}

func TestDaemonFailsFastOn401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	bin := writePlannotatorStub(t)
	cfg := setupCfg(t, srv.URL, bin)

	_, err := Start(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("expected fail-fast on 401")
	}
}
