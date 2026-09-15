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
	"github.com/ajunior/browser-use/internal/overlay"
)

// Target é um alvo resolvido pronto para receber a ação.
type Target struct {
	ObjectID      string
	BackendNodeID int
	Rect          overlay.Rect
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
//   - "text=..."   → elemento cujo texto visível casa
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
			const nodes = document.querySelectorAll('a,button,input,select,textarea,summary,[role],[tabindex],[contenteditable="true"],[draggable="true"],label,li,td,th,h1,h2,h3,p,span,div');
			// "Acionável" desempata: o texto mora no <span>, mas quem aceita ação
			// é o <li draggable> / <a> em volta. Sem isso o alvo vira o texto.
			const acionavel = el => el.matches('a,button,input,select,textarea,summary,[role],[tabindex],[contenteditable="true"],[draggable="true"]') || typeof el.onclick === 'function';
			const texto = el => (el.innerText || el.value || el.getAttribute('aria-label') || '').trim();
			let exatoAcionavel = null, exato = null, parcialAcionavel = null, parcial = null;
			for (const el of nodes) {
				const t = texto(el);
				if (!t) continue;
				if (t === want) {
					if (!exatoAcionavel && acionavel(el)) exatoAcionavel = el;
					if (!exato) exato = el;
				} else if (t.includes(want)) {
					if (!parcialAcionavel && acionavel(el)) parcialAcionavel = el;
					if (!parcial) parcial = el;
				}
			}
			return exatoAcionavel || parcialAcionavel || exato || parcial || null;
		})()`, strconv.Quote(want))
		id, err := evalObject(ctx, client, session, expr)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fmt.Errorf("nenhum elemento com texto %q", want)
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

	// Rola até aparecer (a ação sempre alcança o que está fora da dobra).
	_, _ = client.Send(ctx, "DOM.scrollIntoViewIfNeeded",
		map[string]any{"objectId": objectID}, session)

	rect, err := boxOf(ctx, client, session, objectID)
	if err != nil {
		return nil, fmt.Errorf("alvo %q sem área visível: %w", spec, err)
	}
	t.Rect = rect
	return t, nil
}

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
func boxOf(ctx context.Context, client *cdp.Client, session, objectID string) (overlay.Rect, error) {
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
			return overlay.Rect{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}, nil
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
		return overlay.Rect{}, err
	}
	var res struct {
		Result struct {
			Value overlay.Rect `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return overlay.Rect{}, err
	}
	if res.Result.Value.Width == 0 && res.Result.Value.Height == 0 {
		return overlay.Rect{}, fmt.Errorf("elemento sem tamanho")
	}
	return res.Result.Value, nil
}

// Click clica no alvo com mouse real (e cursor visível).
func Click(ctx context.Context, client *cdp.Client, session string, t *Target, button string, count int) error {
	if button == "" {
		button = "left"
	}
	if count <= 0 {
		count = 1
	}
	cx, cy := t.center()

	_ = overlay.Spotlight(ctx, client, session, &t.Rect)
	_ = overlay.Press(ctx, client, session, cx, cy, button)
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
func Hover(ctx context.Context, client *cdp.Client, session string, t *Target) error {
	cx, cy := t.center()
	_ = overlay.Spotlight(ctx, client, session, &t.Rect)
	_ = overlay.MoveCursor(ctx, client, session, cx, cy)
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
}

// Drag arrasta `from` até `to`.
//
// Arraste HTML5 (draggable/dragstart/drop) não nasce de mouse injetado: o Chrome
// só inicia o gesto a partir de entrada real do usuário, e o caminho previsto no
// CDP para automação (`Input.setInterceptDrags` + `dragIntercepted`) também não
// dispara — medido com contador na própria página, dragstart/dragover/drop
// ficaram em zero, e o que sobrava era um clique de verdade no alvo.
//
// Então o arraste é montado na página: emitimos dragstart/dragenter/dragover/
// drop/dragend com um DataTransfer real, sobre o elemento que está sob o ponto
// de soltura (o evento sobe, então quem escuta no container também recebe).
// Funciona com DnD feito em JS — a maioria. Não funciona para arrastar arquivo
// do sistema, nem quando o site exige evento confiável (`isTrusted`).
func Drag(ctx context.Context, client *cdp.Client, session string, from, to *Target, opts DragOptions) error {
	fx, fy := from.center()
	tx, ty := dropPoint(to, opts.DropAt)

	// O cursor passeia até o destino: quem olha precisa ver o arraste acontecer.
	_ = overlay.Spotlight(ctx, client, session, &from.Rect)
	_ = overlay.MoveCursor(ctx, client, session, fx, fy)
	if d := visualDelay(); d > 0 {
		time.Sleep(d)
	}
	_ = overlay.Spotlight(ctx, client, session, &to.Rect)
	_ = overlay.MoveCursor(ctx, client, session, tx, ty)
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
func Fill(ctx context.Context, client *cdp.Client, session string, t *Target, text string) error {
	cx, cy := t.center()
	_ = overlay.Spotlight(ctx, client, session, &t.Rect)
	_ = overlay.MoveCursor(ctx, client, session, cx, cy)

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
func Type(ctx context.Context, client *cdp.Client, session string, t *Target, text string) error {
	cx, cy := t.center()
	_ = overlay.Spotlight(ctx, client, session, &t.Rect)
	_ = overlay.MoveCursor(ctx, client, session, cx, cy)

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

// Scroll rola o viewport por (dx, dy) a partir do centro.
func Scroll(ctx context.Context, client *cdp.Client, session string, dx, dy float64) error {
	var dims struct {
		Result struct {
			Value struct {
				W float64 `json:"w"`
				H float64 `json:"h"`
			} `json:"value"`
		} `json:"result"`
	}
	raw, err := client.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "({w: window.innerWidth, h: window.innerHeight})",
		"returnByValue": true,
	}, session)
	if err == nil {
		_ = json.Unmarshal(raw, &dims)
	}
	cx := dims.Result.Value.W / 2
	cy := dims.Result.Value.H / 2
	if cx == 0 {
		cx, cy = 640, 400
	}
	if _, err := client.Send(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseWheel", "x": cx, "y": cy, "deltaX": dx, "deltaY": dy,
	}, session); err != nil {
		return err
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

// SetChecked garante o estado de um checkbox/radio (clica se precisar).
func SetChecked(ctx context.Context, client *cdp.Client, session string, t *Target, want bool) (bool, error) {
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
	return true, Click(ctx, client, session, t, "left", 1)
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
