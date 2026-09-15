package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ajunior/browser-use/internal/browser"
	"github.com/ajunior/browser-use/internal/dom"
	"github.com/ajunior/browser-use/internal/protocol"
)

func (a *Agent) clickLike(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	target := req.String("target")
	if target == "" {
		return protocol.Fail(fmt.Errorf("uso: bu %s <alvo>", req.Cmd))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	if req.Cmd == "hover" {
		if err := browser.Hover(ctx, a.client(), sid, t, sess.Presenter); err != nil {
			return protocol.Fail(err)
		}
		return ok(a.finish(ctx, sess, sid, "hover "+target, before))
	}
	button := "left"
	if req.Bool("right", false) {
		button = "right"
	} else if req.Bool("middle", false) {
		button = "middle"
	}
	count := 1
	if req.Bool("double", false) {
		count = 2
	}
	action := "click"
	if count == 2 {
		action = "dblclick"
	}
	if err := browser.Click(ctx, a.client(), sid, t, button, count, sess.Presenter); err != nil {
		return protocol.Fail(falhaDeAcao(action, target, err))
	}
	return ok(a.finish(ctx, sess, sid, action+" "+target, before))
}

func (a *Agent) drag(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	fromSpec := req.String("from")
	toSpec := req.String("to")
	if fromSpec == "" || toSpec == "" {
		return protocol.Fail(fmt.Errorf("uso: bu drag <de> <para>"))
	}
	from, sid, err := a.resolve(ctx, sess, fromSpec)
	if err != nil {
		return protocol.Fail(err)
	}
	to, _, err := a.resolve(ctx, sess, toSpec)
	if err != nil {
		return protocol.Fail(err)
	}
	at := ""
	if req.Bool("topo", false) {
		at = "top"
	} else if req.Bool("base", false) {
		at = "bottom"
	}
	before := a.errCount(sess, sid)
	tipo, mudou, err := browser.Drag(ctx, a.client(), sid, from, to, browser.DragOptions{
		DropAt: at,
		Tipo:   req.String("tipo"),
	}, sess.Presenter)
	if err != nil {
		return protocol.Fail(err)
	}
	label := fmt.Sprintf("drag %s -> %s [%s]", fromSpec, toSpec, tipo)
	if !mudou {
		// Gesto que não pega pode ter terminado em clique real no alvo.
		label += " (não vi mudança de posição — pode não ter pegado)"
	}
	return ok(a.finish(ctx, sess, sid, label, before))
}

func (a *Agent) fillLike(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	target := req.String("target")
	// O argumento se chama `value`, e não `text`, porque `text=` é um seletor de
	// alvo: com o nome `text` o parser engolia `text=Rótulo` como par chave=valor
	// e o alvo virava o conteúdo. Eram justamente os dois comandos em que mais se
	// quer mirar por texto.
	text := req.String("value")
	if target == "" {
		return protocol.Fail(fmt.Errorf("uso: bu %s <alvo> <valor>", req.Cmd))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	if req.Cmd == "fill" {
		err = browser.Fill(ctx, a.client(), sid, t, text, sess.Presenter)
	} else {
		err = browser.Type(ctx, a.client(), sid, t, text, sess.Presenter)
	}
	if err != nil {
		return protocol.Fail(err)
	}
	label := fmt.Sprintf("%s %s = %s", req.Cmd, target, strconv.Quote(text))
	return ok(a.finish(ctx, sess, sid, label, before))
}

func (a *Agent) press(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	key := req.String("key")
	if key == "" {
		return protocol.Fail(fmt.Errorf("uso: bu press <tecla>"))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	if err := browser.Press(ctx, a.client(), sid, key); err != nil {
		return protocol.Fail(err)
	}
	return ok(a.finish(ctx, sess, sid, "press "+key, before))
}

func (a *Agent) selectOption(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	target := req.String("target")
	value := req.String("value")
	if target == "" || value == "" {
		return protocol.Fail(fmt.Errorf("uso: bu select <alvo> <valor>"))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	if err := browser.Select(ctx, a.client(), sid, t, value); err != nil {
		return protocol.Fail(err)
	}
	return ok(a.finish(ctx, sess, sid, fmt.Sprintf("select %s = %s", target, value), before))
}

func (a *Agent) checkLike(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	target := req.String("target")
	if target == "" {
		return protocol.Fail(fmt.Errorf("uso: bu %s <alvo>", req.Cmd))
	}
	t, sid, err := a.resolve(ctx, sess, target)
	if err != nil {
		return protocol.Fail(err)
	}
	before := a.errCount(sess, sid)
	want := req.Cmd == "check"
	clicked, err := browser.SetChecked(ctx, a.client(), sid, t, want, sess.Presenter)
	if err != nil {
		return protocol.Fail(falhaDeAcao(req.Cmd, target, err))
	}
	label := req.Cmd + " " + target
	if !clicked {
		label += " (já estava)"
	}
	return ok(a.finish(ctx, sess, sid, label, before))
}

// falhaDeAcao embrulha o motivo pelo qual a ação não foi enviada.
//
// A recusa precisa dizer o que fazer — foi para isso que ela substituiu o `ok`
// silencioso: quem lê a resposta fica sabendo o próximo passo (esperar
// habilitar, tirar o que cobre, ou clicar pelo ponto com `pos=x,y`).
func falhaDeAcao(acao, alvo string, err error) error {
	return fmt.Errorf("%s em %s não foi enviado: %w", acao, alvo, err)
}

// scroll rola. Sem alvo, rola o que estiver sob o centro da tela; com
// `alvo=<ref|texto|css>`, rola o container daquele alvo.
//
// O alvo existe porque "rolar" tem dois donos possíveis: a página e uma caixa
// que rola dentro dela. A lista de infinite scroll do laboratório mostrou a
// diferença — rolar a página não carrega o próximo lote, e o alvo é a única
// forma de dizer qual caixa rolar.
func (a *Agent) scroll(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	raw := req.String("dy")
	if raw == "" {
		return protocol.Fail(fmt.Errorf("uso: bu scroll <dy> [alvo=<ref|texto|css>] (dy positivo desce)"))
	}
	dy, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return protocol.Fail(fmt.Errorf("dy inválido: %q", raw))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}

	if alvo := req.String("alvo"); alvo != "" {
		t, _, err := a.resolve(ctx, sess, alvo)
		if err != nil {
			return protocol.Fail(err)
		}
		onde, err := browser.ScrollTarget(ctx, a.client(), sid, t.ObjectID, 0, dy)
		if err != nil {
			return protocol.Fail(err)
		}
		sess.Settle(ctx, sid, actionIdle)
		label := fmt.Sprintf("scroll %.0f em %s — agora em %s", dy, alvo, onde)
		sess.UpdateHUD(ctx, label)
		return ok("ok: " + label)
	}

	onde, err := browser.Scroll(ctx, a.client(), sid, 0, dy, req.Bool("pagina", false))
	if err != nil {
		return protocol.Fail(err)
	}
	sess.Settle(ctx, sid, actionIdle)
	label := fmt.Sprintf("scroll %.0f — agora em %s", dy, onde)
	sess.UpdateHUD(ctx, label)
	return ok("ok: " + label)
}

// finish resume o resultado de uma ação e anexa avisos de console.
func (a *Agent) finish(ctx context.Context, sess *browser.Session, sid, label string, errCountBefore int) string {
	sess.Settle(ctx, sid, actionIdle)
	sess.UpdateHUD(ctx, label)
	var b strings.Builder
	fmt.Fprintf(&b, "ok: %s", label)
	newErrs := sess.Observe.Console(sid, "error", 0)
	if len(newErrs) > errCountBefore {
		for _, e := range newErrs[errCountBefore:] {
			fmt.Fprintf(&b, "\n!! console: %s", e.Text)
		}
	}
	url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
	fmt.Fprintf(&b, "\nurl: %s", url)
	return b.String()
}

func (a *Agent) errCount(sess *browser.Session, sid string) int {
	return len(sess.Observe.Console(sid, "error", 0))
}
