// Daemon client: makes sure the daemon is up and sends a request.
package daemonclient

import (
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
	// We do not wait for the process; it is orphaned by design (Setsid).
	_ = cmd.Process.Release()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if socketAlive(socketPath) {
			return socketPath, nil
		}
		time.Sleep(50 * time.Millisecond)
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

// Send ensures the daemon and sends a request, returning the response.
func Send(req protocol.Request) (protocol.Response, error) {
	socketPath, err := EnsureDaemon()
	if err != nil {
		return protocol.Response{}, err
	}
	if req.Agent == "" {
		req.Agent = AgentName()
	}
	return SendTo(socketPath, req)
}

// AgentName is whoever is driving: AXSCOPE_AGENT, or a neutral default.
func AgentName() string {
	if v := os.Getenv("AXSCOPE_AGENT"); v != "" {
		return v
	}
	return "axscope"
}

// SendTo talks directly to a socket, without starting any daemon. This is what
// `stop --all` uses: ensuring the daemon there would resurrect what we want to
// shut down.
func SendTo(socketPath string, req protocol.Request) (protocol.Response, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return protocol.Response{}, err
	}
	defer conn.Close()

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
		return protocol.Response{}, fmt.Errorf("reading daemon response: %w", err)
	}
	return resp, nil
}
