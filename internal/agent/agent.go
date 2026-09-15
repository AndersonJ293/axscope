// Agent: runs the commands over the browser session and returns text for the
// AI agent to read. It is the same dispatcher for CLI, daemon (script) and MCP.
package agent

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/AndersonJ293/axscope/internal/bridge"
	"github.com/AndersonJ293/axscope/internal/browser"
	// cdp is here only to load the *cdp.Client type: whoever speaks the protocol
	// is browser/dom. The conscious exception is the extension bridge, which
	// returns a ready CDP client.
	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/paths"
	"github.com/AndersonJ293/axscope/internal/protocol"
	"github.com/AndersonJ293/axscope/internal/render"
)

const (
	actionIdle = 300 * time.Millisecond
	navTimeout = 45 * time.Second
)

// Agent keeps the browser and the state between commands.
type Agent struct {
	Session  string
	Attach   string
	Headless bool
	Engine   string

	runMu sync.Mutex

	mu     sync.Mutex
	handle *browser.Handle
	sess   *browser.Session
	refs   map[string]int
	// snapGen is the generation of the last read. Every ref carries the
	// generation it was born in (e12#7): a ref from an old read is refused,
	// instead of clicking what today occupies that position.
	snapGen   int
	bridge    *bridge.Server
	extClient *cdp.Client
	agent     string
}

// Close shuts down the browser (if we started it) and the connection.
func (a *Agent) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handle != nil {
		a.handle.Client.Close()
		if !a.handle.Attached {
			a.handle.Kill()
		}
	}
	a.handle = nil
	a.sess = nil
}

// degraded says whether the browser we have is no longer good (it died or the
// connection dropped). Without this, a dead browser would leave the daemon
// alive answering errors forever.
func (a *Agent) degraded() bool {
	if a.handle == nil || a.sess == nil {
		return true
	}
	if a.handle.Client.Err() != nil {
		return true
	}
	if a.handle.Exited() {
		return true
	}
	return false
}

// extension brings up the bridge (once) and waits for the extension to connect.
func (a *Agent) extension(ctx context.Context) (*cdp.Client, error) {
	if a.bridge == nil {
		port := 0
		if v := os.Getenv("AXSCOPE_BRIDGE_PORT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				port = n
			}
		}
		srv, err := bridge.Start(a.Session, port)
		if err != nil {
			return nil, err
		}
		srv.SetLabel(a.agent)
		a.bridge = srv
	}
	if a.extClient != nil && a.extClient.Err() == nil {
		return a.extClient, nil
	}
	client, err := a.bridge.Wait(ctx, 20*time.Second)
	if err != nil {
		return nil, err
	}
	a.extClient = client
	return client, nil
}

func (a *Agent) ensure(ctx context.Context) (*browser.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.degraded() {
		return a.sess, nil
	}
	// Discard what died before bringing it up again.
	if a.handle != nil {
		a.handle.Client.Close()
		if !a.handle.Attached && !a.handle.Exited() {
			a.handle.Kill()
		}
		a.handle = nil
		a.sess = nil
		a.refs = nil
	}
	var handle *browser.Handle
	var err error
	switch {
	case a.Attach != "":
		handle, err = browser.Attach(ctx, a.Attach)
	case a.Engine == browser.EngineExt:
		// We do not launch any browser: the extension in Brave connects to us.
		var client *cdp.Client
		client, err = a.extension(ctx)
		if err == nil {
			handle = &browser.Handle{Client: client, Executable: "(extension)", Attached: true}
		}
	default:
		handle, err = browser.Launch(ctx, browser.LaunchOptions{
			Session:  a.Session,
			Engine:   a.Engine,
			Headless: a.Headless,
		})
	}
	if err != nil {
		return nil, err
	}
	sess, err := browser.NewSession(ctx, handle.Client, false, render.Presenter{})
	if err != nil {
		handle.Client.Close()
		return nil, err
	}
	sess.SetActiveFile(paths.ActiveTabPath(a.Session))
	a.handle = handle
	a.sess = sess
	return sess, nil
}

func (a *Agent) client() *cdp.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handle == nil {
		return nil
	}
	return a.handle.Client
}

// setAgent records who is driving and notifies the extension again, which
// renames the tab group (keeping the number it already had).
func (a *Agent) setAgent(name string) {
	a.mu.Lock()
	if name == "" || a.agent == name {
		a.mu.Unlock()
		return
	}
	a.agent = name
	srv := a.bridge
	a.mu.Unlock()
	if srv != nil {
		srv.SetLabel(name)
	}
}

// Run executes a request. It serializes everything so actions do not mix.
func (a *Agent) Run(ctx context.Context, req protocol.Request) protocol.Response {
	a.runMu.Lock()
	defer a.runMu.Unlock()

	a.setAgent(req.Agent)
	return a.dispatch(ctx, req)
}

// activeSID returns the sessionId of the session's active tab.
func (a *Agent) activeSID(sess *browser.Session) (string, error) {
	return sess.ActiveSID()
}

func (a *Agent) mustSess() *browser.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sess
}

// ok builds a success response.
func ok(text string) protocol.Response {
	return protocol.Response{OK: true, Text: text}
}
