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
)

// Target é um alvo resolvido pronto para receber a ação.
type Target struct {
	ObjectID      string
	BackendNodeID int
	Rect          Rect
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
		id, err := evalObject(ctx, client, session, expr)
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
		id, err := evalObject(ctx, client, session, expr)
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
		id, err := evalObject(ctx, client, session, expr)
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
		_, _ = client.Send(ctx, "DOM.scrollIntoViewIfNeeded",
			map[string]any{"objectId": objectID}, session)
	}

	rect, err := boxOf(ctx, client, session, objectID)
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

// evalObject avalia `expr` e devolve o objectId do elemento (vazio se null).
func evalObject(ctx context.Context, client *cdp.Client, session, expr string) (string, error) {
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": false,
	}, session)
	if err != nil {
		return "", err
	}
	var res struct {
		Result struct {
			Subtype  string `json:"subtype"`
			ObjectID string `json:"objectId"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		return "", fmt.Errorf("%s", res.ExceptionDetails.Text)
	}
	return res.Result.ObjectID, nil
}

// boxOf devolve o retângulo do elemento em px de viewport.
func boxOf(ctx context.Context, client *cdp.Client, session, objectID string) (Rect, error) {
	raw, err := client.Send(ctx, "DOM.getBoxModel", map[string]any{"objectId": objectID}, session)
	if err == nil {
		var model struct {
			Model struct {
				Content []float64 `json:"content"`
			} `json:"model"`
		}
		if json.Unmarshal(raw, &model) == nil && len(model.Model.Content) == 8 {
			q := model.Model.Content
			xs := []float64{q[0], q[2], q[4], q[6]}
			ys := []float64{q[1], q[3], q[5], q[7]}
			minX, maxX := xs[0], xs[0]
			minY, maxY := ys[0], ys[0]
			for _, v := range xs {
				if v < minX {
					minX = v
				}
				if v > maxX {
					maxX = v
				}
			}
			for _, v := range ys {
				if v < minY {
					minY = v
				}
				if v > maxY {
					maxY = v
				}
			}
			return Rect{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}, nil
		}
	}

	// Fallback: getBoundingClientRect no contexto do elemento.
	raw, err = client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			const r = this.getBoundingClientRect();
			return { x: r.x, y: r.y, width: r.width, height: r.height };
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return Rect{}, err
	}
	var res struct {
		Result struct {
			Value Rect `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return Rect{}, err
	}
	if res.Result.Value.Width == 0 && res.Result.Value.Height == 0 {
		return Rect{}, fmt.Errorf("elemento sem tamanho")
	}
	return res.Result.Value, nil
}

// Click clica no alvo com mouse real (e cursor visível).
func Click(ctx context.Context, client *cdp.Client, session string, t *Target, button string, count int, p Presenter) error {
	if button == "" {
		button = "left"
	}
	if count <= 0 {
		count = 1
	}
	cx, cy := t.center()

	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.Press(ctx, client, session, cx, cy, button)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}

	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved", "x": cx, "y": cy,
	}, session); err != nil {
		return err
	}
	buttons := 1
	if button == "right" {
		buttons = 2
	} else if button == "middle" {
		buttons = 4
	}
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mousePressed", "x": cx, "y": cy,
		"button": button, "buttons": buttons, "clickCount": count,
	}, session); err != nil {
		return err
	}
	time.Sleep(15 * time.Millisecond)
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseReleased", "x": cx, "y": cy,
		"button": button, "buttons": 0, "clickCount": count,
	}, session); err != nil {
		return err
	}
	time.Sleep(30 * time.Millisecond)
	return nil
}

// Hover passa o mouse por cima do alvo.
func Hover(ctx context.Context, client *cdp.Client, session string, t *Target, p Presenter) error {
	cx, cy := t.center()
	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.MoveCursor(ctx, client, session, cx, cy)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}
	_, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved", "x": cx, "y": cy,
	}, session)
	return err
}

// DragOptions ajusta o arraste.
type DragOptions struct {
	// DropAt diz onde soltar sobre o alvo: "" (centro), "top" (25% do topo) ou
	// "bottom" (75%). Importa quando o alvo decide antes/depois pela posição.
	DropAt string
	// Tipo força a família do arraste: "html5" ou "ponteiro". Vazio detecta.
	Tipo string
	// Steps é quantos passos de mouse no caminho do arraste por ponteiro.
	Steps int
}

// paraLinha troca o alvo pela "linha" que o contém — o item de lista ou de
// tabela mais próximo.
//
// Descoberto no reorder do LinkedIn: mirar o parágrafo (≈20px, centralizado na
// linha de 48px) ou a linha inteira muda o ponto de soltura — e a biblioteca
// insere no índice da linha sob o ponteiro. Três tentativas erraram a posição
// por causa disso; mirando a linha, acertou de primeira.
func paraLinha(ctx context.Context, client *cdp.Client, session string, t *Target) {
	if t == nil || t.ObjectID == "" {
		return
	}
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": t.ObjectID,
		"functionDeclaration": `function () {
			return this.closest ? (this.closest('li,tr,[role="listitem"],[role="row"]') || this) : this;
		}`,
		"returnByValue": false,
	}, session)
	if err != nil {
		return
	}
	var res struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Result.ObjectID == "" || res.Result.ObjectID == t.ObjectID {
		return
	}
	rect, err := boxOf(ctx, client, session, res.Result.ObjectID)
	if err != nil {
		return
	}
	t.ObjectID = res.Result.ObjectID
	t.Rect = rect
}

// assinaturaDe resume onde o elemento está (índice entre os irmãos + posição),
// para saber se o arraste mudou alguma coisa de fato.
func assinaturaDe(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			if (!this || !this.parentElement) return '';
			const irmaos = [...this.parentElement.children];
			const r = this.getBoundingClientRect();
			return irmaos.indexOf(this) + '@' + Math.round(r.left) + ',' + Math.round(r.top) + '/' + irmaos.length;
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return ""
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	return res.Result.Value
}

// Drag arrasta `from` até `to`.
//
// Duas famílias de arraste convivem na web e não se falam:
//
//   - HTML5 (`draggable`, dragstart/dragover/drop): mouse injetado NÃO inicia o
//     gesto — o Chrome só cria o dragstart a partir de entrada real do usuário.
//     Aqui o arraste é montado na página, com DataTransfer de verdade.
//   - por ponteiro (pointerdown/move/up movendo o elemento, ex.: dnd-kit): é o
//     inverso — o que funciona é mouse de verdade, e evento sintético é ignorado.
//
// A origem com `draggable="true"` (nela ou num ancestral) diz de qual família se
// trata. `tipo=ponteiro|html5` força, se a detecção errar.
func Drag(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) (string, bool, error) {
	// A linha, não o texto: é ela que define o ponto de soltura.
	paraLinha(ctx, client, session, from)
	paraLinha(ctx, client, session, to)

	// Guarda onde a origem estava, para poder dizer se algo mudou de verdade —
	// gesto que não pega costuma terminar em clique, sem aviso nenhum.
	antes := assinaturaDe(ctx, client, session, from.ObjectID)

	tipo := opts.Tipo
	if tipo == "" {
		tipo = dragKind(ctx, client, session, from.ObjectID)
	}
	var err error
	if tipo == "ponteiro" {
		err = dragPointer(ctx, client, session, from, to, opts, p)
	} else {
		tipo = "html5"
		err = dragHTML5(ctx, client, session, from, to, opts, p)
	}
	if err != nil {
		return tipo, false, err
	}

	mudou := true
	if antes != "" {
		if depois := assinaturaDe(ctx, client, session, from.ObjectID); depois != "" {
			mudou = antes != depois
		}
	}
	return tipo, mudou, nil
}

// dragKind diz de que família é o arraste: "html5" ou "ponteiro".
//
// O sinal é o atributo explícito `draggable="true"` — a propriedade `draggable`
// sozinha não serve, porque imagem e link já são arrastáveis por padrão e
// marcariam como HTML5 qualquer clique sobre eles.
func dragKind(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			return this.closest && this.closest('[draggable="true"]') ? 'html5' : 'ponteiro';
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return "html5"
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) == nil && res.Result.Value == "ponteiro" {
		return "ponteiro"
	}
	return "html5"
}

// dragHTML5 monta o arraste na página. É o caminho da família HTML5, que ignora
// mouse injetado: emitimos dragstart/dragenter/dragover/drop/dragend com um
// DataTransfer real, sobre o elemento que está sob o ponto de soltura (o evento
// sobe, então quem escuta no container também recebe).
func dragHTML5(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) error {
	fx, fy := from.center()
	tx, ty := dropPoint(to, opts.DropAt)

	// O cursor passeia até o destino: quem olha precisa ver o arraste acontecer.
	_ = p.Spotlight(ctx, client, session, &from.Rect)
	_ = p.MoveCursor(ctx, client, session, fx, fy)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}
	_ = p.Spotlight(ctx, client, session, &to.Rect)
	_ = p.MoveCursor(ctx, client, session, tx, ty)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}

	onde := opts.DropAt
	if onde == "" {
		onde = "center"
	}
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            from.ObjectID,
		"functionDeclaration": dragScript,
		"arguments": []any{
			map[string]any{"objectId": to.ObjectID},
			map[string]any{"value": onde},
		},
		"returnByValue": true,
	}, session)
	if err != nil {
		return err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if res.ExceptionDetails != nil {
		return fmt.Errorf("%s", res.ExceptionDetails.Text)
	}
	if res.Result.Value != "ok" {
		return fmt.Errorf("arraste não montou: %s", res.Result.Value)
	}
	time.Sleep(40 * time.Millisecond)
	return nil
}

// dragPointer arrasta com mouse de verdade: é o que a família por ponteiro
// entende (pointerdown/move/up). Se a página ignorar o gesto, sobra um clique —
// por isso este caminho só é usado quando a origem NÃO é `draggable`.
func dragPointer(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions, p Presenter) error {
	if opts.Steps <= 0 {
		opts.Steps = 16
	}
	fx, fy := from.center()
	tx, ty := dropPoint(to, opts.DropAt)

	_ = p.Spotlight(ctx, client, session, &from.Rect)
	_ = p.MoveCursor(ctx, client, session, fx, fy)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent",
		map[string]any{"type": "mouseMoved", "x": fx, "y": fy}, session); err != nil {
		return err
	}
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mousePressed", "x": fx, "y": fy,
		"button": "left", "buttons": 1, "clickCount": 1,
	}, session); err != nil {
		return err
	}
	// Uma pausa antes de andar: biblioteca de ponteiro costuma armar o gesto no
	// pointerdown e só passar a acompanhar o movimento no quadro seguinte.
	time.Sleep(40 * time.Millisecond)

	for i := 1; i <= opts.Steps; i++ {
		x := fx + (tx-fx)*float64(i)/float64(opts.Steps)
		y := fy + (ty-fy)*float64(i)/float64(opts.Steps)
		if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
			"type": "mouseMoved", "x": x, "y": y, "buttons": 1,
		}, session); err != nil {
			return err
		}
		time.Sleep(14 * time.Millisecond)
	}

	_ = p.Spotlight(ctx, client, session, &to.Rect)
	_ = p.MoveCursor(ctx, client, session, tx, ty)
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseReleased", "x": tx, "y": ty,
		"button": "left", "buttons": 0, "clickCount": 1,
	}, session); err != nil {
		return err
	}
	time.Sleep(60 * time.Millisecond)
	return nil
}

// dragScript emite a sequência de arraste sobre os elementos reais.
const dragScript = `function (alvo, onde) {
	const fonte = this;
	if (!fonte || !alvo) return 'sem origem ou destino';
	const ponto = (el) => {
		const b = el.getBoundingClientRect();
		let y = b.top + b.height / 2;
		if (onde === 'top') y = b.top + b.height * 0.25;
		else if (onde === 'bottom') y = b.top + b.height * 0.75;
		return { x: b.left + b.width / 2, y: y };
	};
	const pf = ponto(fonte);
	const pd = ponto(alvo);
	const dt = new DataTransfer();
	const dispara = (tipo, el, p) => el.dispatchEvent(new DragEvent(tipo, {
		bubbles: true, cancelable: true, composed: true, dataTransfer: dt,
		clientX: p.x, clientY: p.y, screenX: p.x, screenY: p.y,
	}));
	dispara('dragstart', fonte, pf);
	const sob = document.elementFromPoint(pd.x, pd.y) || alvo;
	dispara('dragenter', sob, pd);
	dispara('dragover', sob, pd);
	dispara('drop', sob, pd);
	dispara('dragend', fonte, pd);
	return 'ok';
}`

// dropPoint devolve onde soltar sobre o alvo.
func dropPoint(t *Target, at string) (float64, float64) {
	switch at {
	case "top", "topo":
		return t.Rect.X + t.Rect.Width/2, t.Rect.Y + t.Rect.Height*0.25
	case "bottom", "base":
		return t.Rect.X + t.Rect.Width/2, t.Rect.Y + t.Rect.Height*0.75
	default:
		return t.center()
	}
}

// Fill substitui o conteúdo do campo (foco + seleção + insertText).
func Fill(ctx context.Context, client *cdp.Client, session string, t *Target, text string, p Presenter) error {
	cx, cy := t.center()
	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.MoveCursor(ctx, client, session, cx, cy)

	if _, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": t.ObjectID,
		"functionDeclaration": `function () {
			if (this.focus) this.focus();
			if (this.select) { this.select(); }
			else {
				const range = document.createRange();
				range.selectNodeContents(this);
				const sel = getSelection();
				sel.removeAllRanges();
				sel.addRange(range);
			}
			return true;
		}`,
		"returnByValue": true,
	}, session); err != nil {
		return err
	}
	if d := visualDelay(); d > 0 {
		time.Sleep(d / 2)
	}
	_, err := client.Send(ctx, "Input.insertText", map[string]any{"text": text}, session)
	return err
}

// Type digita caractere a caractere (dispara handlers de teclado).
func Type(ctx context.Context, client *cdp.Client, session string, t *Target, text string, p Presenter) error {
	cx, cy := t.center()
	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.MoveCursor(ctx, client, session, cx, cy)

	if _, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            t.ObjectID,
		"functionDeclaration": `function () { if (this.focus) this.focus(); return true; }`,
		"returnByValue":       true,
	}, session); err != nil {
		return err
	}
	for _, r := range text {
		s := string(r)
		if _, err := client.Send(ctx, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyDown", "text": s,
		}, session); err != nil {
			return err
		}
		if _, err := client.Send(ctx, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyUp",
		}, session); err != nil {
			return err
		}
		time.Sleep(8 * time.Millisecond)
	}
	return nil
}

// Scroll rola o viewport por (dx, dy). Vai em passos, para quem olha acompanhar
// o movimento em vez de a página pular de uma vez.
//
// Rola pelo scroller sob o centro da tela, e não por roda de mouse: a roda
// depende de quem está sob o ponteiro (numa página com caixa de rolagem no meio
// do caminho, ela rola o container errado) e o ack dela pelo chrome.debugger às
// vezes não volta — medido: 30s de timeout sem rolar nada.
func Scroll(ctx context.Context, client *cdp.Client, session string, dx, dy float64) error {
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    fmt.Sprintf("(%s)(%v, %v)", scrollPassos, dx, dy),
		"returnByValue": true,
		"awaitPromise":  true,
	}, session)
	if err != nil {
		return err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Result.Value != "ok" {
		return fmt.Errorf("não consegui rolar")
	}
	return nil
}

// scrollPassos rola o elemento rolável sob o centro da tela, em passos.
const scrollPassos = `function (dx, dy) {
	const cx = Math.round(innerWidth / 2), cy = Math.round(innerHeight / 2);
	let rolavel = document.scrollingElement || document.documentElement;
	const alvo = document.elementFromPoint(cx, cy);
	if (alvo) {
		let c = alvo;
		while (c) {
			const st = getComputedStyle(c);
			if (/(auto|scroll|overlay)/.test(st.overflowY) && c.scrollHeight > c.clientHeight + 1) { rolavel = c; break; }
			c = c.parentElement;
		}
	}
	const deX = rolavel.scrollLeft, deY = rolavel.scrollTop;
	// Aba em segundo plano estrangula setTimeout, e animar para ninguém só
	// deixa a rolagem lenta: escondida, vai de uma vez.
	const passos = document.hidden ? 1 : 6;
	let i = 0;
	return new Promise((pronto) => {
		const passo = () => {
			i++;
			rolavel.scrollTo({
				left: deX + dx * (i / passos),
				top: deY + dy * (i / passos),
				behavior: 'instant',
			});
			if (i < passos) { setTimeout(passo, 35); return; }
			pronto('ok');
		};
		passo();
	});
}`

// Press envia uma tecla/atalho (ex.: "Enter", "Control+A").
func Press(ctx context.Context, client *cdp.Client, session, combo string) error {
	parts := strings.Split(combo, "+")
	modifiers := 0
	for i := 0; i < len(parts)-1; i++ {
		switch strings.ToLower(strings.TrimSpace(parts[i])) {
		case "alt":
			modifiers |= 1
		case "control", "ctrl":
			modifiers |= 2
		case "meta", "cmd", "command", "super":
			modifiers |= 4
		case "shift":
			modifiers |= 8
		}
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	info := keyInfo(key)
	if info.code == "" {
		return fmt.Errorf("tecla desconhecida: %q", key)
	}
	base := map[string]any{
		"key": info.key, "code": info.code,
		"windowsVirtualKeyCode": info.vk,
		"nativeVirtualKeyCode":  info.vk,
		"modifiers":             modifiers,
	}
	down := map[string]any{"type": "keyDown"}
	for k, v := range base {
		down[k] = v
	}
	// Caractere imprimível precisa de `text` para inserir.
	if len(key) == 1 && modifiers == 0 {
		down["text"] = key
	}
	if _, err := client.Send(ctx, "Input.dispatchKeyEvent", down, session); err != nil {
		return err
	}
	up := map[string]any{"type": "keyUp"}
	for k, v := range base {
		up[k] = v
	}
	_, err := client.Send(ctx, "Input.dispatchKeyEvent", up, session)
	return err
}

type keyDef struct {
	key  string
	code string
	vk   int
}

func keyInfo(name string) keyDef {
	if len(name) == 1 {
		c := strings.ToUpper(name)
		return keyDef{key: name, code: "Key" + c, vk: int(c[0])}
	}
	table := map[string]keyDef{
		"Enter":      {"Enter", "Enter", 13},
		"Tab":        {"Tab", "Tab", 9},
		"Escape":     {"Escape", "Escape", 27},
		"Esc":        {"Escape", "Escape", 27},
		"Backspace":  {"Backspace", "Backspace", 8},
		"Delete":     {"Delete", "Delete", 46},
		"ArrowUp":    {"ArrowUp", "ArrowUp", 38},
		"ArrowDown":  {"ArrowDown", "ArrowDown", 40},
		"ArrowLeft":  {"ArrowLeft", "ArrowLeft", 37},
		"ArrowRight": {"ArrowRight", "ArrowRight", 39},
		"Home":       {"Home", "Home", 36},
		"End":        {"End", "End", 35},
		"PageUp":     {"PageUp", "PageUp", 33},
		"PageDown":   {"PageDown", "PageDown", 34},
		"Space":      {" ", "Space", 32},
	}
	if kd, ok := table[name]; ok {
		return kd
	}
	// F1..F12
	if strings.HasPrefix(name, "F") {
		if n, err := strconv.Atoi(name[1:]); err == nil && n >= 1 && n <= 12 {
			return keyDef{key: name, code: name, vk: 111 + n}
		}
	}
	return keyDef{}
}

// Select escolhe uma opção num <select> nativo (por valor ou rótulo).
func Select(ctx context.Context, client *cdp.Client, session string, t *Target, want string) error {
	if _, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": t.ObjectID,
		"functionDeclaration": `function (want) {
			const norm = (s) => (s || '').trim();
			let chosen = null;
			for (const opt of this.options || []) {
				if (opt.value === want || norm(opt.label) === want || norm(opt.textContent) === want) {
					chosen = opt; break;
				}
			}
			if (!chosen) throw new Error('opção não encontrada: ' + want);
			chosen.selected = true;
			this.dispatchEvent(new Event('input', { bubbles: true }));
			this.dispatchEvent(new Event('change', { bubbles: true }));
			return chosen.value;
		}`,
		"arguments":     []map[string]any{{"value": want}},
		"returnByValue": true,
	}, session); err != nil {
		return err
	}
	return nil
}

// SetChecked garante o estado de um checkbox/radio (clica se precisar).
func SetChecked(ctx context.Context, client *cdp.Client, session string, t *Target, want bool, p Presenter) (bool, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            t.ObjectID,
		"functionDeclaration": `function () { return !!this.checked; }`,
		"returnByValue":       true,
	}, session)
	if err != nil {
		return false, err
	}
	var res struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, err
	}
	if res.Result.Value == want {
		return false, nil
	}
	return true, Click(ctx, client, session, t, "left", 1, p)
}

// Screenshot captura a página e devolve os bytes PNG.
func Screenshot(ctx context.Context, client *cdp.Client, session string, fullPage bool) ([]byte, error) {
	params := map[string]any{"format": "png", "fromSurface": true}
	if fullPage {
		params["captureBeyondViewport"] = true
	}
	raw, err := client.SendTimeout(ctx, "Page.captureScreenshot", params, session, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var res struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return decodeBase64(res.Data)
}
