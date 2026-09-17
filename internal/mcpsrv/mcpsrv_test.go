package mcpsrv

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
)

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

// Regression: the refusal messages from `fill` cite `axscope select`, `check` and
// `upload`, so the test pins the contract that a command the messages tell you to
// use is reachable in the exposed catalog.
func TestCatalogCoversTheCommandsTheMessagesCite(t *testing.T) {
	required := []string{"select", "check", "uncheck", "type", "upload"}
	exposed := map[string]bool{}
	for _, td := range tools() {
		exposed[td.Name] = true
	}
	for _, cmd := range required {
		if !exposed[cmd] {
			t.Errorf("%q is not exposed in the catalog, but the refusal messages tell you to use it", cmd)
		}
	}
}

// Regression: the curated catalog must expose the everyday core — scrolling is
// how a snapshot reaches lazy lists, and the tab/navigation lifecycle is what
// keeps a session from accumulating tabs it cannot close.
func TestCatalogCoversCoreInteractions(t *testing.T) {
	required := []string{"scroll", "newtab", "closetab", "back", "forward", "reload"}
	exposed := map[string]bool{}
	for _, td := range tools() {
		exposed[td.Name] = true
	}
	for _, cmd := range required {
		if !exposed[cmd] {
			t.Errorf("%q is a core interaction and must be exposed in the default catalog", cmd)
		}
	}
}

// The catalog remains a lean set: if it grows by carelessness, this is where it
// is noticed (each schema costs context on every request). status and ping came
// in because a client needs to ask what the state is when a connection drops.
func TestCatalogDoesNotGrowByCarelessness(t *testing.T) {
	if n := len(tools()); n > 35 {
		t.Errorf("the curated catalog has %d tools — above that the context cost stops paying off", n)
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
	read, ok := byName["read"]
	if !ok {
		t.Fatal("read must be in the curated catalog")
	}
	if req, has := read.InputSchema["required"]; has {
		t.Errorf("read marks a field required, but its selector is optional: %v", req)
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
