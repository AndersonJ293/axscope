// Input actions: click, hover, fill, key, select and check.
// No coordinates as the first option.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AndersonJ293/axscope/internal/cdp"
	"github.com/AndersonJ293/axscope/internal/dom"
)

func visualDelay() time.Duration {
	if v := cursorDelayMs(); v > 0 {
		return time.Duration(v) * time.Millisecond
	}
	return 0
}

// cursorDelayMs reads AXSCOPE_CURSOR_DELAY (ms). Default 160.
func cursorDelayMs() int {
	raw := envInt("AXSCOPE_CURSOR_DELAY", 160)
	if raw < 0 {
		return 0
	}
	return raw
}

// Click clicks the target with a real mouse (and a visible cursor).
//
// Before clicking, it checks whether the click will land — and refuses when it
// will not:
//
//   - the target does not accept the action (disabled, `aria-disabled`,
//     `pointer-events: none`) — the `ok` from before was a lie, measured in
//     mission 15 of the lab;
//   - the target is covered by another layer. The click is delivered and whoever
//     receives it is something else, and the page does not complain: the action
//     would answer ok just the same. Measured in lab v2 — with the modal open,
//     clicking a button of the feed returned ok, the counter did not change, and
//     on top of that the click fell on the modal (with whatever side effect the
//     page wanted to give it).
//
// Refusing is better than acting blindly: the action does not happen, but the
// reason reaches whoever asked — and the way to force it exists (`pos=x,y`).
//
// After clicking, it checks whether the event passed through the target. The
// check before sees layers, but it does not know where the browser re-delivers
// the event; the listener does. If it did not pass, the warning comes back
// together with the ok — the click was sent, and whoever reads needs to know it
// did not land.
func Click(ctx context.Context, client *cdp.Client, session string, t *Target, button string, count int, p Presenter) (string, error) {
	if button == "" {
		button = "left"
	}
	if count <= 0 {
		count = 1
	}
	cx, cy := t.actionPoint()

	if reason := clickRefusal(ctx, client, session, t.ObjectID, cx, cy); reason != "" {
		return "", fmt.Errorf("%s", reason)
	}
	prepareClick(ctx, client, session, t.ObjectID)

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

	if !clickReached(ctx, client, session, t.ObjectID) {
		return "the click did not reach the target — some layer in front must have intercepted it", nil
	}
	return "", nil
}

// prepareClick arms a listener on the target to know whether the event passes
// through it.
//
// It is the check that does not depend on heuristics: `elementFromPoint` sees
// layers, but it does not know where the browser re-delivers the event (shadow
// host, iframe). The listener knows — it fires if the target is in the event's
// path, whether as the target or as an ancestor of it (then the event bubbles up
// to it).
func prepareClick(ctx context.Context, client *cdp.Client, session, objectID string) {
	_, _ = client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			if (!this.addEventListener) return false;
			if (this.__buClickFn) this.removeEventListener('click', this.__buClickFn, true);
			this.__buClick = 0;
			this.__buClickFn = () => { this.__buClick++; };
			this.addEventListener('click', this.__buClickFn, { capture: true });
			return true;
		}`,
		"returnByValue": true,
	}, session)
}

// clickReached returns how many clicks the target saw since prepareClick, and
// disarms the listener. Zero after clicking is what matters: the event did not
// pass through it.
func clickReached(ctx context.Context, client *cdp.Client, session, objectID string) bool {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {
			const seen = this.__buClick || 0;
			delete this.__buClick;
			if (this.__buClickFn) {
				this.removeEventListener('click', this.__buClickFn, true);
				delete this.__buClickFn;
			}
			return seen;
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

// jsIsAtPoint defines `isAtPoint(el, x, y)`: is it the element at the point?
//
// The same question serves the click (before clicking) and the scroll (to know
// whether it reached), and it lives in a single place: the two answers
// disagreeing is worse than not asking. That was how the scroll came to say
// "already visible" for a target clipped by the virtualized list — and the
// click, right after, refused it.
//
// A clear DOM ancestor does not count: the event bubbles up and does not go
// down, and what appears clipped stays outside the point. The shadow host above
// counts, because there the browser re-delivers the event to the shadow content.
const jsIsAtPoint = `
	const isAtPoint = (el, x, y) => {
		const over = el.ownerDocument.elementFromPoint(x, y);
		if (!over) return false;
		if (over === el) return true;
		if (el.contains && el.contains(over)) return true;
		let n = el, crossedShadow = false;
		while (n) {
			if (n === over) return crossedShadow;
			if (n.parentNode) { n = n.parentNode; continue; }
			const root = n.getRootNode ? n.getRootNode() : null;
			if (root && root.host) { n = root.host; crossedShadow = true; continue; }
			return false;
		}
		return false;
	};`

// jsActionRefusal defines `actionRefusal(el)`: does the target refuse the click,
// and why?
//
// A single definition, used by the check before clicking and by the wait for
// "enabled": the two disagreeing would be worse than not asking.
const jsActionRefusal = `
	const actionRefusal = (el) => {
		if (el.matches && el.matches(':disabled')) {
			return 'the target is disabled — wait for it to become enabled before clicking';
		}
		if (el.getAttribute && el.getAttribute('aria-disabled') === 'true') {
			return 'the target has aria-disabled — wait for it to become enabled before clicking';
		}
		if (getComputedStyle(el).pointerEvents === 'none') {
			return 'the target has pointer-events: none — the pointer cannot reach it';
		}
		return '';
	};`

// Enabled says whether the target accepts the action — the same criterion that
// the click uses before clicking, for whoever needs to wait for it instead of
// guessing a time.
func Enabled(ctx context.Context, client *cdp.Client, session, objectID string) bool {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function () {` + jsActionRefusal + `
			return actionRefusal(this) === '';
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return false
	}
	var res struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return false
	}
	return res.Result.Value
}

// clickRefusal returns why the click should not be sent — or "" if the path is
// clear. The sentence already comes with the way out, because whoever reads is
// the agent.
//
// The point checked is the same one that will be clicked. Inside an iframe the
// page coordinates do not hold, so there the center measured in the target's own
// document holds — which is the same relative point the browser hits in there.
func clickRefusal(ctx context.Context, client *cdp.Client, session, objectID string, x, y float64) string {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": objectID,
		"functionDeclaration": `function (px, py) {` + jsIsAtPoint + jsActionRefusal + `
			const describe = (el) => {
				const owner = (el.closest && el.closest('[id], [class]')) || el;
				const cls = typeof owner.className === 'string' && owner.className.trim()
					? '.' + owner.className.trim().split(/\s+/).join('.') : '';
				return (owner.tagName || '?').toLowerCase() + (owner.id ? '#' + owner.id : '') + cls;
			};
			const refusal = actionRefusal(this);
			if (refusal) return refusal;
			let inFrame = false;
			try { inFrame = window.top !== window; } catch (e) { inFrame = true; }
			let cx = px, cy = py;
			if (inFrame) {
				const r = this.getBoundingClientRect();
				cx = r.left + r.width / 2;
				cy = r.top + r.height / 2;
			}
			if (isAtPoint(this, cx, cy)) return '';
			const over = this.ownerDocument.elementFromPoint(cx, cy);
			if (!over) return 'the click point is off screen';
			return 'the target is covered by ' + describe(over) +
				' — to click the point anyway, use pos=x,y';
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

// Hover moves the mouse over the target, entering from outside in.
//
// Entering from outside matters: moving the pointer to where it already is does
// not generate `pointerenter`. Without that, a target with enter logic — a
// button that runs away, a menu that opens on hover, a tooltip — saw the gesture
// arrive and the tool answer ok, without the page seeing anything. Measured on
// the lab's runaway button: four hovers in a row produced three escapes, and the
// fourth only came after moving the mouse away.
func Hover(ctx context.Context, client *cdp.Client, session string, t *Target, p Presenter) error {
	cx, cy := t.actionPoint()
	_ = p.Spotlight(ctx, client, session, &t.Rect)

	fx, fy := outsidePoint(ctx, client, session, t.Rect)
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

// outsidePoint returns a point in the viewport outside the target's box, so the
// pointer has somewhere to enter from. It prefers the sides: on the target's
// middle line there is usually free space, while above/below may fall inside a
// neighbor.
func outsidePoint(ctx context.Context, client *cdp.Client, session string, r dom.Rect) (float64, float64) {
	const margin = 6
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
	width, height := dims.Result.Value.W, dims.Result.Value.H
	if width == 0 {
		width, height = 1280, 720
	}

	if x := r.X - margin; x >= 0 {
		return x, cy
	}
	if x := r.X + r.Width + margin; x <= width {
		return x, cy
	}
	if y := r.Y - margin; y >= 0 {
		return cx, y
	}
	if y := r.Y + r.Height + margin; y <= height {
		return cx, y
	}
	return 0, 0
}

// Fill replaces the field's content (focus + selection + insertText).
func Fill(ctx context.Context, client *cdp.Client, session string, t *Target, text string, p Presenter) (string, error) {
	if ok, reason := classifyField(describeField(ctx, client, session, t.ObjectID)); !ok {
		return "", fmt.Errorf("%s", reason)
	}
	cx, cy := t.actionPoint()
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
	if _, err := client.Send(ctx, "Input.insertText", map[string]any{"text": normalizeNewlines(text)}, session); err != nil {
		return "", err
	}
	return fillWarning(text, fieldValue(ctx, client, session, t.ObjectID)), nil
}

// Type types character by character (it fires keyboard handlers).
func Type(ctx context.Context, client *cdp.Client, session string, t *Target, text string, p Presenter) (string, error) {
	if ok, reason := classifyField(describeField(ctx, client, session, t.ObjectID)); !ok {
		return "", fmt.Errorf("%s", reason)
	}
	cx, cy := t.actionPoint()
	_ = p.Spotlight(ctx, client, session, &t.Rect)
	_ = p.MoveCursor(ctx, client, session, cx, cy)

	if _, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            t.ObjectID,
		"functionDeclaration": `function () { if (this.focus) this.focus(); return true; }`,
		"returnByValue":       true,
	}, session); err != nil {
		return "", err
	}
	for _, r := range normalizeNewlines(text) {
		if key := keyFor(r); key != "" {
			if err := Press(ctx, client, session, key); err != nil {
				return "", err
			}
			time.Sleep(8 * time.Millisecond)
			continue
		}
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
	return fillWarning(text, fieldValue(ctx, client, session, t.ObjectID)), nil
}

// keyText is what the key inserts, when it inserts.
//
// The CDP only inserts with `text` in keyDown: sending Enter without it fires
// the handler and does not break the line — measured in lab v3, "first" + Enter
// + "second" became "firstsecond". The named keys that produce whitespace need
// the same care as the printable ones.
func keyText(key string) string {
	switch key {
	case "Enter":
		return "\r"
	case "Tab":
		return "\t"
	}
	return ""
}

// Press sends a key/shortcut (e.g. "Enter", "Control+A").
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
		return fmt.Errorf("unknown key: %q", key)
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
	// A printable character needs `text` to insert — and the keys that insert
	// whitespace too (Enter becomes "\r", Tab becomes "\t").
	if t := keyText(key); t != "" {
		down["text"] = t
	} else if len(key) == 1 && modifiers == 0 {
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

// Select chooses an option in a native <select> (by value or label).
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
			if (!chosen) throw new Error('option not found: ' + want);
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

// SetChecked guarantees the state of a checkbox/radio (it clicks if needed). It
// returns `true` when it clicked and the warning when the click did not reach
// the target.
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
	warning, err := Click(ctx, client, session, t, "left", 1, p)
	return true, warning, err
}
