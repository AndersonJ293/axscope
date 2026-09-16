// Daemon: a process that keeps the browser alive and serves commands over a
// unix socket. One connection = one request (JSON per line) + one response.
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
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AndersonJ293/axscope/internal/agent"
	"github.com/AndersonJ293/axscope/internal/paths"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// Options configure the daemon startup.
type Options struct {
	Session  string
	Attach   string
	Headless bool
	Engine   string
}

// capLog prevents the daemon log from growing without limit. The process writes
// to the file with O_APPEND, so truncating is safe.
func capLog(session string) {
	const maxBytes = 2 << 20 // 2 MB
	path := paths.DaemonLogPath(session)
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxBytes {
		return
	}
	_ = os.Truncate(path, 0)
}

// Run brings up the daemon and only returns when it is terminated.
func Run(ctx context.Context, opts Options) error {
	capLog(opts.Session)
	socketPath := paths.SocketPath(opts.Session)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(paths.StateDir(), "sessions"), 0o755); err != nil {
		return err
	}

	// A leftover socket from a dead daemon must not prevent the bind.
	if info, err := os.Stat(socketPath); err == nil && info.Mode()&os.ModeSocket != 0 {
		if _, err := os.Stat(socketPath); err == nil {
			if !socketAlive(socketPath) {
				_ = os.Remove(socketPath)
			}
		}
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("opening socket %s: %w", socketPath, err)
	}
	defer ln.Close()

	ag := &agent.Agent{Session: opts.Session, Attach: opts.Attach, Headless: opts.Headless, Engine: opts.Engine}
	defer ag.Close()

	writeInfo(opts.Session, socketPath)

	stop := make(chan struct{})
	var stopOnce sync.Once
	shutdown := func() { stopOnce.Do(func() { close(stop) }) }

	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())

	go func() {
		<-ctx.Done()
		shutdown()
	}()

	// Close the listener when asked to stop, unblocking Accept.
	go func() {
		<-stop
		_ = ln.Close()
	}()

	// Idle shutdown: without this the browser stays up forever (and consuming
	// memory) after the agent finishes. 0 disables the feature.
	if idle := idleTimeout(); idle > 0 {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					idleFor := time.Since(time.Unix(0, lastActivity.Load()))
					if idleFor >= idle {
						shutdown()
						return
					}
				}
			}
		}()
	}

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
		lastActivity.Store(time.Now().UnixNano())
		go handle(ctx, conn, ag, shutdown)
	}
}

// idleTimeout reads AXSCOPE_IDLE_MINUTES (default 0 = disabled). It is off by
// default because closing the browser makes the next call start a new Chrome.
func idleTimeout() time.Duration {
	minutes := 0
	if v := os.Getenv("AXSCOPE_IDLE_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			minutes = n
		}
	}
	if minutes <= 0 {
		return 0
	}
	return time.Duration(minutes) * time.Minute
}

func handle(ctx context.Context, conn net.Conn, ag *agent.Agent, shutdown func()) {
	defer conn.Close()
	// A panic must not bring down the daemon and the browser: it becomes an
	// error in the response, with the panic text.
	defer func() {
		if r := recover(); r != nil {
			writeResponse(conn, protocol.Fail(fmt.Errorf("panic in daemon: %v", r)))
		}
	}()
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
		writeResponse(conn, protocol.Fail(fmt.Errorf("invalid request: %w", err)))
		return
	}

	if req.Cmd == "stop" {
		writeResponse(conn, protocol.Response{OK: true, Text: "shutting down"})
		shutdown()
		return
	}

	resp := ag.Run(ctx, req)
	writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, resp protocol.Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return // unreachable: Response has only scalar fields
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
	// Primitive values only, so marshal cannot fail.
	data, _ := json.MarshalIndent(map[string]any{
		"session": session,
		"socket":  socketPath,
		"pid":     pid,
	}, "", "  ")
	_ = os.WriteFile(paths.DaemonInfoPath(session), data, 0o644)
}
