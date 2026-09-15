// Minimal Chrome DevTools Protocol client over WebSocket.
//
// The protocol is JSON-RPC with `id`. With `flatten: true`, each tab becomes a
// "session" identified by `sessionId`, so a single connection at the browser
// level covers all tabs.
package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Error is an error returned by the browser (CDP `error` frame).
type Error struct {
	Method string
	Code   int
	Msg    string
}

func (e *Error) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("cdp %s: %s (code %d)", e.Method, e.Msg, e.Code)
	}
	return fmt.Sprintf("cdp %s: %s", e.Method, e.Msg)
}

// Handler receives the params of a CDP event and the originating session (if any).
type Handler func(params json.RawMessage, sessionID string)

type message struct {
	ID        *int64          `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type pending struct {
	ch     chan *message
	method string
}

type handlerEntry struct {
	id int64
	fn Handler
}

// Client is a live CDP connection to the browser.
type Client struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc

	writeMu  sync.Mutex
	stateMu  sync.Mutex
	nextID   int64
	nextHid  int64
	pending  map[int64]*pending
	handlers map[string][]handlerEntry
	closed   bool
	closeErr error
	done     chan struct{}
}

// Dial connects and starts reading events.
func Dial(ctx context.Context, url string, timeout time.Duration) (*Client, error) {
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, _, err := websocket.Dial(dctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", url, err)
	}
	return FromConn(conn), nil
}

// FromConn adopts an already-open connection (e.g., the extension that
// connected to the daemon) and starts reading events. This way the rest of the
// driver does not know — nor needs to know — where the CDP comes from.
func FromConn(conn *websocket.Conn) *Client {
	// Screenshots in base64 can be large.
	conn.SetReadLimit(256 << 20)

	base, baseCancel := context.WithCancel(context.Background())
	c := &Client{
		conn:     conn,
		ctx:      base,
		cancel:   baseCancel,
		pending:  make(map[int64]*pending),
		handlers: make(map[string][]handlerEntry),
		done:     make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *Client) readLoop() {
	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			c.fail(err)
			return
		}
		var msg message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID != nil {
			c.stateMu.Lock()
			p := c.pending[*msg.ID]
			delete(c.pending, *msg.ID)
			c.stateMu.Unlock()
			if p != nil {
				p.ch <- &msg
			}
			continue
		}
		if msg.Method == "" {
			continue
		}
		c.dispatch(msg.Method, msg.Params, msg.SessionID)
	}
}

func (c *Client) dispatch(method string, params json.RawMessage, sessionID string) {
	c.stateMu.Lock()
	specific := append([]handlerEntry(nil), c.handlers[method]...)
	wildcard := append([]handlerEntry(nil), c.handlers["*"]...)
	c.stateMu.Unlock()

	for _, h := range specific {
		h.fn(params, sessionID)
	}
	for _, h := range wildcard {
		h.fn(params, sessionID)
	}
}

func (c *Client) fail(err error) {
	c.stateMu.Lock()
	if c.closed {
		c.stateMu.Unlock()
		return
	}
	c.closed = true
	c.closeErr = err
	for _, p := range c.pending {
		close(p.ch)
	}
	c.pending = make(map[int64]*pending)
	c.stateMu.Unlock()
	c.cancel()
	close(c.done)
}

// Send sends a command and waits for the result.
func (c *Client) Send(ctx context.Context, method string, params any, sessionID string) (json.RawMessage, error) {
	return c.SendTimeout(ctx, method, params, sessionID, 30*time.Second)
}

// SendTimeout is Send with an explicit deadline.
func (c *Client) SendTimeout(ctx context.Context, method string, params any, sessionID string, timeout time.Duration) (json.RawMessage, error) {
	c.stateMu.Lock()
	if c.closed {
		err := c.closeErr
		c.stateMu.Unlock()
		return nil, fmt.Errorf("CDP connection closed: %w", err)
	}
	id := c.nextID
	c.nextID++
	p := &pending{ch: make(chan *message, 1), method: method}
	c.pending[id] = p
	c.stateMu.Unlock()

	payload := map[string]any{"id": id, "method": method}
	if params == nil {
		payload["params"] = map[string]any{}
	} else {
		payload["params"] = params
	}
	if sessionID != "" {
		payload["sessionId"] = sessionID
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		c.stateMu.Lock()
		delete(c.pending, id)
		c.stateMu.Unlock()
		return nil, err
	}

	c.writeMu.Lock()
	writeErr := c.conn.Write(ctx, websocket.MessageText, raw)
	c.writeMu.Unlock()
	if writeErr != nil {
		c.stateMu.Lock()
		delete(c.pending, id)
		c.stateMu.Unlock()
		return nil, fmt.Errorf("writing %s: %w", method, writeErr)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case msg, ok := <-p.ch:
		if !ok || msg == nil {
			return nil, fmt.Errorf("cdp %s: %w", method, c.Err())
		}
		if msg.Error != nil {
			return nil, &Error{Method: method, Code: msg.Error.Code, Msg: msg.Error.Message}
		}
		return msg.Result, nil
	case <-timer.C:
		c.stateMu.Lock()
		delete(c.pending, id)
		c.stateMu.Unlock()
		return nil, fmt.Errorf("timeout of %s on %s", timeout, method)
	case <-ctx.Done():
		c.stateMu.Lock()
		delete(c.pending, id)
		c.stateMu.Unlock()
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("cdp %s: %w", method, c.Err())
	}
}

// SendJSON is like Send, but deserializes the result into `out`.
func (c *Client) SendJSON(ctx context.Context, method string, params any, sessionID string, out any) error {
	raw, err := c.Send(ctx, method, params, sessionID)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// On registers a handler for an event. Use "*" for all. Returns the canceller.
func (c *Client) On(method string, h Handler) func() {
	c.stateMu.Lock()
	c.nextHid++
	id := c.nextHid
	c.handlers[method] = append(c.handlers[method], handlerEntry{id: id, fn: h})
	c.stateMu.Unlock()

	return func() {
		c.stateMu.Lock()
		defer c.stateMu.Unlock()
		list := c.handlers[method]
		for i, e := range list {
			if e.id == id {
				c.handlers[method] = append(list[:i], list[i+1:]...)
				return
			}
		}
	}
}

// Notify sends a message without waiting for a response (a CDP notification).
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	payload := map[string]any{"method": method}
	if params != nil {
		payload["params"] = params
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.stateMu.Lock()
	closed := c.closed
	c.stateMu.Unlock()
	if closed {
		return fmt.Errorf("CDP connection closed")
	}
	return c.conn.Write(ctx, websocket.MessageText, raw)
}

// Done closes when the connection drops.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns the reason for closure (nil if still alive).
func (c *Client) Err() error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.closeErr
}

// Close terminates the connection.
func (c *Client) Close() {
	c.stateMu.Lock()
	already := c.closed
	c.stateMu.Unlock()
	if already {
		return
	}
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
	c.fail(fmt.Errorf("closed by client"))
}
