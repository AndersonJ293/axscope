// Command parser shared by CLI, daemon (script) and MCP, for the grammar
// `<command> [positional] [key=value] [--flag]`; first positional maps to first spec name.
package command

import (
	"fmt"
	"strings"

	"github.com/AndersonJ293/axscope/internal/protocol"
)

// Spec describes a command.
type Spec struct {
	Cmd        string
	Positional []string
	// Optional lists positionals that can be omitted (they have a default).
	Optional []string
	Flags    []string
	Help     string
}

// Specs is the canonical table. Order does not matter.
var Specs = []Spec{
	{Cmd: "ping", Help: "checks whether the daemon responds"},
	{Cmd: "status", Help: "URL, title, tabs, CDP endpoint and overlay state"},
	{Cmd: "install", Flags: []string{"engine"}, Help: "downloads engines (--engine chrome|shell|all)"},
	{Cmd: "engines", Help: "lists installed engines"},
	{Cmd: "clean", Flags: []string{"all"}, Help: "deletes logs and dead sessions (--all includes profiles and browsers)"},
	{Cmd: "stop", Help: "shuts down the daemon (and the browser, if we started it)"},

	{Cmd: "open", Positional: []string{"url"}, Flags: []string{"new", "force"}, Help: "opens/navigates (--new opens in a new tab; --force leaves a page with unsaved changes)"},
	{Cmd: "snap", Flags: []string{"refs", "all"}, Help: "reads the screen as text (--refs targets only; --all includes footer and shortcuts)"},
	{Cmd: "click", Positional: []string{"target"}, Flags: []string{"right", "middle", "double", "dom"}, Help: "clicks the target, real pointer by default (--dom fires element.click() for pages that ignore synthetic pointer events)"},
	{Cmd: "hover", Positional: []string{"target"}, Help: "hovers over the target"},
	{Cmd: "drag", Positional: []string{"from", "to"}, Flags: []string{"top", "bottom", "type"}, Help: "drags <from> to <to> (--top/--bottom where to drop; type=pointer|html5 forces the family)"},
	{Cmd: "upload", Positional: []string{"file", "target"}, Optional: []string{"target"}, Help: "sends a file (without target it goes to the first <input type=file>; target= on a dropzone)"},
	{Cmd: "fill", Positional: []string{"target", "value"}, Help: "replaces the field's content"},
	{Cmd: "type", Positional: []string{"target", "value"}, Help: "types character by character"},
	{Cmd: "press", Positional: []string{"key"}, Help: "sends a key/shortcut (Enter, Control+A)"},
	{Cmd: "select", Positional: []string{"target", "value"}, Help: "chooses an option of <select> or an ARIA listbox (<value> is the option's label)"},
	{Cmd: "check", Positional: []string{"target"}, Help: "checks checkbox/radio"},
	{Cmd: "uncheck", Positional: []string{"target"}, Help: "unchecks checkbox/radio"},
	{Cmd: "scroll", Positional: []string{"dy", "target"}, Optional: []string{"target"}, Flags: []string{"page"}, Help: "scrolls (positive dy goes down; target= scrolls the container; --page forces the document)"},
	{Cmd: "wait", Positional: []string{"text", "timeout", "within", "url", "urlre"}, Optional: []string{"text", "timeout", "within", "url", "urlre"}, Flags: []string{"enabled", "visible", "gone", "network-idle"}, Help: "waits for the text to appear (within= limits the container; --enabled/--visible/--gone wait for that state of the target, and then the first argument is the target; url=/urlre= wait for the URL; --network-idle waits for the requests to stop)"},
	{Cmd: "waitgone", Positional: []string{"text", "timeout", "within", "url", "urlre"}, Optional: []string{"text", "timeout", "within", "url", "urlre"}, Help: "waits for the text to disappear (timeout in ms; within= limits the container; url=/urlre= wait for the URL to change)"},
	{Cmd: "read", Positional: []string{"selector"}, Optional: []string{"selector"}, Flags: []string{"links", "table"}, Help: "reads the page's main text (--links lists the links as `label — href`; --table reads an HTML <table> as aligned rows)"},
	{Cmd: "find", Positional: []string{"target"}, Flags: []string{"all"}, Help: "shows the ref the last snap gave a css=/text= target, without acting (--all lists every matching ref)"},
	{Cmd: "eval", Positional: []string{"js"}, Flags: []string{"raw"}, Help: "evaluates JavaScript on the page (--raw prints the exact CDP JSON)"},
	{Cmd: "tabs", Help: "lists the tabs"},
	{Cmd: "tab", Positional: []string{"ref"}, Flags: []string{"focus"}, Help: "switches to the tab (index or targetId; --focus brings the window to the front)"},
	{Cmd: "newtab", Positional: []string{"url"}, Optional: []string{"url"}, Help: "opens a new tab"},
	{Cmd: "closetab", Positional: []string{"ref"}, Help: "closes the tab"},
	{Cmd: "back", Help: "goes back in history"},
	{Cmd: "forward", Help: "goes forward in history"},
	{Cmd: "reload", Flags: []string{"hard"}, Help: "reloads the page (--hard bypasses the cache — the remedy for a dead UI)"},
	{Cmd: "console", Flags: []string{"all"}, Help: "console errors/warnings"},
	{Cmd: "net", Positional: []string{"filter"}, Optional: []string{"filter"}, Help: "network requests"},
	{Cmd: "shot", Positional: []string{"path"}, Optional: []string{"path"}, Flags: []string{"full"}, Help: "captures PNG (without a path it goes to /tmp)"},
	{Cmd: "script", Positional: []string{"path"}, Help: "runs a script (file or - for stdin)"},
}

func lookup(cmd string) (Spec, bool) {
	for _, s := range Specs {
		if s.Cmd == cmd {
			return s, true
		}
	}
	return Spec{}, false
}

// Parse turns tokens into a request.
func Parse(tokens []string) (protocol.Request, error) {
	if len(tokens) == 0 {
		return protocol.Request{}, fmt.Errorf("no command")
	}
	spec, ok := lookup(tokens[0])
	if !ok {
		return protocol.Request{}, fmt.Errorf("unknown command: %q (see `axscope help`)", tokens[0])
	}
	req := protocol.Request{Cmd: spec.Cmd, Args: map[string]any{}}

	positional := append([]string(nil), spec.Positional...)
	known := map[string]bool{}
	for _, p := range spec.Positional {
		known[p] = true
	}
	for _, f := range spec.Flags {
		known[f] = true
	}

	for _, token := range tokens[1:] {
		switch {
		case strings.HasPrefix(token, "--"):
			name := strings.TrimPrefix(token, "--")
			if !known[name] {
				return protocol.Request{}, fmt.Errorf("command %q has no --%s flag", spec.Cmd, name)
			}
			req.Args[name] = true
		// `k=v` only counts when `k` is a known argument — that way a free value
		// (JS, text with "=") lands as positional, not as a key=value pair.
		case strings.Contains(token, "=") && !strings.HasPrefix(token, "=") &&
			known[strings.SplitN(token, "=", 2)[0]]:
			parts := strings.SplitN(token, "=", 2)
			req.Args[parts[0]] = parts[1]
		default:
			if len(positional) == 0 {
				return protocol.Request{}, fmt.Errorf("extra argument in %q: %q", spec.Cmd, token)
			}
			req.Args[positional[0]] = token
			positional = positional[1:]
		}
	}
	return req, nil
}

// Help returns the help text.
func Help() string {
	var b strings.Builder
	b.WriteString("axscope — agent-driven browser\n\n")
	b.WriteString("usage: axscope <command> [args] [key=value] [--flag]\n")
	b.WriteString("global flags: (default) extension in Brave | --chrome (dedicated Chrome) | --headless (windowless)\n\n")
	for _, s := range Specs {
		line := "  " + s.Cmd
		for _, p := range s.Positional {
			line += " <" + p + ">"
		}
		for _, f := range s.Flags {
			line += " [--" + f + "]"
		}
		b.WriteString(line + "\n      " + s.Help + "\n")
	}
	return b.String()
}
