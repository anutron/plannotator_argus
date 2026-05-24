package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.ArgusBaseURL == "" || !strings.HasPrefix(c.ArgusBaseURL, "http") {
		t.Errorf("ArgusBaseURL bad: %q", c.ArgusBaseURL)
	}
	if c.ListenAddr == "" {
		t.Errorf("ListenAddr empty")
	}
	if c.MCPHeartbeat != 5*time.Minute {
		t.Errorf("MCPHeartbeat = %v, want 5m", c.MCPHeartbeat)
	}
	if c.SessionTTL != 10*time.Minute {
		t.Errorf("SessionTTL = %v, want 10m", c.SessionTTL)
	}
}

func TestLoadFromEnv(t *testing.T) {
	c := Default()
	t.Setenv("PLANNOTATOR_LISTEN_ADDR", "127.0.0.1:9999")
	t.Setenv("PLANNOTATOR_MCP_HEARTBEAT", "30s")
	if err := c.LoadFromEnv(); err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if c.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("ListenAddr = %q", c.ListenAddr)
	}
	if c.MCPHeartbeat != 30*time.Second {
		t.Errorf("MCPHeartbeat = %v", c.MCPHeartbeat)
	}
}

func TestLoadFromEnvBadDuration(t *testing.T) {
	c := Default()
	t.Setenv("PLANNOTATOR_MCP_HEARTBEAT", "not-a-duration")
	if err := c.LoadFromEnv(); err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestLoadScopeTokenMissing(t *testing.T) {
	dir := t.TempDir()
	c := Default()
	c.ArgusTokenPath = filepath.Join(dir, "missing-token")
	_, err := c.LoadScopeToken()
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "argus token mint") {
		t.Errorf("expected mint hint in error, got: %v", err)
	}
}

func TestLoadScopeTokenEmpty(t *testing.T) {
	dir := t.TempDir()
	c := Default()
	c.ArgusTokenPath = filepath.Join(dir, "empty-token")
	if err := os.WriteFile(c.ArgusTokenPath, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := c.LoadScopeToken()
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestLoadScopeTokenSuccess(t *testing.T) {
	dir := t.TempDir()
	c := Default()
	c.ArgusTokenPath = filepath.Join(dir, "token")
	if err := os.WriteFile(c.ArgusTokenPath, []byte("  abcdef123  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := c.LoadScopeToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "abcdef123" {
		t.Errorf("LoadScopeToken = %q, want %q", tok, "abcdef123")
	}
}

func TestLoadOrCreateHookTokenCreatesOnce(t *testing.T) {
	dir := t.TempDir()
	c := Default()
	c.HookTokenPath = filepath.Join(dir, "hook-token")

	tok1, err := c.LoadOrCreateHookToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok1) < 32 {
		t.Errorf("token too short: %q", tok1)
	}
	// File must exist with mode 0600.
	info, err := os.Stat(c.HookTokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("hook token mode = %v, want 0600", info.Mode().Perm())
	}

	// Second call must return the same token (preserved).
	tok2, err := c.LoadOrCreateHookToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok1 != tok2 {
		t.Errorf("token regenerated on second call: %q != %q", tok1, tok2)
	}
}
