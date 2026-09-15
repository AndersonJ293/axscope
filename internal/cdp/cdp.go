// Cliente mínimo de Chrome DevTools Protocol sobre WebSocket.
//
// O protocolo é JSON-RPC com `id`. Com `flatten: true`, cada aba vira uma
// "sessão" identificada por `sessionId`, então uma única conexão no nível do
// browser cobre todas as abas.
package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Error é um erro devolvido pelo browser (frame `error` do CDP).
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

// Handler recebe os params de um evento CDP e a sessão de origem (se houver).
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

// Client é uma conexão CDP viva com o browser.
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

// Dial conecta e começa a ler eventos.
func Dial(ctx context.Context, url string, timeout time.Duration) (*Client, error) {
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, _, err := websocket.Dial(dctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("conectando em %s: %w", url, err)
	}
	return FromConn(conn), nil
}

// FromConn adota uma conexão já aberta (ex.: a extensão que se conectou ao
// daemon) e começa a ler eventos. Assim o resto do driver não sabe — nem
// precisa saber — de onde o CDP vem.
func FromConn(conn *websocket.Conn) *Client {
	// Screenshots em base64 podem ser grandes.
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

// Send envia um comando e espera o resultado.
func (c *Client) Send(ctx context.Context, method string, params any, sessionID string) (json.RawMessage, error) {
	return c.SendTimeout(ctx, method, params, sessionID, 30*time.Second)
}

// SendTimeout é Send com prazo explícito.
func (c *Client) SendTimeout(ctx context.Context, method string, params any, sessionID string, timeout time.Duration) (json.RawMessage, error) {
	c.stateMu.Lock()
	if c.closed {
		err := c.closeErr
		c.stateMu.Unlock()
		return nil, fmt.Errorf("conexão CDP fechada: %w", err)
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
		return nil, fmt.Errorf("escrevendo %s: %w", method, writeErr)
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
		return nil, fmt.Errorf("timeout de %s em %s", timeout, method)
	case <-ctx.Done():
		c.stateMu.Lock()
		delete(c.pending, id)
		c.stateMu.Unlock()
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("cdp %s: %w", method, c.Err())
	}
}

// SendJSON é como Send, mas desserializa o resultado em `out`.
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

// On registra um handler para um evento. Use "*" para todos. Devolve o cancelador.
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

// Done fecha quando a conexão cai.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err devolve o motivo do fechamento (nil se ainda vivo).
func (c *Client) Err() error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.closeErr
}

// Close encerra a conexão.
func (c *Client) Close() {
	c.stateMu.Lock()
	already := c.closed
	c.stateMu.Unlock()
	if already {
		return
	}
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
	c.fail(fmt.Errorf("encerrado pelo cliente"))
}
