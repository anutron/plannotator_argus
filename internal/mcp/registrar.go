package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/anutron/plannotator_argus/internal/argus"
)

// ToolDefinition is the metadata used to populate an argus tool registration.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Registrar keeps a set of MCP tool registrations alive with argus. It
// registers each definition on Start, re-POSTs them on a heartbeat
// (default 5 minutes), and DELETEs them on Stop.
type Registrar struct {
	client      *argus.Client
	callbackURL string // e.g. "http://127.0.0.1:7745"
	authHeader  string
	heartbeat   time.Duration
	log         *slog.Logger

	mu          sync.Mutex
	definitions []ToolDefinition
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

// NewRegistrar constructs a registrar. callbackBase is the daemon's HTTP
// base URL (the MCP server's Addr() with `http://` prefix); the per-tool
// callback_url is built by appending `/mcp/<tool-name>`.
func NewRegistrar(client *argus.Client, callbackBase, authHeader string, log *slog.Logger) *Registrar {
	if log == nil {
		log = slog.Default()
	}
	return &Registrar{
		client:      client,
		callbackURL: callbackBase,
		authHeader:  authHeader,
		heartbeat:   5 * time.Minute,
		log:         log,
	}
}

// SetHeartbeat overrides the default 5-minute heartbeat interval.
func (r *Registrar) SetHeartbeat(d time.Duration) {
	r.heartbeat = d
}

// Add queues a definition for registration on Start.
func (r *Registrar) Add(def ToolDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.definitions = append(r.definitions, def)
}

// Start registers every queued definition once and launches a heartbeat
// goroutine. Returns an error if the initial registration of any tool fails
// (so the caller can fail-fast on a revoked token).
func (r *Registrar) Start(ctx context.Context) error {
	r.mu.Lock()
	defs := append([]ToolDefinition(nil), r.definitions...)
	r.stopCh = make(chan struct{})
	r.mu.Unlock()

	for _, d := range defs {
		if err := r.register(ctx, d); err != nil {
			return err
		}
	}
	r.wg.Add(1)
	go r.heartbeatLoop()
	return nil
}

// Stop unregisters every definition with argus and joins the heartbeat
// goroutine. Errors from unregister are logged but not returned.
func (r *Registrar) Stop(ctx context.Context) error {
	r.mu.Lock()
	close(r.stopCh)
	defs := append([]ToolDefinition(nil), r.definitions...)
	r.mu.Unlock()
	r.wg.Wait()
	for _, d := range defs {
		if err := r.client.UnregisterTool(ctx, d.Name); err != nil {
			r.log.Warn("unregister tool", "name", d.Name, "err", err)
		}
	}
	return nil
}

func (r *Registrar) register(ctx context.Context, d ToolDefinition) error {
	reg := argus.ToolRegistration{
		Name:        d.Name,
		Description: d.Description,
		InputSchema: d.InputSchema,
		CallbackURL: fmt.Sprintf("%s/mcp/%s", r.callbackURL, d.Name),
		AuthHeader:  r.authHeader,
	}
	if err := r.client.RegisterTool(ctx, reg); err != nil {
		return fmt.Errorf("register %s: %w", d.Name, err)
	}
	return nil
}

func (r *Registrar) heartbeatLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.mu.Lock()
			defs := append([]ToolDefinition(nil), r.definitions...)
			r.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			for _, d := range defs {
				if err := r.register(ctx, d); err != nil {
					r.log.Warn("heartbeat re-register failed", "tool", d.Name, "err", err)
				}
			}
			cancel()
		}
	}
}
