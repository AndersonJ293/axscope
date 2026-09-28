// MCP server over stdio talking to the same daemon as the CLI, so both share one
// browser and one set of refs; JSON-RPC 2.0 per line, hand-rolled for zero deps.
package mcpsrv

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AndersonJ293/axscope/internal/command"
	"github.com/AndersonJ293/axscope/internal/daemonclient"
	"github.com/AndersonJ293/axscope/internal/paths"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

const protocolVersion = "2025-06-18"

// defaultCallMinutes bounds a tool call when the client does not cancel and the
// daemon never answers, so the call cannot hang an agent forever.
const defaultCallMinutes = 10

// sessionOnce keeps the auto-provisioned session stable for the process life:
// the first call decides it, every later one reads the same id.
var (
	sessionOnce     sync.Once
	sessionID       string
	autoProvisioned bool
)

// defaultAutoIdleMinutes is how long an auto-provisioned session may sit without
// a command before the daemon shuts itself down. It is an MCP-only default: an
// explicit AXSCOPE_IDLE_MINUTES (0 = never) wins, and explicit sessions keep the
// daemon up until `stop`.
const defaultAutoIdleMinutes = "30"

// ensureSession gives the MCP server a session of its own unless AXSCOPE_SESSION
// was set, which joins that one on purpose (two instances sharing a session is
// the explicit way to survive a client restart). The CLI keeps `default`, so an
// MCP instance no longer shares the browser, the tabs and the refs by accident.
func ensureSession(clientName string) string {
	sessionOnce.Do(func() {
		if v := os.Getenv("AXSCOPE_SESSION"); v != "" {
			sessionID = v
			return
		}
		sessionID = autoSession(clientName)
		autoProvisioned = true
		_ = os.Setenv("AXSCOPE_SESSION", sessionID)
		if os.Getenv("AXSCOPE_IDLE_MINUTES") == "" {
			_ = os.Setenv("AXSCOPE_IDLE_MINUTES", defaultAutoIdleMinutes)
		}
	})
	return sessionID
}

// stopAutoSession takes down the daemon an auto-provisioned session brought up,
// so an MCP client exiting does not leave an orphan browser behind. An explicit
// session is left alone: it is shared on purpose, and its owner stops it.
func stopAutoSession() {
	if !autoProvisioned {
		return
	}
	socket := paths.SocketPath(os.Getenv("AXSCOPE_SESSION"))
	if _, err := os.Stat(socket); err != nil {
		return // the daemon never came up, or is already gone
	}
	_, _ = daemonclient.SendTo(socket, protocol.Request{Cmd: "stop"})
}

// autoSession is a short, filesystem-safe id: the client name and four random
// hex characters, e.g. `opencode-a1b2`.
func autoSession(clientName string) string {
	return sessionPrefix(clientName) + "-" + randomHex(2)
}

// sessionPrefix lowercases the client name into the [a-z0-9-] the session path
// accepts, bounded so the socket name stays short.
func sessionPrefix(clientName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(clientName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	prefix := strings.Trim(b.String(), "-")
	if len(prefix) > 20 {
		prefix = strings.Trim(prefix[:20], "-")
	}
	if prefix == "" {
		return "mcp"
	}
	return prefix
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail in practice; the pid keeps the id unique.
		return strconv.Itoa(os.Getpid())
	}
	return hex.EncodeToString(buf)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// server is the stdio transport state. A tool call runs in its own goroutine, so
// a slow or stuck command no longer freezes the whole server: ping keeps
// answering and a cancellation is read and delivered while the call is in
// flight. The writer is shared, so every response takes the lock.
type server struct {
	ctx    context.Context
	writer *bufio.Writer
	wmu    sync.Mutex

	mu    sync.Mutex
	calls map[string]context.CancelFunc

	// wg tracks the tool calls in flight so stdin EOF drains them: a pipe still
	// reading stdout expects their responses.
	wg sync.WaitGroup

	// call runs a tool; a test swaps it. Nil means callTool.
	call func(context.Context, string, map[string]any) map[string]any
}

// Run serves the MCP protocol on stdin/stdout until EOF.
func Run(ctx context.Context) error {
	// The daemon is detached (Setsid) so the browser survives between commands;
	// without this an auto session would outlive the client that asked for it.
	defer stopAutoSession()

	s := &server{
		ctx:    ctx,
		writer: bufio.NewWriter(os.Stdout),
		calls:  map[string]context.CancelFunc{},
	}

	ppid := os.Getppid()
	touch()
	claimNewest(ppid)
	defer releaseNewest(ppid)

	// stdin is read on its own goroutine: a blocked read must not keep the
	// server from hearing a signal (the client's SIGTERM used to be swallowed)
	// or the watchdog.
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		reader := bufio.NewReaderSize(os.Stdin, 8<<20)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				lines <- line
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()

	watch := time.NewTicker(watchEvery)
	defer watch.Stop()
	var runErr error
loop:
	for {
		select {
		case line := <-lines:
			touch()
			s.dispatch(line)
		case err := <-readErr:
			if err != io.EOF {
				runErr = err
			}
			break loop
		case <-ctx.Done():
			// The calls in flight run on s.ctx, which is ctx: they are cancelled
			// and answer promptly, so the drain below is short.
			break loop
		case <-watch.C:
			if exit, why := shouldExit(ppid, os.Getppid(), superseded(ppid), idleFor(), s.inFlight()); exit {
				fmt.Fprintf(os.Stderr, "axscope mcp: exiting — %s\n", why)
				break loop
			}
		}
	}
	// A pipe still reading stdout expects the calls in flight to answer. Each has
	// its own bound (callTimeout), so the drain cannot wait forever.
	s.wg.Wait()
	s.flush()
	return runErr
}

// inFlight counts the tool calls running now.
func (s *server) inFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// dispatch routes one line. A notification is handled inline (a cancellation
// must not sit behind the call it cancels); a tools/call runs in its own
// goroutine; everything else stays inline, keeping the handshake ordered.
func (s *server) dispatch(line []byte) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return
	}
	var req rpcRequest
	if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
		return
	}
	if req.Method == "notifications/cancelled" {
		s.cancel(req.Params)
		return
	}
	// A notification without an id never gets a response.
	if len(req.ID) == 0 {
		return
	}
	if req.Method == "tools/call" {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(req)
		}()
		return
	}
	s.handle(req)
}

// handleLine is the single-request path, kept for tests: it builds a server over
// the writer and handles the line synchronously.
func handleLine(ctx context.Context, line []byte, writer *bufio.Writer) {
	s := &server{ctx: ctx, writer: writer, calls: map[string]context.CancelFunc{}}
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return
	}
	var req rpcRequest
	if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
		return
	}
	if len(req.ID) == 0 {
		return
	}
	s.handle(req)
}

// handle answers one request. The caller decides where it runs.
func (s *server) handle(req rpcRequest) {
	switch req.Method {
	case "initialize":
		// The MCP client name (opencode, claude, cursor…) names the tab group,
		// unless AXSCOPE_AGENT is set: an explicit choice wins over auto-detection.
		var params struct {
			ClientInfo struct {
				Name string `json:"name"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(req.Params, &params)
		name := displayName(params.ClientInfo.Name)
		if name != "" && os.Getenv("AXSCOPE_AGENT") == "" {
			_ = os.Setenv("AXSCOPE_AGENT", name)
		}
		ensureSession(name)
		s.write(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "axscope", "version": "0.1.0"},
			"instructions":    instructions(),
		}})

	case "ping":
		s.write(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})

	case "tools/list":
		s.write(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools()}})

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.write(errResponse(req.ID, -32602, "invalid params"))
			return
		}
		ctx, cancel := s.beginCall(req.ID)
		result := s.caller()(ctx, params.Name, params.Arguments)
		s.endCall(req.ID, cancel)
		s.write(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})

	case "resources/list":
		s.write(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"resources": []any{}}})

	case "prompts/list":
		s.write(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"prompts": []any{}}})

	default:
		s.write(errResponse(req.ID, -32601, "unsupported method: "+req.Method))
	}
}

// caller is the tool runner, defaulting to callTool.
func (s *server) caller() func(context.Context, string, map[string]any) map[string]any {
	if s.call != nil {
		return s.call
	}
	return callTool
}

// beginCall registers a tool call under a cancellable, bounded context. The bound
// is a backstop for a client that never cancels: AXSCOPE_MCP_TIMEOUT_MINUTES
// (0 = no bound), so a daemon that never answers cannot hold the call forever.
func (s *server) beginCall(id json.RawMessage) (context.Context, context.CancelFunc) {
	var ctx context.Context
	var cancel context.CancelFunc
	if d := callTimeout(); d > 0 {
		ctx, cancel = context.WithTimeout(s.ctx, d)
	} else {
		ctx, cancel = context.WithCancel(s.ctx)
	}
	s.mu.Lock()
	s.calls[idKey(id)] = cancel
	s.mu.Unlock()
	return ctx, cancel
}

// endCall forgets a finished call and releases its context.
func (s *server) endCall(id json.RawMessage, cancel context.CancelFunc) {
	s.mu.Lock()
	delete(s.calls, idKey(id))
	s.mu.Unlock()
	cancel()
}

// cancel delivers a client cancellation to the in-flight call, if it is still
// running. A late cancel for a finished call is a no-op.
func (s *server) cancel(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	s.mu.Lock()
	cancel := s.calls[idKey(p.RequestID)]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// write sends one response and flushes it: a concurrent caller must not sit in
// the buffer until the next request.
func (s *server) write(resp rpcResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.writer.Write(append(data, '\n'))
	_ = s.writer.Flush()
}

func (s *server) flush() {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = s.writer.Flush()
}

// callTimeout bounds one tool call. It is a safety net, not the expected path:
// a well-behaved client cancels first. 0 disables the bound.
func callTimeout() time.Duration {
	minutes := defaultCallMinutes
	if v := os.Getenv("AXSCOPE_MCP_TIMEOUT_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			minutes = n
		}
	}
	if minutes <= 0 {
		return 0
	}
	return time.Duration(minutes) * time.Minute
}

// idKey normalizes a JSON-RPC id so a cancellation's numeric or string form
// matches the id the request carried (clients are not consistent about it).
func idKey(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return ""
	}
	if s[0] == '"' {
		var v string
		if json.Unmarshal(raw, &v) == nil {
			return v
		}
	}
	return s
}

func callTool(ctx context.Context, name string, args map[string]any) map[string]any {
	if args == nil {
		args = map[string]any{}
	}
	// Tools arrive after initialize; this covers a client that skips it.
	ensureSession("")
	resp, err := daemonclient.SendContext(ctx, protocol.Request{Cmd: name, Args: args})
	if err != nil {
		return toolText("error: "+err.Error(), true)
	}
	if !resp.OK {
		return toolText("error: "+resp.Error, true)
	}
	if tip := batchTips.observe(name, time.Now()); tip != "" {
		resp.Text += "\n" + tip
	}
	if b64 := resp.Image; b64 != nil {
		return map[string]any{"content": []map[string]any{
			{"type": "text", "text": resp.Text},
			{"type": "image", "data": b64.Base64, "mimeType": b64.MimeType},
		}}
	}
	return toolText(resp.Text, false)
}

// batchTip nudges an agent that drives the page one action per call toward
// batch: the same steps in one round trip. It speaks once per streak and then
// only every tipEvery actions, so it informs without becoming noise.
type batchTip struct {
	mu     sync.Mutex
	streak int
	last   time.Time
}

const (
	tipAfter = 3
	tipEvery = 8
	// tipGap ends a streak: calls this far apart are not a sequence a batch
	// would have saved.
	tipGap = 90 * time.Second
)

var batchTips batchTip

// actionTools are the calls a batch can fold together; reads (snap, read, find)
// are how an agent decides the next step, so they do not count.
var actionTools = map[string]bool{
	"click": true, "hover": true, "fill": true, "type": true, "press": true,
	"select": true, "check": true, "uncheck": true, "scroll": true, "open": true,
	"wait": true, "waitgone": true, "drag": true, "upload": true, "dialog": true,
	"back": true, "forward": true, "reload": true, "tab": true, "newtab": true,
}

func (t *batchTip) observe(tool string, now time.Time) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !actionTools[tool] || now.Sub(t.last) > tipGap {
		t.streak = 0
	}
	t.last = now
	if !actionTools[tool] {
		return ""
	}
	t.streak++
	if t.streak == tipAfter || (t.streak > tipAfter && (t.streak-tipAfter)%tipEvery == 0) {
		return fmt.Sprintf("tip: %d single actions in a row — `batch` runs known steps in one call (steps=[\"fill e5 x\", \"click e7\"], snap=final), much faster", t.streak)
	}
	return ""
}

func toolText(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// coreMCP is the default catalog. Each schema costs context on every request and
// clients cap the tool count across all servers (Cursor at 40), so the catalog
// is the few calls an agent makes directly, and every other command runs as a
// step of batch, whose description carries their grammar. shot stays a tool of
// its own because it answers an image, which a batch step cannot.
// AXSCOPE_MCP_TOOLS=all exposes one tool per command instead.
var coreMCP = map[string]bool{
	"batch": true,
	"snap":  true,
	"open":  true,
	"click": true,
	"fill":  true,
	"wait":  true,
	"shot":  true,
	"help":  true,
}

// tools derives the tools from the command table, so CLI and MCP do not diverge.
func tools() []toolDef {
	all := os.Getenv("AXSCOPE_MCP_TOOLS") == "all"
	out := make([]toolDef, 0, len(command.Specs))
	for _, spec := range command.Specs {
		if mcpHidden(spec.Cmd) {
			continue
		}
		if !all && !coreMCP[spec.Cmd] {
			continue
		}
		props := map[string]any{}
		optional := map[string]bool{}
		for _, o := range spec.Optional {
			optional[o] = true
		}
		var required []string
		for _, p := range spec.Positional {
			props[p] = map[string]any{"type": "string"}
			if !optional[p] {
				required = append(required, p)
			}
		}
		for _, f := range spec.Flags {
			props[f] = map[string]any{"type": "boolean"}
		}
		for name, prop := range schemaOverrides[spec.Cmd] {
			props[name] = prop
		}
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		desc := spec.Help
		if spec.Cmd == "batch" && !all {
			desc += ". " + stepGrammar()
		}
		out = append(out, toolDef{
			Name:        spec.Cmd,
			Description: desc,
			InputSchema: schema,
		})
	}
	return out
}

// stepGrammar lists the commands that are not tools of their own, in the step
// syntax, so an agent knows they exist and how to write them without a call to
// help. It is derived from command.Specs, so it cannot drift from the parser.
func stepGrammar() string {
	var parts []string
	for _, spec := range command.Specs {
		if coreMCP[spec.Cmd] || mcpHidden(spec.Cmd) || cliOnlyMCP[spec.Cmd] {
			continue
		}
		optional := map[string]bool{}
		for _, o := range spec.Optional {
			optional[o] = true
		}
		line := spec.Cmd
		for _, p := range spec.Positional {
			if optional[p] {
				line += " [" + p + "]"
			} else {
				line += " <" + p + ">"
			}
		}
		for _, f := range spec.Flags {
			line += " [--" + f + "]"
		}
		if spec.Rest {
			line += " (" + spec.Positional[len(spec.Positional)-1] + " = the rest of the line)"
		}
		parts = append(parts, line)
	}
	return "Every other command runs as a step (`help` explains each): " + strings.Join(parts, "; ")
}

// cliOnlyMCP are commands that exist for the CLI's own housekeeping; listing
// them to an agent is noise.
var cliOnlyMCP = map[string]bool{"engines": true, "clean": true, "script": true, "ping": true}

// schemaOverrides types the arguments the command table can only call strings.
// A batch's steps are a list — asking a model to JSON-encode it inside a string
// is how quoting bugs are born.
var schemaOverrides = map[string]map[string]any{
	"batch": {
		"steps": map[string]any{
			"type":        "array",
			"description": `commands in order; each is a command line ("click e3", "fill css=#q 'hello world'") or an object ({"cmd":"fill","target":"e5","value":"x"})`,
			"items": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "object", "properties": map[string]any{"cmd": map[string]any{"type": "string"}}, "required": []string{"cmd"}},
				},
			},
		},
		"snap": map[string]any{"type": "string", "enum": []string{"none", "final"}, "description": "final appends a snap after the last step"},
	},
}

// mcpHidden reports the commands that are never MCP tools: the daemon or the CLI
// settles stop/install/sessions before any browser action is involved. `ping` is
// not here: it is the client's liveness check and works without a browser.
func mcpHidden(cmd string) bool {
	switch cmd {
	case "stop", "install", "sessions":
		return true
	}
	return false
}

// mcpCommands is how many commands the full catalog would expose.
func mcpCommands() int {
	n := 0
	for _, spec := range command.Specs {
		if !mcpHidden(spec.Cmd) {
			n++
		}
	}
	return n
}

// instructions says, at handshake time, that the catalog is curated: a client
// that sees a partial list otherwise assumes the rest does not exist and
// reimplements it with `eval`.
func instructions() string {
	exposed, total := len(tools()), mcpCommands()
	note := "axscope drives a real browser (snap → ref → act). Prefer `batch` for known steps: it runs a list of commands in one call and snap=final reads the screen at the end. "
	if exposed >= total {
		note += fmt.Sprintf("All %d commands are exposed; call the `help` tool for the list with their arguments.", total)
	} else {
		// The grammar is repeated here, not only in batch's description: a client
		// may keep a tool's schema from an earlier connection (Claude Code served
		// batch without it after a reconnect), while the instructions arrive
		// fresh with every handshake.
		note += fmt.Sprintf("%d tools cover the %d commands: the rest run as steps of `batch`. AXSCOPE_MCP_TOOLS=all exposes one tool per command instead. %s.", exposed, total, stepGrammar())
	}
	return note + " This MCP server has a browser session of its own (`status` names it; set AXSCOPE_SESSION to share one)."
}

// displayName turns the MCP client name into a readable label:
// "opencode" -> "Opencode", "claude-desktop" -> "Claude Desktop".
func displayName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' '
	})
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func errResponse(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}
