// Package daemon wires the plannotator-argus daemon's subsystems together
// and provides the main Run loop the CLI invokes from `start --foreground`.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/anutron/plannotator_argus/internal/argus"
	"github.com/anutron/plannotator_argus/internal/config"
	"github.com/anutron/plannotator_argus/internal/hook"
	"github.com/anutron/plannotator_argus/internal/mcp"
	"github.com/anutron/plannotator_argus/internal/plannotator"
)

// SessionShutdownGrace bounds how long Stop waits for in-flight Plannotator
// subprocesses to exit (after ctx cancel SIGKILLs them via exec.CommandContext).
// Even SIGKILL'd processes need a moment for the kernel to deliver the signal
// and for the Wait() goroutine to drain.
const SessionShutdownGrace = 10 * time.Second

// Daemon is the running instance.
type Daemon struct {
	Cfg       *config.Config
	Log       *slog.Logger
	Argus     *argus.Client
	Runner    *plannotator.Runner
	Store     *mcp.SessionStore
	MCPServer *mcp.Server
	Registrar *mcp.Registrar
	HookToken string

	parentCtx    context.Context
	parentCancel context.CancelFunc
	sessionWG    *sync.WaitGroup
	gcStop       chan struct{}
	gcWG         sync.WaitGroup
	stopOnce     sync.Once
}

// Start brings the daemon up. Returns the running *Daemon, ready for Stop.
func Start(ctx context.Context, cfg *config.Config, log *slog.Logger) (*Daemon, error) {
	if cfg == nil {
		var err error
		cfg, err = config.Default()
		if err != nil {
			return nil, err
		}
	}
	if log == nil {
		log = slog.Default()
	}
	if err := cfg.EnsureStateDir(); err != nil {
		return nil, fmt.Errorf("state dir: %w", err)
	}
	scopeToken, err := cfg.LoadScopeToken()
	if err != nil {
		return nil, err
	}
	hookToken, err := cfg.LoadOrCreateHookToken()
	if err != nil {
		return nil, fmt.Errorf("hook token: %w", err)
	}
	runner, err := plannotator.NewFromEnv(cfg.PlannotatorBin)
	if err != nil {
		return nil, fmt.Errorf("plannotator: %w", err)
	}
	if err := runner.HealthCheck(ctx); err != nil {
		return nil, fmt.Errorf("plannotator health check: %w", err)
	}

	client := argus.New(cfg.ArgusBaseURL, scopeToken)
	store := mcp.NewSessionStore()

	authHeader, err := mcp.GenerateAuthHeader()
	if err != nil {
		return nil, fmt.Errorf("auth header: %w", err)
	}

	parentCtx, parentCancel := context.WithCancel(context.Background())
	sessionWG := &sync.WaitGroup{}

	srv := mcp.NewServer(cfg.ListenAddr, authHeader, log)

	deps := &mcp.HandlerDeps{
		Runner:    runner,
		Store:     store,
		Log:       log,
		URLPoll:   5 * time.Second,
		ParentCtx: parentCtx,
		SessionWG: sessionWG,
	}
	srv.RegisterHandler("plannotator_annotate", mcp.AnnotateHandler(deps))
	srv.RegisterHandler("plannotator_review", mcp.ReviewHandler(deps))
	srv.RegisterHandler("plannotator_setup_goal", mcp.SetupGoalHandler(deps))
	srv.RegisterHandler("plannotator_last", mcp.LastHandler(deps))
	srv.RegisterHandler("plannotator_session_result", mcp.SessionResultHandler(deps))

	srv.RegisterExtraRoute("/hook", hook.New(hookToken, runner, log).ServeHTTP)

	if err := srv.Start(ctx); err != nil {
		parentCancel()
		return nil, fmt.Errorf("start mcp server: %w", err)
	}

	callback := "http://" + srv.Addr()
	registrar := mcp.NewRegistrar(client, callback, authHeader, log)
	registrar.SetHeartbeat(cfg.MCPHeartbeat)
	for _, def := range toolDefinitions() {
		registrar.Add(def)
	}
	if err := registrar.Start(ctx); err != nil {
		_ = srv.Stop()
		parentCancel()
		return nil, fmt.Errorf("register tools: %w", err)
	}

	d := &Daemon{
		Cfg:          cfg,
		Log:          log,
		Argus:        client,
		Runner:       runner,
		Store:        store,
		MCPServer:    srv,
		Registrar:    registrar,
		HookToken:    hookToken,
		parentCtx:    parentCtx,
		parentCancel: parentCancel,
		sessionWG:    sessionWG,
		gcStop:       make(chan struct{}),
	}
	d.gcWG.Add(1)
	go d.gcLoop()
	log.Info("plannotator-argus ready",
		"argus_base_url", cfg.ArgusBaseURL,
		"mcp_addr", srv.Addr(),
		"plannotator_bin", runner.BinaryPath,
	)
	return d, nil
}

// Stop tears the daemon down. Safe to call multiple times.
//
// Order matters: (1) cancel the parent context so exec.CommandContext
// SIGKILLs in-flight Plannotator subprocesses; (2) wait for session
// goroutines to join (bounded by SessionShutdownGrace); (3) stop the
// GC loop; (4) unregister tools with argus; (5) shut the HTTP listener.
func (d *Daemon) Stop(ctx context.Context) {
	if d == nil {
		return
	}
	d.stopOnce.Do(func() {
		d.parentCancel()
		if waited := waitWithTimeout(d.sessionWG, SessionShutdownGrace); !waited {
			d.Log.Warn("session goroutines did not exit within grace", "grace", SessionShutdownGrace)
		}
		close(d.gcStop)
		d.gcWG.Wait()
		if d.Registrar != nil {
			_ = d.Registrar.Stop(ctx)
		}
		if d.MCPServer != nil {
			_ = d.MCPServer.Stop()
		}
		d.Log.Info("plannotator-argus stopped")
	})
}

// Run brings the daemon up, acquires an exclusive advisory lock on the
// pidfile (refusing to start if another daemon already holds it), blocks
// until either ctx is canceled or the registrar reports a fatal heartbeat
// failure, then gracefully shuts down.
//
// A fatal heartbeat failure causes Run to return the underlying error so
// the CLI can propagate a non-zero exit code; launchd then restarts the
// daemon (its plist has KeepAlive.SuccessfulExit=false with a 60-second
// ThrottleInterval) so the next process can re-run URL discovery and pick
// up argus on its new port.
func Run(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	d, err := Start(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer d.Stop(context.Background())

	lock, err := AcquirePIDLock(d.Cfg.PIDPath())
	if err != nil {
		return fmt.Errorf("acquire pidfile lock: %w", err)
	}
	defer lock.Release()

	var fatalCh <-chan error
	if d.Registrar != nil {
		fatalCh = d.Registrar.Fatal()
	}
	select {
	case <-ctx.Done():
		return nil
	case fatalErr, ok := <-fatalCh:
		if !ok || fatalErr == nil {
			// Channel closed or sent nil; treat as normal exit.
			return nil
		}
		log.Error("fatal heartbeat failure; exiting for launchd restart", "err", fatalErr)
		return fatalErr
	}
}

func waitWithTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (d *Daemon) gcLoop() {
	defer d.gcWG.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-d.gcStop:
			return
		case <-ticker.C:
			removed := d.Store.GC(d.Cfg.SessionTTL)
			if removed > 0 {
				d.Log.Debug("session store gc", "removed", removed)
			}
		}
	}
}

// toolDefinitions returns the five MCP tool registrations.
func toolDefinitions() []mcp.ToolDefinition {
	cwdProp := map[string]any{"type": "string", "description": "Caller's working directory (use $PWD)"}
	return []mcp.ToolDefinition{
		{
			Name:        "plannotator_annotate",
			Description: "Open Plannotator's annotation UI on a file, folder, or URL. Returns immediately with {session_id, url, status:'pending'}; poll plannotator_session_result for the final annotations. Always pass $PWD as cwd.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":  cwdProp,
					"path": map[string]any{"type": "string", "description": "File path under cwd, or http(s):// URL"},
				},
				"required": []string{"cwd", "path"},
			},
		},
		{
			Name:        "plannotator_review",
			Description: "Open Plannotator's review UI on the current git branch (git=true) or a PR URL. Returns immediately with {session_id, url, status:'pending'}; poll plannotator_session_result for the final review feedback.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":    cwdProp,
					"pr_url": map[string]any{"type": "string", "description": "Optional GitHub PR URL"},
					"git":    map[string]any{"type": "boolean", "description": "Set true to review the current local branch"},
				},
				"required": []string{"cwd"},
			},
		},
		{
			Name:        "plannotator_setup_goal",
			Description: "Drive Plannotator's setup-goal flow. mode is 'interview' or 'facts'; bundle_path is the path to a bundle JSON under cwd. Returns immediately with {session_id, url, status:'pending'}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":         cwdProp,
					"mode":        map[string]any{"type": "string", "enum": []string{"interview", "facts"}},
					"bundle_path": map[string]any{"type": "string", "description": "Path to bundle JSON under cwd"},
				},
				"required": []string{"cwd", "mode", "bundle_path"},
			},
		},
		{
			Name:        "plannotator_last",
			Description: "Open Plannotator on the most recently rendered assistant message. Returns immediately with {session_id, url, status:'pending'}; poll plannotator_session_result for annotations.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd": cwdProp,
				},
				"required": []string{"cwd"},
			},
		},
		{
			Name:        "plannotator_session_result",
			Description: "Fetch the current state of a Plannotator session previously started via plannotator_annotate / plannotator_review / plannotator_setup_goal / plannotator_last. Long-polls up to wait_seconds (default 20, max 25) waiting for the session to resolve. Returns {session_id, status, url?, result?, error?}.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":          cwdProp,
					"session_id":   map[string]any{"type": "string", "description": "Session ID returned by an earlier verb tool"},
					"wait_seconds": map[string]any{"type": "integer", "description": "Optional long-poll deadline in seconds (default 20, max 25)"},
				},
				"required": []string{"session_id"},
			},
		},
	}
}
