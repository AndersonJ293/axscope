package agent

import (
	"context"
	"fmt"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// handler is the single signature of the commands: it receives the session (nil
// for those that do not need a browser) and returns the response.
type handler func(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response

// route says how a command is served and whether it requires a live browser.
type route struct {
	needsSession bool
	handle       handler
}

// routes is the command → handler registry. It is the only place that ties the
// command name to what it does; before, a new command meant touching the switch
// and the handler. The command.Specs table remains the source of truth of what
// exists (CLI, help and MCP derive from it).
func (a *Agent) routes() map[string]route {
	return map[string]route{
		"ping":   {handle: a.ping},
		"status": {handle: a.status},
		"script": {handle: a.runScript},

		"open":     {needsSession: true, handle: a.open},
		"snap":     {needsSession: true, handle: a.snap},
		"click":    {needsSession: true, handle: a.clickLike},
		"hover":    {needsSession: true, handle: a.clickLike},
		"drag":     {needsSession: true, handle: a.drag},
		"upload":   {needsSession: true, handle: a.upload},
		"fill":     {needsSession: true, handle: a.fillLike},
		"type":     {needsSession: true, handle: a.fillLike},
		"press":    {needsSession: true, handle: a.press},
		"select":   {needsSession: true, handle: a.selectOption},
		"check":    {needsSession: true, handle: a.checkLike},
		"uncheck":  {needsSession: true, handle: a.checkLike},
		"scroll":   {needsSession: true, handle: a.scroll},
		"wait":     {needsSession: true, handle: a.wait},
		"waitgone": {needsSession: true, handle: a.wait},
		"read":     {needsSession: true, handle: a.read},
		"eval":     {needsSession: true, handle: a.eval},
		"tabs":     {needsSession: true, handle: a.tabs},
		"tab":      {needsSession: true, handle: a.switchTab},
		"newtab":   {needsSession: true, handle: a.newTab},
		"closetab": {needsSession: true, handle: a.closeTab},
		"back":     {needsSession: true, handle: a.history},
		"forward":  {needsSession: true, handle: a.history},
		"reload":   {needsSession: true, handle: a.reload},
		"console":  {needsSession: true, handle: a.console},
		"net":      {needsSession: true, handle: a.net},
		"shot":     {needsSession: true, handle: a.shot},
	}
}

// dispatch runs the request. ping/status/script do not bring up a browser; the
// rest require the session to be ensured. An unknown command also ensures the
// session before refusing — that is what the switch did, and the error is the
// same.
func (a *Agent) dispatch(ctx context.Context, req protocol.Request) protocol.Response {
	r, found := a.routes()[req.Cmd]
	if found && !r.needsSession {
		return r.handle(ctx, nil, req)
	}
	sess, err := a.ensure(ctx)
	if err != nil {
		return protocol.Fail(err)
	}
	if !found {
		return protocol.Fail(fmt.Errorf("command %q is not handled by the daemon", req.Cmd))
	}
	return r.handle(ctx, sess, req)
}

func (a *Agent) ping(_ context.Context, _ *browser.Session, _ protocol.Request) protocol.Response {
	return ok("pong")
}
