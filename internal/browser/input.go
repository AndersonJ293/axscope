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
// Devolve o motivo quando o alvo recusa o clique — desabilitado, `aria-disabled`
// ou `pointer-events: none`. A página ignora o evento e a ação responderia ok do
// mesmo jeito, que é o pior desfecho para um agente: ele segue como se tivesse
// agido. Medido na missão 15 do laboratório, onde "Coletar resultado" fica
// desabilitado enquanto o job roda.
//
// O clique é disparado mesmo assim: a página pode ter handler num ancestral, e
// quem decide é ela — o que não pode é o motivo ficar só com a gente.
func Click(ctx context.Context, client *cdp.Client, session string, t *Target, button string, count int, p Presenter) (string, error) {
	if button == "" {
		button = "left"
	}
	if count <= 0 {
		count = 1
	}
	cx, cy := t.ondeAgir()
	motivo := inativo(ctx, client, session, t.ObjectID)

	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.Press(ctx, client, session, cx, cy, button)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}

	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved", "x": cx, "y": cy,
	}, session); err != nil {
		return motivo, err
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
		return motivo, err
	}
	time.Sleep(15 * time.Millisecond)
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseReleased", "x": cx, "y": cy,
		"button": button, "buttons": 0, "clickCount": count,
	}, session); err != nil {
		return motivo, err
	}
	time.Sleep(30 * time.Millisecond)
	return motivo, nil
}

// inativo devolve o motivo pelo qual o elemento recusa ação — ou "" se ele
// aceita.
//
// Vale a ascendência do `disabled` porque é assim que a página se comporta: um
// `<span>` dentro de um `<button disabled>` também não recebe clique. E
// `pointer-events` é herdado, então o `none` de um ancestral já aparece aqui.
func inativo(ctx context.Context, client *cdp.Client, session, objectID string) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			if (this.disabled || (this.closest && this.closest('[disabled]'))) return 'desabilitado';
			if (this.getAttribute && this.getAttribute('aria-disabled') === 'true') return 'com aria-disabled';
			if (getComputedStyle(this).pointerEvents === 'none') return 'com pointer-events: none';
			return '';
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

// Hover passa o mouse por cima do alvo.
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
func Fill(ctx context.Context, client *cdp.Client, session string, t *Target, text string, p Presenter) error {
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
	cx, cy := t.ondeAgir()
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
// `true` quando clicou e o motivo quando o alvo recusa clique.
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
	motivo, err := Click(ctx, client, session, t, "left", 1, p)
	return true, motivo, err
}
