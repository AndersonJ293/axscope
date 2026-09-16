package agent

import (
	"sort"
	"testing"

	"github.com/AndersonJ293/axscope/internal/command"
)

// cliOnly lists the commands in command.Specs that no agent route serves:
// install/engines/clean are settled in cmd/axscope, and stop by the daemon's
// accept loop. Any new command without a route fails this guard.
var cliOnly = map[string]bool{
	"install": true,
	"engines": true,
	"clean":   true,
	"stop":    true,
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
