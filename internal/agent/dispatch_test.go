package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/AndersonJ293/axscope/internal/command"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// A dropped connection must name the way out instead of reading as a dead end.
func TestSessionErrorNamesRecovery(t *testing.T) {
	got := sessionError(fmt.Errorf("connection closed")).Error()
	for _, want := range []string{"connection closed", "axscope status", "retries"} {
		if !strings.Contains(got, want) {
			t.Errorf("sessionError does not say %q: %s", want, got)
		}
	}
}

// help is how a client discovers the command surface over MCP; it must answer
// without a browser, and list the commands (not just the usage line).
func TestHelpListsTheCommands(t *testing.T) {
	resp := (&Agent{}).help(context.Background(), nil, protocol.Request{})
	if !resp.OK {
		t.Fatalf("help failed: %+v", resp)
	}
	for _, want := range []string{"usage: axscope", "snap", "eval"} {
		if !strings.Contains(resp.Text, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
}

// cliOnly lists the commands in command.Specs that no agent route serves:
// install/engines/clean/sessions are settled in cmd/axscope, and stop by the
// daemon's accept loop. Any new command without a route fails this guard.
var cliOnly = map[string]bool{
	"install":  true,
	"engines":  true,
	"clean":    true,
	"sessions": true,
	"stop":     true,
}

// TestRoutesCoverCommandSpecs is the guard that CLI, help, script and MCP stay in
// sync with dispatch: command.Specs is the single source of truth for what
// exists, so every command in it must be routed or deliberately CLI-only.
func TestRoutesCoverCommandSpecs(t *testing.T) {
	routes := (&Agent{}).routes()

	for _, spec := range command.Specs {
		if _, ok := routes[spec.Cmd]; ok {
			continue
		}
		if cliOnly[spec.Cmd] {
			continue
		}
		t.Errorf("command %q is in command.Specs but no agent route serves it; add it to routes() or to the documented cliOnly list", spec.Cmd)
	}
}

// TestRoutesHaveNoPhantomCommands pins the other direction: a route for a name
// that command.Specs does not contain would be invisible to the CLI, the help
// and the MCP catalog — dead behavior nobody can reach.
func TestRoutesHaveNoPhantomCommands(t *testing.T) {
	known := map[string]bool{}
	for _, spec := range command.Specs {
		known[spec.Cmd] = true
	}

	var phantom []string
	for name := range (&Agent{}).routes() {
		if !known[name] {
			phantom = append(phantom, name)
		}
	}
	sort.Strings(phantom)
	for _, name := range phantom {
		t.Errorf("route %q serves no command in command.Specs; the CLI/help/MCP would never offer it", name)
	}
}

// TestCLIOnlyCommandsAreNotRouted keeps the explicit list honest: the commands
// that are handled outside the daemon must not creep into routes(), where they
// would bring up a browser for no reason.
func TestCLIOnlyCommandsAreNotRouted(t *testing.T) {
	routes := (&Agent{}).routes()
	for cmd := range cliOnly {
		if _, ok := routes[cmd]; ok {
			t.Errorf("%q is listed as CLI-only but has an agent route; reconcile the list with dispatch.go", cmd)
		}
	}
}
