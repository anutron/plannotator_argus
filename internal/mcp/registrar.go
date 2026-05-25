package mcp

import (
	"context"
	"errors"
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

// defaultFastRetry is how long after a first transport-level heartbeat
// failure the registrar schedules a single fast-retry attempt. Two
// consecutive transport failures – the original plus this fast retry –
// trigger a fatal exit.
const defaultFastRetry = 30 * time.Second

// Registrar keeps a set of MCP tool registrations alive with argus. It
// registers each definition on Start, re-POSTs them on a heartbeat
// (default 5 minutes), and DELETEs them on Stop.
//
// The heartbeat loop classifies each round's outcome and propagates a fatal
// error onto Fatal() when the link to argus is gone. Daemon.Run selects on
// that channel and exits non-zero so launchd can restart the daemon onto a
// freshly discovered argus URL.
type Registrar struct {
	client      *argus.Client
	callbackURL string // e.g. "http://127.0.0.1:7745"
	authHeader  string
	heartbeat   time.Duration
	fastRetry   time.Duration
	log         *slog.Logger

	mu          sync.Mutex
	definitions []ToolDefinition
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
	fatalCh     chan error
	fatalOnce   sync.Once

	// Heartbeat classifier state. Guarded by stateMu so test helpers can
	// observe it from another goroutine without racing the loop.
	stateMu              sync.Mutex
	consecutiveTransport int
	fastRetryTimer       *time.Timer
	fastRetryC           <-chan time.Time
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
		fastRetry:   defaultFastRetry,
		log:         log,
		// Buffered so the heartbeat goroutine can send-and-exit without
		// requiring a reader to be parked on Fatal() at exactly that moment.
		fatalCh: make(chan error, 1),
	}
}

// SetHeartbeat overrides the default 5-minute heartbeat interval.
func (r *Registrar) SetHeartbeat(d time.Duration) {
	r.heartbeat = d
}

// SetFastRetry overrides the default 30-second fast-retry delay scheduled
// after a single transport-level heartbeat failure.
func (r *Registrar) SetFastRetry(d time.Duration) {
	r.fastRetry = d
}

// Fatal returns the channel that emits at most one error when the registrar
// classifies the argus link as permanently lost (two consecutive transport
// failures, or an HTTP 401 from any heartbeat). Daemon.Run selects on this.
func (r *Registrar) Fatal() <-chan error {
	return r.fatalCh
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
//
// Stop uses a fresh 10s context for the DELETE calls so that a caller-
// cancelled context (e.g. SIGTERM-driven shutdown) doesn't abort cleanup
// and leave stale tool registrations until argus's idle sweep.
func (r *Registrar) Stop(ctx context.Context) error {
	var firstStop bool
	r.stopOnce.Do(func() { firstStop = true })
	if !firstStop {
		return nil
	}
	close(r.stopCh)
	r.mu.Lock()
	defs := append([]ToolDefinition(nil), r.definitions...)
	r.mu.Unlock()
	r.wg.Wait()

	// Cancel any in-flight fast-retry timer so it doesn't keep a goroutine
	// alive past Stop. Safe because the heartbeat goroutine has exited.
	r.stateMu.Lock()
	if r.fastRetryTimer != nil {
		r.fastRetryTimer.Stop()
		r.fastRetryTimer = nil
		r.fastRetryC = nil
	}
	r.stateMu.Unlock()

	cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, d := range defs {
		if err := r.client.UnregisterTool(cleanCtx, d.Name); err != nil {
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

// roundOutcome captures the worst-case classification of a single heartbeat
// round across all tools.
type roundOutcome int

const (
	outcomeSuccess         roundOutcome = iota // all tools 200/201
	outcomeHTTPWarn                            // some tool returned non-401 non-2xx (no transport, no 401)
	outcomeTransportFailed                     // some tool returned a transport-level error (no 401)
	outcomeUnauthorized                        // some tool returned 401 (overrides everything)
)

// classifyError maps a single tool registration error onto a roundOutcome
// component. A nil error means success for that tool.
func classifyError(err error) roundOutcome {
	if err == nil {
		return outcomeSuccess
	}
	if errors.Is(err, argus.ErrUnauthorized) {
		return outcomeUnauthorized
	}
	var herr *argus.HTTPError
	if errors.As(err, &herr) {
		// Any non-401 HTTP status. Including 5xx and other 4xx.
		return outcomeHTTPWarn
	}
	// Everything else (transport error from http.Client.Do, marshal error,
	// request build error) is treated as transport-level. In practice the
	// transport errors are dominated by connection-refused / DNS / timeout /
	// EOF coming back from http.Client.Do.
	return outcomeTransportFailed
}

// heartbeatRound re-POSTs every queued definition once and returns the
// worst-case outcome plus a representative error for logging.
func (r *Registrar) heartbeatRound() (roundOutcome, error) {
	r.mu.Lock()
	defs := append([]ToolDefinition(nil), r.definitions...)
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	worst := outcomeSuccess
	var worstErr error
	for _, d := range defs {
		err := r.register(ctx, d)
		o := classifyError(err)
		if o > worst {
			worst = o
			worstErr = err
		}
	}
	return worst, worstErr
}

// heartbeatLoop is the long-running classifier. It selects on the main
// heartbeat ticker, the fast-retry timer (when armed), and stopCh.
func (r *Registrar) heartbeatLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.heartbeat)
	defer ticker.Stop()

	for {
		// Snapshot the fast-retry channel under the lock so we don't race
		// with the recovery path zeroing it out.
		r.stateMu.Lock()
		fastC := r.fastRetryC
		r.stateMu.Unlock()

		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.handleRound(false)
		case <-fastC:
			r.handleRound(true)
		}
	}
}

// handleRound runs one heartbeat round and reacts to its outcome. isFastRetry
// is true when this round was triggered by the fast-retry timer rather than
// the normal heartbeat ticker.
func (r *Registrar) handleRound(isFastRetry bool) {
	outcome, repErr := r.heartbeatRound()

	switch outcome {
	case outcomeSuccess:
		r.onSuccess()
	case outcomeUnauthorized:
		r.log.Error("heartbeat unauthorized; treating as fatal", "err", repErr)
		r.emitFatal(repErr)
	case outcomeTransportFailed:
		r.onTransportFailure(isFastRetry, repErr)
	case outcomeHTTPWarn:
		// Argus is reachable but having a moment. Log and keep going on
		// the normal cadence; do NOT increment the transport counter and
		// do NOT schedule a fast retry.
		r.log.Warn("heartbeat http error (non-401); not fatal", "err", repErr)
	}
}

// onSuccess clears all failure state and cancels any pending fast retry.
func (r *Registrar) onSuccess() {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.fastRetryTimer != nil {
		r.fastRetryTimer.Stop()
		r.fastRetryTimer = nil
		r.fastRetryC = nil
	}
	r.consecutiveTransport = 0
}

// onTransportFailure classifies a transport-level failure.
//   - First failure (isFastRetry=false): warn, mark counter, arm fast retry.
//   - Second consecutive failure (isFastRetry=true OR counter >= 1): fatal.
func (r *Registrar) onTransportFailure(isFastRetry bool, repErr error) {
	r.stateMu.Lock()
	// If this round is the fast-retry firing, it's the second consecutive
	// transport failure by construction.
	if isFastRetry || r.consecutiveTransport >= 1 {
		r.consecutiveTransport++
		if r.fastRetryTimer != nil {
			r.fastRetryTimer.Stop()
			r.fastRetryTimer = nil
			r.fastRetryC = nil
		}
		r.stateMu.Unlock()
		r.log.Error("second consecutive heartbeat transport failure; treating as fatal", "err", repErr)
		r.emitFatal(repErr)
		return
	}
	// First failure path.
	r.consecutiveTransport = 1
	// Replace any prior timer (defensive; should be nil here).
	if r.fastRetryTimer != nil {
		r.fastRetryTimer.Stop()
	}
	r.fastRetryTimer = time.NewTimer(r.fastRetry)
	r.fastRetryC = r.fastRetryTimer.C
	r.stateMu.Unlock()
	r.log.Warn("heartbeat transport failure; scheduling fast retry", "delay", r.fastRetry, "err", repErr)
}

// emitFatal sends err onto fatalCh exactly once.
func (r *Registrar) emitFatal(err error) {
	r.fatalOnce.Do(func() {
		select {
		case r.fatalCh <- err:
		default:
			// Channel buffered to 1; if it's full, a fatal was already sent.
		}
	})
}

// --- test introspection -----------------------------------------------------

// pendingFastRetry reports whether a fast-retry timer is currently armed.
// Exposed for tests in the same package.
func (r *Registrar) pendingFastRetry() bool {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.fastRetryTimer != nil
}

// consecutiveTransportFailures returns the current count of consecutive
// transport-level failures. Exposed for tests in the same package.
func (r *Registrar) consecutiveTransportFailures() int {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.consecutiveTransport
}
