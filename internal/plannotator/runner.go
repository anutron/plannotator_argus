// Package plannotator wraps the user's `plannotator` CLI binary. The
// daemon shells out to it from outside any argus task sandbox, so
// Plannotator's local browser session and session-file writes succeed
// normally.
package plannotator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Runner shells out to Plannotator.
type Runner struct {
	// BinaryPath is the absolute path of the `plannotator` binary. Resolved
	// from PATH at startup; tests may override.
	BinaryPath string

	// SessionsDir is the directory Plannotator writes its session metadata
	// to (default ~/.plannotator/sessions/). The daemon polls this directory
	// to discover the URL the user's browser should open.
	SessionsDir string
}

// NewFromEnv resolves the plannotator binary using PLANNOTATOR_BIN if set,
// else the first `plannotator` on $PATH. Returns an error if neither
// resolves to an executable file.
func NewFromEnv(override string) (*Runner, error) {
	bin, err := resolveBin(override)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	return &Runner{
		BinaryPath:  bin,
		SessionsDir: filepath.Join(home, ".plannotator", "sessions"),
	}, nil
}

func resolveBin(override string) (string, error) {
	if override != "" {
		info, err := os.Stat(override)
		if err != nil {
			return "", fmt.Errorf("PLANNOTATOR_BIN=%q: %w", override, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("PLANNOTATOR_BIN=%q is a directory", override)
		}
		return override, nil
	}
	p, err := exec.LookPath("plannotator")
	if err != nil {
		return "", fmt.Errorf("`plannotator` not found on $PATH (set PLANNOTATOR_BIN to override)")
	}
	return p, nil
}

// HealthCheck runs `plannotator --version` with a 5-second timeout. Used at
// daemon startup to fail fast if the binary is broken or wrong.
func (r *Runner) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.BinaryPath, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("plannotator --version: %w (output: %q)", err, string(out))
	}
	return nil
}

// RunResult is the outcome of a non-streaming Plannotator subprocess.
type RunResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Err      error
}

// Run invokes Plannotator with the given args, captures stdout/stderr up to
// 8 MiB each, and returns when the subprocess exits. Errors from the
// exec.Cmd are wrapped in RunResult.Err; non-zero exit codes are NOT
// converted to errors (callers may want to inspect stderr regardless).
func (r *Runner) Run(ctx context.Context, args []string, stdin io.Reader) RunResult {
	cmd := exec.CommandContext(ctx, r.BinaryPath, args...)
	cmd.Stdin = stdin
	var stdout, stderr limitedBuffer
	stdout.cap = 8 << 20
	stderr.cap = 8 << 20
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	// `Run()` returns an error for non-zero exit. We capture it but do not
	// treat exit code as a fatal error here.
	return RunResult{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: exitCode,
		Err:      ignoreExitErr(err),
	}
}

// Spawn starts a Plannotator subprocess in the background, returning the
// underlying *exec.Cmd so callers can read PID, attach stdout/stderr, and
// wait. The caller owns the process lifecycle.
func (r *Runner) Spawn(ctx context.Context, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, r.BinaryPath, args...)
}

// DiscoverSessionURL scans the sessions directory for a file matching the
// given pid, polling up to `timeout`. Returns the URL Plannotator opened in
// the browser, or "" if no session file appeared in time.
func (r *Runner) DiscoverSessionURL(pid int, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	path := filepath.Join(r.SessionsDir, fmt.Sprintf("%d.json", pid))
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			var s struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(raw, &s) == nil && s.URL != "" {
				return s.URL
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ""
}

// limitedBuffer caps how much it'll record. Used so a Plannotator misbehavior
// can't OOM the daemon.
type limitedBuffer struct {
	buf bytes.Buffer
	cap int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.cap > 0 && b.buf.Len()+len(p) > b.cap {
		remaining := b.cap - b.buf.Len()
		if remaining > 0 {
			b.buf.Write(p[:remaining])
		}
		return len(p), nil // pretend we consumed it all so writers don't error
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

func ignoreExitErr(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}
