package argus

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// shortSocketPath returns a unix-socket path short enough for macOS's
// 104-byte sun_path limit. t.TempDir() under /var/folders/... routinely
// blows past that, so we mint our own short path under /tmp and clean it
// up via t.Cleanup.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ptd-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

// fakeDaemon is a minimal stand-in for argus's daemon socket: it accepts
// connections, reads the dispatch prefix byte, and serves a single RPC
// service that the test programs to respond however it wants.
type fakeDaemon struct {
	ln      net.Listener
	svc     *fakePortsSvc
	stopped atomic.Bool
}

type fakePortsSvc struct {
	// Behavior knobs. Tests set one of these before clients connect.
	resp FakePortsResp // returned when err is nil
	err  error         // when non-nil, RPC fails with this error
}

// FakePortsArgs and FakePortsResp are exported because net/rpc refuses to
// register methods whose argument or reply types are unexported. The wire
// format is JSON (jsonrpc codec) so field names — not Go-side types — are
// what cross the socket: as long as the JSON shape matches argus's
// `PortsResp`, the client decodes it into our unexported `portsResp`
// without trouble.
type FakePortsArgs struct{}

type FakePortsResp struct {
	MCPPort int
	APIPort int
}

// FakeDaemon is the receiver registered as "Daemon" with net/rpc, so its
// method names map to "Daemon.<method>". Must be exported for net/rpc to
// find the methods via reflection.
type FakeDaemon struct{ svc *fakePortsSvc }

// Ports mimics argus's `Daemon.Ports`. Tests program svc.resp / svc.err to
// drive client-side behavior.
func (d *FakeDaemon) Ports(_ *FakePortsArgs, resp *FakePortsResp) error {
	if d.svc.err != nil {
		return d.svc.err
	}
	*resp = d.svc.resp
	return nil
}

// newFakeDaemon listens on a unix socket and serves a fake Daemon.Ports
// RPC. Returns the socket path. The daemon is torn down via t.Cleanup.
func newFakeDaemon(t *testing.T, svc *fakePortsSvc) string {
	t.Helper()
	sockPath := shortSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sockPath, err)
	}
	fd := &fakeDaemon{ln: ln, svc: svc}
	t.Cleanup(func() {
		fd.stopped.Store(true)
		_ = ln.Close()
	})

	server := rpc.NewServer()
	if err := server.RegisterName("Daemon", &FakeDaemon{svc: svc}); err != nil {
		t.Fatalf("register Daemon: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if fd.stopped.Load() {
					return
				}
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// Read dispatch prefix byte (mirrors argus's handleConn).
				var prefix [1]byte
				if _, err := c.Read(prefix[:]); err != nil {
					return
				}
				if prefix[0] != rpcPrefixByte {
					return
				}
				server.ServeCodec(jsonrpc.NewServerCodec(c))
			}(conn)
		}
	}()
	return sockPath
}

// shortMissingSocket returns a path inside a short /tmp dir that does not
// exist. Used by the "socket missing" test to avoid the t.TempDir overflow
// on macOS.
func shortMissingSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ptd-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "missing.sock")
}

// TestDiscover_Success verifies discovery returns the API port wrapped in
// the canonical URL when the RPC succeeds with a non-zero APIPort.
// Covers spec scenario "Daemon.Ports discovery succeeds".
func TestDiscover_Success(t *testing.T) {
	sock := newFakeDaemon(t, &fakePortsSvc{resp: FakePortsResp{MCPPort: 7841, APIPort: 7841}})

	url, ok := discover(context.Background(), sock, 2*time.Second)
	if !ok {
		t.Fatalf("expected ok=true, got false (url=%q)", url)
	}
	want := "http://127.0.0.1:7841"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

// TestDiscover_SocketMissing verifies a missing socket file fails fast and
// returns ok=false without panicking. Covers spec scenarios "Daemon.Ports
// discovery is unavailable, falls back to default" and "Discovery failure
// is not fatal".
func TestDiscover_SocketMissing(t *testing.T) {
	missing := shortMissingSocket(t)

	url, ok := discover(context.Background(), missing, 500*time.Millisecond)
	if ok {
		t.Fatalf("expected ok=false for missing socket, got url=%q", url)
	}
	if url != "" {
		t.Errorf("url = %q, want empty string", url)
	}
}

// TestDiscover_RPCError verifies a server-side RPC error returns ok=false
// rather than propagating. Covers spec scenario "Daemon.Ports discovery is
// unavailable, falls back to default" (RPC errors path).
func TestDiscover_RPCError(t *testing.T) {
	sock := newFakeDaemon(t, &fakePortsSvc{err: errors.New("simulated RPC failure")})

	url, ok := discover(context.Background(), sock, 2*time.Second)
	if ok {
		t.Fatalf("expected ok=false on RPC error, got url=%q", url)
	}
	if url != "" {
		t.Errorf("url = %q, want empty string", url)
	}
}

// TestDiscover_Timeout verifies a hung socket is abandoned within the
// configured timeout. Covers spec scenario "Discovery is bounded by a
// short timeout".
func TestDiscover_Timeout(t *testing.T) {
	sockPath := shortSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	// Accept connections but never respond. The client should give up on
	// timeout.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open without responding.
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	const timeout = 200 * time.Millisecond
	start := time.Now()
	url, ok := discover(context.Background(), sockPath, timeout)
	elapsed := time.Since(start)

	if ok {
		t.Fatalf("expected ok=false on timeout, got url=%q", url)
	}
	// Allow generous slack on slow CI; the assertion is "did not exceed
	// timeout by more than 5x", which still catches hangs.
	if elapsed > 5*timeout {
		t.Errorf("discover took %v with %v timeout (more than 5x)", elapsed, timeout)
	}
}

// TestDiscover_Timeout500ms exercises the 500 ms timeout required by the
// "Argus base URL discovery" spec ("Discovery is bounded by a short
// timeout"). A stalling server holds the connection open; the client must
// abandon the attempt well before a noticeable startup delay.
func TestDiscover_Timeout500ms(t *testing.T) {
	sockPath := shortSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	const timeout = 500 * time.Millisecond
	start := time.Now()
	url, ok := discover(context.Background(), sockPath, timeout)
	elapsed := time.Since(start)

	if ok {
		t.Fatalf("expected ok=false on 500ms timeout, got url=%q", url)
	}
	if elapsed > 2*timeout {
		t.Errorf("discover took %v with %v timeout (more than 2x)", elapsed, timeout)
	}
}

// TestDiscover_ZeroAPIPort verifies discovery returns ok=false when argus
// reports APIPort=0 (its REST API server is disabled), because there is no
// usable plugin-API URL to construct. Covers the spec requirement that
// discovery only succeeds when a real URL is available; falls back to
// hardcoded default otherwise.
func TestDiscover_ZeroAPIPort(t *testing.T) {
	sock := newFakeDaemon(t, &fakePortsSvc{resp: FakePortsResp{MCPPort: 7841, APIPort: 0}})

	url, ok := discover(context.Background(), sock, 2*time.Second)
	if ok {
		t.Fatalf("expected ok=false when APIPort=0, got url=%q", url)
	}
	if url != "" {
		t.Errorf("url = %q, want empty string", url)
	}
}

// TestDiscover_MalformedResponse verifies that a server that closes the
// connection mid-handshake (the simplest malformed-response stand-in)
// returns ok=false. Covers the "any error... returns without logging at
// error level" branch of the spec.
func TestDiscover_MalformedResponse(t *testing.T) {
	sockPath := shortSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Read the prefix byte then immediately close: jsonrpc on the
			// client side will see EOF / unexpected response.
			var prefix [1]byte
			_, _ = conn.Read(prefix[:])
			_ = conn.Close()
		}
	}()

	url, ok := discover(context.Background(), sockPath, 500*time.Millisecond)
	if ok {
		t.Fatalf("expected ok=false on malformed response, got url=%q", url)
	}
	if url != "" {
		t.Errorf("url = %q, want empty string", url)
	}
}

// TestDefaultDaemonSocketPath sanity-checks that the default socket path
// resolves under the user's home directory. The implementation falls back
// to a relative path only when $HOME is unset, which we don't simulate
// here.
func TestDefaultDaemonSocketPath(t *testing.T) {
	p := DefaultDaemonSocketPath()
	if !strings.HasSuffix(p, "/.argus/daemon.sock") {
		t.Errorf("DefaultDaemonSocketPath = %q, want suffix /.argus/daemon.sock", p)
	}
}
