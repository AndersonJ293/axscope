package agent

import (
	"context"
	"fmt"

	"github.com/ajunior/browser-use/internal/browser"
	"github.com/ajunior/browser-use/internal/protocol"
)

// handler é a assinatura única dos comandos: recebe a sessão (nil nos que não
// precisam de browser) e devolve a resposta.
type handler func(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response

// route diz como um comando é atendido e se exige browser vivo.
type route struct {
	needsSession bool
	handle       handler
}

// routes é o registro comando → handler. É o único lugar que liga o nome do
// comando ao que ele faz; antes, comando novo exigia mexer no switch e no
// handler. A tabela de command.Specs continua sendo a fonte da verdade do que
// existe (CLI, ajuda e MCP derivam dela).
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

// dispatch executa o pedido. ping/status/script não sobem browser; o resto
// exige a sessão garantida. Comando desconhecido também garante a sessão antes
// de recusar — é o que o switch fazia, e o erro é o mesmo.
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
		return protocol.Fail(fmt.Errorf("comando %q não é tratado pelo daemon", req.Cmd))
	}
	return r.handle(ctx, sess, req)
}

func (a *Agent) ping(_ context.Context, _ *browser.Session, _ protocol.Request) protocol.Response {
	return ok("pong")
}
