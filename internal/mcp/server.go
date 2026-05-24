package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Handler dispatches one MCP tool invocation. Implementations parse the
// request payload, perform the work, and return either a result envelope
// (which the server JSON-encodes as `{"content":[{"type":"text","text":<json>}], "isError":false}`)
// or an error (which becomes `isError:true` with a text content block).
type Handler interface {
	Handle(ctx context.Context, input json.RawMessage) (any, error)
}

// HandlerFunc adapts a function into a Handler.
type HandlerFunc func(ctx context.Context, input json.RawMessage) (any, error)

func (f HandlerFunc) Handle(ctx context.Context, input json.RawMessage) (any, error) {
	return f(ctx, input)
}

// Server is the callback HTTP listener argus POSTs MCP invocations to.
type Server struct {
	listenAddr string
	authHeader string
	log        *slog.Logger

	mu       sync.RWMutex
	handlers map[string]Handler
	extra    map[string]http.HandlerFunc // /hook etc.

	listener net.Listener
	httpSrv  *http.Server
}

// NewServer returns a server bound to listenAddr (use ":0" to let the OS
// pick a port). The authHeader is the per-process random secret argus will
// present on every callback; we constant-time compare incoming Authorization.
func NewServer(listenAddr, authHeader string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		listenAddr: listenAddr,
		authHeader: authHeader,
		log:        log,
		handlers:   make(map[string]Handler),
		extra:      make(map[string]http.HandlerFunc),
	}
}

// RegisterHandler binds a Handler to an MCP tool name. The HTTP route is
// `/mcp/<name>`.
func (s *Server) RegisterHandler(name string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[name] = h
}

// RegisterExtraRoute mounts a non-MCP HTTP route on the same listener (e.g.
// /hook). The caller's HandlerFunc is responsible for its own auth.
func (s *Server) RegisterExtraRoute(path string, h http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.extra[path] = h
}

// Start binds the listener and begins serving in a background goroutine.
// Safe to call once. Use Addr() to read the bound address (useful with ":0").
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.listenAddr, err)
	}
	s.listener = ln

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/", s.handleMCP)

	s.mu.RLock()
	for path, h := range s.extra {
		mux.HandleFunc(path, h)
	}
	s.mu.RUnlock()

	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Warn("mcp server exited", "err", err)
		}
	}()
	return nil
}

// Stop gracefully shuts the HTTP listener down with a short deadline.
func (s *Server) Stop() error {
	if s.httpSrv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpSrv.Shutdown(ctx)
}

// Addr returns the bound address (host:port). Safe after Start.
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !EqualConstantTime(r.Header.Get("Authorization"), s.authHeader) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	tool := strings.TrimPrefix(r.URL.Path, "/mcp/")
	s.mu.RLock()
	h, ok := s.handlers[tool]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "unknown tool: "+tool, http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		s.writeToolError(w, "read body: "+err.Error())
		return
	}
	var envelope struct {
		Tool  string          `json:"tool"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		s.writeToolError(w, "decode envelope: "+err.Error())
		return
	}
	result, err := h.Handle(r.Context(), envelope.Input)
	if err != nil {
		s.writeToolError(w, err.Error())
		return
	}
	s.writeToolResult(w, result)
}

func (s *Server) writeToolResult(w http.ResponseWriter, result any) {
	body, err := json.Marshal(result)
	if err != nil {
		s.writeToolError(w, "marshal result: "+err.Error())
		return
	}
	resp := map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(body)}},
		"isError": false,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) writeToolError(w http.ResponseWriter, msg string) {
	resp := map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
