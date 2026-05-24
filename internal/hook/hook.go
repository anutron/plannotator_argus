// Package hook implements the POST /hook HTTP endpoint that Claude Code's
// ExitPlanMode stop hook can invoke from inside an argus task sandbox.
//
// Auth: persistent bearer token at ~/.plannotator/argus-plugin-token,
// readable from inside the sandbox.
//
// Behavior: pipes the request body into a freshly-spawned `plannotator`
// process (no args) and streams the subprocess's stdout back as the
// response body. No artificial timeout — the connection stays open as
// long as Plannotator takes.
package hook

import (
	"bytes"
	"crypto/subtle"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/anutron/plannotator_argus/internal/plannotator"
)

// Handler is the http.Handler for POST /hook.
type Handler struct {
	Token  string // expected bearer token (without "Bearer " prefix)
	Runner *plannotator.Runner
	Log    *slog.Logger
}

// New constructs a Handler.
func New(token string, runner *plannotator.Runner, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{Token: token, Runner: runner, Log: log}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.Token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Use r.Context() so the daemon can cancel via shutdown.
	res := h.Runner.Run(r.Context(), nil, bytes.NewReader(body))
	if res.Err != nil {
		http.Error(w, fmt.Sprintf("plannotator: %v", res.Err), http.StatusInternalServerError)
		return
	}
	if res.ExitCode != 0 {
		stderr := tail(res.Stderr, 4096)
		http.Error(w, fmt.Sprintf("plannotator exit %d: %s", res.ExitCode, stderr), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res.Stdout)
}

func tail(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[len(b)-n:])
}
