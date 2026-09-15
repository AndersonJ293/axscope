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

func (a *Agent) wait(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	want := req.String("text")
	if want == "" {
		return protocol.Fail(fmt.Errorf("uso: bu %s <texto>", req.Cmd))
	}
	sid, err := a.activeSID(sess)
	if err != nil {
		return protocol.Fail(err)
	}
	timeout := navTimeout
	if v := req.Int("timeout", 0); v > 0 {
		timeout = time.Duration(v) * time.Millisecond
	}
	present := req.Cmd == "wait"
	start := time.Now()
	deadline := start.Add(timeout)
	for time.Now().Before(deadline) {
		onde, err := localizaTexto(ctx, a.client(), sid, want)
		if err == nil && (onde != "") == present {
			verb := "apareceu"
			if !present {
				verb = "sumiu"
			}
			msg := fmt.Sprintf("ok: %q %s em %dms", want, verb, time.Since(start).Milliseconds())
			if onde != "" {
				msg += " — em " + onde
			}
			return ok(msg)
		}
		time.Sleep(120 * time.Millisecond)
	}
	if present {
		return protocol.Fail(fmt.Errorf("%q não apareceu em %s", want, timeout))
	}
	return protocol.Fail(fmt.Errorf("%q não sumiu em %s", want, timeout))
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
// expressaoLocalizaTexto procura o texto na página e descreve onde achou.
//
// Varre o documento, os shadow roots abertos e os iframes de mesma origem: a
// leitura já mostra o que está lá dentro, então a espera tem de enxergar o
// mesmo — senão o agente vê o texto e não consegue esperar por ele.
func expressaoLocalizaTexto(want string) string {
	return fmt.Sprintf(`(() => {
		const want = %s;
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
		anda(document, false);
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
	})()`, strconv.Quote(want))
}

func localizaTexto(ctx context.Context, client *cdp.Client, session, want string) (string, error) {
	raw, err := dom.Eval(ctx, client, session, expressaoLocalizaTexto(want))
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", nil
	}
	var onde string
	if err := json.Unmarshal(raw, &onde); err != nil {
		return "", err
	}
	return onde, nil
}
