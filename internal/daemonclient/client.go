// Cliente do daemon: garante que ele está de pé e envia um pedido.
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

// EnsureDaemon sobe o daemon (destacado) se ainda não houver um atendendo.
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
		return "", fmt.Errorf("subindo daemon: %w", err)
	}
	// Não esperamos o processo; ele é órfão por design (Setsid).
	_ = cmd.Process.Release()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if socketAlive(socketPath) {
			return socketPath, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", fmt.Errorf("daemon não subiu em 20s (veja %s)", paths.DaemonLogPath(paths.Session()))
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

// Send garante o daemon e envia um pedido, devolvendo a resposta.
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

// AgentName é quem está dirigindo: AXSCOPE_AGENT, ou um padrão neutro.
func AgentName() string {
	if v := os.Getenv("AXSCOPE_AGENT"); v != "" {
		return v
	}
	return "axscope"
}

// SendTo fala direto com um socket, sem subir daemon nenhum. É o que o
// `stop --all` usa: garantir o daemon ali seria ressuscitar o que se quer fechar.
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
		return protocol.Response{}, fmt.Errorf("lendo resposta do daemon: %w", err)
	}
	return resp, nil
}
