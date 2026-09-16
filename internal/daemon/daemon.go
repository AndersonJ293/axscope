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
	"syscall"
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

// listenUnix opens the daemon socket owned by the current user only. The mode
// cannot be left to the umask: if the socket ever ends up writable it becomes
// reachable by other local users.
func listenUnix(socketPath string) (net.Listener, error) {
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// ensureRuntimeDir creates the directory that holds the socket and refuses one
// it does not exclusively own: the temp fallback is a shared, predictable path,
// and a directory controlled by another user lets them replace the socket the
// clients connect to.
func ensureRuntimeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("runtime directory %s is not a directory", dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("runtime directory %s is owned by another user", dir)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("runtime directory %s is not owner-only: %w", dir, err)
		}
	}
	return nil
}

// Run brings up the daemon and only returns when it is terminated.
func Run(ctx context.Context, opts Options) error {
	capLog(opts.Session)
	socketPath := paths.SocketPath(opts.Session)
	if err := ensureRuntimeDir(filepath.Dir(socketPath)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(paths.StateDir(), "sessions"), 0o755); err != nil {
		return err
	}

	// A leftover socket from a dead daemon must not prevent the bind.
	removeStaleSocket(socketPath)

	ln, err := listenUnix(socketPath)
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

// removeStaleSocket removes a socket left behind by a dead daemon so it does
// not block the bind. A live socket, or anything that is not a socket, is kept.
func removeStaleSocket(socketPath string) {
	info, err := os.Stat(socketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return
	}
	if socketAlive(socketPath) {
		return
	}
	_ = os.Remove(socketPath)
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
