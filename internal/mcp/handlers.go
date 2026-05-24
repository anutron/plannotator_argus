package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/anutron/plannotator_argus/internal/plannotator"
)

// HandlerDeps bundles the runtime dependencies the verb handlers share.
//
// ParentCtx and SessionWG are wired by daemon.Start so that all subprocess
// goroutines spawned by verb handlers can be SIGKILL'd on daemon shutdown
// (via the cancellation semantics of exec.CommandContext) and joined before
// Stop returns.
type HandlerDeps struct {
	Runner    *plannotator.Runner
	Store     *SessionStore
	Log       *slog.Logger
	URLPoll   time.Duration // how long to poll for session URL discovery; default 5s
	ParentCtx context.Context
	SessionWG *sync.WaitGroup
}

func (d *HandlerDeps) urlPoll() time.Duration {
	if d.URLPoll <= 0 {
		return 5 * time.Second
	}
	return d.URLPoll
}

// sessionEnvelope is the shape returned by every verb-starter tool.
type sessionEnvelope struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url,omitempty"`
	Status    string `json:"status"`
}

// startSession spawns plannotator with the given args, creates a Session in
// the store, kicks off a background goroutine to handle the subprocess's
// lifecycle, and returns the envelope.
//
// Spawn failures are surfaced as a tool error (so the agent gets isError:true)
// rather than as a "failed session" envelope with no error field. A failed
// spawn means no session ever existed — there's nothing to poll.
func (d *HandlerDeps) startSession(ctx context.Context, verb string, args []string) (any, error) {
	parent := d.ParentCtx
	if parent == nil {
		parent = context.Background()
	}
	// exec.CommandContext SIGKILLs the child when the ctx is cancelled,
	// so deriving from ParentCtx makes daemon shutdown reach every
	// in-flight subprocess.
	cmd := d.Runner.Spawn(parent, args)
	stdout := &capturedBuffer{cap: 8 << 20}
	stderr := &capturedBuffer{cap: 8 << 20}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn plannotator: %w", err)
	}
	sess := d.Store.Create(verb)
	pid := cmd.Process.Pid

	if d.SessionWG != nil {
		d.SessionWG.Add(2)
	}

	// Best-effort URL discovery (poll Plannotator's sessions/<pid>.json).
	go func() {
		if d.SessionWG != nil {
			defer d.SessionWG.Done()
		}
		url := d.Runner.DiscoverSessionURL(parent, pid, d.urlPoll())
		if url != "" {
			d.Store.SetURL(sess.ID, url)
		}
	}()

	// Lifecycle goroutine.
	go func() {
		if d.SessionWG != nil {
			defer d.SessionWG.Done()
		}
		err := cmd.Wait()
		if err != nil {
			msg := err.Error()
			if len(stderr.Bytes()) > 0 {
				msg = fmt.Sprintf("%s: %s", msg, tail(stderr.Bytes(), 4096))
			}
			d.Store.MarkFailed(sess.ID, msg)
			return
		}
		raw := stdout.Bytes()
		if len(raw) == 0 {
			d.Store.MarkComplete(sess.ID, json.RawMessage(`null`))
			return
		}
		// If stdout is valid JSON, pass it through verbatim; otherwise
		// wrap it as a string so plannotator_session_result always returns
		// a parseable object (Plannotator's `last` verb emits prose, not JSON).
		if json.Valid(raw) {
			d.Store.MarkComplete(sess.ID, json.RawMessage(raw))
			return
		}
		wrapped, _ := json.Marshal(map[string]string{"raw": string(raw)})
		d.Store.MarkComplete(sess.ID, json.RawMessage(wrapped))
	}()

	// Wait briefly for the URL so it can ride back on the initial envelope.
	deadline := time.Now().Add(d.urlPoll())
	for time.Now().Before(deadline) {
		snap, err := d.Store.Get(sess.ID)
		if err == nil && (snap.URL != "" || snap.Status != StatusPending) {
			return sessionEnvelope{SessionID: sess.ID, URL: snap.URL, Status: string(snap.Status)}, nil
		}
		select {
		case <-ctx.Done():
			return sessionEnvelope{SessionID: sess.ID, Status: string(StatusPending)}, nil
		case <-time.After(100 * time.Millisecond):
		}
	}
	return sessionEnvelope{SessionID: sess.ID, Status: string(StatusPending)}, nil
}

// AnnotateHandler returns a Handler for the plannotator_annotate tool.
func AnnotateHandler(deps *HandlerDeps) Handler {
	return HandlerFunc(func(ctx context.Context, input json.RawMessage) (any, error) {
		var p struct {
			Cwd  string `json:"cwd"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return nil, fmt.Errorf("decode input: %w", err)
		}
		if p.Cwd == "" {
			return nil, fmt.Errorf("cwd is required")
		}
		if p.Path == "" {
			return nil, fmt.Errorf("path is required")
		}
		resolved, err := ResolvePath(p.Cwd, p.Path)
		if err != nil {
			return nil, fmt.Errorf("resolve path: %w", err)
		}
		return deps.startSession(ctx, "annotate", []string{"annotate", resolved, "--json"})
	})
}

// ReviewHandler returns a Handler for the plannotator_review tool.
func ReviewHandler(deps *HandlerDeps) Handler {
	return HandlerFunc(func(ctx context.Context, input json.RawMessage) (any, error) {
		var p struct {
			Cwd   string `json:"cwd"`
			PRURL string `json:"pr_url"`
			Git   bool   `json:"git"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return nil, fmt.Errorf("decode input: %w", err)
		}
		if p.Cwd == "" {
			return nil, fmt.Errorf("cwd is required")
		}
		args := []string{"review"}
		if p.Git {
			args = append(args, "--git")
		}
		if p.PRURL != "" {
			args = append(args, p.PRURL)
		}
		return deps.startSession(ctx, "review", args)
	})
}

// SetupGoalHandler returns a Handler for the plannotator_setup_goal tool.
func SetupGoalHandler(deps *HandlerDeps) Handler {
	return HandlerFunc(func(ctx context.Context, input json.RawMessage) (any, error) {
		var p struct {
			Cwd        string `json:"cwd"`
			Mode       string `json:"mode"`
			BundlePath string `json:"bundle_path"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return nil, fmt.Errorf("decode input: %w", err)
		}
		if p.Cwd == "" {
			return nil, fmt.Errorf("cwd is required")
		}
		if p.Mode != "interview" && p.Mode != "facts" {
			return nil, fmt.Errorf("mode must be 'interview' or 'facts'")
		}
		if p.BundlePath == "" {
			return nil, fmt.Errorf("bundle_path is required")
		}
		resolved, err := ResolvePath(p.Cwd, p.BundlePath)
		if err != nil {
			return nil, fmt.Errorf("resolve bundle_path: %w", err)
		}
		return deps.startSession(ctx, "setup-goal", []string{"setup-goal", p.Mode, resolved})
	})
}

// LastHandler returns a Handler for the plannotator_last tool.
func LastHandler(deps *HandlerDeps) Handler {
	return HandlerFunc(func(ctx context.Context, input json.RawMessage) (any, error) {
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return nil, fmt.Errorf("decode input: %w", err)
		}
		if p.Cwd == "" {
			return nil, fmt.Errorf("cwd is required")
		}
		return deps.startSession(ctx, "last", []string{"last"})
	})
}

// SessionResultHandler returns a Handler for the plannotator_session_result
// tool. Long-polls up to wait_seconds (default 20, max 25).
func SessionResultHandler(deps *HandlerDeps) Handler {
	return HandlerFunc(func(ctx context.Context, input json.RawMessage) (any, error) {
		var p struct {
			Cwd         string `json:"cwd"`
			SessionID   string `json:"session_id"`
			WaitSeconds int    `json:"wait_seconds"`
		}
		if err := json.Unmarshal(input, &p); err != nil {
			return nil, fmt.Errorf("decode input: %w", err)
		}
		if p.SessionID == "" {
			return nil, fmt.Errorf("session_id is required")
		}
		wait := p.WaitSeconds
		if wait <= 0 {
			wait = 20
		}
		if wait > 25 {
			wait = 25
		}
		sess, err := deps.Store.WaitForResolution(ctx, p.SessionID, time.Duration(wait)*time.Second)
		// ErrUnknownSession is a real tool error. Context cancellation
		// (caller hung up early) is not — return whatever snapshot we have
		// so the agent can poll again with the same session_id.
		if errors.Is(err, ErrUnknownSession) {
			return nil, err
		}
		out := map[string]any{
			"session_id": sess.ID,
			"status":     string(sess.Status),
		}
		if sess.URL != "" {
			out["url"] = sess.URL
		}
		if sess.Result != nil {
			out["result"] = sess.Result
		}
		if sess.Error != "" {
			out["error"] = sess.Error
		}
		return out, nil
	})
}

// capturedBuffer is a small io.Writer with a hard byte cap.
type capturedBuffer struct {
	cap int
	buf []byte
}

func (c *capturedBuffer) Write(p []byte) (int, error) {
	if c.cap > 0 && len(c.buf)+len(p) > c.cap {
		remaining := c.cap - len(c.buf)
		if remaining > 0 {
			c.buf = append(c.buf, p[:remaining]...)
		}
		return len(p), nil
	}
	c.buf = append(c.buf, p...)
	return len(p), nil
}

func (c *capturedBuffer) Bytes() []byte { return c.buf }

func tail(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[len(b)-n:])
}
