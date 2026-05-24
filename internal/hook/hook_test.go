package hook

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anutron/plannotator_argus/internal/plannotator"
)

func writeStub(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "plannotator")
	if err := os.WriteFile(bin, []byte("#!/bin/bash\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func newServer(t *testing.T, token, stub string) *httptest.Server {
	t.Helper()
	runner := &plannotator.Runner{BinaryPath: stub, SessionsDir: t.TempDir()}
	h := New(token, runner, nil)
	return httptest.NewServer(h)
}

func TestHookHappyPath(t *testing.T) {
	stub := writeStub(t, `cat - | sed 's/in/out/'`)
	srv := newServer(t, "tok", stub)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader([]byte(`{"in":"yes"}`)))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"out":"yes"`) {
		t.Errorf("body = %q", body)
	}
}

func TestHookMissingToken(t *testing.T) {
	stub := writeStub(t, `exit 0`)
	srv := newServer(t, "tok", stub)
	defer srv.Close()

	resp, err := http.Post(srv.URL, "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestHookWrongToken(t *testing.T) {
	stub := writeStub(t, `exit 0`)
	srv := newServer(t, "tok", stub)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader([]byte(`{}`)))
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

func TestHookMethodNotAllowed(t *testing.T) {
	stub := writeStub(t, `exit 0`)
	srv := newServer(t, "tok", stub)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestHookSubprocessFailure(t *testing.T) {
	stub := writeStub(t, fmt.Sprintf(`echo "an error" >&2; exit 7`))
	srv := newServer(t, "tok", stub)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "an error") {
		t.Errorf("expected stderr in response body, got %q", body)
	}
}
