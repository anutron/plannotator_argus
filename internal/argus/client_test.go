package argus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterToolSendsAuthAndVersionHeaders(t *testing.T) {
	var gotAuth, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-Argus-Plugin-Version")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"name":"plannotator_test","scope":"plannotator"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "token-abc")
	err := c.RegisterTool(context.Background(), ToolRegistration{
		Name:        "plannotator_test",
		Description: "test",
		InputSchema: map[string]any{"type": "object"},
		CallbackURL: "http://127.0.0.1:0/mcp/test",
		AuthHeader:  "Bearer secret",
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	if gotAuth != "Bearer token-abc" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer token-abc")
	}
	if gotVersion != "1" {
		t.Errorf("X-Argus-Plugin-Version = %q, want %q", gotVersion, "1")
	}
}

func TestRegisterToolMarshalsBody(t *testing.T) {
	var got ToolRegistration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := New(srv.URL, "token-abc")
	reg := ToolRegistration{
		Name:        "plannotator_annotate",
		Description: "annotate a doc",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"cwd": map[string]any{"type": "string"}}},
		CallbackURL: "http://127.0.0.1:7745/mcp/annotate",
		AuthHeader:  "Bearer secret",
	}
	if err := c.RegisterTool(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	if got.Name != "plannotator_annotate" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.CallbackURL != "http://127.0.0.1:7745/mcp/annotate" {
		t.Errorf("CallbackURL = %q", got.CallbackURL)
	}
	if got.AuthHeader != "Bearer secret" {
		t.Errorf("AuthHeader = %q", got.AuthHeader)
	}
}

func TestRegisterToolUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, "bad-token")
	err := c.RegisterTool(context.Background(), ToolRegistration{Name: "plannotator_x"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestUnregisterToolIdempotent(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "token-abc")
	if err := c.UnregisterTool(context.Background(), "plannotator_annotate"); err != nil {
		t.Errorf("first call: %v", err)
	}
	if err := c.UnregisterTool(context.Background(), "plannotator_annotate"); err != nil {
		t.Errorf("second call (404 should be ignored): %v", err)
	}
}

func TestRegisterTool500ReturnsBodySnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"db down"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	err := c.RegisterTool(context.Background(), ToolRegistration{Name: "plannotator_x"})
	if err == nil || !contains(err.Error(), "db down") {
		t.Errorf("err = %v, want body snippet in error", err)
	}
}

func TestRegisterToolNetworkError(t *testing.T) {
	// 127.0.0.1:1 is reserved; the connect should fail immediately.
	c := New("http://127.0.0.1:1", "tok")
	err := c.RegisterTool(context.Background(), ToolRegistration{Name: "plannotator_x"})
	if err == nil {
		t.Error("expected network error")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestUnregisterToolUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := New(srv.URL, "bad-token")
	err := c.UnregisterTool(context.Background(), "plannotator_x")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}
