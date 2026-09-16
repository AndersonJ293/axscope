// Bridge to the extension: the daemon opens a local WebSocket, the extension
// connects and forwards CDP to chrome.debugger while synthesizing the Target domain.
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/AndersonJ293/axscope/internal/cdp"
)

// DefaultPort is the first port in the range where the daemon waits for the extension.
const DefaultPort = 8787

// portSpan is how many concurrent sessions fit, one port per session.
const portSpan = 16

// Server accepts connections from the extension and delivers them as CDP clients.
type Server struct {
	mu       sync.Mutex
	listener net.Listener
	http     *http.Server
	port     int
	session  string
	label    string
	last     *cdp.Client
	conns    chan *cdp.Client
}

// SetLabel changes the displayed agent name and notifies the extension, which renames the tab group.
func (s *Server) SetLabel(label string) {
	s.mu.Lock()
	if label == "" || s.label == label {
		s.mu.Unlock()
		return
	}
	s.label = label
	last := s.last
	s.mu.Unlock()
	if last != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = last.Notify(ctx, "__session", map[string]any{"session": s.session, "agent": label})
	}
}

func (s *Server) currentLabel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.label
}

// Start brings up the server on 127.0.0.1 and tells the extension which session it owns; each session takes one port.
func Start(session string, basePort int) (*Server, error) {
	if basePort <= 0 {
		basePort = DefaultPort
	}
	var ln net.Listener
	var lastErr error
	for offset := 0; offset < portSpan; offset++ {
		candidate, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", basePort+offset))
		if err == nil {
			ln = candidate
			break
		}
		lastErr = err
	}
	if ln == nil {
		return nil, fmt.Errorf(
			"no free port between %d and %d for the extension: %w",
			basePort, basePort+portSpan-1, lastErr)
	}
	s := &Server{
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
		session:  session,
		conns:    make(chan *cdp.Client, 4),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/cdp", func(w http.ResponseWriter, r *http.Request) {
		// Only listen on loopback; the chrome-extension:// origin would fail the
		// default Origin check.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		// Handshake: tell the extension which session/tab group is its and how
		// to display that group (the name of the agent that is driving).
		hello, _ := json.Marshal(map[string]any{
			"method": "__session",
			"params": map[string]any{"session": s.session, "agent": s.currentLabel()},
		})
		wctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		_ = conn.Write(wctx, websocket.MessageText, hello)
		cancel()

		client := cdp.FromConn(conn)
		s.mu.Lock()
		s.last = client
		s.mu.Unlock()
		select {
		case s.conns <- client:
		default:
			// There is already an active connection; discard the extra one
			// instead of piling up.
			client.Close()
			return
		}
		<-client.Done()
	})

	s.http = &http.Server{Handler: mux}
	// Serve returns net.ErrClosed after Close; the error is not actionable.
	go func() { _ = s.http.Serve(ln) }()
	return s, nil
}

// Port returns the effective port (useful when 0 was requested).
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// Wait waits for the extension to connect and returns the CDP connection.
func (s *Server) Wait(ctx context.Context, timeout time.Duration) (*cdp.Client, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case c := <-s.conns:
		return c, nil
	case <-timer.C:
		return nil, fmt.Errorf(
			"the axscope extension did not connect on port %d within %s.\n"+
				"Check: (1) Brave is open; (2) the extension is loaded at brave://extensions;\n"+
				"(3) the extension icon shows 'connected'. If Brave is not open, use --chrome or --headless",
			s.Port(), timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close brings down the server.
func (s *Server) Close() {
	if s.http != nil {
		_ = s.http.Close()
	}
}
