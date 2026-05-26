package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withDiscovery swaps the package-level discovery seam for the duration of
// a test. Returns a restore func registered on t.Cleanup. Tests use this
// instead of standing up a real fake daemon for every assertion about
// Default()'s wiring.
func withDiscovery(t *testing.T, fn func(ctx context.Context, timeout time.Duration) (string, bool)) {
	t.Helper()
	old := discoverArgusBaseURL
	discoverArgusBaseURL = fn
	t.Cleanup(func() { discoverArgusBaseURL = old })
}

func mustDefault(t *testing.T) *Config {
	t.Helper()
	// Stub discovery off by default so test runs on hosts with a live
	// argus daemon don't make real RPC calls during config tests that
	// don't care about ArgusBaseURL.
	withDiscovery(t, func(context.Context, time.Duration) (string, bool) { return "", false })
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDefault(t *testing.T) {
	// Stub discovery out so this baseline test does not depend on whether
	// a real argus daemon socket happens to be present on the host.
	withDiscovery(t, func(context.Context, time.Duration) (string, bool) { return "", false })
	t.Setenv("PLANNOTATOR_ARGUS_BASE_URL", "")
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

// TestDefault_DiscoveryFailureFallsBack covers spec scenarios "Daemon.Ports
// discovery is unavailable, falls back to default" and "Discovery failure
// is not fatal". When the env var is unset and discovery returns ok=false,
// Default must populate the hardcoded fallback URL and not error.
func TestDefault_DiscoveryFailureFallsBack(t *testing.T) {
	t.Setenv("PLANNOTATOR_ARGUS_BASE_URL", "")
	withDiscovery(t, func(context.Context, time.Duration) (string, bool) { return "", false })

	c, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if c.ArgusBaseURL != "http://127.0.0.1:7743" {
		t.Errorf("ArgusBaseURL = %q, want hardcoded fallback http://127.0.0.1:7743", c.ArgusBaseURL)
	}
}

// TestDefault_DiscoverySuccess covers spec scenario "Daemon.Ports
// discovery succeeds". When the env var is unset and discovery returns a
// URL, Default must use it.
func TestDefault_DiscoverySuccess(t *testing.T) {
	t.Setenv("PLANNOTATOR_ARGUS_BASE_URL", "")
	withDiscovery(t, func(context.Context, time.Duration) (string, bool) {
		return "http://127.0.0.1:7841", true
	})

	c, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if c.ArgusBaseURL != "http://127.0.0.1:7841" {
		t.Errorf("ArgusBaseURL = %q, want discovered http://127.0.0.1:7841", c.ArgusBaseURL)
	}
}

// TestDefault_EnvOverrideSkipsDiscovery covers spec scenario "Env override
// wins unconditionally". When PLANNOTATOR_ARGUS_BASE_URL is set, Default
// must not invoke discovery at all, and LoadFromEnv must then copy the
// env value onto the Config.
func TestDefault_EnvOverrideSkipsDiscovery(t *testing.T) {
	t.Setenv("PLANNOTATOR_ARGUS_BASE_URL", "http://127.0.0.1:9999")

	discoveryCalled := false
	withDiscovery(t, func(context.Context, time.Duration) (string, bool) {
		discoveryCalled = true
		return "http://127.0.0.1:7841", true
	})

	c, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if discoveryCalled {
		t.Errorf("discovery was called despite env override")
	}
	// Default leaves the fallback URL on the struct; LoadFromEnv applies
	// the override. Together they match the operator's pin.
	if err := c.LoadFromEnv(); err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if c.ArgusBaseURL != "http://127.0.0.1:9999" {
		t.Errorf("ArgusBaseURL after LoadFromEnv = %q, want http://127.0.0.1:9999", c.ArgusBaseURL)
	}
}

// TestDefault_DiscoveryBoundedByTimeout verifies the discovery seam is
// invoked with the spec-mandated 500 ms timeout so callers cannot
// inadvertently widen the startup-blocking window.
func TestDefault_DiscoveryBoundedByTimeout(t *testing.T) {
	t.Setenv("PLANNOTATOR_ARGUS_BASE_URL", "")

	var gotTimeout time.Duration
	withDiscovery(t, func(_ context.Context, timeout time.Duration) (string, bool) {
		gotTimeout = timeout
		return "", false
	})

	if _, err := Default(); err != nil {
		t.Fatalf("Default: %v", err)
	}
	if gotTimeout != 500*time.Millisecond {
		t.Errorf("discovery timeout = %v, want 500ms", gotTimeout)
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
