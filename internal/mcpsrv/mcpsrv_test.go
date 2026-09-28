package mcpsrv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// help must be in the default catalog: a client that cannot list the commands
// guesses tool names (or reaches for `eval`) — a real session did exactly that.
func TestHelpIsInTheCuratedCatalog(t *testing.T) {
	t.Setenv("AXSCOPE_MCP_TOOLS", "")
	names := map[string]bool{}
	for _, td := range tools() {
		names[td.Name] = true
	}
	if !names["help"] {
		t.Error("help must be exposed by default")
	}
}

// The auto session is a filesystem path component, so the prefix has to be safe
// and short: anything outside [a-z0-9-] becomes a dash and ".." cannot survive.
func TestSessionPrefix(t *testing.T) {
	cases := []struct {
		client, want string
	}{
		{"opencode", "opencode"},
		{"Claude Desktop", "claude-desktop"},
		{"cursor/../etc", "cursor----etc"},
		{"", "mcp"},
		{"../..", "mcp"},
		{"verylongclientnamehere", "verylongclientnamehe"},
	}
	for _, c := range cases {
		if got := sessionPrefix(c.client); got != c.want {
			t.Errorf("sessionPrefix(%q) = %q, want %q", c.client, got, c.want)
		}
	}
}

// The id shape is the contract the README shows (`opencode-a1b2`).
func TestAutoSessionShape(t *testing.T) {
	got := autoSession("OpenCode")
	if !strings.HasPrefix(got, "opencode-") || len(got) != len("opencode-")+4 {
		t.Errorf("autoSession = %q, want opencode- plus 4 chars", got)
	}
	if a, b := autoSession("x"), autoSession("x"); a == b {
		t.Errorf("two auto sessions collided: %q", a)
	}
}

// An MCP instance that was not told a session gets one of its own, so it does
// not share the browser, the tabs and the refs with the CLI by accident. The
// once is reset here because it is process-wide by design.
func TestEnsureSessionAutoProvisions(t *testing.T) {
	sessionOnce, sessionID, autoProvisioned = sync.Once{}, "", false
	t.Setenv("AXSCOPE_SESSION", "")
	if got := ensureSession("opencode"); !strings.HasPrefix(got, "opencode-") {
		t.Errorf("ensureSession = %q, want an opencode- id", got)
	}
	if os.Getenv("AXSCOPE_SESSION") == "" {
		t.Error("ensureSession must export the id, so the daemon inherits it")
	}
	if !autoProvisioned {
		t.Error("autoProvisioned must be set, so the session is stopped on exit")
	}
}

// An explicit AXSCOPE_SESSION is used as is: that is the shared mode, and its
// daemon must outlive this server.
func TestEnsureSessionKeepsExplicit(t *testing.T) {
	sessionOnce, sessionID, autoProvisioned = sync.Once{}, "", false
	t.Setenv("AXSCOPE_SESSION", "work")
	if got := ensureSession("opencode"); got != "work" {
		t.Errorf("ensureSession = %q, want work", got)
	}
	if autoProvisioned {
		t.Error("an explicit session must not be stopped on exit")
	}
}

// An auto session shuts itself down when the client goes away; the window is a
// default, not a rule.
func TestEnsureSessionSetsIdleDefault(t *testing.T) {
	sessionOnce, sessionID, autoProvisioned = sync.Once{}, "", false
	t.Setenv("AXSCOPE_SESSION", "")
	t.Setenv("AXSCOPE_IDLE_MINUTES", "")
	ensureSession("opencode")
	if got := os.Getenv("AXSCOPE_IDLE_MINUTES"); got != defaultAutoIdleMinutes {
		t.Errorf("AXSCOPE_IDLE_MINUTES = %q, want %q", got, defaultAutoIdleMinutes)
	}
}

// An explicit idle window wins, including 0 = never.
func TestEnsureSessionKeepsExplicitIdle(t *testing.T) {
	sessionOnce, sessionID, autoProvisioned = sync.Once{}, "", false
	t.Setenv("AXSCOPE_SESSION", "")
	t.Setenv("AXSCOPE_IDLE_MINUTES", "0")
	ensureSession("opencode")
	if got := os.Getenv("AXSCOPE_IDLE_MINUTES"); got != "0" {
		t.Errorf("AXSCOPE_IDLE_MINUTES = %q, want 0 (never)", got)
	}
}

// stop/install/sessions are settled by the daemon or the CLI and are not browser
// actions; the switch in tools() must exclude them even with the full catalog
// requested (AXSCOPE_MCP_TOOLS=all), where the curated filter is out of the way.
// `ping` stays exposed: it is the client's liveness check.
func TestCatalogExcludesCLIOnlyCommandsWithAll(t *testing.T) {
	t.Setenv("AXSCOPE_MCP_TOOLS", "all")
	exposed := map[string]bool{}
	for _, td := range tools() {
		exposed[td.Name] = true
	}
	for _, cmd := range []string{"stop", "install", "sessions"} {
		if exposed[cmd] {
			t.Errorf("%q must not be exposed as an MCP tool", cmd)
		}
	}
	// A browser action must still be there, or the "excluded" assertion above
	// would pass on an empty catalog too.
	if !exposed["open"] {
		t.Error("with the full catalog, a browser command such as open must be exposed")
	}
}

// The MCP client name becomes the tab group label; the transformation has to be
// stable and readable (empty stays empty so initialize does not set AXSCOPE_AGENT).
func TestDisplayName(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"opencode", "Opencode"},
		{"claude-desktop", "Claude Desktop"},
		{"claude_desktop", "Claude Desktop"},
		{"cursor.sh", "Cursor Sh"},
		{"  visual   studio  code ", "Visual Studio Code"},
		{"a", "A"},
		{"already Title", "Already Title"},
	}
	for _, c := range cases {
		if got := displayName(c.raw); got != c.want {
			t.Errorf("displayName(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// The refusal messages cite select, check, type, upload, scroll and the tab and
// history commands; each must be reachable — a tool, or a step of batch named
// in its description (a command the agent cannot see, it reimplements in eval).
func TestCatalogReachesTheCommandsTheMessagesCite(t *testing.T) {
	t.Setenv("AXSCOPE_MCP_TOOLS", "")
	exposed := map[string]string{}
	for _, td := range tools() {
		exposed[td.Name] = td.Description
	}
	grammar, ok := exposed["batch"]
	if !ok {
		t.Fatal("batch must be in the default catalog")
	}
	steps := map[string]bool{}
	_, list, _ := strings.Cut(grammar, "): ")
	for _, step := range strings.Split(list, "; ") {
		steps[strings.Fields(step)[0]] = true
	}
	for _, cmd := range []string{"select", "check", "uncheck", "type", "upload", "press", "scroll", "newtab", "closetab", "back", "forward", "reload", "read", "eval"} {
		if _, isTool := exposed[cmd]; !isTool && !steps[cmd] {
			t.Errorf("%q is neither a tool nor named in batch's step grammar", cmd)
		}
	}
}

// The default catalog is the few direct calls; if it grows, this is where it is
// noticed (each schema costs context on every request, and Cursor caps the tool
// count across every server at 40).
func TestCatalogStaysCore(t *testing.T) {
	t.Setenv("AXSCOPE_MCP_TOOLS", "")
	if n := len(tools()); n > 8 {
		t.Errorf("the default catalog has %d tools, want at most 8 — add a batch step instead", n)
	}
}

// Regression: the catalog must not mark an optional positional as required. A
// client rejected `read` with `selector: Missing key`, because the schema carried
// a required array for a field the command does not need.
func TestSchemaDoesNotRequireOptionalPositionals(t *testing.T) {
	byName := map[string]toolDef{}
	for _, td := range tools() {
		byName[td.Name] = td
	}
	snap, ok := byName["snap"]
	if !ok {
		t.Fatal("snap must be in the default catalog")
	}
	if req, has := snap.InputSchema["required"]; has {
		t.Errorf("snap marks a field required, but within/depth are optional: %v", req)
	}
	// The other side of the contract: a command that does need an argument still
	// marks it, so the check above is not passing on an empty schema.
	open, ok := byName["open"]
	if !ok {
		t.Fatal("open must be in the curated catalog")
	}
	if req, _ := open.InputSchema["required"].([]string); len(req) != 1 || req[0] != "url" {
		t.Errorf("open required = %v, want [url]", open.InputSchema["required"])
	}
}

// A client that sees a partial catalog otherwise assumes the rest does not exist
// and reimplements it with `eval`; the handshake has to say that the set is
// curated and how to open it.
func TestInitializeCarriesTheCatalogNote(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	handleLine(context.Background(), line, w)
	_ = w.Flush()
	out := buf.String()
	for _, want := range []string{"instructions", "AXSCOPE_MCP_TOOLS=all"} {
		if !strings.Contains(out, want) {
			t.Errorf("initialize did not carry %q: %s", want, out)
		}
	}
}

// The note names the exposed count and the total, so the gap between them is
// visible from inside the client.
func TestInstructionsNameTheCounts(t *testing.T) {
	t.Setenv("AXSCOPE_MCP_TOOLS", "")
	text := instructions()
	if !strings.Contains(text, strconv.Itoa(len(tools()))) || !strings.Contains(text, strconv.Itoa(mcpCommands())) {
		t.Errorf("instructions do not name the counts (%d of %d): %q", len(tools()), mcpCommands(), text)
	}
}

// response is discarded (only the AXSCOPE_AGENT side effect matters here).
// initialize(t, client) drives the handshake with the given clientInfo.name; the
// response is discarded (only the AXSCOPE_AGENT side effect matters here).
func initialize(t *testing.T, client string) {
	t.Helper()
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"` + client + `"}}}`)
	handleLine(context.Background(), line, bufio.NewWriter(&bytes.Buffer{}))
}

// The README documents that an explicit AXSCOPE_AGENT names the tab group, with the
// client name as the fallback. Regression: initialize used to overwrite it.
func TestInitializeKeepsExplicitAgent(t *testing.T) {
	t.Setenv("AXSCOPE_AGENT", "Opencode")
	initialize(t, "cli")
	if got := os.Getenv("AXSCOPE_AGENT"); got != "Opencode" {
		t.Errorf("AXSCOPE_AGENT = %q, want %q (an explicit setting must win)", got, "Opencode")
	}
}

// Without AXSCOPE_AGENT, the client name still names the group (opencode sends "cli").
func TestInitializeUsesClientNameWhenAgentUnset(t *testing.T) {
	t.Setenv("AXSCOPE_AGENT", "")
	initialize(t, "cli")
	if got := os.Getenv("AXSCOPE_AGENT"); got != "Cli" {
		t.Errorf("AXSCOPE_AGENT = %q, want %q", got, "Cli")
	}
}

// syncBuffer is a bytes.Buffer safe to read while the server writes to it: the
// slow-call test has to inspect the output before the call finishes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A cancellation's id must match whether the client sent a number or a string,
// or the cancel would land nowhere and the call would keep waiting.
func TestIDKeyNormalizesNumberAndString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`7`, "7"},
		{`"7"`, "7"},
		{`"abc"`, "abc"},
		{`  9  `, "9"},
		{``, ""},
	}
	for _, c := range cases {
		if got := idKey(json.RawMessage(c.in)); got != c.want {
			t.Errorf("idKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The bound is a backstop, and 0 must mean "no bound" — WithTimeout(0) would be
// an already-expired context and would refuse every call.
func TestCallTimeout(t *testing.T) {
	t.Setenv("AXSCOPE_MCP_TIMEOUT_MINUTES", "")
	if got := callTimeout(); got != 10*time.Minute {
		t.Errorf("default callTimeout = %v, want 10m", got)
	}
	t.Setenv("AXSCOPE_MCP_TIMEOUT_MINUTES", "3")
	if got := callTimeout(); got != 3*time.Minute {
		t.Errorf("callTimeout = %v, want 3m", got)
	}
	t.Setenv("AXSCOPE_MCP_TIMEOUT_MINUTES", "0")
	if got := callTimeout(); got != 0 {
		t.Errorf("callTimeout = %v, want 0 (no bound)", got)
	}
}

// 0 minutes must produce a cancellable-but-live context, not an expired one.
func TestBeginCallWithNoBoundIsNotExpired(t *testing.T) {
	t.Setenv("AXSCOPE_MCP_TIMEOUT_MINUTES", "0")
	s := &server{ctx: context.Background(), calls: map[string]context.CancelFunc{}}
	ctx, cancel := s.beginCall(json.RawMessage(`1`))
	defer s.endCall(json.RawMessage(`1`), cancel)
	select {
	case <-ctx.Done():
		t.Fatal("0 minutes must mean no bound, not an expired context")
	default:
	}
}

// A client cancellation must reach the in-flight call; a late one for a finished
// call must be a harmless no-op.
func TestCancelReachesTheInFlightCall(t *testing.T) {
	s := &server{ctx: context.Background(), calls: map[string]context.CancelFunc{}}
	ctx, cancel := s.beginCall(json.RawMessage(`"abc"`))
	defer s.endCall(json.RawMessage(`"abc"`), cancel)

	s.cancel(json.RawMessage(`{"requestId":"abc"}`))
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel did not reach the call it named")
	}
	s.cancel(json.RawMessage(`{"requestId":"gone"}`)) // no panic, no effect
}

// Regression: a tools/call is dispatched in its own goroutine, so a slow command
// cannot block ping (the client's liveness check) behind it. The old synchronous
// loop froze the whole server until the call returned.
func TestSlowCallDoesNotBlockPing(t *testing.T) {
	out := &syncBuffer{}
	s := &server{
		ctx:    context.Background(),
		writer: bufio.NewWriter(out),
		calls:  map[string]context.CancelFunc{},
		call: func(context.Context, string, map[string]any) map[string]any {
			time.Sleep(300 * time.Millisecond)
			return toolText("done", false)
		},
	}
	s.dispatch([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"snap","arguments":{}}}`))
	s.dispatch([]byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(out.String(), `"id":2`) {
		if time.Now().After(deadline) {
			t.Fatalf("ping was blocked behind a slow call; output: %s", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The tip speaks at the third action in a row, then only every tipEvery, and a
// read or a pause breaks the streak — it informs, it does not nag.
func TestBatchTipCadence(t *testing.T) {
	var tip batchTip
	now := time.Now()
	var spoke []int
	for i := 1; i <= 3+2*tipEvery; i++ {
		if tip.observe("click", now) != "" {
			spoke = append(spoke, i)
		}
	}
	want := []int{3, 3 + tipEvery, 3 + 2*tipEvery}
	if len(spoke) != len(want) || spoke[0] != want[0] || spoke[1] != want[1] || spoke[2] != want[2] {
		t.Errorf("spoke at %v, want %v", spoke, want)
	}

	tip = batchTip{}
	tip.observe("click", now)
	tip.observe("click", now)
	tip.observe("snap", now)
	if tip.observe("click", now) != "" {
		t.Error("a read must reset the streak")
	}
	tip.observe("click", now)
	if tip.observe("click", now.Add(2*tipGap)) != "" {
		t.Error("a long pause must reset the streak")
	}
}

// A batch's steps are a list in the schema; a string would push the model into
// JSON-in-a-string quoting.
func TestBatchStepsSchemaIsAList(t *testing.T) {
	t.Setenv("AXSCOPE_MCP_TOOLS", "")
	for _, td := range tools() {
		if td.Name != "batch" {
			continue
		}
		props := td.InputSchema["properties"].(map[string]any)
		if props["steps"].(map[string]any)["type"] != "array" {
			t.Errorf("steps schema = %v", props["steps"])
		}
		return
	}
	t.Error("batch is not in the default catalog")
}
