package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anutron/plannotator_argus/internal/argus"
)

// recorder synchronizes test-side reads with handler-side writes so the
// race detector doesn't flag the obvious slice append from inside an
// httptest handler.
type recorder struct {
	mu       sync.Mutex
	posts    []string
	deletes  []string
}

func (r *recorder) addPost(s string)   { r.mu.Lock(); r.posts = append(r.posts, s); r.mu.Unlock() }
func (r *recorder) addDelete(s string) { r.mu.Lock(); r.deletes = append(r.deletes, s); r.mu.Unlock() }
func (r *recorder) postCount() int     { r.mu.Lock(); defer r.mu.Unlock(); return len(r.posts) }
func (r *recorder) deleteSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.deletes))
	copy(out, r.deletes)
	return out
}

func TestRegistrarRegistersOnStart(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.addPost(r.URL.Path)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := argus.New(srv.URL, "tok")
	r := NewRegistrar(client, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.SetHeartbeat(time.Hour) // disable heartbeat for this test
	r.Add(ToolDefinition{Name: "plannotator_a"})
	r.Add(ToolDefinition{Name: "plannotator_b"})

	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer r.Stop(context.Background())
	if rec.postCount() != 2 {
		t.Errorf("registered %d, want 2", rec.postCount())
	}
}

func TestRegistrarFailsFastOn401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := argus.New(srv.URL, "bad")
	r := NewRegistrar(client, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.Add(ToolDefinition{Name: "plannotator_a"})
	if err := r.Start(context.Background()); err == nil {
		t.Fatal("expected error from 401")
	}
}

func TestRegistrarHeartbeatRePOSTs(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := argus.New(srv.URL, "tok")
	r := NewRegistrar(client, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.SetHeartbeat(50 * time.Millisecond)
	r.Add(ToolDefinition{Name: "plannotator_a"})

	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	r.Stop(context.Background())

	got := posts.Load()
	if got < 3 {
		t.Errorf("posts = %d, want >= 3 (initial + 2 heartbeats)", got)
	}
}

func TestRegistrarUnregistersOnStop(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			rec.addDelete(r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := argus.New(srv.URL, "tok")
	r := NewRegistrar(client, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.SetHeartbeat(time.Hour)
	r.Add(ToolDefinition{Name: "plannotator_a"})
	r.Add(ToolDefinition{Name: "plannotator_b"})

	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.Stop(context.Background())
	if got := rec.deleteSnapshot(); len(got) != 2 {
		t.Errorf("deletes = %v, want 2", got)
	}
}

func TestRegistrarStopIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client := argus.New(srv.URL, "tok")
	r := NewRegistrar(client, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.SetHeartbeat(time.Hour)
	r.Add(ToolDefinition{Name: "plannotator_a"})
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second Stop must not panic or block.
	if err := r.Stop(context.Background()); err != nil {
		t.Errorf("second Stop returned err: %v", err)
	}
}
