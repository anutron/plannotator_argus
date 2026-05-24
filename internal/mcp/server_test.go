package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestServerRoundTripSuccess(t *testing.T) {
	srv := NewServer(":0", "Bearer secret", nil)
	srv.RegisterHandler("plannotator_test", HandlerFunc(func(ctx context.Context, input json.RawMessage) (any, error) {
		var p struct {
			Cwd string `json:"cwd"`
		}
		_ = json.Unmarshal(input, &p)
		return map[string]any{"echo": p.Cwd}, nil
	}))
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	body := []byte(`{"tool":"plannotator_test","input":{"cwd":"/tmp"}}`)
	req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/mcp/plannotator_test", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.IsError {
		t.Errorf("isError = true; body: %s", raw)
	}
	if len(parsed.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(parsed.Content))
	}
	if parsed.Content[0].Text != `{"echo":"/tmp"}` {
		t.Errorf("text = %q", parsed.Content[0].Text)
	}
}

func TestServerUnauthorized(t *testing.T) {
	srv := NewServer(":0", "Bearer secret", nil)
	srv.RegisterHandler("plannotator_test", HandlerFunc(func(ctx context.Context, input json.RawMessage) (any, error) {
		return nil, nil
	}))
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/mcp/plannotator_test", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestServerUnknownTool(t *testing.T) {
	srv := NewServer(":0", "Bearer secret", nil)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/mcp/plannotator_nonsense", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestServerHandlerErrorBecomesIsErrorTrue(t *testing.T) {
	srv := NewServer(":0", "Bearer secret", nil)
	srv.RegisterHandler("plannotator_test", HandlerFunc(func(ctx context.Context, input json.RawMessage) (any, error) {
		return nil, errors.New("boom")
	}))
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/mcp/plannotator_test", bytes.NewReader([]byte(`{"tool":"plannotator_test","input":{}}`)))
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		IsError bool `json:"isError"`
		Content []struct{ Text string } `json:"content"`
	}
	_ = json.Unmarshal(raw, &parsed)
	if !parsed.IsError {
		t.Errorf("isError = false; body: %s", raw)
	}
	if len(parsed.Content) == 0 || parsed.Content[0].Text != "boom" {
		t.Errorf("error text = %v", parsed.Content)
	}
}

func TestServerExtraRoute(t *testing.T) {
	srv := NewServer(":0", "Bearer secret", nil)
	srv.RegisterExtraRoute("/hook", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("from-hook"))
	})
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	resp, err := http.Get("http://" + srv.Addr() + "/hook")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want 418", resp.StatusCode)
	}
}
