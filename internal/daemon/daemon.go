// Daemon: um processo que mantém o browser vivo e atende comandos por um socket
// unix. Uma conexão = um pedido (JSON por linha) + uma resposta.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/ajunior/browser-use/internal/agent"
	"github.com/ajunior/browser-use/internal/paths"
	"github.com/ajunior/browser-use/internal/protocol"
)

// Options configuram a subida do daemon.
type Options struct {
	Session  string
	Attach   string
	Headless bool
	Engine   string
}

// Run sobe o daemon e só retorna quando ele é encerrado.
func Run(ctx context.Context, opts Options) error {
	socketPath := paths.SocketPath(opts.Session)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(paths.StateDir(), "sessions"), 0o755); err != nil {
		return err
	}

	// Socket remanescente de um daemon morto não pode impedir o bind.
	if info, err := os.Stat(socketPath); err == nil && info.Mode()&os.ModeSocket != 0 {
		if _, err := os.Stat(socketPath); err == nil {
			if !socketAlive(socketPath) {
				_ = os.Remove(socketPath)
			}
		}
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("abrindo socket %s: %w", socketPath, err)
	}
	defer ln.Close()

	ag := &agent.Agent{Session: opts.Session, Attach: opts.Attach, Headless: opts.Headless, Engine: opts.Engine}
	defer ag.Close()

	writeInfo(opts.Session, socketPath)

	stop := make(chan struct{})
	var stopOnce sync.Once
	shutdown := func() { stopOnce.Do(func() { close(stop) }) }

	go func() {
		<-ctx.Done()
		shutdown()
	}()

	// Fecha o listener quando pedirem parada, destravando o Accept.
	go func() {
		<-stop
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-stop:
				cleanup(socketPath, opts.Session)
				return nil
			default:
				if errors.Is(err, net.ErrClosed) {
					cleanup(socketPath, opts.Session)
					return nil
				}
				continue
			}
		}
		go handle(ctx, conn, ag, shutdown)
	}
}

func handle(ctx context.Context, conn net.Conn, ag *agent.Agent, shutdown func()) {
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, 1<<20)
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return
	}
	line = trimNewline(line)
	if len(line) == 0 {
		return
	}

	var req protocol.Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeResponse(conn, protocol.Fail(fmt.Errorf("pedido inválido: %w", err)))
		return
	}

	if req.Cmd == "stop" {
		writeResponse(conn, protocol.Response{OK: true, Text: "encerrando"})
		shutdown()
		return
	}

	resp := ag.Run(ctx, req)
	writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, resp protocol.Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(data, '\n'))
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func socketAlive(socketPath string) bool {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func cleanup(socketPath, session string) {
	_ = os.Remove(socketPath)
	_ = os.Remove(paths.DaemonInfoPath(session))
}

func writeInfo(session, socketPath string) {
	pid := os.Getpid()
	data, _ := json.MarshalIndent(map[string]any{
		"session": session,
		"socket":  socketPath,
		"pid":     pid,
	}, "", "  ")
	_ = os.WriteFile(paths.DaemonInfoPath(session), data, 0o644)
}
