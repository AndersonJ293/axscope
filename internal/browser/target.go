// Resolução de alvo: ref, seletor CSS, texto visível ou posição viram um alvo
// com geometria na tela — trazido ao alcance com rolagem visível, para quem
// olha acompanhar em vez de a página pular.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ajunior/browser-use/internal/cdp"
	"github.com/ajunior/browser-use/internal/dom"
)

// Target é um alvo resolvido pronto para receber a ação.
type Target struct {
	ObjectID      string
	BackendNodeID int
	Rect          dom.Rect
	Description   string
	// Ponto, quando não é nil, é onde a ação deve acontecer — o alvo veio de
	// `pos=x,y`. O Rect continua sendo o do elemento sob o ponto, para o
	// destaque e para o hover saber de onde entrar; sem separar os dois, a ação
	// cairia no centro do elemento, que num iframe fica a dezenas de pixels do
	// lugar pedido.
	Ponto *Ponto
}

// Ponto é uma coordenada de tela.
type Ponto struct{ X, Y float64 }

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
	var ponto *Ponto
	// Expressão que produziu o nó, para poder resolver de novo se a rolagem
	// invalidar o que foi resolvido (lista virtualizada recria as linhas).
	var expr string

	switch {
	case strings.HasPrefix(spec, "css="):
		sel := strings.TrimPrefix(spec, "css=")
		expr = expressaoCSS(sel)
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
		expr = expressaoTexto(want)
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
		expr = fmt.Sprintf("document.elementFromPoint(%v, %v)", x, y)
		id, err := dom.EvalObject(ctx, client, session, expr)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fmt.Errorf("nada em %s (fora da tela?)", spec)
		}
		objectID = id
		ponto = &Ponto{X: x, Y: y}

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

	t := &Target{ObjectID: objectID, BackendNodeID: backendID, Description: spec, Ponto: ponto}

	// Traz para a tela em passos visíveis; se não bastar, garante com o scroll
	// direto — o que não pode é a ação não alcançar o alvo.
	if !scrollAoAlcance(ctx, client, session, objectID) {
		_ = dom.ScrollTo(ctx, client, session, objectID)
	}

	rect, err := dom.BoxOf(ctx, client, session, objectID)
	if err != nil && expr != "" {
		// A rolagem pode ter invalidado o nó: lista virtualizada recria as
		// linhas conforme rola, e o elemento resolvido antes vira órfão (sem
		// caixa). Resolve de novo pela mesma expressão e mede outra vez.
		if id, errEval := dom.EvalObject(ctx, client, session, expr); errEval == nil && id != "" && id != objectID {
			objectID = id
			rect, err = dom.BoxOf(ctx, client, session, objectID)
		}
	}
	if err != nil {
		if want := textoPedido(spec); want != "" {
			if msg := textoEscondido(ctx, client, session, want); msg != "" {
				return nil, fmt.Errorf("%s", msg)
			}
		}
		return nil, fmt.Errorf("alvo %q sem área visível: %w", spec, err)
	}
	t.ObjectID = objectID
	t.Rect = rect
	return t, nil
}

// sobASombra anda também dentro de shadow roots abertos.
//
// A árvore de acessibilidade **achata** shadow DOM: a leitura mostra o botão que
// está lá dentro, com nome e ref, e o ref alcança (resolve por backendNodeId).
// A mira por DOM, não — ela andava só no documento claro, de modo que a leitura
// mostrava e o `text=` não alcançava. Medido na missão 12 do laboratório.
const sobASombra = `
	const sobASombra = (raiz, sel, acc) => {
		for (const el of raiz.querySelectorAll(sel)) acc.push(el);
		for (const el of raiz.querySelectorAll('*')) {
			if (el.shadowRoot) sobASombra(el.shadowRoot, sel, acc);
		}
		return acc;
	};`

// jsCandidatos lista os elementos que podem ser alvo de texto.
const jsCandidatos = `
	const sel = 'a,button,input,select,textarea,summary,[role],[tabindex],[aria-label],[contenteditable="true"],[draggable="true"],label,li,td,th,h1,h2,h3,p,span,div';`

// jsTextoDeElemento diz o texto/nome de um elemento e se ele está à vista.
//
// Compartilhado entre a busca por texto e a contagem de candidatos, para as duas
// concordarem — contar por um critério e escolher por outro seria pior do que não
// contar.
const jsTextoDeElemento = `
	const texto = el => {
		const aria = (el.getAttribute('aria-label') || '').trim();
		if (aria) return aria;
		if (el.labels && el.labels.length) {
			const rotulo = (el.labels[0].innerText || '').trim();
			if (rotulo) return rotulo;
		}
		const visivel = (el.innerText || '').trim();
		if (visivel) return visivel;
		const dica = (el.getAttribute('placeholder') || el.getAttribute('title') || '').trim();
		if (dica) return dica;
		return (el.value || '').trim();
	};
	const oculto = el => {
		if (el.getClientRects().length === 0) return 1;
		return getComputedStyle(el).visibility === 'hidden' ? 1 : 0;
	};`

// expressaoTexto monta a busca por texto visível/nome acessível.
//
// Separada de ResolveTarget porque é testável — e o teste existe para fixar que
// ela atravessa shadow root.
func expressaoTexto(want string) string {
	return fmt.Sprintf(`(() => {
		const want = %s;
		%s
		%s
		%s
		const nodes = sobASombra(document, sel, []);
		// "Acionável" desempata: o texto mora no <span>, mas quem aceita ação
		// é o <li draggable> / <a> em volta. Sem isso o alvo vira o texto.
		const acionavel = el => el.matches('a,button,input,select,textarea,summary,[role],[tabindex],[contenteditable="true"],[draggable="true"]') || typeof el.onclick === 'function';
		// Ordem de preferência: nome exato, depois à vista, depois o mais justo
		// (menos sobra de texto); acionável só desempata. O "à vista" vem cedo
		// de propósito: um item de menu fechado casando antes do visível é o que
		// fazia o clique por texto mirar num botão de outro menu. E vem depois do
		// exato porque mirar por texto é mirar pelo nome: nome exato escondido
		// ainda ganha de nome parcial visível.
		const melhorQue = (a, b) => a[0] - b[0] || a[1] - b[1] || a[2] - b[2] || a[3] - b[3] || a[4] - b[4];
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
			const atual = [exato ? 0 : 1, oculto(el), t.length - want.length, cromo(el), acionavel(el) ? 0 : 1];
			if (chave === null || melhorQue(atual, chave) < 0) {
				escolhido = el;
				chave = atual;
			}
		}
		return escolhido;
	})()`, strconv.Quote(want), sobASombra, jsCandidatos, jsTextoDeElemento)
}

// expressaoContagem conta quantos elementos casam com o texto e quantos estão à
// vista. É o que deixa a recusa dizer por que não deu, em vez de só dizer que não
// deu — quem lê fica sabendo se precisa abrir um menu ou um modal.
func expressaoContagem(want string) string {
	return fmt.Sprintf(`(() => {
		const want = %s;
		%s
		%s
		%s
		const todos = sobASombra(document, sel, []).filter(el => {
			const t = texto(el);
			return t !== '' && (t === want || t.includes(want));
		});
		return { total: todos.length, visiveis: todos.filter(el => !oculto(el)).length };
	})()`, strconv.Quote(want), sobASombra, jsCandidatos, jsTextoDeElemento)
}

// expressaoCSS monta a busca por seletor CSS.
//
// O seletor CSS tem semântica própria no documento claro, então ele é tentado
// primeiro. Só quando não acha nada é que vale procurar dentro de shadow roots:
// assim uma página que sempre funcionou não muda de alvo, e a que tem web
// component deixa de ser um beco sem saída.
func expressaoCSS(sel string) string {
	alvo := strconv.Quote(sel)
	return fmt.Sprintf(`(() => {
		%s
		return document.querySelector(%s) || sobASombra(document, %s, [])[0] || null;
	})()`, sobASombra, alvo, alvo)
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
			if (document.hidden) rolavel.dispatchEvent(new Event('scroll'));
			pronto(visivel() ? 'ok' : 'nao');
		};
		passo();
	});
}`

// ondeAgir devolve o ponto exato onde a ação acontece: o ponto pedido, quando o
// alvo veio de `pos=x,y`; senão o centro do elemento.
// textoPedido devolve o texto de um alvo `text=`, ou "" para as outras formas.
func textoPedido(spec string) string {
	if !strings.HasPrefix(spec, "text=") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(spec, "text="))
}

// textoEscondido explica por que um alvo de texto não tem área visível, quando a
// causa é todos os candidatos estarem escondidos — o caso do menu fechado, que
// antes só rendia "sem área visível" e deixava quem lê sem próximo passo.
func textoEscondido(ctx context.Context, client *cdp.Client, session, want string) string {
	raw, err := dom.Eval(ctx, client, session, expressaoContagem(want))
	if err != nil {
		return ""
	}
	var res struct {
		Total    int `json:"total"`
		Visiveis int `json:"visiveis"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Total == 0 || res.Visiveis > 0 {
		return ""
	}
	return mensagemTextoEscondido(want, res.Total)
}

// mensagemTextoEscondido é a frase da recusa — separada para o teste prender o
// que ela precisa dizer: quantos são, e o próximo passo.
func mensagemTextoEscondido(want string, total int) string {
	return fmt.Sprintf(
		"achei %d elementos com texto %q e todos estão escondidos — abra o que os revela (um menu, um painel) e tente de novo",
		total, want)
}

func (t *Target) ondeAgir() (float64, float64) {
	if t.Ponto != nil {
		return t.Ponto.X, t.Ponto.Y
	}
	return t.Rect.X + t.Rect.Width/2, t.Rect.Y + t.Rect.Height/2
}
