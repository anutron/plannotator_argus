package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustDefault(t *testing.T) *Config {
	t.Helper()
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDefault(t *testing.T) {
	c := mustDefault(t)
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
	c := mustDefault(t)
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
	c := mustDefault(t)
	t.Setenv("PLANNOTATOR_MCP_HEARTBEAT", "not-a-duration")
	if err := c.LoadFromEnv(); err == nil {
		t.Errorf("expected env-parse error, got nil")
	}
}

func TestLoadScopeTokenMissing(t *testing.T) {
	dir := t.TempDir()
	c := mustDefault(t)
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
	c := mustDefault(t)
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
	c := mustDefault(t)
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
	c := mustDefault(t)
	c.HookTokenPath = filepath.Join(dir, "hook-token")

	tok1, err := c.LoadOrCreateHookToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok1) < 32 {
		t.Errorf("token too short: %q", tok1)
	}
	info, err := os.Stat(c.HookTokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("hook token mode = %v, want 0600", info.Mode().Perm())
	}

	tok2, err := c.LoadOrCreateHookToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok1 != tok2 {
		t.Errorf("token regenerated on second call: %q != %q", tok1, tok2)
	}
}

func TestLoadScopeTokenFromMintOutput(t *testing.T) {
	// Verbatim shape of `argus token mint --scope plannotator` output.
	mint := `id:    2
scope: plannotator
label: plannotator
token: 9ecf80bdd44b6f0d6152c9850adbec5a8cb374073514ad77aeb90c41fe9a9687

Store this token now — it will not be shown again.
`
	dir := t.TempDir()
	c := mustDefault(t)
	c.ArgusTokenPath = filepath.Join(dir, "token")
	if err := os.WriteFile(c.ArgusTokenPath, []byte(mint), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := c.LoadScopeToken()
	if err != nil {
		t.Fatal(err)
	}
	want := "9ecf80bdd44b6f0d6152c9850adbec5a8cb374073514ad77aeb90c41fe9a9687"
	if tok != want {
		t.Errorf("got %q, want %q", tok, want)
	}
}

func TestLoadScopeTokenMultilineWithoutTokenLineRejected(t *testing.T) {
	dir := t.TempDir()
	c := mustDefault(t)
	c.ArgusTokenPath = filepath.Join(dir, "token")
	// Multi-line content with no `token:` line — must be rejected so we
	// don't ship a bearer string that contains embedded newlines.
	if err := os.WriteFile(c.ArgusTokenPath, []byte("abc\ndef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := c.LoadScopeToken()
	if err == nil {
		t.Error("expected error for whitespace-bearing token")
	}
}

func TestLoadScopeTokenRejectsNonPrintable(t *testing.T) {
	dir := t.TempDir()
	c := mustDefault(t)
	c.ArgusTokenPath = filepath.Join(dir, "token")
	if err := os.WriteFile(c.ArgusTokenPath, []byte("token: abc\x01def\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := c.LoadScopeToken()
	if err == nil {
		t.Error("expected error for non-printable byte in token")
	}
}

func TestLoadOrCreateHookTokenMissingDir(t *testing.T) {
	c := mustDefault(t)
	c.HookTokenPath = "/nonexistent/dir/hook-token"
	_, err := c.LoadOrCreateHookToken()
	if err == nil {
		t.Error("expected error for missing parent dir")
	}
}
