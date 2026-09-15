// Ações: agir por identidade (ref/css/texto), com cursor renderizado, auto-scroll
// e espera de convergência. Nada de coordenada como primeira opção.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/dom"
)

// Target é um alvo resolvido pronto para receber a ação.
type Target struct {
	ObjectID      string
	BackendNodeID int
	Rect          dom.Rect
	Description   string
}

func visualDelay() time.Duration {
	if v := cursorDelayMs(); v > 0 {
		return time.Duration(v) * time.Millisecond
	}
	return 0
}

// cursorDelayMs lê BROWSER_USE_CURSOR_DELAY (ms). Default 160.
func cursorDelayMs() int {
	raw := envInt("BROWSER_USE_CURSOR_DELAY", 160)
	if raw < 0 {
		return 0
	}
	return raw
}

// ResolveTarget resolve uma referência em um alvo com geometria.
//
// Formas aceitas:
//   - "e12"        → ref do último `snap`
//   - "css=..."    → seletor CSS
//   - "text=..."   → nome acessível ou texto visível que casa
//   - "pos=x,y"    → o elemento sob o ponto (último recurso, para alvo sem nome)
func ResolveTarget(ctx context.Context, client *cdp.Client, session string, refs map[string]int, spec string) (*Target, error) {
	if spec == "" {
		return nil, fmt.Errorf("alvo vazio")
	}

	var objectID string
	var backendID int

	switch {
	case strings.HasPrefix(spec, "css="):
		sel := strings.TrimPrefix(spec, "css=")
		expr := fmt.Sprintf("document.querySelector(%s)", strconv.Quote(sel))
		id, err := dom.EvalObject(ctx, client, session, expr)
		if err != nil {
			return nil, fmt.Errorf("seletor inválido %q: %w", sel, err)
		}
		if id == "" {
			return nil, fmt.Errorf("nenhum elemento para o seletor %q", sel)
		}
		objectID = id

	case strings.HasPrefix(spec, "text="):
		want := strings.TrimSpace(strings.TrimPrefix(spec, "text="))
		expr := fmt.Sprintf(`(() => {
			const want = %s;
			const nodes = document.querySelectorAll('a,button,input,select,textarea,summary,[role],[tabindex],[aria-label],[contenteditable="true"],[draggable="true"],label,li,td,th,h1,h2,h3,p,span,div');
			// "Acionável" desempata: o texto mora no <span>, mas quem aceita ação
			// é o <li draggable> / <a> em volta. Sem isso o alvo vira o texto.
			const acionavel = el => el.matches('a,button,input,select,textarea,summary,[role],[tabindex],[contenteditable="true"],[draggable="true"]') || typeof el.onclick === 'function';
			// O nome acessível manda: quando o alvo não tem texto nenhum e só um
			// aria-label, é ele que a árvore mostra — e é por ele que o agente lê
			// a tela. Olhar só o texto deixaria esse alvo inalcançável.
			const texto = el => (el.getAttribute('aria-label') || el.innerText || el.value || '').trim();
			// Ordem de preferência: nome exato antes de parcial; depois o mais
			// justo (menos sobra de texto); acionável só desempata. Sem o "mais
			// justo", o primeiro que contém o texto é sempre o container da
			// página inteira — e o alvo vira a tela toda.
			const melhorQue = (a, b) => a[0] - b[0] || a[1] - b[1] || a[2] - b[2] || a[3] - b[3];
			// Nome que colide com o cromo da página é armadilha: o menu "⋯" do
			// LinkedIn se chama "Resources", igual ao "Resources" do topo. Fora
			// do cromo ganha o desempate.
			const cromo = el => el.closest('nav,header,footer,[role="navigation"],[role="banner"],[role="contentinfo"]') ? 1 : 0;
			let escolhido = null, chave = null;
			for (const el of nodes) {
				const t = texto(el);
				if (!t) continue;
				const exato = t === want;
				if (!exato && !t.includes(want)) continue;
				const atual = [exato ? 0 : 1, t.length - want.length, cromo(el), acionavel(el) ? 0 : 1];
				if (chave === null || melhorQue(atual, chave) < 0) {
					escolhido = el;
					chave = atual;
				}
			}
			return escolhido;
		})()`, strconv.Quote(want))
		id, err := dom.EvalObject(ctx, client, session, expr)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fmt.Errorf(
				"nenhum elemento com texto %q — se a página carrega por rolagem, desça até a seção e tente de novo", want)
		}
		objectID = id

	case strings.HasPrefix(spec, "pos="):
		// Último recurso, para alvo sem nome acessível nenhum (alça de arrastar
		// sem aria-label, por exemplo). É posição, não identidade: quebra fácil.
		xy := strings.Split(strings.TrimPrefix(spec, "pos="), ",")
		if len(xy) != 2 {
			return nil, fmt.Errorf("posição mal formada %q — use pos=x,y", spec)
		}
		x, errX := strconv.ParseFloat(strings.TrimSpace(xy[0]), 64)
		y, errY := strconv.ParseFloat(strings.TrimSpace(xy[1]), 64)
		if errX != nil || errY != nil {
			return nil, fmt.Errorf("posição mal formada %q — use pos=x,y", spec)
		}
		expr := fmt.Sprintf("document.elementFromPoint(%v, %v)", x, y)
		id, err := dom.EvalObject(ctx, client, session, expr)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fmt.Errorf("nada em %s (fora da tela?)", spec)
		}
		objectID = id

	default:
		backend, ok := refs[spec]
		if !ok {
			return nil, fmt.Errorf("ref %q não existe — rode `snap` de novo (as refs são por leitura)", spec)
		}
		backendID = backend
		var res struct {
			Object struct {
				ObjectID string `json:"objectId"`
			} `json:"object"`
		}
		if err := client.SendJSON(ctx, "DOM.resolveNode",
			map[string]any{"backendNodeId": backend}, session, &res); err != nil {
			return nil, fmt.Errorf("ref %q não resolve mais (página mudou?) — rode `snap` de novo", spec)
		}
		if res.Object.ObjectID == "" {
			return nil, fmt.Errorf("ref %q não resolve mais — rode `snap` de novo", spec)
		}
		objectID = res.Object.ObjectID
	}

	t := &Target{ObjectID: objectID, BackendNodeID: backendID, Description: spec}

	// Traz para a tela em passos visíveis; se não bastar, garante com o scroll
	// direto — o que não pode é a ação não alcançar o alvo.
	if !scrollAoAlcance(ctx, client, session, objectID) {
		_ = dom.ScrollTo(ctx, client, session, objectID)
	}

	rect, err := dom.BoxOf(ctx, client, session, objectID)
	if err != nil {
		return nil, fmt.Errorf("alvo %q sem área visível: %w", spec, err)
	}
	t.Rect = rect
	return t, nil
}

// scrollAoAlcance rola em passos até o elemento ficar visível, para quem olha
// acompanhar o movimento em vez de ver a página pular. Devolve true se alcançou.
//
// Rola pelo `scrollTop` do ancestral que de fato rola, e não por roda de mouse
// num ponto fixo: a roda vai para quem estiver sob o ponteiro, e numa página com
// container rolável no meio do caminho (caixa de rolagem interna, lista virtual)
// ela rola o container errado — e a página não anda.
func scrollAoAlcance(ctx context.Context, client *cdp.Client, session, objectID string) bool {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": scrollScript,
		"arguments":           []any{map[string]any{"value": 40}},
		"returnByValue":       true,
		"awaitPromise":        true,
	}, session)
	if err != nil {
		return false
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return false
	}
	// "ja" já estava visível, "ok" rolou e chegou. O resto é "não deu" — aí o
	// chamador cai no scroll direto, em vez de fingir que está tudo bem.
	return res.Result.Value == "ja" || res.Result.Value == "ok"
}

// scrollScript rola o ancestral rolável do elemento, em passos, e confirma.
const scrollScript = `function (intervalo) {
	const el = this;
	if (!el || !el.getBoundingClientRect) return Promise.resolve('sem alvo');
	const margem = 60;
	const visivel = () => {
		const r = el.getBoundingClientRect();
		return r.top >= margem && r.bottom <= innerHeight - margem;
	};
	if (visivel()) return Promise.resolve('ja');

	const rolavel = (() => {
		let c = el.parentElement;
		while (c) {
			const st = getComputedStyle(c);
			if (/(auto|scroll|overlay)/.test(st.overflowY) && c.scrollHeight > c.clientHeight + 1) return c;
			c = c.parentElement;
		}
		return document.scrollingElement || document.documentElement;
	})();

	const de = rolavel.scrollTop;
	const r = el.getBoundingClientRect();
	const delta = (r.top - innerHeight / 2 + r.height / 2);
	// Aba em segundo plano estrangula setTimeout; escondida, vai de uma vez.
	const passos = document.hidden ? 1 : 6;
	let i = 0;
	return new Promise((pronto) => {
		const passo = () => {
			i++;
			// behavior 'instant' é obrigatório: com scroll-behavior smooth no
			// CSS, atribuir scrollTop anima — e o passo seguinte reinicia a
			// animação, de modo que a rolagem nunca anda. A animação é a nossa,
			// nos passos acima.
			rolavel.scrollTo({ top: de + delta * (i / passos), behavior: 'instant' });
			if (i < passos) { setTimeout(passo, intervalo); return; }
			pronto(visivel() ? 'ok' : 'nao');
		};
		passo();
	});
}`

func (t *Target) center() (float64, float64) {
	return t.Rect.X + t.Rect.Width/2, t.Rect.Y + t.Rect.Height/2
}
