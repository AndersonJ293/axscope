// Daemon client: makes sure the daemon is up and sends a request.
package daemonclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/AndersonJ293/axscope/internal/paths"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// EnsureDaemon starts the daemon (detached) if there is not one already serving.
func EnsureDaemon() (string, error) {
	return EnsureDaemonContext(context.Background())
}

// EnsureDaemonContext is EnsureDaemon under the caller's context: a cancelled
// client does not keep waiting out the daemon startup window.
func EnsureDaemonContext(ctx context.Context) (string, error) {
	socketPath := paths.SocketPath(paths.Session())
	if socketAlive(socketPath) {
		return socketPath, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(paths.DaemonLogPath(paths.Session())), 0o755); err != nil {
		return "", err
	}
	logFile, err := os.OpenFile(paths.DaemonLogPath(paths.Session()),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "serve")
	cmd.Env = os.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("starting daemon: %w", err)
	}
	// Do not wait for the process; Setsid orphans it by design.
	_ = cmd.Process.Release()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if socketAlive(socketPath) {
			return socketPath, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("daemon did not come up within 20s (see %s)", paths.DaemonLogPath(paths.Session()))
}

func socketAlive(socketPath string) bool {
	if _, err := os.Stat(socketPath); err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Send ensures the daemon and sends a request. The CLI uses it with a background
// context: a human can interrupt, and a `script` or a long `wait` may run to the
// end. An agent reaches for SendContext instead.
func Send(req protocol.Request) (protocol.Response, error) {
	return SendContext(context.Background(), req)
}

// SendContext is Send under the caller's context. Cancelling it closes the
// connection, so a client is not left reading a response a stuck daemon will
// never write; the daemon notices the close and stops the command too. This is
// how an MCP cancellation releases the daemon's serialized run lock.
func SendContext(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	socketPath, err := EnsureDaemonContext(ctx)
	if err != nil {
		return protocol.Response{}, err
	}
	if req.Agent == "" {
		req.Agent = AgentName()
	}
	return SendToContext(ctx, socketPath, req)
}

// AgentName is whoever is driving: AXSCOPE_AGENT, or a neutral default.
func AgentName() string {
	if v := os.Getenv("AXSCOPE_AGENT"); v != "" {
		return v
	}
	return "axscope"
}

// SendTo talks directly to a socket without starting a daemon; `stop --all` uses
// it to avoid resurrecting what is being shut down.
func SendTo(socketPath string, req protocol.Request) (protocol.Response, error) {
	return SendToContext(context.Background(), socketPath, req)
}

// SendToContext is SendTo under the caller's context. The read has no timeout of
// its own: without one, a daemon that accepted the request and never answered
// would block the client forever, which is exactly how an agent hangs.
func SendToContext(ctx context.Context, socketPath string, req protocol.Request) (protocol.Response, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return protocol.Response{}, err
	}
	defer conn.Close()

	// Decode does not watch the context; closing the connection is what unblocks
	// it, both on a deadline and on an explicit cancel.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	payload, err := json.Marshal(req)
	if err != nil {
		return protocol.Response{}, err
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return protocol.Response{}, err
	}

	dec := json.NewDecoder(conn)
	var resp protocol.Response
	if err := dec.Decode(&resp); err != nil {
		if ctx.Err() != nil {
			return protocol.Response{}, ctx.Err()
		}
		return protocol.Response{}, fmt.Errorf("reading daemon response: %w", err)
	}
	return resp, nil
}
