// Package argus's discovery helper resolves the live argus plugin-API base
// URL by calling argus's `Daemon.Ports` JSON-RPC method over its unix socket.
//
// Discovery is best-effort: any failure (socket missing, dial refused, RPC
// error, malformed response, timeout) returns ok=false so the caller can
// fall back to a hardcoded default. Errors are not logged at error level;
// see the "Argus base URL discovery" requirement in the
// `plannotator-argus-plugin` spec for the full contract.
//
// TODO(aaron, argus PR #630): the wire shape below is derived from argus's
// in-tree code at the time of writing. The argus daemon exposes an
// unauthenticated JSON-RPC server over `~/.argus/daemon.sock`. A connection
// must first write a single dispatch byte ('R') before the server installs
// its `net/rpc/jsonrpc` codec (see argus `internal/daemon/daemon.go`'s
// `handleConn`). The method is `Daemon.Ports`, args are an empty struct, and
// the response is `{MCPPort int, APIPort int}` (see argus
// `internal/daemon/types.go`'s `PortsResp`). plannotator-argus wants the
// `APIPort` because `/api/mcp/tools` lives on argus's REST API server, not
// the MCP one. If PR #630 ships a different surface (method name, prefix
// byte, response field, additional transport), update the constants and
// `portsResp` struct below; the rest of this file is intentionally thin so
// the change is a few-line patch.
package argus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"time"
)

// DefaultDaemonSocketPath returns the canonical path of argus's daemon
// socket (`$HOME/.argus/daemon.sock`). Mirrors argus's own
// `daemon.DefaultSocketPath` without importing argus as a dependency.
func DefaultDaemonSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// Fall through with an obviously-bad path so Discover's caller still
		// gets ok=false rather than a panic. Discover will surface this as a
		// "dial: no such file" outcome which is the right behavior.
		return filepath.Join(".argus", "daemon.sock")
	}
	return filepath.Join(home, ".argus", "daemon.sock")
}

// rpcPrefixByte is the dispatch byte the argus daemon expects on every
// JSON-RPC connection ('R' for RPC, vs. 'S' for streaming).
const rpcPrefixByte = 'R'

// rpcMethodPorts is argus's discovery method name.
const rpcMethodPorts = "Daemon.Ports"

// portsResp mirrors argus's `daemon.PortsResp`. Kept inline (rather than
// importing the argus module) so plannotator-argus stays a thin client with
// no upstream module coupling.
type portsResp struct {
	MCPPort int
	APIPort int
}

// rpcEmpty mirrors argus's `daemon.Empty` — a placeholder for RPC methods
// that take no arguments.
type rpcEmpty struct{}

// Discover attempts to acquire argus's plugin-API base URL via the
// `Daemon.Ports` RPC. On success it returns `(http://127.0.0.1:<APIPort>,
// true)`. On any failure it returns `("", false)`; the caller should fall
// back to a hardcoded default. The call is bounded by `timeout` so an
// unreachable RPC never blocks startup.
func Discover(ctx context.Context, timeout time.Duration) (string, bool) {
	return discover(ctx, DefaultDaemonSocketPath(), timeout)
}

// discover is the testable form of Discover. Tests inject a custom socket
// path pointing at a fake daemon.
func discover(ctx context.Context, sockPath string, timeout time.Duration) (string, bool) {
	resp, err := callDaemonPorts(ctx, sockPath, timeout)
	if err != nil {
		return "", false
	}
	if resp.APIPort <= 0 {
		// Argus's API server is disabled (APIPort=0) — no plugin-API URL
		// to hand back. Caller falls back to the hardcoded default.
		return "", false
	}
	return fmt.Sprintf("http://127.0.0.1:%d", resp.APIPort), true
}

// callDaemonPorts performs the unix-socket dial, prefix-byte handshake,
// jsonrpc call, and response decoding. Errors are returned but not logged;
// the caller folds them into a boolean. The timeout bounds dial, write, and
// the round-trip RPC call together via a derived context.
func callDaemonPorts(ctx context.Context, sockPath string, timeout time.Duration) (*portsResp, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("dial argus daemon socket: %w", err)
	}
	// Ensure deadline applies to the prefix write and the jsonrpc round trip.
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte{rpcPrefixByte}); err != nil {
		return nil, fmt.Errorf("write rpc prefix: %w", err)
	}

	client := jsonrpc.NewClient(conn)
	defer client.Close()

	resp := &portsResp{}
	// Run the RPC on a goroutine so we can race it against the context
	// deadline. jsonrpc.Client.Call is blocking and ignores context, but the
	// underlying conn's SetDeadline above will already error the call out
	// near the deadline; the select here is belt-and-braces in case a
	// future codec change desynchronizes that.
	done := make(chan error, 1)
	go func() {
		done <- client.Call(rpcMethodPorts, &rpcEmpty{}, resp)
	}()
	select {
	case err := <-done:
		if err != nil {
			if errors.Is(err, rpc.ErrShutdown) {
				return nil, fmt.Errorf("rpc shutdown: %w", err)
			}
			return nil, fmt.Errorf("rpc call %s: %w", rpcMethodPorts, err)
		}
		return resp, nil
	case <-dialCtx.Done():
		return nil, fmt.Errorf("rpc call %s: %w", rpcMethodPorts, dialCtx.Err())
	}
}
