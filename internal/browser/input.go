// Ações de entrada: clique, hover, preenchimento, tecla, seleção, rolagem e
// captura. Nada de coordenada como primeira opção.
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
// Hover passa o mouse por cima do alvo, entrando de fora para dentro.
//
// Entrar de fora importa: mover o ponteiro para onde ele já está não gera
// `pointerenter`. Sem isso, um alvo com lógica de enter — botão que foge, menu
// que abre no hover, tooltip — via o gesto chegar e a ferramenta responder ok,
// sem a página ver nada. Medido no botão fujão do laboratório: quatro hovers
// seguidos produziram três fugas, e a quarta só veio depois de tirar o mouse.
func Hover(ctx context.Context, client *cdp.Client, session string, t *Target, p Presenter) error {
	cx, cy := t.center()
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

// ScrollTarget rola o container do alvo — o próprio, se ele rola, ou o
// ancestral rolável mais próximo; sem nenhum, o documento.
func ScrollTarget(ctx context.Context, client *cdp.Client, session, objectID string, dx, dy float64) error {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            objectID,
		"functionDeclaration": scrollDoAlvo,
		"arguments": []any{
			map[string]any{"value": dx},
			map[string]any{"value": dy},
		},
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
		return fmt.Errorf("não consegui rolar o alvo")
	}
	return nil
}

// scrollDoAlvo rola o container do elemento, em passos.
const scrollDoAlvo = `function (dx, dy) {
	const rola = (el) => {
		if (!el || !el.scrollHeight) return false;
		const st = getComputedStyle(el);
		return /(auto|scroll|overlay)/.test(st.overflowY) && el.scrollHeight > el.clientHeight + 1;
	};
	let rolavel = null;
	if (rola(this)) rolavel = this;
	else {
		let c = this && this.parentElement;
		while (c) { if (rola(c)) { rolavel = c; break; } c = c.parentElement; }
	}
	if (!rolavel) rolavel = document.scrollingElement || document.documentElement;
	const deX = rolavel.scrollLeft, deY = rolavel.scrollTop;
	const passos = (document.hidden || !rolavel.scrollHeight) ? 1 : 6;
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
