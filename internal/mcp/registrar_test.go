package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anutron/plannotator_argus/internal/argus"
)

func TestRegistrarRegistersOnStart(t *testing.T) {
	var registered []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registered = append(registered, r.URL.Path)
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
	if len(registered) != 2 {
		t.Errorf("registered %d, want 2: %v", len(registered), registered)
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
	var deletes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes = append(deletes, r.URL.Path)
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
	if len(deletes) != 2 {
		t.Errorf("deletes = %v, want 2", deletes)
	}
}
