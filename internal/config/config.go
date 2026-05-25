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

// Default returns a Config populated with v1 defaults. Errors only if
// the user's home directory can't be resolved (extremely rare; would
// otherwise silently relocate the daemon's state to the cwd).
func Default() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	if home == "" {
		return nil, fmt.Errorf("resolve home dir: $HOME is unset")
	}
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
	}, nil
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

// LoadScopeToken reads the argus scope token from disk. Accepts either a
// bare token (single line) or the full `argus token mint` output (multi-line
// metadata with a `token: <hex>` line). Returns an error with a suggested
// fix if the file is missing, empty, or doesn't contain a parseable token.
func (c *Config) LoadScopeToken() (string, error) {
	raw, err := os.ReadFile(c.ArgusTokenPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("argus scope token missing at %s — mint one with: argus token mint --scope plannotator > %s && chmod 600 %s", c.ArgusTokenPath, c.ArgusTokenPath, c.ArgusTokenPath)
	}
	if err != nil {
		return "", fmt.Errorf("read scope token %s: %w", c.ArgusTokenPath, err)
	}
	tok, err := parseScopeToken(string(raw))
	if err != nil {
		return "", fmt.Errorf("%s: %w", c.ArgusTokenPath, err)
	}
	return tok, nil
}

// parseScopeToken extracts a token from either a bare-token file or the
// full `argus token mint` output. The mint command's output looks like:
//
//	id:    7
//	scope: plannotator
//	label: plannotator
//	token: 9ecf80bdd44b6f0d...
//
//	Store this token now ...
//
// If a `token: <value>` line is present, its value is returned. Otherwise
// the whole content is trimmed and validated as a single token.
func parseScopeToken(raw string) (string, error) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "token:"
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			val := strings.TrimSpace(line[len(prefix):])
			if val != "" {
				return val, validateTokenBytes(val)
			}
		}
	}
	tok := strings.TrimSpace(raw)
	if tok == "" {
		return "", fmt.Errorf("scope token file is empty")
	}
	// Reject multi-line / whitespace-bearing content that isn't `argus token
	// mint` output — it would break HTTP header encoding downstream.
	if strings.ContainsAny(tok, " \t\n\r") {
		return "", fmt.Errorf("scope token contains whitespace and no `token: <value>` line found — paste only the token, or pipe the full `argus token mint` output verbatim")
	}
	return tok, validateTokenBytes(tok)
}

// validateTokenBytes rejects tokens that contain bytes Go's net/http will
// refuse in a header value. Argus tokens are hex strings so the realistic
// failure mode is hidden control characters from a botched copy/paste.
func validateTokenBytes(tok string) error {
	for i := 0; i < len(tok); i++ {
		b := tok[i]
		if b < 0x20 || b > 0x7e {
			return fmt.Errorf("token contains non-printable byte 0x%02x at offset %d", b, i)
		}
	}
	return nil
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
