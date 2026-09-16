package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/command"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

func (a *Agent) shot(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	path := req.String("path")
	if path == "" {
		// Without a path, it goes to the system temporary — never to the project.
		f, err := os.CreateTemp("", "axscope-*.png")
		if err != nil {
			return protocol.Fail(err)
		}
		path = f.Name()
		_ = f.Close()
	}
	sid, err := sess.ActiveSID()
	if err != nil {
		return protocol.Fail(err)
	}
	data, err := browser.Screenshot(ctx, a.client(), sid, req.Bool("full", false))
	if err != nil {
		return protocol.Fail(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return protocol.Fail(err)
	}
	return ok(fmt.Sprintf("ok: %s (%d bytes)", path, len(data)))
}

// runScript runs a script line by line, stopping at the first error.
func (a *Agent) runScript(ctx context.Context, _ *browser.Session, req protocol.Request) protocol.Response {
	content := req.String("content")
	if content == "" {
		path := req.String("path")
		if path == "" || path == "-" {
			return protocol.Fail(fmt.Errorf("empty script"))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return protocol.Fail(err)
		}
		content = string(data)
	}

	var b strings.Builder
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		tokens, err := splitTokens(trimmed)
		if err != nil {
			fmt.Fprintf(&b, "> %s\n!! %v\n", trimmed, err)
			continue
		}
		sub, err := command.Parse(tokens)
		if err != nil {
			fmt.Fprintf(&b, "> %s\n!! %v\n", trimmed, err)
			continue
		}
		fmt.Fprintf(&b, "> %s\n", trimmed)
		if sub.Cmd == "script" {
			fmt.Fprintf(&b, "!! script cannot call script\n")
			continue
		}
		res := a.dispatch(ctx, sub)
		if res.OK {
			fmt.Fprintf(&b, "%s\n", res.Text)
		} else {
			fmt.Fprintf(&b, "!! %s\n", res.Error)
			break // script stops at the first error
		}
	}
	return ok(strings.TrimRight(b.String(), "\n"))
}
