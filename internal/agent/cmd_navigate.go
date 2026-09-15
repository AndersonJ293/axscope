package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/ajunior/browser-use/internal/browser"
	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/dom"
	"github.com/ajunior/browser-use/internal/protocol"
)

func (a *Agent) open(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	url := req.String("url")
	if url == "" {
		return protocol.Fail(fmt.Errorf("uso: bu open <url> [--new]"))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	if req.Bool("new", false) {
		tab, err := sess.NewTab(ctx, url)
		if err != nil {
			return protocol.Fail(err)
		}
		sid = tab.SessionID
	} else if err := sess.Navigate(ctx, sid, url, navTimeout); err != nil {
		return protocol.Fail(err)
	}
	sess.UpdateHUD(ctx, "open "+url)
	title, _ := dom.EvalString(ctx, a.client(), sid, "document.title")
	return ok(fmt.Sprintf("ok: %s\n%s", url, title))
}

// wait espera algo acontecer: um texto aparecer (ou sumir, no waitgone), ou um
// alvo chegar a um estado.
//
// O estado existe porque esperar por texto não cobre o caso mais comum de app
// real: o botão que só habilita depois. No laboratório v3, "Enviar candidatura"
// libera ~1,1s depois de aparecer, sem mudar de texto — e sem isto a saída era
// injetar setTimeout por eval.
//
// `dentro=` limita a busca de texto a um container. Sem escopo, texto que também
// aparece num menu lateral sempre visível casa antes do que se espera: medido no
// v3, a espera por um cargo voltou em 2ms, com o dropdown da busca ainda fechado.
func (a *Agent) wait(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	pedido := req.String("text")
	if pedido == "" {
		return protocol.Fail(fmt.Errorf("uso: bu %s <texto|alvo> [timeout] [dentro=<alvo>]", req.Cmd))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	timeout := navTimeout
	if v := req.Int("timeout", 0); v > 0 {
		timeout = time.Duration(v) * time.Millisecond
	}
	start := time.Now()
	deadline := start.Add(timeout)

	if estado := estadoPedido(req); estado != "" {
		return a.esperaEstado(ctx, sess, sid, pedido, estado, start, deadline)
	}

	raiz, err := a.raizDaBusca(ctx, sess, sid, req.String("dentro"))
	if err != nil {
		return protocol.Fail(err)
	}

	present := req.Cmd == "wait"
	for time.Now().Before(deadline) {
		onde, err := localizaTexto(ctx, a.client(), sid, pedido, raiz)
		if err == nil && (onde != "") == present {
			verbo := "apareceu"
			if !present {
				verbo = "sumiu"
			}
			msg := fmt.Sprintf("ok: %q %s em %dms", pedido, verbo, time.Since(start).Milliseconds())
			if onde != "" {
				msg += " — em " + onde
			}
			return ok(msg)
		}
		time.Sleep(120 * time.Millisecond)
	}
	if present {
		return protocol.Fail(fmt.Errorf("%q não apareceu em %s", pedido, timeout))
	}
	return protocol.Fail(fmt.Errorf("%q não sumiu em %s", pedido, timeout))
}

// estadoPedido devolve o estado pedido por flag, ou "" quando a espera é por
// texto.
func estadoPedido(req protocol.Request) string {
	for _, e := range []string{"habilitado", "visivel", "sumiu"} {
		if req.Bool(e, false) {
			return e
		}
	}
	return ""
}

// raizDaBusca devolve o objectId de onde a busca de texto começa: o container
// pedido em `dentro=`, ou o documento.
func (a *Agent) raizDaBusca(ctx context.Context, sess *browser.Session, sid, dentro string) (string, error) {
	if dentro == "" {
		return dom.EvalObject(ctx, a.client(), sid, "document")
	}
	t, _, err := a.resolve(ctx, sess, dentro)
	if err != nil {
		return "", fmt.Errorf("dentro=%s: %w", dentro, err)
	}
	return t.ObjectID, nil
}

// esperaEstado espera o alvo chegar ao estado pedido: "habilitado" (aceita
// clique), "visivel" (existe e tem caixa na tela) ou "sumiu" (deixou de
// resolver).
func (a *Agent) esperaEstado(ctx context.Context, sess *browser.Session, sid, alvo, estado string, start, deadline time.Time) protocol.Response {
	if estado == "sumiu" {
		// Confere que ele existe antes: senão um alvo escrito errado "some" na
		// hora, e a resposta daria como certo o que nunca houve.
		if _, _, err := a.resolve(ctx, sess, alvo); err != nil {
			return protocol.Fail(fmt.Errorf("%s não resolve, então não tem o que sumir: %w", alvo, err))
		}
	}
	for time.Now().Before(deadline) {
		if chegou, err := a.noEstado(ctx, sess, alvo, estado); err == nil && chegou {
			return ok(fmt.Sprintf("ok: %s %s em %dms", alvo, verboDoEstado(estado), time.Since(start).Milliseconds()))
		}
		time.Sleep(120 * time.Millisecond)
	}
	return protocol.Fail(fmt.Errorf("%s não %s em %dms", alvo, verboDoEstado(estado), time.Since(start).Milliseconds()))
}

// noEstado diz se o alvo está no estado pedido.
func (a *Agent) noEstado(ctx context.Context, sess *browser.Session, alvo, estado string) (bool, error) {
	t, sid, err := a.resolve(ctx, sess, alvo)
	if err != nil {
		// Deixar de resolver é o que "sumiu" espera — e só isso.
		return estado == "sumiu", nil
	}
	switch estado {
	case "habilitado":
		return browser.Habilitado(ctx, a.client(), sid, t.ObjectID), nil
	case "visivel":
		// Resolveu e tem caixa: é o que "visível" quer dizer aqui (se está no
		// ponto, quem cuida disso é o clique, que recusa o contrário).
		return true, nil
	}
	return false, nil
}

func verboDoEstado(estado string) string {
	switch estado {
	case "habilitado":
		return "habilitou"
	case "visivel":
		return "ficou visível"
	default:
		return "sumiu"
	}
}

func (a *Agent) history(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	delta := -1
	if req.Cmd == "forward" {
		delta = 1
	}
	if err := sess.HistoryMove(ctx, sid, delta, navTimeout); err != nil {
		return protocol.Fail(err)
	}
	url, _ := dom.EvalString(ctx, a.client(), sid, "location.href")
	return ok(fmt.Sprintf("ok: %s\nurl: %s", req.Cmd, url))
}

func (a *Agent) reload(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	if err := sess.Reload(ctx, sid, navTimeout); err != nil {
		return protocol.Fail(err)
	}
	return ok("ok: recarreguei")
}

// pageHasText diz se o corpo da página contém o texto pedido.
// localizaTexto diz se o texto está na página e, se estiver, descreve onde.
//
// Devolver só "achei" engana: o texto pode já existir em outro canto e a espera
// voltar em 1ms. Aconteceu na missão 3 do laboratório, esperando "Barreiras" —
// que a própria lista de missões já citava. Dizer onde achou deixa o agente
// conferir se é o lugar que ele queria.
// localizadorDeTexto procura o texto a partir do objeto onde a função roda — o
// documento, ou o escopo pedido em `dentro=` — e descreve onde achou.
//
// Varre shadow roots abertos e iframes de mesma origem: a leitura já mostra o
// conteúdo dos dois, então a espera enxerga o mesmo — senão o agente vê o texto
// e não consegue esperar por ele.
func localizadorDeTexto(want string) string {
	return `function () {
		const want = ` + strconv.Quote(want) + `;
		const raizes = [];
		const anda = (raiz, emFrame) => {
			raizes.push([raiz, emFrame]);
			for (const el of raiz.querySelectorAll('*')) {
				if (el.shadowRoot) anda(el.shadowRoot, emFrame);
				if (el.tagName === 'IFRAME') {
					try { if (el.contentDocument) anda(el.contentDocument, true); } catch (e) {}
				}
			}
		};
		anda(this, false);
		// O menor elemento que contém o texto é o candidato mais provável de ser
		// "o" alvo — mesma lógica do desempate por sobra na mira por texto.
		let melhor = null, sobra = Infinity, emFrame = false;
		for (const par of raizes) {
			const raiz = par[0], corpo = raiz.body;
			// A parada barata vale para documento; shadow root não tem body e é
			// pequeno, então nem vale a pena.
			if (corpo && !(corpo.innerText || '').includes(want)) continue;
			for (const el of raiz.querySelectorAll('*')) {
				const t = el.innerText || '';
				if (!t.includes(want)) continue;
				const s = t.trim().length - want.length;
				if (s < sobra) { sobra = s; melhor = el; emFrame = par[1]; }
			}
		}
		if (!melhor) return '';
		let marca = '';
		if (melhor.id) marca = '#' + melhor.id;
		else if (typeof melhor.className === 'string' && melhor.className.trim())
			marca = '.' + melhor.className.trim().split(/\s+/)[0];
		const trecho = (melhor.innerText || '').trim().slice(0, 60);
		return melhor.tagName.toLowerCase() + marca + (emFrame ? ' (dentro de iframe)' : '') + ' — "' + trecho + '"';
	}`
}

// localizaTexto roda a busca a partir da raiz dada (o documento, de
// Runtime.evaluate, ou o container de `dentro=`) e devolve a descrição de onde
// achou — vazia quando não achou.
func localizaTexto(ctx context.Context, client *cdp.Client, session, want, raiz string) (string, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            raiz,
		"functionDeclaration": localizadorDeTexto(want),
		"returnByValue":       true,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	return res.Result.Value, nil
}
