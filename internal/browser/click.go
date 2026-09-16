// Click and hover: the pointer actions and the checks that keep them honest.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
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

// Click clicks the target with a real mouse, refusing when the point is disabled
// or covered and warning when the event does not reach the target.
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
	_ = p.PressCursor(ctx, client, session, cx, cy, button)
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

// DOMClick fires a programmatic click on the resolved node (`element.click()`),
// for pages that only trust synthetic DOM clicks. It has no pointer geometry and
// no hit-testing, so it also bypasses whatever covers the element — that is the
// point of the opt-in, and the trade-off to document.
func DOMClick(ctx context.Context, client *cdp.Client, session string, t *Target) error {
	raw, err := client.Send(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": t.ObjectID,
		"functionDeclaration": `function () {
			if (typeof this.click !== 'function') return 'not a clickable element';
			this.click();
			return '';
		}`,
		"returnByValue": true,
	}, session)
	if err != nil {
		return err
	}
	var res struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return fmt.Errorf("the DOM click had no readable answer")
	}
	if res.Result.Value != "" {
		return fmt.Errorf("the target does not accept a DOM click (%s) — it may need the real pointer", res.Result.Value)
	}
	return nil
}

// prepareClick arms a click listener on the target so clickReached can tell
// whether the event actually passed through it (shadow host, iframe included).
// A failed arm is harmless: clickReached then reports reached.
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

// clickReached reports whether the target saw the click since prepareClick, and
// disarms the listener. An unreadable answer counts as reached.
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

// jsIsAtPoint defines `isAtPoint(el, x, y)`: whether the element is at the point.
// Shared by the click and the scroll so the two agree; a clear DOM ancestor does
// not count, but a shadow host above does (the browser re-delivers the event).
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

// jsActionRefusal defines `actionRefusal(el)`: the reason the target refuses the
// click, shared by the pre-click check and the wait for "enabled".
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

// Enabled reports whether the target accepts the action, by the same criterion
// the click uses before clicking.
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

// clickRefusal returns why the click should not be sent, or "" when the path is
// clear. Inside an iframe it checks the center in the target's own document.
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

// Hover moves the mouse over the target, entering from outside so pointerenter
// fires; moving to where the pointer already is does not.
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

// outsidePoint returns a point outside the target's box to enter from, preferring
// the sides where there is usually free space.
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
		// A failed read falls back to 1280x720 below.
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
