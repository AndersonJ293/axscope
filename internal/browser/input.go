// Ações de entrada: clique, hover, preenchimento, tecla, seleção e captura.
// Nada de coordenada como primeira opção.
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

// Click clica no alvo com mouse real (e cursor visível).
//
// Antes de clicar, confere se o clique vai chegar — e recusa quando não vai:
//
//   - o alvo não aceita ação (desabilitado, `aria-disabled`, `pointer-events:
//     none`) — o `ok` de antes era mentira, medido na missão 15 do laboratório;
//   - o alvo está coberto por outra camada. O clique é entregue e quem o recebe
//     é outra coisa, e a página não reclama: a ação responderia ok do mesmo
//     jeito. Medido no laboratório v2 — com o modal aberto, clicar num botão do
//     feed devolveu ok, o contador não mudou, e ainda por cima o clique caiu no
//     modal (com o efeito colateral que a página quisesse dar a ele).
//
// Recusar é melhor do que agir às cegas: a ação não acontece, mas o motivo
// chega a quem pediu — e a saída para forçar existe (`pos=x,y`).
//
// Depois de clicar, confere se o evento passou pelo alvo. A conferência de antes
// enxerga camadas, mas não sabe para onde o navegador reentrega o evento; a
// escuta sabe. Se não passou, o aviso volta junto com o ok — o clique foi
// enviado, e quem lê precisa saber que ele não chegou.
func Click(ctx context.Context, client *cdp.Client, session string, t *Target, button string, count int, p Presenter) (string, error) {
	if button == "" {
		button = "left"
	}
	if count <= 0 {
		count = 1
	}
	cx, cy := t.ondeAgir()

	if motivo := recusaDeClique(ctx, client, session, t.ObjectID, cx, cy); motivo != "" {
		return "", fmt.Errorf("%s", motivo)
	}
	preparaClique(ctx, client, session, t.ObjectID)

	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.Press(ctx, client, session, cx, cy, button)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}

	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved", "x": cx, "y": cy,
	}, session); err != nil {
		return "", err
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
		return "", err
	}
	time.Sleep(15 * time.Millisecond)
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseReleased", "x": cx, "y": cy,
		"button": button, "buttons": 0, "clickCount": count,
	}, session); err != nil {
		return "", err
	}
	time.Sleep(30 * time.Millisecond)

	if !cliqueChegou(ctx, client, session, t.ObjectID) {
		return "o clique não chegou ao alvo — alguma camada na frente deve ter interceptado", nil
	}
	return "", nil
}

// preparaClique arma uma escuta no alvo para saber se o evento passa por ele.
//
// É a conferência que não depende de heurística: `elementFromPoint` enxerga
// camadas, mas não sabe para onde o navegador reentrega o evento (shadow host,
// iframe). A escuta sabe — ela dispara se o alvo estiver no caminho do evento,
// seja como alvo, seja como ancestral dele (aí o evento sobe até ele).
func preparaClique(ctx context.Context, client *cdp.Client, session, objectID string) {
	_, _ = client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			if (!this.addEventListener) return false;
			if (this.__buCliqueFn) this.removeEventListener('click', this.__buCliqueFn, true);
			this.__buClique = 0;
			this.__buCliqueFn = () => { this.__buClique++; };
			this.addEventListener('click', this.__buCliqueFn, { capture: true });
			return true;
		}`,
		"returnByValue": true,
	}, session)
}

// cliqueChegou devolve quantos cliques o alvo viu desde preparaClique, e desarma
// a escuta. Zero depois de clicar é o que interessa: o evento não passou por ele.
func cliqueChegou(ctx context.Context, client *cdp.Client, session, objectID string) bool {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			const vistos = this.__buClique || 0;
			delete this.__buClique;
			if (this.__buCliqueFn) {
				this.removeEventListener('click', this.__buCliqueFn, true);
				delete this.__buCliqueFn;
			}
			return vistos;
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return true
	}
	var res struct {
		Result struct {
			Value int `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return true
	}
	return res.Result.Value > 0
}

// jsEstaNoPonto define `estaNoPonto(el, x, y)`: é o elemento que está no ponto?
//
// A mesma pergunta serve ao clique (antes de clicar) e à rolagem (para saber se
// alcançou), e fica num lugar só: as duas respostas discordarem é pior do que
// não perguntar. Foi assim que a rolagem passou a dizer "já está visível" para um
// alvo recortado pela lista virtualizada — e o clique, logo depois, o recusou.
//
// Ancestral no DOM claro não conta: o evento borbulha para cima e não desce, e o
// que aparece recortado fica fora do ponto. O shadow host acima conta, porque aí
// o navegador reentrega o evento ao conteúdo da sombra.
const jsEstaNoPonto = `
	const estaNoPonto = (el, x, y) => {
		const sob = el.ownerDocument.elementFromPoint(x, y);
		if (!sob) return false;
		if (sob === el) return true;
		if (el.contains && el.contains(sob)) return true;
		let n = el, cruzouSombra = false;
		while (n) {
			if (n === sob) return cruzouSombra;
			if (n.parentNode) { n = n.parentNode; continue; }
			const raiz = n.getRootNode ? n.getRootNode() : null;
			if (raiz && raiz.host) { n = raiz.host; cruzouSombra = true; continue; }
			return false;
		}
		return false;
	};`

// recusaDeClique devolve por que o clique não deve ser enviado — ou "" se o
// caminho está livre. A frase já vem com a saída, porque quem lê é o agente.
//
// O ponto conferido é o mesmo que será clicado. Dentro de um iframe as
// coordenadas da página não valem, então lá vale o centro medido no documento do
// próprio alvo — que é o mesmo ponto relativo que o navegador acerta lá dentro.
func recusaDeClique(ctx context.Context, client *cdp.Client, session, objectID string, x, y float64) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function (px, py) {` + jsEstaNoPonto + `
			const descreve = (el) => {
				const dono = (el.closest && el.closest('[id], [class]')) || el;
				const cls = typeof dono.className === 'string' && dono.className.trim()
					? '.' + dono.className.trim().split(/\s+/).join('.') : '';
				return (dono.tagName || '?').toLowerCase() + (dono.id ? '#' + dono.id : '') + cls;
			};
			if (this.matches && this.matches(':disabled')) {
				return 'o alvo está desabilitado — espere ele habilitar antes de clicar';
			}
			if (this.getAttribute && this.getAttribute('aria-disabled') === 'true') {
				return 'o alvo está com aria-disabled — espere ele habilitar antes de clicar';
			}
			if (getComputedStyle(this).pointerEvents === 'none') {
				return 'o alvo está com pointer-events: none — o ponteiro não chega nele';
			}
			let emFrame = false;
			try { emFrame = window.top !== window; } catch (e) { emFrame = true; }
			let cx = px, cy = py;
			if (emFrame) {
				const r = this.getBoundingClientRect();
				cx = r.left + r.width / 2;
				cy = r.top + r.height / 2;
			}
			if (estaNoPonto(this, cx, cy)) return '';
			const sob = this.ownerDocument.elementFromPoint(cx, cy);
			if (!sob) return 'o ponto do clique está fora da tela';
			return 'o alvo está coberto por ' + descreve(sob) +
				' — para clicar no ponto assim mesmo, use pos=x,y';
		}`,
		"arguments": []any{
			map[string]any{"value": x},
			map[string]any{"value": y},
		},
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

// Hover passa o mouse por cima do alvo, entrando de fora para dentro.
//
// Entrar de fora importa: mover o ponteiro para onde ele já está não gera
// `pointerenter`. Sem isso, um alvo com lógica de enter — botão que foge, menu
// que abre no hover, tooltip — via o gesto chegar e a ferramenta responder ok,
// sem a página ver nada. Medido no botão fujão do laboratório: quatro hovers
// seguidos produziram três fugas, e a quarta só veio depois de tirar o mouse.
func Hover(ctx context.Context, client *cdp.Client, session string, t *Target, p Presenter) error {
	cx, cy := t.ondeAgir()
	_ = p.Spotlight(ctx, client, session, &t.Rect)

	fx, fy := pontoFora(ctx, client, session, t.Rect)
	_ = p.MoveCursor(ctx, client, session, fx, fy)
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved", "x": fx, "y": fy,
	}, session); err != nil {
		return err
	}
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}

	_ = p.MoveCursor(ctx, client, session, cx, cy)
	_, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved", "x": cx, "y": cy,
	}, session)
	return err
}

// pontoFora devolve um ponto no viewport fora da caixa do alvo, para o ponteiro
// ter de onde entrar. Prefere os lados: na linha do meio do alvo costuma haver
// espaço livre, enquanto acima/abaixo pode cair dentro de um vizinho.
func pontoFora(ctx context.Context, client *cdp.Client, session string, r dom.Rect) (float64, float64) {
	const folga = 6
	cx := r.X + r.Width/2
	cy := r.Y + r.Height/2

	var dims struct {
		Result struct {
			Value struct {
				W float64 `json:"w"`
				H float64 `json:"h"`
			} `json:"value"`
		} `json:"result"`
	}
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "({w: innerWidth, h: innerHeight})",
		"returnByValue": true,
	}, session)
	if err == nil {
		_ = json.Unmarshal(raw, &dims)
	}
	largura, altura := dims.Result.Value.W, dims.Result.Value.H
	if largura == 0 {
		largura, altura = 1280, 720
	}

	if x := r.X - folga; x >= 0 {
		return x, cy
	}
	if x := r.X + r.Width + folga; x <= largura {
		return x, cy
	}
	if y := r.Y - folga; y >= 0 {
		return cx, y
	}
	if y := r.Y + r.Height + folga; y <= altura {
		return cx, y
	}
	return 0, 0
}

// Fill substitui o conteúdo do campo (foco + seleção + insertText).
func Fill(ctx context.Context, client *cdp.Client, session string, t *Target, text string, p Presenter) (string, error) {
	if ok, motivo := classificaCampo(descreveCampo(ctx, client, session, t.ObjectID)); !ok {
		return "", fmt.Errorf("%s", motivo)
	}
	cx, cy := t.ondeAgir()
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
		return "", err
	}
	if d := visualDelay(); d > 0 {
		time.Sleep(d / 2)
	}
	if _, err := client.Send(ctx, "Input.insertText", map[string]any{"text": text}, session); err != nil {
		return "", err
	}
	return avisoDePreenchimento(text, valorDoCampo(ctx, client, session, t.ObjectID)), nil
}

// Type digita caractere a caractere (dispara handlers de teclado).
func Type(ctx context.Context, client *cdp.Client, session string, t *Target, text string, p Presenter) (string, error) {
	if ok, motivo := classificaCampo(descreveCampo(ctx, client, session, t.ObjectID)); !ok {
		return "", fmt.Errorf("%s", motivo)
	}
	cx, cy := t.ondeAgir()
	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.MoveCursor(ctx, client, session, cx, cy)

	if _, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            t.ObjectID,
		"functionDeclaration": `function () { if (this.focus) this.focus(); return true; }`,
		"returnByValue":       true,
	}, session); err != nil {
		return "", err
	}
	for _, r := range text {
		s := string(r)
		if _, err := client.Send(ctx, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyDown", "text": s,
		}, session); err != nil {
			return "", err
		}
		if _, err := client.Send(ctx, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyUp",
		}, session); err != nil {
			return "", err
		}
		time.Sleep(8 * time.Millisecond)
	}
	return avisoDePreenchimento(text, valorDoCampo(ctx, client, session, t.ObjectID)), nil
}

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

// SetChecked garante o estado de um checkbox/radio (clica se precisar). Devolve
// `true` quando clicou e o aviso quando o clique não chegou ao alvo.
func SetChecked(ctx context.Context, client *cdp.Client, session string, t *Target, want bool, p Presenter) (bool, string, error) {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            t.ObjectID,
		"functionDeclaration": `function () { return !!this.checked; }`,
		"returnByValue":       true,
	}, session)
	if err != nil {
		return false, "", err
	}
	var res struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, "", err
	}
	if res.Result.Value == want {
		return false, "", nil
	}
	aviso, err := Click(ctx, client, session, t, "left", 1, p)
	return true, aviso, err
}
