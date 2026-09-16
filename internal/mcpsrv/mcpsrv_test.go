package mcpsrv

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"testing"
)

// ping/stop/install are settled by the daemon or the CLI and are not browser
// actions; the switch in tools() must exclude them even with the full catalog
// requested (AXSCOPE_MCP_TOOLS=all), where the curated filter is out of the way.
func TestCatalogExcludesCLIOnlyCommandsWithAll(t *testing.T) {
	t.Setenv("AXSCOPE_MCP_TOOLS", "all")
	exposed := map[string]bool{}
	for _, td := range tools() {
		exposed[td.Name] = true
	}
	for _, cmd := range []string{"ping", "stop", "install"} {
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
// is noticed (each schema costs context on every request).
func TestCatalogDoesNotGrowByCarelessness(t *testing.T) {
	if n := len(tools()); n > 30 {
		t.Errorf("the curated catalog has %d tools — above that the context cost stops paying off", n)
	}
}

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
