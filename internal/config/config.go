// Package config holds the plannotator-argus daemon configuration. Defaults
// are picked for a single-user macOS host; everything is overridable via
// environment variables.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is the daemon configuration. All fields have working defaults; the
// only thing the operator must provide is the argus scope token file.
type Config struct {
	ArgusBaseURL    string
	PlannotatorBin  string // empty = "plannotator" on $PATH
	ArgusTokenPath  string
	HookTokenPath   string
	StateDir        string
	ListenAddr      string
	MCPHeartbeat    time.Duration
	SessionTTL      time.Duration
}

// Default returns a Config populated with v1 defaults.
func Default() *Config {
	home, _ := os.UserHomeDir()
	stateDir := filepath.Join(home, ".plannotator")
	return &Config{
		ArgusBaseURL:   "http://127.0.0.1:7743",
		PlannotatorBin: "",
		ArgusTokenPath: filepath.Join(stateDir, "argus-api-token"),
		HookTokenPath:  filepath.Join(stateDir, "argus-plugin-token"),
		StateDir:       stateDir,
		ListenAddr:     "127.0.0.1:7745",
		MCPHeartbeat:   5 * time.Minute,
		SessionTTL:     10 * time.Minute,
	}
}

// PIDPath returns the path of the daemon's PID file.
func (c *Config) PIDPath() string {
	return filepath.Join(c.StateDir, "argus-plugin.pid")
}

// EnsureStateDir creates the state directory with mode 0700.
func (c *Config) EnsureStateDir() error {
	return os.MkdirAll(c.StateDir, 0o700)
}

// LoadFromEnv overrides Config fields from environment variables. Returns an
// error only if a value fails to parse; missing env vars leave defaults.
func (c *Config) LoadFromEnv() error {
	if v := os.Getenv("PLANNOTATOR_ARGUS_BASE_URL"); v != "" {
		c.ArgusBaseURL = v
	}
	if v := os.Getenv("PLANNOTATOR_BIN"); v != "" {
		c.PlannotatorBin = v
	}
	if v := os.Getenv("PLANNOTATOR_ARGUS_TOKEN_PATH"); v != "" {
		c.ArgusTokenPath = v
	}
	if v := os.Getenv("PLANNOTATOR_HOOK_TOKEN_PATH"); v != "" {
		c.HookTokenPath = v
	}
	if v := os.Getenv("PLANNOTATOR_STATE_DIR"); v != "" {
		c.StateDir = v
	}
	if v := os.Getenv("PLANNOTATOR_LISTEN_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("PLANNOTATOR_MCP_HEARTBEAT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("PLANNOTATOR_MCP_HEARTBEAT: %w", err)
		}
		c.MCPHeartbeat = d
	}
	if v := os.Getenv("PLANNOTATOR_SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("PLANNOTATOR_SESSION_TTL: %w", err)
		}
		c.SessionTTL = d
	}
	return nil
}

// LoadScopeToken reads the argus scope token from disk. Returns an error
// with a suggested fix if the file is missing or empty.
func (c *Config) LoadScopeToken() (string, error) {
	raw, err := os.ReadFile(c.ArgusTokenPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("argus scope token missing at %s — mint one with: argus token mint --scope plannotator > %s && chmod 600 %s", c.ArgusTokenPath, c.ArgusTokenPath, c.ArgusTokenPath)
	}
	if err != nil {
		return "", fmt.Errorf("read scope token %s: %w", c.ArgusTokenPath, err)
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return "", fmt.Errorf("scope token file %s is empty", c.ArgusTokenPath)
	}
	return tok, nil
}

// LoadOrCreateHookToken returns the hook-endpoint bearer token, creating it
// on disk (mode 0600) if it does not yet exist.
func (c *Config) LoadOrCreateHookToken() (string, error) {
	if raw, err := os.ReadFile(c.HookTokenPath); err == nil {
		tok := strings.TrimSpace(string(raw))
		if tok != "" {
			return tok, nil
		}
	}
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(c.HookTokenPath, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write hook token %s: %w", c.HookTokenPath, err)
	}
	return tok, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
