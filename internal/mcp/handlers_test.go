package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anutron/plannotator_argus/internal/plannotator"
)

func newTestDeps(t *testing.T) (*HandlerDeps, string) {
	t.Helper()
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stub plannotator: writes a session file with a URL and prints JSON on stdout.
	stub := filepath.Join(dir, "plannotator")
	script := fmt.Sprintf(`#!/bin/bash
case "$1" in
  --version) echo "stub 0.0"; exit 0;;
esac
# Honor STUB_DELAY for tests that need to observe pending state.
if [[ -n "$STUB_DELAY" ]]; then sleep "$STUB_DELAY"; fi
# Write a session file with a deterministic URL so DiscoverSessionURL succeeds.
PORT="${STUB_PORT:-9000}"
cat > %q/$$.json <<EOF
{"pid": $$, "port": $PORT, "url": "http://localhost:$PORT", "mode": "annotate", "startedAt": "2026-01-01T00:00:00Z"}
EOF
if [[ -n "$STUB_STDERR" ]]; then printf '%%s' "$STUB_STDERR" >&2; fi
printf '%%s' "${STUB_STDOUT:-{\"ok\":true\}}"
exit "${STUB_EXIT:-0}"
`, sessionsDir)
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &plannotator.Runner{BinaryPath: stub, SessionsDir: sessionsDir}
	deps := &HandlerDeps{
		Runner:  runner,
		Store:   NewSessionStore(),
		Log:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		URLPoll: 2 * time.Second,
	}
	return deps, dir
}

func TestAnnotateHandlerHappyPath(t *testing.T) {
	deps, dir := newTestDeps(t)
	t.Setenv("STUB_STDOUT", `{"annotations":["a"]}`)
	cwd := dir
	_ = os.WriteFile(filepath.Join(cwd, "doc.md"), []byte("# hi"), 0o600)

	h := AnnotateHandler(deps)
	input := []byte(fmt.Sprintf(`{"cwd":%q,"path":"doc.md"}`, cwd))
	out, err := h.Handle(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	env := out.(sessionEnvelope)
	if env.SessionID == "" {
		t.Error("SessionID empty")
	}
	if env.Status != string(StatusPending) && env.Status != string(StatusComplete) {
		t.Errorf("Status = %q", env.Status)
	}

	// Wait for completion; poll session_result.
	resultH := SessionResultHandler(deps)
	resInput := []byte(fmt.Sprintf(`{"cwd":%q,"session_id":%q,"wait_seconds":5}`, cwd, env.SessionID))
	resAny, err := resultH.Handle(context.Background(), resInput)
	if err != nil {
		t.Fatal(err)
	}
	res := resAny.(map[string]any)
	if res["status"] != string(StatusComplete) {
		t.Errorf("status = %v, want complete", res["status"])
	}
	if res["result"] == nil {
		t.Errorf("result missing: %v", res)
	}
}

func TestAnnotateHandlerRejectsTraversal(t *testing.T) {
	deps, dir := newTestDeps(t)
	input := []byte(fmt.Sprintf(`{"cwd":%q,"path":"../../etc/passwd"}`, dir))
	_, err := AnnotateHandler(deps).Handle(context.Background(), input)
	if err == nil {
		t.Error("expected traversal error")
	}
}

func TestAnnotateHandlerRequiresCwdAndPath(t *testing.T) {
	deps, _ := newTestDeps(t)
	if _, err := AnnotateHandler(deps).Handle(context.Background(), []byte(`{}`)); err == nil {
		t.Error("expected error for missing cwd")
	}
	if _, err := AnnotateHandler(deps).Handle(context.Background(), []byte(`{"cwd":"/tmp"}`)); err == nil {
		t.Error("expected error for missing path")
	}
}

func TestSetupGoalRejectsBadMode(t *testing.T) {
	deps, dir := newTestDeps(t)
	input := []byte(fmt.Sprintf(`{"cwd":%q,"mode":"nope","bundle_path":"a.json"}`, dir))
	_, err := SetupGoalHandler(deps).Handle(context.Background(), input)
	if err == nil {
		t.Error("expected error for bad mode")
	}
}

func TestReviewHandlerAcceptsOptionalArgs(t *testing.T) {
	deps, dir := newTestDeps(t)
	t.Setenv("STUB_STDOUT", `null`)
	input := []byte(fmt.Sprintf(`{"cwd":%q,"git":true}`, dir))
	out, err := ReviewHandler(deps).Handle(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	env := out.(sessionEnvelope)
	if env.SessionID == "" {
		t.Error("SessionID empty")
	}
}

func TestLastHandler(t *testing.T) {
	deps, dir := newTestDeps(t)
	out, err := LastHandler(deps).Handle(context.Background(), []byte(fmt.Sprintf(`{"cwd":%q}`, dir)))
	if err != nil {
		t.Fatal(err)
	}
	if out.(sessionEnvelope).SessionID == "" {
		t.Error("SessionID empty")
	}
}

func TestAnnotateHandlerSpawnFailureSurfacesError(t *testing.T) {
	deps, dir := newTestDeps(t)
	// Point the runner at a non-executable path so cmd.Start() fails.
	deps.Runner.BinaryPath = filepath.Join(dir, "does-not-exist")
	_ = os.WriteFile(filepath.Join(dir, "doc.md"), []byte("# hi"), 0o600)

	input := []byte(fmt.Sprintf(`{"cwd":%q,"path":"doc.md"}`, dir))
	out, err := AnnotateHandler(deps).Handle(context.Background(), input)
	if err == nil {
		t.Fatalf("expected spawn error, got out=%v", out)
	}
	if !strings.Contains(err.Error(), "spawn plannotator") {
		t.Errorf("err = %v, want contain spawn plannotator", err)
	}
	// Critically: no session should have been created.
	if deps.Store.Len() != 0 {
		t.Errorf("session store len = %d, want 0 after spawn failure", deps.Store.Len())
	}
}

func TestSessionResultUnknownSession(t *testing.T) {
	deps, dir := newTestDeps(t)
	input := []byte(fmt.Sprintf(`{"cwd":%q,"session_id":"nope"}`, dir))
	_, err := SessionResultHandler(deps).Handle(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("err = %v, want unknown-session error", err)
	}
}

func TestSessionResultRespectsWaitSecondsClamp(t *testing.T) {
	deps, _ := newTestDeps(t)
	sess := deps.Store.Create("annotate")
	// wait_seconds=999 should clamp to 25, but we don't actually wait that
	// long — set up a goroutine to mark complete after 50ms and ensure we
	// get back before any heavy waiting.
	go func() {
		time.Sleep(50 * time.Millisecond)
		deps.Store.MarkComplete(sess.ID, json.RawMessage(`null`))
	}()
	start := time.Now()
	input := []byte(fmt.Sprintf(`{"session_id":%q,"wait_seconds":999}`, sess.ID))
	out, err := SessionResultHandler(deps).Handle(context.Background(), input)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("returned after %v; clamp likely not effective", elapsed)
	}
	if out.(map[string]any)["status"] != string(StatusComplete) {
		t.Errorf("status = %v", out)
	}
}
